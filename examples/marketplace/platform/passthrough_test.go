package main

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

// TestFlexitypePassthrough covers the read-only proxy the console's TypeScript
// SDK client points at.
//
// The console must be able to speak the REAL flexitype API — that is what lets
// it use the SDK's services, hooks and soft-typing helpers unchanged — while
// the browser holds no merchant credential. This proxy is the whole of that
// bargain, so the parts worth pinning are: it reaches the right tenant, it
// refuses a write, it refuses the admin API, and it never echoes the token.
func TestFlexitypePassthrough(t *testing.T) {
	Convey("Given an onboarded merchant and the console credential", t, func() {
		ft := newFlexitype(t)
		store := newTestStore(t)
		sf := newFakeStorefront(t, "internal-token")
		onboarder := newOnboarder(t, store, sf, ft)
		ctx := context.Background()

		tenant := newTenant("through")
		merchant, err := onboarder.Onboard(ctx, OnboardInput{
			ID: tenant, DisplayName: "Through Merchant", Tenant: tenant,
		})
		So(err, ShouldBeNil)
		handler := NewAPI(store, onboarder, "console-token", ft.url, quietLogger()).Handler()

		base := "/api/merchants/" + merchant.ID + "/flexitype/api/v1"

		Convey("When the console lists type definitions through it", func() {
			status, body, raw := call(handler, http.MethodGet, base+"/type-definitions?limit=100", "")

			Convey("Then flexitype answers with that merchant's own schema", func() {
				So(status, ShouldEqual, http.StatusOK)
				items, ok := body["items"].([]any)
				So(ok, ShouldBeTrue)
				names := []string{}
				for _, item := range items {
					entry, _ := item.(map[string]any)
					name, _ := entry["internal_name"].(string)
					names = append(names, name)
				}
				// The ecommerce template onboarding applied.
				So(names, ShouldContain, "product")
			})

			Convey("Then the merchant's token is not in the response", func() {
				So(raw, ShouldNotContainSubstring, merchant.Token)
			})
		})

		Convey("When a second merchant's console reads through its own path", func() {
			other := newTenant("through-other")
			second, oerr := onboarder.Onboard(ctx, OnboardInput{
				ID: other, DisplayName: "Other Merchant", Tenant: other,
			})
			So(oerr, ShouldBeNil)

			first, _, _ := call(handler, http.MethodGet, base+"/type-definitions?limit=100", "")
			status, body, _ := call(handler, http.MethodGet,
				"/api/merchants/"+second.ID+"/flexitype/api/v1/type-definitions?limit=100", "")

			Convey("Then each reads its own tenant, and the ids differ", func() {
				So(first, ShouldEqual, http.StatusOK)
				So(status, ShouldEqual, http.StatusOK)
				items, _ := body["items"].([]any)
				So(len(items), ShouldBeGreaterThan, 0)
				// Applying a template COPIES it, so the same internal name is a
				// different row in each tenant.
				So(typeIDsOf(t, handler, base), ShouldNotResemble,
					typeIDsOf(t, handler, "/api/merchants/"+second.ID+"/flexitype/api/v1"))
			})
		})

		Convey("When the console tries to WRITE through it", func() {
			status, body, _ := call(handler, http.MethodPost, base+"/type-definitions",
				`{"internal_name":"sneaky","display_name":"Sneaky"}`)

			Convey("Then it is refused, and told where a write belongs", func() {
				So(status, ShouldEqual, http.StatusMethodNotAllowed)
				message, _ := body["error"].(map[string]any)["message"].(string)
				So(message, ShouldContainSubstring, "read-only")
			})

			Convey("And nothing was created", func() {
				_, listed, _ := call(handler, http.MethodGet, base+"/type-definitions?limit=100", "")
				So(listed["items"], ShouldNotBeNil)
				items, _ := listed["items"].([]any)
				for _, item := range items {
					entry, _ := item.(map[string]any)
					So(entry["internal_name"], ShouldNotEqual, "sneaky")
				}
			})
		})

		Convey("When the console reaches for the admin API through it", func() {
			status, _, _ := call(handler, http.MethodGet, base+"/admin/service-accounts?tenant_name=default", "")

			Convey("Then it is refused before the request is made", func() {
				So(status, ShouldEqual, http.StatusForbidden)
			})
		})

		Convey("When an unknown merchant is named in the path", func() {
			status, _, _ := call(handler, http.MethodGet,
				"/api/merchants/nobody/flexitype/api/v1/type-definitions", "")

			Convey("Then it is a 404, not somebody else's catalog", func() {
				So(status, ShouldEqual, http.StatusNotFound)
			})
		})

		Convey("When a caller presents no console credential", func() {
			req := httptest.NewRequest(http.MethodGet, base+"/type-definitions", nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			Convey("Then the passthrough is closed too, like every other route", func() {
				So(rec.Code, ShouldEqual, http.StatusUnauthorized)
				So(rec.Body.String(), ShouldNotContainSubstring, merchant.Token)
			})
		})

		Convey("When a caller sends an Authorization header of its own", func() {
			// The console credential authenticates the request; the merchant is
			// taken from the PATH. A caller cannot swap tenants by sending a
			// different bearer token, because this one is replaced.
			req := httptest.NewRequest(http.MethodGet, base+"/type-definitions?limit=1", nil)
			req.Header.Set("Authorization", "Bearer console-token")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			Convey("Then the merchant's own token is what reaches flexitype", func() {
				So(rec.Code, ShouldEqual, http.StatusOK)
			})
		})
	})
}

// typeIDsOf reads the type-definition ids visible through one passthrough base.
func typeIDsOf(t *testing.T, handler http.Handler, base string) []string {
	t.Helper()
	_, body, _ := call(handler, http.MethodGet, base+"/type-definitions?limit=100", "")
	items, _ := body["items"].([]any)
	ids := []string{}
	for _, item := range items {
		entry, _ := item.(map[string]any)
		if id, ok := entry["id"].(string); ok {
			ids = append(ids, id)
		}
	}
	return ids
}

// captureLogger writes structured log lines into a buffer a test can read.
func captureLogger(into *strings.Builder) Logger {
	return slog.New(slog.NewTextHandler(into, nil))
}

// TestPassthroughDoesNotLogTheToken pins that a failed forward cannot put the
// credential in an operator's log.
func TestPassthroughDoesNotLogTheToken(t *testing.T) {
	Convey("Given a passthrough pointed at an address that refuses connections", t, func() {
		var captured strings.Builder
		store := newTestStore(t)
		ctx := context.Background()
		So(store.Save(ctx, Merchant{
			ID: "alpine", Tenant: "alpine", DisplayName: "Alpine",
			Token: "sa_secret_token_value", WebhookSecret: "whsec",
		}), ShouldBeNil)

		through := newPassthrough(store, "http://127.0.0.1:1", captureLogger(&captured))

		Convey("When a read is forwarded and the connection fails", func() {
			req := httptest.NewRequest(http.MethodGet, "/api/merchants/alpine/flexitype/api/v1/type-definitions", nil)
			req.SetPathValue("id", "alpine")
			req.SetPathValue("path", "type-definitions")
			rec := httptest.NewRecorder()
			through.handle(rec, req)

			Convey("Then the caller gets a gateway error", func() {
				So(rec.Code, ShouldEqual, http.StatusBadGateway)
			})

			Convey("And neither the log nor the response carries the token", func() {
				So(captured.String(), ShouldNotContainSubstring, "sa_secret_token_value")
				So(rec.Body.String(), ShouldNotContainSubstring, "sa_secret_token_value")
			})
		})
	})
}
