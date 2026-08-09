package flexitype_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/mediaurl"
)

// The secret a test deployment signs with. Long enough for the signer's floor.
const testMediaSecret = "media-url-signing-secret-for-tests-0123456789"

// TestSignedMediaURLs covers #552.
//
// Media bytes sit behind the same authentication as the rest of the API, and
// the token carries the tenant, so a public surface — a storefront, a
// catalogue page, an email — could not link to an image at all. It had to
// proxy every request through a service holding a tenant credential, which
// carries the whole file through a process with no other reason to touch it
// and defeats any CDN in front of it. The marketplace storefront does exactly
// that proxying.
func TestSignedMediaURLs(t *testing.T) {
	Convey("Given a service with media storage and link signing", t, func() {
		svc := flexitype.NewInMemory()
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		handler := svc.APIHandler(flexitype.APIConfig{
			AllowAnonymous: true,
			MediaURLSecret: testMediaSecret,
		})

		ia := svc.Interactors(ctx)
		product, err := ia.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "product", DisplayName: "Product",
		})
		So(err, ShouldBeNil)
		photo, err := svc.Interactors(ctx).Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: product.ID.String(), InternalName: "photo",
			DisplayName: "Photo", DataType: "media",
		})
		So(err, ShouldBeNil)

		objectKey := uploadPhoto(handler, product.ID.String(), "p-1", photo.ID.String(), "PNGDATA")
		So(objectKey, ShouldNotBeEmpty)

		Convey("When an authenticated caller mints a link", func() {
			status, body := callMedia(handler, http.MethodGet,
				"/api/v1/media/"+objectKey+"/signed-url?ttl_seconds=600", "")

			Convey("Then it is issued with an expiry", func() {
				So(status, ShouldEqual, http.StatusOK)
				So(body["url"], ShouldNotBeNil)
				So(body["expires_at"], ShouldNotBeNil)
			})

			Convey("Then anyone holding it fetches the bytes with NO credential", func() {
				link, _ := body["url"].(string)
				req := httptest.NewRequest(http.MethodGet, link, nil)
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)

				So(rec.Code, ShouldEqual, http.StatusOK)
				So(rec.Body.String(), ShouldEqual, "PNGDATA")
			})

			Convey("Then the bytes cannot render as active content on this origin", func() {
				// They are tenant-supplied and this route is public.
				link, _ := body["url"].(string)
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, link, nil))

				So(rec.Header().Get("X-Content-Type-Options"), ShouldEqual, "nosniff")
				So(rec.Header().Get("Content-Security-Policy"), ShouldContainSubstring, "sandbox")
			})

			Convey("Then the object key is not readable out of the link", func() {
				link, _ := body["url"].(string)
				So(link, ShouldNotContainSubstring, objectKey)
			})
		})

		Convey("When a link is tampered with", func() {
			status, body := callMedia(handler, http.MethodGet, "/api/v1/media/"+objectKey+"/signed-url", "")
			So(status, ShouldEqual, http.StatusOK)
			link, _ := body["url"].(string)

			Convey("Then a changed signature is a 404, not a hint", func() {
				forged := link[:len(link)-4] + "dead"
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, forged, nil))
				So(rec.Code, ShouldEqual, http.StatusNotFound)
			})

			Convey("And a token from another deployment's key is refused", func() {
				other, oerr := mediaurl.NewSigner(strings.Repeat("z", mediaurl.MinSecretLength))
				So(oerr, ShouldBeNil)
				token, _, terr := other.Sign(valueobjects.DefaultTenant.String(), objectKey, time.Minute, time.Now())
				So(terr, ShouldBeNil)

				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/media/signed/"+token, nil))
				So(rec.Code, ShouldEqual, http.StatusNotFound)
			})
		})

		Convey("When a link is redeemed after it expires", func() {
			signer, serr := mediaurl.NewSigner(testMediaSecret)
			So(serr, ShouldBeNil)
			// Signed an hour ago, for a minute.
			token, _, terr := signer.Sign(valueobjects.DefaultTenant.String(), objectKey,
				time.Minute, time.Now().Add(-time.Hour))
			So(terr, ShouldBeNil)

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/media/signed/"+token, nil))

			Convey("Then it is a 404, the same answer a forged link gets", func() {
				// Distinguishing expired from forged tells a probing holder
				// which half to work on.
				So(rec.Code, ShouldEqual, http.StatusNotFound)
			})
		})

		Convey("When a link is minted for an object that does not exist", func() {
			status, _ := callMedia(handler, http.MethodGet,
				"/api/v1/media/01JBQ8Z0000000000000000000/signed-url", "")

			Convey("Then it is a 404: a caller cannot mint a link to a key it cannot read", func() {
				So(status, ShouldEqual, http.StatusNotFound)
			})
		})

		Convey("When no lifetime is asked for", func() {
			status, body := callMedia(handler, http.MethodGet, "/api/v1/media/"+objectKey+"/signed-url", "")
			So(status, ShouldEqual, http.StatusOK)

			Convey("Then the link is short-lived rather than unbounded", func() {
				expires, perr := time.Parse(time.RFC3339, body["expires_at"].(string))
				So(perr, ShouldBeNil)
				So(expires.Sub(time.Now().UTC()), ShouldBeLessThanOrEqualTo, mediaurl.DefaultTTL)
			})
		})

		Convey("When a year-long link is asked for", func() {
			status, body := callMedia(handler, http.MethodGet,
				"/api/v1/media/"+objectKey+"/signed-url?ttl_seconds=31536000", "")
			So(status, ShouldEqual, http.StatusOK)

			Convey("Then it is capped at the maximum rather than granted", func() {
				expires, perr := time.Parse(time.RFC3339, body["expires_at"].(string))
				So(perr, ShouldBeNil)
				So(expires.Sub(time.Now().UTC()), ShouldBeLessThanOrEqualTo, mediaurl.MaxTTL)
			})
		})
	})

	Convey("Given a service with media storage but NO signing secret", t, func() {
		svc := flexitype.NewInMemory()
		handler := svc.APIHandler(flexitype.APIConfig{AllowAnonymous: true})

		Convey("When a link is asked for", func() {
			status, body := callMedia(handler, http.MethodGet,
				"/api/v1/media/01JBQ8Z0000000000000000000/signed-url", "")

			Convey("Then the capability is reported as disabled, not as a bad request", func() {
				// A retry cannot make this deployment able to answer, so a 422
				// would send a client into a retry loop against a structural
				// gap.
				So(status, ShouldEqual, http.StatusNotImplemented)
				raw, _ := json.Marshal(body)
				So(string(raw), ShouldContainSubstring, "FLEXITYPE_MEDIA_URL_SECRET")
			})
		})

		Convey("When a signed path is fetched anyway", func() {
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/media/signed/anything", nil))

			Convey("Then it is a 404: no link can be valid here", func() {
				So(rec.Code, ShouldEqual, http.StatusNotFound)
			})
		})
	})
}

