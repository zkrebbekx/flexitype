package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
)

// shopperGet drives the shopper API and returns the decoded body.
func shopperGet(handler http.Handler, path string) (int, map[string]any) {
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec.Code, body
}

// entityIDs pulls the entity ids out of a shopper list response.
func entityIDs(body map[string]any) []string {
	items, _ := body["items"].([]any)
	out := []string{}
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		id, _ := item["entity_id"].(string)
		out = append(out, id)
	}
	return out
}

// TestOnlyActiveProductsReachShoppers pins the visibility rule.
//
// A draft is a merchant's unfinished work and an archived product is one it
// withdrew. Neither is an offer. Leaking either would publish an unreleased
// product — a real commercial harm — so the rule is enforced in the store,
// on every read path, rather than in each handler.
func TestOnlyActiveProductsReachShoppers(t *testing.T) {
	Convey("Given a merchant with one active, one draft and one archived product", t, func() {
		store := newTestStore(t)
		baseURL, accounts := newFlexitype(t, "merchant-a")
		c := seedMerchant(t, store, baseURL, accounts["merchant-a"], "Merchant A", "secret-a")
		apparel := subtype(t, c, "apparel", "Apparel", "size", "string")
		ctx := context.Background()

		writeProduct(t, c, apparel, "live-1", map[string]any{
			"name": "Published Jacket", "sku": "LIVE-1", "status": "active", "price": "120.00",
		})
		writeProduct(t, c, apparel, "draft-1", map[string]any{
			"name": "Secret Jacket", "sku": "DRAFT-1", "status": "draft", "price": "130.00",
		})
		writeProduct(t, c, apparel, "gone-1", map[string]any{
			"name": "Withdrawn Jacket", "sku": "GONE-1", "status": "archived", "price": "140.00",
		})

		projector := NewProjector(store, baseURL, 10*time.Second)
		count, err := projector.Backfill(ctx, "merchant-a")
		So(err, ShouldBeNil)
		So(count, ShouldEqual, 3) // all three ARE projected; only the read path hides two

		handler := NewAPI("merchant-a", store, projector, "internal-token", "", quietLogger()).Handler(
			NewIngest("merchant-a", store, NewDebouncer(0, func(context.Context, entityKey) error { return nil }, quietLogger()), quietLogger()))

		Convey("When a shopper lists the catalog", func() {
			status, body := shopperGet(handler, "/api/products")

			Convey("Then only the active product is there", func() {
				So(status, ShouldEqual, http.StatusOK)
				So(entityIDs(body), ShouldResemble, []string{"live-1"})
			})
		})

		Convey("When a shopper searches for a word only the draft carries", func() {
			status, body := shopperGet(handler, "/api/products?q=secret")

			Convey("Then the draft does not surface through search either", func() {
				So(status, ShouldEqual, http.StatusOK)
				So(entityIDs(body), ShouldBeEmpty)
			})
		})

		Convey("When a shopper asks for the draft by its exact id", func() {
			status, _ := shopperGet(handler, "/api/products/draft-1")

			Convey("Then it is a 404: a draft is not reachable by guessing", func() {
				So(status, ShouldEqual, http.StatusNotFound)
			})
		})

		Convey("When a shopper asks for the archived product by its exact id", func() {
			status, _ := shopperGet(handler, "/api/products/gone-1")

			Convey("Then it is a 404", func() {
				So(status, ShouldEqual, http.StatusNotFound)
			})
		})

		Convey("When a shopper passes status=draft as a filter", func() {
			status, body := shopperGet(handler, "/api/products?status=draft")

			Convey("Then nothing comes back: the filter cannot widen what is visible", func() {
				So(status, ShouldEqual, http.StatusOK)
				So(entityIDs(body), ShouldBeEmpty)
			})
		})

		Convey("When the merchant publishes the draft", func() {
			writeProduct(t, c, apparel, "draft-1", map[string]any{"status": "active"})
			So(projector.Project(ctx, "merchant-a", apparel, "draft-1"), ShouldBeNil)

			Convey("Then it becomes visible with no second backfill", func() {
				_, body := shopperGet(handler, "/api/products")
				So(entityIDs(body), ShouldHaveLength, 2)
			})
		})

		Convey("When the merchant archives the live product", func() {
			writeProduct(t, c, apparel, "live-1", map[string]any{"status": "archived"})
			So(projector.Project(ctx, "merchant-a", apparel, "live-1"), ShouldBeNil)

			Convey("Then it disappears from the storefront", func() {
				_, body := shopperGet(handler, "/api/products")
				So(entityIDs(body), ShouldBeEmpty)
			})
		})
	})
}

