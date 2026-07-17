package http

import (
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/zkrebbekx/flexitype/application"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
)

// maxMediaUpload caps a media upload request body.
const maxMediaUpload = 32 << 20 // 32 MiB

// uploadMedia stores an uploaded file for a media attribute and writes the
// resulting media value. The multipart form carries the file as "file".
func (s *server) uploadMedia(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxMediaUpload)
	if err := r.ParseMultipartForm(maxMediaUpload); err != nil {
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
		writeError(w, s.log, domainerrors.NewValidation("media storage is not configured"))
		return
	}
	key := chi.URLParam(r, "objectKey")
	// Confirm the caller's tenant owns this object key before serving it. Keys
	// are flat ULIDs in a shared blob namespace and leak into value payloads,
	// exports and revision snapshots, so streaming one unchecked is a
	// cross-tenant file read (IDOR). A mismatch is a NotFound — the same
	// response as a missing key, so ownership isn't probeable.
	owned, err := application.FromContext(r.Context()).Values().MediaKeyOwned(r.Context(), key)
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	if !owned {
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
