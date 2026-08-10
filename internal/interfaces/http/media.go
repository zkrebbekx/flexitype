package http

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/zkrebbekx/flexitype/application"
	"github.com/zkrebbekx/flexitype/application/uow"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

// uploadMedia stores an uploaded file for a media attribute and writes the
// resulting media value. The multipart form carries the file as "file".
func (s *server) uploadMedia(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, s.maxMediaBytes)
	if err := r.ParseMultipartForm(s.maxMediaBytes); err != nil {
		writeError(w, s.log, domainerrors.NewValidation("could not read upload: "+err.Error()))
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, s.log, domainerrors.NewValidation("missing file: "+err.Error()))
		return
	}
	defer func() { _ = file.Close() }()

	filename := ""
	if header != nil {
		filename = header.Filename
	}

	snap, err := application.FromContext(r.Context()).Values().UploadMedia(r.Context(),
		chi.URLParam(r, "typeDefinitionID"), chi.URLParam(r, "entityID"), chi.URLParam(r, "attributeID"),
		file, filename)
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusCreated, snap)
}

// downloadMedia streams a stored object by key. The media value's fetchable
// URL points here.
func (s *server) downloadMedia(w http.ResponseWriter, r *http.Request) {
	if s.blobs == nil {
		// A deployment with no blob store cannot ever serve this, so it is a
		// capability gap, not a caller error. Reporting VALIDATION/422 made it
		// indistinguishable from a malformed request, and retry logic kept
		// retrying against a deployment that structurally cannot answer.
		s.featureDisabled(w, "media storage")
		return
	}
	key := chi.URLParam(r, "objectKey")
	// Confirm the caller's tenant owns this object key, and that the caller may
	// read an attribute referencing it, before serving the bytes. Keys are flat
	// ULIDs in a shared blob namespace and leak into value payloads, exports and
	// revision snapshots, so streaming one unchecked is a cross-tenant file read
	// (IDOR); serving one for an unreadable attribute defeats the field ACL that
	// masks the same value on every other read surface. Either failure is a
	// NotFound — the same response as a missing key, so neither ownership nor
	// permission is probeable.
	readable, err := application.FromContext(r.Context()).Values().MediaKeyReadable(r.Context(), key)
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	if !readable {
		writeError(w, s.log, domainerrors.NewNotFound("media", key))
		return
	}
	rc, mime, err := s.blobs.Open(r.Context(), key)
	if err != nil {
		writeError(w, s.log, domainerrors.NewNotFound("media", key))
		return
	}
	defer func() { _ = rc.Close() }()

	if mime != "" {
		w.Header().Set("Content-Type", mime)
	}
	// Force a download and forbid sniffing: an unrestricted media attribute
	// accepts any bytes, so HTML/JS uploaded to it must never render inline
	// from this origin (stored XSS). attachment downloads it, nosniff stops
	// the browser second-guessing the declared type, and the locked CSP
	// neutralises any active content should it be viewed anyway.
	w.Header().Set("Content-Disposition", "attachment")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Cache-Control", "private, max-age=300")
	_, _ = io.Copy(w, rc)
}

// signedURLResponse is the link and when it stops working.
type signedURLResponse struct {
	URL       string `json:"url"`
	ExpiresAt string `json:"expires_at"`
}

