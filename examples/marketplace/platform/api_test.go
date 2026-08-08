package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

// call drives the merchant API with the console credential.
func call(handler http.Handler, method, path, body string) (int, map[string]any, string) {
	var reader *bytes.Reader
	if body != "" {
		reader = bytes.NewReader([]byte(body))
	}
	var req *http.Request
	if reader != nil {
		req = httptest.NewRequest(method, path, reader)
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	req.Header.Set("Authorization", "Bearer console-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	var decoded map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &decoded)
	return rec.Code, decoded, rec.Body.String()
}

// TestMerchantAPIProxiesToTheMerchantsOwnTenant covers the thin merchant API.
//
// It adds nothing the console could not do itself EXCEPT hold the token, so
// the two things worth pinning are that it reaches the right tenant and that
// the token never leaves this process.
func TestMerchantAPIProxiesToTheMerchantsOwnTenant(t *testing.T) {
	Convey("Given an onboarded merchant", t, func() {
		ft := newFlexitype(t)
		store := newTestStore(t)
		sf := newFakeStorefront(t, "internal-token")
		onboarder := newOnboarder(t, store, sf, ft)
		ctx := context.Background()

		tenant := newTenant("api")
		merchant, err := onboarder.Onboard(ctx, OnboardInput{
			ID: tenant, DisplayName: "API Merchant", Tenant: tenant,
		})
		So(err, ShouldBeNil)

		handler := NewAPI(store, onboarder, "console-token", ft.url, quietLogger()).Handler()

		Convey("When the console lists merchants", func() {
			status, _, raw := call(handler, http.MethodGet, "/api/merchants", "")

			Convey("Then the response never carries the service-account token", func() {
				So(status, ShouldEqual, http.StatusOK)
				So(raw, ShouldContainSubstring, "API Merchant")
				So(raw, ShouldNotContainSubstring, merchant.Token)
				So(raw, ShouldNotContainSubstring, merchant.WebhookSecret)
			})
		})

		Convey("When the console calls with no credential", func() {
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/merchants", nil))

			Convey("Then it is refused", func() {
				So(rec.Code, ShouldEqual, http.StatusUnauthorized)
			})
		})

		Convey("When the merchant creates a subtype with its own fields", func() {
			status, body, _ := call(handler, http.MethodPost, "/api/merchants/"+tenant+"/types", `{
				"internal_name":"apparel","display_name":"Apparel",
				"attributes":[
					{"internal_name":"size","display_name":"Size","data_type":"string"},
					{"internal_name":"colour","display_name":"Colour","data_type":"string"}
				]}`)
			So(status, ShouldEqual, http.StatusOK)
			typeID, _ := body["id"].(string)
			So(typeID, ShouldNotBeEmpty)

			Convey("Then the subtype's effective attributes include the inherited ones", func() {
				status, attrs, _ := call(handler, http.MethodGet,
					"/api/merchants/"+tenant+"/types/"+typeID+"/attributes", "")
				So(status, ShouldEqual, http.StatusOK)
				names := effectiveNames(attrs)
				So(names, ShouldContain, "size")
				So(names, ShouldContain, "colour")
				So(names, ShouldContain, "name")
				So(names, ShouldContain, "price")
				So(names, ShouldContain, "status")
			})

			Convey("Then creating the same subtype again is safe", func() {
				status, _, _ := call(handler, http.MethodPost, "/api/merchants/"+tenant+"/types", `{
					"internal_name":"apparel","display_name":"Apparel",
					"attributes":[{"internal_name":"size","display_name":"Size","data_type":"string"}]}`)
				So(status, ShouldEqual, http.StatusOK)
			})

			Convey("When the merchant writes a product", func() {
				status, body, _ := call(handler, http.MethodPut,
					"/api/merchants/"+tenant+"/products/tee-1", `{
						"type":"apparel",
						"values":{"name":"Linen Tee","sku":"TEE-1","status":"active",
						          "price":"19.99","currency":"EUR","in_stock":true,"size":"M"}}`)

				Convey("Then every field lands in one batch", func() {
					So(status, ShouldEqual, http.StatusOK)
					So(body["written"], ShouldEqual, float64(7))
				})

				Convey("Then reading it back gives values keyed by attribute name", func() {
					status, body, _ := call(handler, http.MethodGet,
						"/api/merchants/"+tenant+"/products/tee-1?type=apparel", "")
					So(status, ShouldEqual, http.StatusOK)
					values, _ := body["values"].(map[string]any)
					So(values["name"], ShouldEqual, "Linen Tee")
					So(values["size"], ShouldEqual, "M")
				})

				Convey("Then it is listed by the default FQL query", func() {
					status, body, _ := call(handler, http.MethodGet, "/api/merchants/"+tenant+"/products", "")
					So(status, ShouldEqual, http.StatusOK)
					items, _ := body["items"].([]any)
					So(items, ShouldHaveLength, 1)
				})

				Convey("Then deleting it removes its values", func() {
					status, _, _ := call(handler, http.MethodDelete,
						"/api/merchants/"+tenant+"/products/tee-1?type=apparel", "")
					So(status, ShouldEqual, http.StatusNoContent)

					_, body, _ := call(handler, http.MethodGet, "/api/merchants/"+tenant+"/products", "")
					items, _ := body["items"].([]any)
					So(items, ShouldBeEmpty)
				})
			})

			Convey("When the merchant writes an unknown field", func() {
				status, body, _ := call(handler, http.MethodPut,
					"/api/merchants/"+tenant+"/products/tee-2",
					`{"type":"apparel","values":{"no_such_field":"x"}}`)

				Convey("Then it is a 422 that names the field, not a 500", func() {
					So(status, ShouldEqual, http.StatusUnprocessableEntity)
					message, _ := body["error"].(map[string]any)["message"].(string)
					So(message, ShouldContainSubstring, "no_such_field")
				})
			})
		})

		Convey("When the console names a merchant that does not exist", func() {
			status, _, _ := call(handler, http.MethodGet, "/api/merchants/no-such-merchant/types", "")

			Convey("Then it is a 404", func() {
				So(status, ShouldEqual, http.StatusNotFound)
			})
		})
	})
}

// effectiveNames pulls the attribute internal names out of an
// effective-attributes response.
func effectiveNames(body map[string]any) []string {
	items, _ := body["items"].([]any)
	out := []string{}
	for _, raw := range items {
		entry, _ := raw.(map[string]any)
		attr, _ := entry["attribute"].(map[string]any)
		name, _ := attr["internal_name"].(string)
		out = append(out, name)
	}
	return out
}

// TestMerchantTokenNeverReachesALog pins the credential-handling rule that the
// README states: the token is stored, used and never printed.
func TestMerchantTokenNeverReachesALog(t *testing.T) {
	Convey("Given an onboarded merchant and a captured log", t, func() {
		ft := newFlexitype(t)
		store := newTestStore(t)
		sf := newFakeStorefront(t, "internal-token")

		var captured strings.Builder
		onboarder := newOnboarderWithLog(t, store, sf, ft, &captured)
		tenant := newTenant("logsafe")

		merchant, err := onboarder.Onboard(context.Background(), OnboardInput{
			ID: tenant, DisplayName: "Log Safe", Tenant: tenant,
		})
		So(err, ShouldBeNil)

		Convey("Then onboarding logged the merchant but not its credential", func() {
			So(captured.String(), ShouldContainSubstring, tenant)
			So(captured.String(), ShouldNotContainSubstring, merchant.Token)
			So(captured.String(), ShouldNotContainSubstring, merchant.WebhookSecret)
		})
	})
}
