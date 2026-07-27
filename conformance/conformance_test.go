package conformance_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	"github.com/zkrebbekx/flexitype/client"
)

// TestClientConformance drives the first-party Go client against a real
// standalone handler (the in-memory service exposed over httptest), so the
// client can never silently drift from the API.
func TestClientConformance(t *testing.T) {
	Convey("Given a standalone flexitype service and its Go client", t, func() {
		svc := flexitype.NewInMemory(flexitype.WithSearchIndex())
		ts := httptest.NewServer(svc.APIHandler(flexitype.APIConfig{}))
		Reset(ts.Close)

		c, err := client.New(ts.URL) // dev mode: auth disabled, no token
		So(err, ShouldBeNil)
		ctx := context.Background()

		Convey("When modelling a type, attributes and values", func() {
			product, err := c.Types().Create(ctx, client.CreateTypeInput{InternalName: "product", DisplayName: "Product"})
			So(err, ShouldBeNil)
			So(product.ID, ShouldNotBeEmpty)

			price, err := c.Attributes().Create(ctx, client.CreateAttributeInput{
				TypeDefinitionID: product.ID, InternalName: "price", DisplayName: "Price", DataType: "decimal",
			})
			So(err, ShouldBeNil)
			name, err := c.Attributes().Create(ctx, client.CreateAttributeInput{
				TypeDefinitionID: product.ID, InternalName: "name", DisplayName: "Name", DataType: "string",
			})
			So(err, ShouldBeNil)

			_, err = c.Values().Set(ctx, client.SetValueInput{
				AttributeDefinitionID: price.ID, EntityID: "sku-1", TypeDefinitionID: product.ID, Value: json.RawMessage(`"19.99"`),
			})
			So(err, ShouldBeNil)
			_, err = c.Values().Set(ctx, client.SetValueInput{
				AttributeDefinitionID: name.ID, EntityID: "sku-1", TypeDefinitionID: product.ID, Value: json.RawMessage(`"Widget"`),
			})
			So(err, ShouldBeNil)

			Convey("Then it reads back through the typed surface", func() {
				got, err := c.Types().Get(ctx, product.ID)
				So(err, ShouldBeNil)
				So(got.InternalName, ShouldEqual, "product")

				effs, err := c.Types().EffectiveAttributes(ctx, product.ID)
				So(err, ShouldBeNil)
				So(len(effs), ShouldEqual, 2)

				vals, err := c.Entities().Values(ctx, product.ID, "sku-1")
				So(err, ShouldBeNil)
				So(len(vals), ShouldEqual, 2)
			})

			Convey("And an FQL query iterates the matching entities", func() {
				var ids []string
				for row, err := range c.Query(ctx, "product", `price > 10`) {
					So(err, ShouldBeNil)
					ids = append(ids, row.EntityID)
				}
				So(ids, ShouldResemble, []string{"sku-1"})
			})

			Convey("And GraphQL returns the same data", func() {
				var res struct {
					Product struct {
						Edges []struct {
							Node struct {
								EntityID string `json:"entityId"`
								Name     string `json:"name"`
							} `json:"node"`
						} `json:"edges"`
					} `json:"product"`
				}
				err := c.GraphQL(ctx, `{ product(filter:"price > 10") { edges { node { entityId name } } } }`, nil, &res)
				So(err, ShouldBeNil)
				So(len(res.Product.Edges), ShouldEqual, 1)
				So(res.Product.Edges[0].Node.Name, ShouldEqual, "Widget")
			})
		})

		Convey("When a resource is missing", func() {
			_, err := c.Attributes().Get(ctx, "01KX9V0000000000000000000A") // valid ulid, absent
			Convey("Then the error is a typed NOT_FOUND matched by errors.Is", func() {
				So(err, ShouldNotBeNil)
				So(errors.Is(err, client.ErrNotFound), ShouldBeTrue)
			})
		})

		Convey("When a pagination cursor is malformed", func() {
			_, err := c.Types().List(ctx, client.ListTypesOptions{ListOptions: client.ListOptions{Cursor: "not-a-valid-cursor"}})
			Convey("Then the server's VALIDATION error surfaces as a typed error", func() {
				So(err, ShouldNotBeNil)
				So(errors.Is(err, client.ErrValidation), ShouldBeTrue)
			})
		})

		Convey("When paging a list and requesting the total", func() {
			for i := 0; i < 5; i++ {
				_, err := c.Types().Create(ctx, client.CreateTypeInput{InternalName: "t" + string(rune('a'+i)), DisplayName: "T"})
				So(err, ShouldBeNil)
			}

			page, err := c.Types().List(ctx, client.ListTypesOptions{ListOptions: client.ListOptions{Limit: 2, Total: true}})
			So(err, ShouldBeNil)
			So(len(page.Items), ShouldEqual, 2)
			So(page.PageInfo.TotalCount, ShouldNotBeNil)
			So(*page.PageInfo.TotalCount, ShouldEqual, 5)

			Convey("Then the All iterator walks every page exactly once", func() {
				seen := map[string]bool{}
				count := 0
				for td, err := range c.Types().All(ctx, client.ListTypesOptions{ListOptions: client.ListOptions{Limit: 2}}) {
					So(err, ShouldBeNil)
					So(seen[td.ID], ShouldBeFalse)
					seen[td.ID] = true
					count++
				}
				So(count, ShouldEqual, 5)
			})
		})

		Convey("When using units of measure", func() {
			fam, err := c.UnitFamilies().Create(ctx, client.CreateUnitFamilyInput{
				Name: "mass", BaseUnit: "g", Units: map[string]float64{"g": 1, "kg": 1000},
			})
			So(err, ShouldBeNil)
			product, err := c.Types().Create(ctx, client.CreateTypeInput{InternalName: "product", DisplayName: "Product"})
			So(err, ShouldBeNil)
			weight, err := c.Attributes().Create(ctx, client.CreateAttributeInput{
				TypeDefinitionID: product.ID, InternalName: "weight", DisplayName: "Weight",
				DataType: "quantity", UnitFamilyID: fam.ID, DisplayUnit: "g",
			})
			So(err, ShouldBeNil)
			_, err = c.Values().Set(ctx, client.SetValueInput{
				AttributeDefinitionID: weight.ID, EntityID: "w1", TypeDefinitionID: product.ID,
				Value: json.RawMessage(`{"magnitude":"5","unit":"kg"}`),
			})
			So(err, ShouldBeNil)

			Convey("Then FQL compares across units via the family", func() {
				page, err := c.QueryPage(ctx, "product", `weight > 1500 g`)
				So(err, ShouldBeNil)
				So(len(page.Items), ShouldEqual, 1)
				So(page.Items[0].EntityID, ShouldEqual, "w1")
			})
		})

		Convey("When bootstrapping a tenant from a template", func() {
			res, err := c.Schema().ApplyTemplate(ctx, "product-catalog")
			So(err, ShouldBeNil)
			Convey("Then the schema is created and templates list", func() {
				So(res.Types.Created, ShouldBeGreaterThan, 0)
				tmpls, err := c.Schema().Templates(ctx)
				So(err, ShouldBeNil)
				So(len(tmpls), ShouldBeGreaterThanOrEqualTo, 2)
			})
		})

		Convey("When reading features", func() {
			f, err := c.Features(ctx)
			So(err, ShouldBeNil)
			So(f.SearchIndex, ShouldBeTrue)
		})
	})
}