// TestSignedMediaURLRejectsAWeakSecret pins the floor on the signing key.
func TestSignedMediaURLRejectsAWeakSecret(t *testing.T) {
	Convey("Given a deployment configured with a short signing key", t, func() {
		svc := flexitype.NewInMemory()

		Convey("When the API handler is built", func() {
			_, err := svc.NewAPIHandler(flexitype.APIConfig{
				AllowAnonymous: true, MediaURLSecret: "short",
			})

			Convey("Then it is refused at start-up: every link ever issued would be forgeable", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "at least")
			})
		})
	})
}

// call drives the API and decodes the JSON body.
func callMedia(handler http.Handler, method, path, body string) (int, map[string]any) {
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	var decoded map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &decoded)
	return rec.Code, decoded
}

// uploadPhoto stores one file through the API and returns its object key.
func uploadPhoto(handler http.Handler, typeID, entityID, attributeID, content string) string {
	var buf bytes.Buffer
	form := multipart.NewWriter(&buf)
	part, err := form.CreateFormFile("file", "photo.png")
	if err != nil {
		return ""
	}
	if _, err := io.WriteString(part, content); err != nil {
		return ""
	}
	if err := form.Close(); err != nil {
		return ""
	}

	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/entities/"+typeID+"/"+entityID+"/attributes/"+attributeID+"/media", &buf)
	req.Header.Set("Content-Type", form.FormDataContentType())
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var body struct {
		Value struct {
			ObjectKey string `json:"object_key"`
		} `json:"value"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return body.Value.ObjectKey
}
