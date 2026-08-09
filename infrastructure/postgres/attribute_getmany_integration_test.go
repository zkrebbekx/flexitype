package postgres_test

import (
	"context"
	"testing"

	_ "github.com/lib/pq"
	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/infrastructure/postgres"
	"github.com/zkrebbekx/flexitype/pkg/db"

	"github.com/zkrebbekx/flexitype/internal/testdb"
)

// TestAttributeGetManyIntegration pins the two properties GetMany's callers
// depend on, both of which the per-key dataloader path violated.
//
// One statement, not one per key: the dataloader awaited each thunk before
// requesting the next, so every key closed its own batch and paid the full
// batch window. The field ACL calls GetMany on every non-admin value read, so
// the cost landed on exactly the principals a permission set restricts.
//
// A missing id is skipped, not an error: an activity entry or a feed envelope
// can name an attribute that no longer exists, and one such row must not fail
// the whole page.
func TestAttributeGetManyIntegration(t *testing.T) {
	pool := openIntegrationDB(t)
	defer func() { _ = pool.Close() }()
	ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
	if err := postgres.Migrate(ctx, db.NewTransactor(pool)); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	Convey("Given a type carrying five attribute definitions", t, func() {
		testdb.TruncateTables(t, pool, "flexitype_attribute_value", "flexitype_attribute_definition", "flexitype_type_definition", "flexitype_entity_summary", "flexitype_schema_version")

		svc := flexitype.New(pool)
		it := svc.Interactors(ctx)
		product, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "product", DisplayName: "Product",
		})
		So(err, ShouldBeNil)

		names := []string{"sku", "cost", "colour", "weight_note", "supplier_ref"}
		ids := make([]valueobjects.AttributeDefinitionID, 0, len(names))
		for _, n := range names {
			a, err := it.Attributes().Create(ctx, appattribute.CreateInput{
				TypeDefinitionID: product.ID.String(), InternalName: n, DisplayName: n, DataType: "string",
			})
			So(err, ShouldBeNil)
			id, err := valueobjects.ParseAttributeDefinitionID(a.ID.String())
			So(err, ShouldBeNil)
			ids = append(ids, id)
		}

		counting := &countingQuerier{DB: pool}
		repos := postgres.NewRepositories(counting)

		Convey("When all five are loaded in one GetMany call", func() {
			counting.reset()
			defs, err := repos.Attributes.GetMany(ctx, ids)

			Convey("Then every definition returns in the requested order", func() {
				So(err, ShouldBeNil)
				So(defs, ShouldHaveLength, len(ids))
				for i, d := range defs {
					So(d.ID().String(), ShouldEqual, ids[i].String())
					So(d.InternalName(), ShouldEqual, names[i])
				}
			})

			Convey("Then it costs one statement, not one per key", func() {
				So(counting.queries(), ShouldHaveLength, 1)
			})
		})

		Convey("When the key set names an attribute that does not exist", func() {
			missing := valueobjects.NewAttributeDefinitionID()
			mixed := []valueobjects.AttributeDefinitionID{ids[0], missing, ids[1]}
			counting.reset()
			defs, err := repos.Attributes.GetMany(ctx, mixed)

			Convey("Then the missing id is skipped rather than failing the call", func() {
				So(err, ShouldBeNil)
				So(defs, ShouldHaveLength, 2)
				So(defs[0].ID().String(), ShouldEqual, ids[0].String())
				So(defs[1].ID().String(), ShouldEqual, ids[1].String())
			})

			Convey("Then it still costs one statement", func() {
				So(counting.queries(), ShouldHaveLength, 1)
			})
		})

		Convey("When the key set is empty", func() {
			counting.reset()
			defs, err := repos.Attributes.GetMany(ctx, nil)

			Convey("Then it returns nothing without querying", func() {
				So(err, ShouldBeNil)
				So(defs, ShouldBeEmpty)
			})
		})
	})
}