// TestEventCursorConformance drives the feed-cursor round trip the SDK is
// documented to support.
//
// Both directions used field names the server does not: GetCursor read
// `after_seq` where the server writes `position`, so it returned 0 with a NIL
// error on every call — indistinguishable from a fresh consumer's legitimate
// start. CommitCursor sent `after_seq`/`expected_seq` into a strict decoder,
// so every commit was a hard 422 and the cursor never advanced.
//
// The two halves masked each other: a consumer replayed the whole retained
// feed on every restart, and the replay looked like the cursor simply never
// being committed — which it was not. Nothing in either half reported a
// failure, and no conformance case exercised the pair.
func TestEventCursorConformance(t *testing.T) {
	// Cursors need the transactional outbox, which the in-memory service does
	// not implement (it always direct-dispatches), so this case runs against
	// Postgres and skips without a DSN. CI provides one.
	dsn := os.Getenv("FLEXITYPE_TEST_DSN")
	if dsn == "" {
		t.Skip("FLEXITYPE_TEST_DSN not set; skipping the event-cursor conformance case")
	}

	Convey("Given a service with the outbox enabled and its Go client", t, func() {
		pool, err := sqlx.Connect("postgres", dsn)
		So(err, ShouldBeNil)
		Reset(func() { _ = pool.Close() })

		svc := flexitype.New(pool, flexitype.WithOutbox())
		So(svc.Migrate(context.Background()), ShouldBeNil)
		pool.MustExec(`TRUNCATE flexitype_event_cursor`)

		ts := httptest.NewServer(svc.APIHandler(flexitype.APIConfig{}))
		Reset(ts.Close)

		c, err := client.New(ts.URL)
		So(err, ShouldBeNil)
		ctx := context.Background()

		Convey("When a consumer reads its cursor before committing anything", func() {
			position, err := c.Events().GetCursor(ctx, "search-indexer")

			Convey("Then it starts at zero, without an error", func() {
				So(err, ShouldBeNil)
				So(position, ShouldEqual, 0)
			})
		})

		Convey("When the consumer commits a position", func() {
			err := c.Events().CommitCursor(ctx, "search-indexer", 7, 0)

			Convey("Then the commit is accepted", func() {
				So(err, ShouldBeNil)
			})

			Convey("Then reading it back returns what was committed", func() {
				So(err, ShouldBeNil)
				position, gerr := c.Events().GetCursor(ctx, "search-indexer")
				So(gerr, ShouldBeNil)
				So(position, ShouldEqual, int64(7))
			})

			Convey("Then advancing it again from the committed position works", func() {
				So(err, ShouldBeNil)
				So(c.Events().CommitCursor(ctx, "search-indexer", 12, 7), ShouldBeNil)
				position, gerr := c.Events().GetCursor(ctx, "search-indexer")
				So(gerr, ShouldBeNil)
				So(position, ShouldEqual, int64(12))
			})

			Convey("Then a stale expected position is refused, not silently applied", func() {
				So(err, ShouldBeNil)
				// The compare-and-swap this endpoint exists for: a second
				// replica that read an older position must lose.
				cerr := c.Events().CommitCursor(ctx, "search-indexer", 9, 0)
				So(cerr, ShouldNotBeNil)

				position, gerr := c.Events().GetCursor(ctx, "search-indexer")
				So(gerr, ShouldBeNil)
				So(position, ShouldEqual, int64(7))
			})
		})
	})
}

