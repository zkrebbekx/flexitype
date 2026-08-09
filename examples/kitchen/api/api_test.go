package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/client"
)

func quietLogger() Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// kitchen boots a real flexitype, applies the schema, and returns the API in
// front of it. Nothing is faked: the costs below are the service's own.
func kitchen(t *testing.T) http.Handler {
	t.Helper()
	ctx := context.Background()
	base := newFlexitype(t)

	c, err := client.New(base)
	So(err, ShouldBeNil)
	So(ensureSchema(ctx, c, quietLogger()), ShouldBeNil)

	api, err := NewAPI(ctx, c, quietLogger())
	So(err, ShouldBeNil)
	return api.Handler()
}

// call drives the API and decodes the response.
func call(handler http.Handler, method, path, body string) (int, map[string]any) {
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, bytes.NewReader([]byte(body)))
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	var decoded map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &decoded)
	return rec.Code, decoded
}

// seedShortcrust writes two ingredients and a dish that uses them.
//
//	flour   1.20 per 1 kg pack        500 g in the dish  ->  0.60
//	butter  7.50 per 1 kg pack        250 g in the dish  ->  1.875
//	                                                  dish  =  2.475
func seedShortcrust(handler http.Handler) {
	status, _ := call(handler, http.MethodPut, "/api/ingredients/flour",
		`{"name":"Flour","supplier":"Mills","pack_size":{"magnitude":"1","unit":"kg"},"pack_price":"1.20"}`)
	So(status, ShouldEqual, http.StatusOK)
	status, _ = call(handler, http.MethodPut, "/api/ingredients/butter",
		`{"name":"Butter","supplier":"Dairy","pack_size":{"magnitude":"1","unit":"kg"},"pack_price":"7.50"}`)
	So(status, ShouldEqual, http.StatusOK)

	status, _ = call(handler, http.MethodPut, "/api/dishes/shortcrust",
		`{"course":"dessert","status":"on_menu","name":{"":"Shortcrust tart","fr":"Tarte sablée"},
		  "price":{"dine_in":"8.50","delivery":"9.50","catering":"7.00"}}`)
	So(status, ShouldEqual, http.StatusOK)

	status, _ = call(handler, http.MethodPut, "/api/dishes/shortcrust/lines/line-flour",
		`{"ingredient_id":"flour","quantity":{"magnitude":"500","unit":"g"}}`)
	So(status, ShouldEqual, http.StatusOK)
	status, _ = call(handler, http.MethodPut, "/api/dishes/shortcrust/lines/line-butter",
		`{"ingredient_id":"butter","quantity":{"magnitude":"250","unit":"g"}}`)
	So(status, ShouldEqual, http.StatusOK)
}

// TestTheServiceDoesTheCosting is the claim this example exists to make.
//
// No line of this application adds up a price. The cost of a dish is a rollup
// over its lines; a line's cost is its quantity times a rollup over the
// ingredient it points at; and the ingredient's cost per kilogram is a formula
// over the pack it is bought in.
func TestTheServiceDoesTheCosting(t *testing.T) {
	Convey("Given a dish with two ingredient lines", t, func() {
		handler := kitchen(t)
		seedShortcrust(handler)

		Convey("When the dish is read", func() {
			status, dish := call(handler, http.MethodGet, "/api/dishes/shortcrust", "")

			Convey("Then its food cost is the total of its lines", func() {
				So(status, ShouldEqual, http.StatusOK)
				// 500 g of flour at 1.20/kg, 250 g of butter at 7.50/kg.
				So(dish["food_cost"], ShouldEqual, "2.475")
				So(dish["line_count"], ShouldEqual, float64(2))
			})

			Convey("Then each line carries its own cost", func() {
				lines, _ := dish["lines"].([]any)
				So(lines, ShouldHaveLength, 2)
				costs := map[string]string{}
				for _, raw := range lines {
					line, _ := raw.(map[string]any)
					costs[line["ingredient"].(string)], _ = line["line_cost"].(string)
				}
				So(costs["Flour"], ShouldEqual, "0.6")
				So(costs["Butter"], ShouldEqual, "1.875")
			})
		})

		Convey("When a supplier's price list raises the butter price", func() {
			// The whole demonstration, in one call. It writes ONE value per
			// ingredient — the pack price — and nothing else.
			status, body := call(handler, http.MethodPost, "/api/ingredients/import",
				"id,name,supplier,pack_size,pack_unit,pack_price\n"+
					"butter,Butter,Dairy,1,kg,9.00\n")
			So(status, ShouldEqual, http.StatusOK)
			So(body["ingredients"], ShouldEqual, float64(1))

			Convey("Then the dish recosts itself, two relationships away", func() {
				_, dish := call(handler, http.MethodGet, "/api/dishes/shortcrust", "")
				// 0.6 + (0.250 kg * 9.00)
				So(dish["food_cost"], ShouldEqual, "2.85")
			})
		})

		Convey("When a line is removed", func() {
			status, _ := call(handler, http.MethodDelete,
				"/api/dishes/shortcrust/lines/line-butter", "")
			So(status, ShouldEqual, http.StatusOK)

			Convey("Then the cost follows the link, with nothing written to the dish", func() {
				_, dish := call(handler, http.MethodGet, "/api/dishes/shortcrust", "")
				So(dish["food_cost"], ShouldEqual, "0.6")
				So(dish["line_count"], ShouldEqual, float64(1))
			})
		})
	})
}

