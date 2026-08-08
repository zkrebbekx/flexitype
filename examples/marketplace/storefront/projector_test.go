package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
)

// TestProjectorIsIdempotentAndOrderIndependent pins the property the whole
// storefront rests on.
//
// Webhook delivery is at-least-once and unordered. A projector that APPLIED an
// event payload would write "price = 19.99" after the "price = 24.99" that
// superseded it, and keep the stale price until the next write. This projector
// re-reads the entity instead, so the row converges on the current value set
// whatever the order or the number of deliveries.
func TestProjectorIsIdempotentAndOrderIndependent(t *testing.T) {
	Convey("Given a merchant with one product in flexitype", t, func() {
		store := newTestStore(t)
		baseURL, accounts := newFlexitype(t, "merchant-a")
		c := seedMerchant(t, store, baseURL, accounts["merchant-a"], "Merchant A", "secret-a")
		apparel := subtype(t, c, "apparel", "Apparel", "size", "string")
		ctx := context.Background()

		writeProduct(t, c, apparel, "tee-1", map[string]any{
			"name": "Linen Tee", "sku": "TEE-1", "status": "active",
			"price": "19.99", "currency": "EUR", "in_stock": true, "size": "M",
		})
		projector := NewProjector(store, baseURL, 10*time.Second)

		Convey("When the same entity is projected three times", func() {
			for range 3 {
				So(projector.Project(ctx, "merchant-a", apparel, "tee-1"), ShouldBeNil)
			}

			Convey("Then there is exactly one row, with the current values", func() {
				items, err := store.Search(ctx, Filter{})
				So(err, ShouldBeNil)
				So(items, ShouldHaveLength, 1)
				So(items[0].Name, ShouldEqual, "Linen Tee")
				So(*items[0].Price, ShouldEqual, "19.99")
				So(items[0].Subtype, ShouldEqual, "apparel")
				So(items[0].MerchantName, ShouldEqual, "Merchant A")
			})

			Convey("Then the subtype's own field lands in the attributes JSONB", func() {
				items, err := store.Search(ctx, Filter{})
				So(err, ShouldBeNil)
				var attrs map[string]any
				So(json.Unmarshal(items[0].Attributes, &attrs), ShouldBeNil)
				So(attrs["size"], ShouldEqual, "M")
			})
		})

		Convey("When the price changes and an OLD delivery is replayed afterwards", func() {
			// The projection of the first write is the "old" delivery: it is
			// replayed after the price has already moved on.
			So(projector.Project(ctx, "merchant-a", apparel, "tee-1"), ShouldBeNil)
			writeProduct(t, c, apparel, "tee-1", map[string]any{"price": "24.99"})
			So(projector.Project(ctx, "merchant-a", apparel, "tee-1"), ShouldBeNil)
			So(projector.Project(ctx, "merchant-a", apparel, "tee-1"), ShouldBeNil)

			Convey("Then the row carries the CURRENT price, not the replayed one", func() {
				items, err := store.Search(ctx, Filter{})
				So(err, ShouldBeNil)
				So(items, ShouldHaveLength, 1)
				So(*items[0].Price, ShouldEqual, "24.99")
			})
		})

		Convey("When every value of the product is removed", func() {
			So(projector.Project(ctx, "merchant-a", apparel, "tee-1"), ShouldBeNil)
			So(c.Entities().Remove(ctx, apparel, "tee-1"), ShouldBeNil)
			So(projector.Project(ctx, "merchant-a", apparel, "tee-1"), ShouldBeNil)

			Convey("Then the product leaves the catalog", func() {
				items, err := store.Search(ctx, Filter{})
				So(err, ShouldBeNil)
				So(items, ShouldBeEmpty)
			})

			Convey("And projecting it again is still a no-op", func() {
				So(projector.Project(ctx, "merchant-a", apparel, "tee-1"), ShouldBeNil)
			})
		})
	})
}

// TestProjectorPicksUpANewAttribute covers the schema cache.
//
// A merchant can add a field to its subtype at any moment. The cache would
// otherwise name the new attribute nothing for up to its TTL, and the value
// would land in the projection keyed by an opaque id.
func TestProjectorPicksUpANewAttribute(t *testing.T) {
	Convey("Given a merchant whose product is already projected", t, func() {
		store := newTestStore(t)
		baseURL, accounts := newFlexitype(t, "merchant-a")
		c := seedMerchant(t, store, baseURL, accounts["merchant-a"], "Merchant A", "secret-a")
		electronics := subtype(t, c, "electronics", "Electronics", "voltage", "integer")
		ctx := context.Background()

		writeProduct(t, c, electronics, "lamp-1", map[string]any{
			"name": "Desk Lamp", "sku": "LAMP-1", "status": "active", "price": "39.00", "voltage": 12,
		})
		projector := NewProjector(store, baseURL, 10*time.Second)
		So(projector.Project(ctx, "merchant-a", electronics, "lamp-1"), ShouldBeNil)

		Convey("When the merchant adds a field and writes it", func() {
			addAttribute(t, c, electronics, "warranty_months", "integer")
			writeProduct(t, c, electronics, "lamp-1", map[string]any{"warranty_months": 24})
			So(projector.Project(ctx, "merchant-a", electronics, "lamp-1"), ShouldBeNil)

			Convey("Then the new field is named, not keyed by its id", func() {
				items, err := store.Search(ctx, Filter{})
				So(err, ShouldBeNil)
				So(items, ShouldHaveLength, 1)
				var attrs map[string]any
				So(json.Unmarshal(items[0].Attributes, &attrs), ShouldBeNil)
				So(attrs["warranty_months"], ShouldEqual, float64(24))
			})
		})
	})
}