// TestInternalEndpointsRequireTheSharedCredential covers the platform-only
// surface. Merchant registration hands over a service-account token, so an
// unauthenticated caller must not reach it.
func TestInternalEndpointsRequireTheSharedCredential(t *testing.T) {
	Convey("Given a running storefront", t, func() {
		store := newTestStore(t)
		projector := NewProjector(store, "http://127.0.0.1:1", time.Second)
		handler := NewAPI("merchant-a", store, projector, "internal-token", "", quietLogger()).Handler(
			NewIngest("merchant-a", store, NewDebouncer(0, func(context.Context, entityKey) error { return nil }, quietLogger()), quietLogger()))

		Convey("When a caller registers a merchant with no credential", func() {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPut, "/internal/merchants/merchant-a",
				jsonBody(`{"display_name":"X","token":"ft_a_b","webhook_secret":"s"}`))
			handler.ServeHTTP(rec, req)

			Convey("Then it is refused", func() {
				So(rec.Code, ShouldEqual, http.StatusUnauthorized)
			})
		})

		Convey("When a caller starts a backfill with the wrong credential", func() {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/internal/merchants/merchant-a/backfill", nil)
			req.Header.Set("X-Internal-Token", "guessed")
			handler.ServeHTTP(rec, req)

			Convey("Then it is refused", func() {
				So(rec.Code, ShouldEqual, http.StatusUnauthorized)
			})
		})

		Convey("When the platform registers a merchant with the shared credential", func() {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPut, "/internal/merchants/merchant-a",
				jsonBody(`{"display_name":"Merchant A","token":"ft_a_b","webhook_secret":"s"}`))
			req.Header.Set("X-Internal-Token", "internal-token")
			handler.ServeHTTP(rec, req)

			Convey("Then it is accepted", func() {
				So(rec.Code, ShouldEqual, http.StatusNoContent)
			})

			Convey("And the shopper merchant list never carries the token", func() {
				_, body := shopperGet(handler, "/api/store")
				raw, err := json.Marshal(body)
				So(err, ShouldBeNil)
				So(string(raw), ShouldNotContainSubstring, "ft_a_b")
				So(string(raw), ShouldContainSubstring, "Merchant A")
			})
		})
	})
}

// TestProductImageRedirectsToASignedLink covers the image path.
//
// The bytes live behind a merchant credential, so this storefront used to
// proxy every image: the whole file crossed a process that has no other reason
// to touch it, and no CDN in front of the storefront could cache it. A signed
// link moves the fetch to the browser without giving it a merchant token.
func TestProductImageRedirectsToASignedLink(t *testing.T) {
	Convey("Given a merchant with a product image", t, func() {
		store := newTestStore(t)
		baseURL, accounts := newFlexitype(t, "merchant-a")
		c := seedMerchant(t, store, baseURL, accounts["merchant-a"], "Merchant A", "secret-a")
		apparel := subtype(t, c, "apparel", "Apparel", "size", "string")
		ctx := context.Background()

		writeProduct(t, c, apparel, "shot-1", map[string]any{
			"name": "Photographed Jacket", "sku": "IMG-1", "status": "active", "price": "99.00",
		})
		imageAttr := attrID(t, c, apparel, "image")
		// A real 1x1 PNG: the media constraint checks the SNIFFED type, so
		// arbitrary bytes are refused as "media type is not allowed".
		pixel, derr := base64.StdEncoding.DecodeString(
			"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==")
		So(derr, ShouldBeNil)
		_, err := c.Entities().UploadMedia(ctx, apparel, "shot-1", imageAttr,
			"photo.png", "image/png", bytes.NewReader(pixel))
		So(err, ShouldBeNil)

		projector := NewProjector(store, baseURL, 10*time.Second)
		So(projector.Project(ctx, "merchant-a", apparel, "shot-1"), ShouldBeNil)

		Convey("When the storefront knows the address a browser reaches flexitype on", func() {
			handler := NewAPI("merchant-a", store, projector, "internal-token", baseURL, quietLogger()).Handler(
				NewIngest("merchant-a", store, NewDebouncer(0, func(context.Context, entityKey) error { return nil }, quietLogger()), quietLogger()))

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/products/shot-1/image", nil))

			Convey("Then the shopper is redirected instead of being served the bytes", func() {
				So(rec.Code, ShouldEqual, http.StatusFound)
				// The response is a redirect page, not the image: the bytes never
				// pass through this process.
				So(bytes.Contains(rec.Body.Bytes(), pixel), ShouldBeFalse)
				So(rec.Header().Get("Location"), ShouldStartWith, baseURL+"/media/signed/")
			})

			Convey("And the link carries no merchant token", func() {
				So(rec.Header().Get("Location"), ShouldNotContainSubstring, accounts["merchant-a"].token)
			})

			Convey("And following it with no credential returns the bytes", func() {
				resp, gerr := http.Get(rec.Header().Get("Location"))
				So(gerr, ShouldBeNil)
				defer func() { _ = resp.Body.Close() }()
				body, rerr := io.ReadAll(resp.Body)
				So(rerr, ShouldBeNil)
				So(resp.StatusCode, ShouldEqual, http.StatusOK)
				So(body, ShouldResemble, pixel)
			})
		})

		Convey("When no public address is configured", func() {
			handler := NewAPI("merchant-a", store, projector, "internal-token", "", quietLogger()).Handler(
				NewIngest("merchant-a", store, NewDebouncer(0, func(context.Context, entityKey) error { return nil }, quietLogger()), quietLogger()))

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/products/shot-1/image", nil))

			Convey("Then it proxies the bytes as it always did", func() {
				So(rec.Code, ShouldEqual, http.StatusOK)
				So(rec.Body.Bytes(), ShouldResemble, pixel)
			})
		})
	})
}