// TestUnitsConvertOnTheWayIn covers the unit family.
//
// A kitchen buys in whatever the supplier sells and cooks in grams. Costing is
// therefore unit conversion, and getting it wrong makes every price wrong.
func TestUnitsConvertOnTheWayIn(t *testing.T) {
	Convey("Given the same ingredient bought in different units", t, func() {
		handler := kitchen(t)

		// One pound of butter at 3.40, and 500 g of the same butter at 3.75.
		status, _ := call(handler, http.MethodPut, "/api/ingredients/butter-lb",
			`{"name":"Butter (lb)","pack_size":{"magnitude":"1","unit":"lb"},"pack_price":"3.40"}`)
		So(status, ShouldEqual, http.StatusOK)
		status, _ = call(handler, http.MethodPut, "/api/ingredients/butter-g",
			`{"name":"Butter (g)","pack_size":{"magnitude":"500","unit":"g"},"pack_price":"3.75"}`)
		So(status, ShouldEqual, http.StatusOK)

		Convey("When their costs per kilogram are read", func() {
			_, body := call(handler, http.MethodGet, "/api/ingredients", "")
			items, _ := body["items"].([]any)
			costs := map[string]string{}
			for _, raw := range items {
				item, _ := raw.(map[string]any)
				costs[item["id"].(string)], _ = item["cost_per_kg"].(string)
			}

			Convey("Then both are per kilogram, whatever the invoice said", func() {
				// 3.40 / 0.45359237 kg, and 3.75 / 0.5 kg.
				// 3.40 / 0.45359237 kg. A quantity's base magnitude is a float, so a
				// cost derived by dividing by one carries the float's tail — the
				// money values themselves stay exact decimals.
				So(costs["butter-lb"], ShouldStartWith, "7.4957")
				So(costs["butter-g"], ShouldEqual, "7.5")
			})

			Convey("And the pack keeps the unit it was entered in", func() {
				for _, raw := range items {
					item, _ := raw.(map[string]any)
					if item["id"] == "butter-lb" {
						size, _ := item["pack_size"].(map[string]any)
						So(size["unit"], ShouldEqual, "lb")
					}
				}
			})
		})
	})
}

// TestPriceIsPerChannelAndNamePerLocale covers scoped and localized values.
func TestPriceIsPerChannelAndNamePerLocale(t *testing.T) {
	Convey("Given a dish priced for three channels and named in two locales", t, func() {
		handler := kitchen(t)
		seedShortcrust(handler)

		Convey("When the dish is read", func() {
			_, dish := call(handler, http.MethodGet, "/api/dishes/shortcrust", "")

			Convey("Then one attribute holds a price per channel", func() {
				prices, _ := dish["price"].(map[string]any)
				So(prices["dine_in"], ShouldEqual, "8.50")
				So(prices["delivery"], ShouldEqual, "9.50")
				So(prices["catering"], ShouldEqual, "7.00")
			})

			Convey("Then one attribute holds a name per locale", func() {
				names, _ := dish["name"].(map[string]any)
				So(names[""], ShouldEqual, "Shortcrust tart")
				So(names["fr"], ShouldEqual, "Tarte sablée")
			})

			Convey("Then the margin is reported per channel", func() {
				// The service cannot compute this: a formula reads the base
				// scope, and a scoped price has no single base value.
				margins, _ := dish["margin"].(map[string]any)
				So(margins["dine_in"], ShouldEqual, "0.7088")
				So(margins["catering"], ShouldEqual, "0.6464")
			})
		})
	})
}