// TestSilentlyBrokenMethodsConformance drives the methods that could never
// return a correct result, so none of them can regress to silence.
//
// AsOf decoded a bare Revision object through the {"items":[...]} list
// helper, found no "items" key, and returned an empty slice with a NIL error
// on every call — so a point-in-time audit read rendered an empty entity and
// concluded the data was never set. Diff sent no query where the server
// requires ?to=. ListServiceAccounts sent `tenant` where the server reads
// `tenant_name`, so the filter was dropped and the call 422'd whether or not
// a tenant was passed.
func TestSilentlyBrokenMethodsConformance(t *testing.T) {
	Convey("Given a standalone service and its Go client", t, func() {
		svc := flexitype.NewInMemory()
		ts := httptest.NewServer(svc.APIHandler(flexitype.APIConfig{}))
		Reset(ts.Close)

		c, err := client.New(ts.URL)
		So(err, ShouldBeNil)
		ctx := context.Background()

		product, err := c.Types().Create(ctx, client.CreateTypeInput{
			InternalName: "widget", DisplayName: "Widget",
		})
		So(err, ShouldBeNil)
		name, err := c.Attributes().Create(ctx, client.CreateAttributeInput{
			TypeDefinitionID: product.ID, InternalName: "name",
			DisplayName: "Name", DataType: "string",
		})
		So(err, ShouldBeNil)
		_, err = c.Values().Set(ctx, client.SetValueInput{
			AttributeDefinitionID: name.ID, EntityID: "w1",
			TypeDefinitionID: product.ID, Value: json.RawMessage(`"first"`),
		})
		So(err, ShouldBeNil)

		first, err := c.Entities().CreateRevision(ctx, product.ID, "w1", "v1")
		So(err, ShouldBeNil)

		Convey("When reading the entity as of a time after the revision", func() {
			at := first.CreatedAt.Add(time.Second).UTC().Format(time.RFC3339)
			values, err := c.Entities().AsOf(ctx, product.ID, "w1", at)

			Convey("Then the values come back, rather than an empty slice", func() {
				So(err, ShouldBeNil)
				So(values, ShouldNotBeEmpty)
				So(values[0].InternalName, ShouldEqual, "name")
			})

			Convey("Then the whole revision is reachable too", func() {
				rev, rerr := c.Entities().AsOfRevision(ctx, product.ID, "w1", at)
				So(rerr, ShouldBeNil)
				So(rev.ID, ShouldEqual, first.ID)
				So(rev.Seq, ShouldEqual, first.Seq)
			})
		})

		Convey("When no timestamp is given", func() {
			_, err := c.Entities().AsOf(ctx, product.ID, "w1", "")

			Convey("Then it says a timestamp is required, without a round trip", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "timestamp")
			})
		})

		Convey("When diffing one revision against another", func() {
			_, err := c.Values().Set(ctx, client.SetValueInput{
				AttributeDefinitionID: name.ID, EntityID: "w1",
				TypeDefinitionID: product.ID, Value: json.RawMessage(`"second"`),
			})
			So(err, ShouldBeNil)
			second, err := c.Entities().CreateRevision(ctx, product.ID, "w1", "v2")
			So(err, ShouldBeNil)

			diff, err := c.Revisions().Diff(ctx, first.ID, second.ID)

			Convey("Then the diff is returned, not a validation error", func() {
				So(err, ShouldBeNil)
				So(string(diff), ShouldContainSubstring, "second")
			})
		})

		Convey("When listing service accounts with no tenant", func() {
			_, err := c.Admin().ListServiceAccounts(ctx, "")

			Convey("Then it says a tenant is required, without a round trip", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "tenant")
			})
		})
	})
}

