package flexitype_test

import (
	"context"
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	appquery "github.com/zkrebbekx/flexitype/application/query"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

// TestInternalProjectionReadYourWritesPostgres proves the #211 fix: internal
// projections (the computed-attribute materializer and the search index) are
// maintained synchronously in the originating request in BOTH delivery modes,
// so their consistency does NOT depend on WithOutbox. Each mode runs the exact
// same assertions and — critically — never runs the outbox relay: if the
// projections still rode the external delivery bus, the outbox path would defer
// them to the relay and these read-your-writes checks would fail.
func TestInternalProjectionReadYourWritesPostgres(t *testing.T) {
	pool := openTestDB(t)
	defer func() { _ = pool.Close() }()

	// One migration is enough; it creates every table (outbox included) and is
	// idempotent under the advisory lock.
	if err := flexitype.New(pool).Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	modes := []struct {
		name string
		opts []flexitype.Option
	}{
		{"default (no outbox)", []flexitype.Option{flexitype.WithSearchIndex()}},
		{"with outbox", []flexitype.Option{flexitype.WithSearchIndex(), flexitype.WithOutbox()}},
	}

	for _, mode := range modes {
		mode := mode
		Convey("Given a Postgres service ("+mode.name+"), the relay never run", t, func() {
			truncateAll(t, pool)
			svc := flexitype.New(pool, mode.opts...)

			ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
			it := svc.Interactors(ctx)

			product, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
				InternalName: "product", DisplayName: "Product",
			})
			So(err, ShouldBeNil)
			typeID := product.ID.String()

			mkAttr := func(name, dt string) {
				_, e := it.Attributes().Create(ctx, appattribute.CreateInput{
					TypeDefinitionID: typeID, InternalName: name, DisplayName: name, DataType: dt,
				})
				So(e, ShouldBeNil)
			}
			mkAttr("price", "float")
			mkAttr("cost", "float")
			mkAttr("name", "string")
			margin, err := it.Attributes().Create(ctx, appattribute.CreateInput{
				TypeDefinitionID: typeID, InternalName: "margin", DisplayName: "Margin", DataType: "float",
				Computed: json.RawMessage(`{"kind":"formula","formula":"(price - cost) / price"}`),
			})
			So(err, ShouldBeNil)

			// attrID resolves an attribute's id by internal name (a fresh lookup so
			// writes never depend on a stale in-request cache).
			attrID := func(name string) string {
				list, e := svc.Interactors(ctx).Attributes().List(ctx, appattribute.ListInput{TypeDefinitionID: typeID})
				So(e, ShouldBeNil)
				for _, a := range list.Items {
					if a.InternalName == name {
						return a.ID.String()
					}
				}
				return ""
			}
			setVal := func(name string, raw json.RawMessage) {
				_, e := svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{
					AttributeDefinitionID: attrID(name), EntityID: "p1", TypeDefinitionID: typeID, Value: raw,
				})
				So(e, ShouldBeNil)
			}

			Convey("When a write drives the computed margin (no relay)", func() {
				setVal("price", json.RawMessage(`100`))
				setVal("cost", json.RawMessage(`40`))

				Convey("Then the same service immediately reads the recomputed value (read-your-writes)", func() {
					vals, e := svc.Interactors(ctx).Values().ListByEntity(ctx, typeID, "p1")
					So(e, ShouldBeNil)
					var got float64
					found := false
					for _, v := range vals {
						if v.AttributeDefinitionID.String() == margin.ID.String() {
							got, found = v.Value.Float(), true
						}
					}
					So(found, ShouldBeTrue)
					So(got, ShouldAlmostEqual, 0.6)
				})
			})

			Convey("When a textual value is written (no relay)", func() {
				setVal("name", json.RawMessage(`"Trailblazer"`))

				Convey("Then the entity is immediately findable via matches() in the originating request", func() {
					out, e := svc.Interactors(ctx).Query().Execute(ctx, appquery.ExecuteInput{
						Type: "product", Query: `matches("Trailblazer")`,
					})
					So(e, ShouldBeNil)
					ids := make([]string, 0, len(out.Items))
					for _, r := range out.Items {
						ids = append(ids, r.EntityID)
					}
					So(ids, ShouldContain, "p1")
				})
			})
		})
	}
}