// signMediaURL mints a signed, expiring link to one stored object.
//
// It is a GET because it CHANGES NOTHING: it hands back a capability to read
// an object the caller can already read. As a POST it was unreachable to a
// read-only credential — including the cross-tenant reader, which is the
// caller with the strongest reason to want a link and no way to write.
//
// It is gated on exactly the check the authenticated download makes: the
// caller's tenant must own a value referencing the key, and the caller must be
// able to READ the attribute holding it. A caller who cannot fetch the bytes
// must not be able to mint a link that lets anyone else fetch them.
func (s *server) signMediaURL(w http.ResponseWriter, r *http.Request) {
	if s.blobs == nil {
		s.featureDisabled(w, "media storage")
		return
	}
	if s.mediaSigner == nil {
		// A capability gap, not a caller error: no configuration of this
		// request can make the deployment able to answer.
		s.featureDisabled(w, "signed media URLs (set FLEXITYPE_MEDIA_URL_SECRET)")
		return
	}

	key := chi.URLParam(r, "objectKey")
	readable, err := application.FromContext(r.Context()).Values().MediaKeyReadable(r.Context(), key)
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	// A NotFound for both "no such key" and "not yours", so neither ownership
	// nor permission is probeable — the same answer the download gives.
	if !readable {
		writeError(w, s.log, domainerrors.NewNotFound("media", key))
		return
	}

	ttl := 0
	if raw := r.URL.Query().Get("ttl_seconds"); raw != "" {
		parsed, perr := strconv.Atoi(raw)
		if perr != nil || parsed < 0 {
			writeError(w, s.log, domainerrors.NewValidation("ttl_seconds must be a non-negative integer"))
			return
		}
		ttl = parsed
	}

	tenant := uow.TenantFromContext(r.Context()).String()
	token, expires, err := s.mediaSigner.Sign(
		tenant, key, time.Duration(ttl)*time.Second, s.now())
	if err != nil {
		writeError(w, s.log, domainerrors.NewValidation(err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, signedURLResponse{
		URL:       "/media/signed/" + token,
		ExpiresAt: expires.Format(time.RFC3339),
	})
}

// downloadSignedMedia serves an object to a caller holding a valid link.
//
// It is UNAUTHENTICATED on purpose: the signature is the credential, and the
// whole reason the link exists is that a public surface holds no tenant token.
// The tenant comes out of the SIGNED token, never from the request, so a
// holder cannot point a valid signature at another tenant's object.
//
// Every failure — malformed, forged, expired, unknown key, media disabled — is
// the same 404. A link is a bearer credential, and distinguishing "expired"
// from "forged" tells a probing holder which half to work on.
// tokenOwnsKey reports whether an object key is recorded under the tenant a
// signed token names.
//
// This route is outside authentication — the signature IS the credential — so
// there is no principal on the context to read the tenant from. The tenant
// comes from the verified claims, and nothing else.
func (s *server) tokenOwnsKey(ctx context.Context, tenant, objectKey string) bool {
	if s.factory == nil {
		return false
	}
	owned, err := s.factory.New(uow.WithTenant(ctx, valueobjects.TenantID(tenant))).
		Values().MediaKeyBelongsToTenant(ctx, valueobjects.TenantID(tenant), objectKey)
	if err != nil {
		return false
	}
	return owned
}

func (s *server) downloadSignedMedia(w http.ResponseWriter, r *http.Request) {
	notFound := func() {
		http.Error(w, "not found", http.StatusNotFound)
	}
	if s.blobs == nil || s.mediaSigner == nil {
		notFound()
		return
	}
	claims, err := s.mediaSigner.Verify(chi.URLParam(r, "token"), s.now())
	if err != nil {
		notFound()
		return
	}
	// ENFORCE the tenant the token carries.
	//
	// The signature covers the tenant and the key, and a token can only be
	// minted for a key its tenant already owns — so today no token can name
	// another tenant's object, because blob keys are globally unique ULIDs.
	// That made this check redundant and its absence invisible: the guarantee
	// rested on the shape of the keyspace rather than on the claim the
	// package documents. A future keyspace that is unique only per tenant
	// would turn that into a cross-tenant read with nothing here to stop it.
	//
	// The blob store is a flat global keyspace, so ownership is resolved
	// through the value that records the key.
	if !s.tokenOwnsKey(r.Context(), claims.Tenant, claims.ObjectKey) {
		notFound()
		return
	}
	rc, mime, err := s.blobs.Open(r.Context(), claims.ObjectKey)
	if err != nil {
		notFound()
		return
	}
	defer func() { _ = rc.Close() }()

	if mime != "" {
		w.Header().Set("Content-Type", mime)
	}
	// The bytes are tenant-supplied and this route is public, so they must
	// never render as active content on this origin.
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Content-Disposition", "inline")
	// PUBLIC, because a shared cache holding this is the point.
	//
	// The whole reason a signed link exists is that a public surface — a
	// storefront, a catalogue page, an email — can name an image without
	// holding a tenant credential, and a CDN in front of it can serve the
	// bytes. `private` forbids exactly that shared cache, so every redemption
	// reached origin and the feature delivered none of what it was built for.
	// It was copied from the authenticated route, where private IS right
	// because that route needs a credential.
	//
	// Safe here because the URL is the capability: the signature is part of
	// it, so it is the cache key, and max-age never outlives the token. Once
	// the link expires a cache must revalidate, and the signature no longer
	// verifies. immutable says what is already true — the object key is a
	// ULID naming immutable bytes, so a cached response can never be stale.
	//
	// A negative age can only come from a token already past its expiry,
	// which Verify has rejected above; clamping keeps a malformed header out
	// of the response either way.
	maxAge := int(time.Until(claims.ExpiresAt).Seconds())
	if maxAge < 0 {
		maxAge = 0
	}
	w.Header().Set("Cache-Control", "public, max-age="+strconv.Itoa(maxAge)+", immutable")
	w.WriteHeader(http.StatusOK)
	if _, err := io.Copy(w, rc); err != nil {
		s.log.Error().Err(err).Msg("stream signed media")
	}
}
