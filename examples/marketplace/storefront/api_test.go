package main

import (
	"context"
	"encoding/json"
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

		handler := NewAPI(store, projector, "internal-token", quietLogger()).Handler(
			NewIngest(store, NewDebouncer(0, func(context.Context, entityKey) error { return nil }, quietLogger()), quietLogger()))

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
			status, _ := shopperGet(handler, "/api/products/merchant-a/draft-1")

			Convey("Then it is a 404: a draft is not reachable by guessing", func() {
				So(status, ShouldEqual, http.StatusNotFound)
			})
		})

		Convey("When a shopper asks for the archived product by its exact id", func() {
			status, _ := shopperGet(handler, "/api/products/merchant-a/gone-1")

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
		handler := NewAPI(store, projector, "internal-token", quietLogger()).Handler(
			NewIngest(store, NewDebouncer(0, func(context.Context, entityKey) error { return nil }, quietLogger()), quietLogger()))

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
				_, body := shopperGet(handler, "/api/merchants")
				raw, err := json.Marshal(body)
				So(err, ShouldBeNil)
				So(string(raw), ShouldNotContainSubstring, "ft_a_b")
				So(string(raw), ShouldContainSubstring, "Merchant A")
			})
		})
	})
}