// TestADishReachesTheMenuOnlyWhenComplete covers the dependency and the gate
// that acts on it.
//
// The dependency makes `allergens` required once a dish declares it has them.
// The service enforces that when the attribute itself is written, so setting
// the flag alone is ACCEPTED — the rule describes what the dish needs, not the
// order a chef fills it in. Publishing is where "needs" becomes "must", and
// the gate reads the service's completeness report rather than re-deriving
// the rule.
func TestADishReachesTheMenuOnlyWhenComplete(t *testing.T) {
	Convey("Given a dish that declares it contains allergens", t, func() {
		handler := kitchen(t)
		seedShortcrust(handler)

		status, _ := call(handler, http.MethodPut, "/api/dishes/shortcrust",
			`{"contains_allergens":true}`)
		So(status, ShouldEqual, http.StatusOK)

		Convey("When it is published with no allergen list", func() {
			status, body := call(handler, http.MethodPost, "/api/dishes/shortcrust/publish", "")

			Convey("Then it is refused, naming what is missing", func() {
				So(status, ShouldEqual, http.StatusUnprocessableEntity)
				missing, _ := body["missing"].([]any)
				So(missing, ShouldContain, "allergens")
			})
		})

		Convey("When the allergens are listed", func() {
			status, dish := call(handler, http.MethodPut, "/api/dishes/shortcrust",
				`{"allergens":["gluten","milk"]}`)
			So(status, ShouldEqual, http.StatusOK)
			allergens, _ := dish["allergens"].([]any)
			So(allergens, ShouldHaveLength, 2)

			Convey("Then it publishes", func() {
				status, published := call(handler, http.MethodPost, "/api/dishes/shortcrust/publish", "")
				So(status, ShouldEqual, http.StatusOK)
				So(published["status"], ShouldEqual, "on_menu")
			})
		})
	})
}

// TestAMenuChangeIsScheduled covers the change set.
func TestAMenuChangeIsScheduled(t *testing.T) {
	Convey("Given a dish on the menu", t, func() {
		handler := kitchen(t)
		seedShortcrust(handler)

		Convey("When a price rise is scheduled for the future", func() {
			status, body := call(handler, http.MethodPost, "/api/menu-changes",
				`{"name":"Autumn menu","publish_at":"2099-01-01T06:00:00Z",
				  "prices":{"shortcrust":{"dine_in":"9.50"}}}`)

			Convey("Then it is approved and waiting, not applied", func() {
				So(status, ShouldEqual, http.StatusOK)
				So(body["state"], ShouldEqual, "approved")

				_, dish := call(handler, http.MethodGet, "/api/dishes/shortcrust", "")
				prices, _ := dish["price"].(map[string]any)
				So(prices["dine_in"], ShouldEqual, "8.50")
			})
		})

		Convey("When a price rise is published now", func() {
			status, body := call(handler, http.MethodPost, "/api/menu-changes",
				`{"name":"Correction","prices":{"shortcrust":{"dine_in":"9.00","delivery":"10.00"}}}`)
			So(status, ShouldEqual, http.StatusOK)
			So(body["state"], ShouldEqual, "published")

			Convey("Then every price in it moved together", func() {
				_, dish := call(handler, http.MethodGet, "/api/dishes/shortcrust", "")
				prices, _ := dish["price"].(map[string]any)
				So(prices["dine_in"], ShouldEqual, "9.00")
				So(prices["delivery"], ShouldEqual, "10.00")
				// Untouched by the change.
				So(prices["catering"], ShouldEqual, "7.00")
			})
		})
	})
}