// TestErasureAndValuesConformance drives the capabilities that had no client
// method at all — most importantly both right-to-erasure endpoints, which
// docs/erasure.md documents and only raw net/http could reach.
func TestErasureAndValuesConformance(t *testing.T) {
	Convey("Given a standalone service and its Go client", t, func() {
		svc := flexitype.NewInMemory()
		ts := httptest.NewServer(svc.APIHandler(flexitype.APIConfig{}))
		Reset(ts.Close)

		c, err := client.New(ts.URL)
		So(err, ShouldBeNil)
		ctx := context.Background()

		product, err := c.Types().Create(ctx, client.CreateTypeInput{
			InternalName: "product", DisplayName: "Product",
		})
		So(err, ShouldBeNil)
		sku, err := c.Attributes().Create(ctx, client.CreateAttributeInput{
			TypeDefinitionID: product.ID, InternalName: "sku",
			DisplayName: "SKU", DataType: "string",
		})
		So(err, ShouldBeNil)
		for _, entity := range []string{"p1", "p2"} {
			_, err := c.Values().Set(ctx, client.SetValueInput{
				AttributeDefinitionID: sku.ID, EntityID: entity,
				TypeDefinitionID: product.ID, Value: json.RawMessage(`"ABC"`),
			})
			So(err, ShouldBeNil)
		}

		Convey("When listing values filtered by attribute", func() {
			page, err := c.Values().List(ctx, client.ListValuesOptions{
				AttributeDefinitionID: sku.ID,
			})

			Convey("Then both entities' values come back", func() {
				So(err, ShouldBeNil)
				So(page.Items, ShouldHaveLength, 2)
			})
		})

		Convey("When listing values filtered to one entity", func() {
			page, err := c.Values().List(ctx, client.ListValuesOptions{EntityID: "p1"})

			Convey("Then only that entity's values come back", func() {
				So(err, ShouldBeNil)
				So(page.Items, ShouldHaveLength, 1)
			})
		})

		Convey("When purging one entity", func() {
			report, err := c.Entities().Purge(ctx, product.ID, "p1")

			Convey("Then the report says what was erased", func() {
				So(err, ShouldBeNil)
				So(report.ValuesPurged, ShouldEqual, 1)
			})

			Convey("Then the entity's values are gone, not archived", func() {
				So(err, ShouldBeNil)
				page, lerr := c.Values().List(ctx, client.ListValuesOptions{
					EntityID: "p1", IncludeArchived: true,
				})
				So(lerr, ShouldBeNil)
				So(page.Items, ShouldBeEmpty)
			})

			Convey("Then the other entity is untouched", func() {
				So(err, ShouldBeNil)
				page, lerr := c.Values().List(ctx, client.ListValuesOptions{EntityID: "p2"})
				So(lerr, ShouldBeNil)
				So(page.Items, ShouldHaveLength, 1)
			})
		})

		Convey("When purging the whole tenant", func() {
			report, err := c.PurgeTenant(ctx)

			Convey("Then every entity's values are erased", func() {
				So(err, ShouldBeNil)
				So(report.ValuesPurged, ShouldEqual, 2)

				page, lerr := c.Values().List(ctx, client.ListValuesOptions{IncludeArchived: true})
				So(lerr, ShouldBeNil)
				So(page.Items, ShouldBeEmpty)
			})
		})

		Convey("When recomputing computed attributes", func() {
			// No computed attributes are defined, so this checks the route is
			// reachable at all: it used to be gated behind the search index.
			count, err := c.RecomputeComputed(ctx)

			Convey("Then it is served and reports a count", func() {
				So(err, ShouldBeNil)
				So(count, ShouldBeGreaterThanOrEqualTo, 0)
			})
		})
	})
}