// TestBackfillIsRerunnable covers the repair path.
//
// A subscription only carries what happens after it is registered, so a
// merchant that imported a catalogue before onboarding has no events to
// replay. The backfill must therefore be safe to run at any time, including
// over a catalog it has already projected.
func TestBackfillIsRerunnable(t *testing.T) {
	Convey("Given a merchant with three products written before any subscription", t, func() {
		store := newTestStore(t)
		baseURL, accounts := newFlexitype(t, "merchant-b")
		c := seedMerchant(t, store, baseURL, accounts["merchant-b"], "Merchant B", "secret-b")
		apparel := subtype(t, c, "apparel", "Apparel", "colour", "string")
		ctx := context.Background()

		for _, id := range []string{"p1", "p2", "p3"} {
			writeProduct(t, c, apparel, id, map[string]any{
				"name": "Shirt " + id, "sku": "SKU-" + id, "status": "active", "price": "10.00",
			})
		}
		projector := NewProjector(store, baseURL, 10*time.Second)

		Convey("When the backfill runs", func() {
			count, err := projector.Backfill(ctx, "merchant-b")
			So(err, ShouldBeNil)
			So(count, ShouldEqual, 3)

			Convey("Then all three appear in the catalog", func() {
				items, err := store.Search(ctx, Filter{})
				So(err, ShouldBeNil)
				So(items, ShouldHaveLength, 3)
			})

			Convey("And running it again projects the same three, not six", func() {
				again, err := projector.Backfill(ctx, "merchant-b")
				So(err, ShouldBeNil)
				So(again, ShouldEqual, 3)

				items, err := store.Search(ctx, Filter{})
				So(err, ShouldBeNil)
				So(items, ShouldHaveLength, 3)
			})
		})
	})
}

// TestStorefrontAggregatesHeterogeneousSchemas is the point of the example: a
// shopper sees every merchant's products in one list, even though the two
// merchants declared DIFFERENT subtypes of the shared product type.
func TestStorefrontAggregatesHeterogeneousSchemas(t *testing.T) {
	Convey("Given two merchants with different subtypes", t, func() {
		store := newTestStore(t)
		baseURL, accounts := newFlexitype(t, "merchant-a", "merchant-b")
		ctx := context.Background()

		a := seedMerchant(t, store, baseURL, accounts["merchant-a"], "Alpine Apparel", "secret-a")
		apparel := subtype(t, a, "apparel", "Apparel", "size", "string")
		writeProduct(t, a, apparel, "tee-1", map[string]any{
			"name": "Merino Base Layer", "sku": "A-1", "status": "active", "price": "89.00", "size": "L",
		})

		b := seedMerchant(t, store, baseURL, accounts["merchant-b"], "Bolt Electronics", "secret-b")
		electronics := subtype(t, b, "electronics", "Electronics", "voltage", "integer")
		writeProduct(t, b, electronics, "lamp-1", map[string]any{
			"name": "Merino Wool Lamp Shade", "sku": "B-1", "status": "active", "price": "45.00", "voltage": 12,
		})

		projector := NewProjector(store, baseURL, 10*time.Second)
		_, err := projector.Backfill(ctx, "merchant-a")
		So(err, ShouldBeNil)
		_, err = projector.Backfill(ctx, "merchant-b")
		So(err, ShouldBeNil)

		Convey("When a shopper searches across the whole marketplace", func() {
			items, err := store.Search(ctx, Filter{Query: "merino"})
			So(err, ShouldBeNil)

			Convey("Then both merchants' products come back, each with its own subtype", func() {
				So(items, ShouldHaveLength, 2)
				bySubtype := map[string]Product{}
				for _, item := range items {
					bySubtype[item.Subtype] = item
				}
				So(bySubtype["apparel"].MerchantName, ShouldEqual, "Alpine Apparel")
				So(bySubtype["electronics"].MerchantName, ShouldEqual, "Bolt Electronics")
			})
		})

		Convey("When a shopper filters by merchant and price", func() {
			items, err := store.Search(ctx, Filter{Tenant: "merchant-b", MinPrice: "40", MaxPrice: "50"})
			So(err, ShouldBeNil)

			Convey("Then only that merchant's matching product comes back", func() {
				So(items, ShouldHaveLength, 1)
				So(items[0].EntityID, ShouldEqual, "lamp-1")
			})
		})
	})
}