// TestAStorefrontServesOneMerchant is the property that replaced the
// cross-tenant one.
//
// An earlier version of this example ran ONE storefront across every merchant.
// That forced it to hold every merchant's credential, and then a cross-tenant
// scope in the service to avoid holding them. Both were the wrong shape: the
// tenant comes from the token, and a storefront per merchant needs no
// privilege that crosses a tenant boundary at all.
//
// What holds it now is that this process knows one tenant, and refuses
// everything else at the edges.
func TestAStorefrontServesOneMerchant(t *testing.T) {
	Convey("Given a storefront deployed for merchant-a", t, func() {
		store := newTestStore(t)
		ctx := context.Background()
		So(store.UpsertMerchant(ctx, Merchant{
			Tenant: "merchant-a", DisplayName: "Merchant A",
			Token: "ft_a_b", WebhookSecret: "secret-a",
		}), ShouldBeNil)
		// A row for another merchant, as a shared database would have.
		So(store.UpsertMerchant(ctx, Merchant{
			Tenant: "merchant-b", DisplayName: "Merchant B",
			Token: "ft_b_c", WebhookSecret: "secret-b",
		}), ShouldBeNil)
		So(store.UpsertProduct(ctx, Product{
			Tenant: "merchant-b", EntityID: "other-1", TypeID: "t", Subtype: "apparel",
			Name: "Somebody else's jacket", SKU: "B-1", Status: "active",
		}), ShouldBeNil)

		projector := NewProjector(store, "http://127.0.0.1:1", time.Second)
		handler := NewAPI("merchant-a", store, projector, "internal-token", "", quietLogger()).Handler(
			NewIngest("merchant-a", store, NewDebouncer(0, func(context.Context, entityKey) error { return nil }, quietLogger()), quietLogger()))

		Convey("When a shopper lists the catalog", func() {
			_, body := shopperGet(handler, "/api/products")

			Convey("Then another merchant's product is not in it", func() {
				So(entityIDs(body), ShouldBeEmpty)
			})
		})

		Convey("When a shopper asks for another merchant's product by id", func() {
			status, _ := shopperGet(handler, "/api/products/other-1")

			Convey("Then it is a 404: there is no path that names another tenant", func() {
				So(status, ShouldEqual, http.StatusNotFound)
			})
		})

		Convey("When the platform registers a DIFFERENT merchant's credential", func() {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPut, "/internal/merchants/merchant-b",
				jsonBody(`{"display_name":"Merchant B","token":"ft_b_c","webhook_secret":"s"}`))
			req.Header.Set("X-Internal-Token", "internal-token")
			handler.ServeHTTP(rec, req)

			Convey("Then it is refused: this process holds one merchant's token", func() {
				So(rec.Code, ShouldEqual, http.StatusForbidden)
				So(rec.Body.String(), ShouldContainSubstring, "merchant-a")
			})
		})

		Convey("When a delivery arrives on another merchant's hook path", func() {
			env := valueEnvelope("e1", "merchant-b", "type-1", "p-1")
			rec := httptest.NewRecorder()
			rec2 := httptest.NewRecorder()
			handler.ServeHTTP(rec, signedDelivery("merchant-b", "secret-b", env, time.Now()))
			handler.ServeHTTP(rec2, signedDelivery("merchant-b", "guessed", env, time.Now()))

			Convey("Then it is refused, and looks exactly like a bad signature", func() {
				// Answering differently would make the set of merchants a
				// storefront serves probeable from outside.
				So(rec.Code, ShouldEqual, http.StatusUnauthorized)
				So(rec2.Code, ShouldEqual, http.StatusUnauthorized)
				So(rec.Body.String(), ShouldEqual, rec2.Body.String())
			})
		})
	})
}
