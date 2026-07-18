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
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

// TestFieldLevelAccessParityPostgres re-runs the field-level ACL suite
// (infrastructure/memory/field_acl_test.go) against Postgres. A restricted
// principal's reads and writes must be filtered the SAME way over the real SQL
// store: a none-on-cost principal can never see cost through ListByEntity,
// Get-by-id, EffectiveAttributes, grid, facets, export, or FQL — the last of
// which is oracle-safety (a restricted attribute must be "unknown", not a
// forbidden-but-existing leak). The admin still sees everything.
func TestFieldLevelAccessParityPostgres(t *testing.T) {
	pool := openTestDB(t)
	defer func() { _ = pool.Close() }()

	svc := flexitype.New(pool, flexitype.WithSearchIndex())
	if err := svc.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	Convey("Given a product with sku and a restricted cost attribute (Postgres)", t, func() {
		truncateAll(t, pool)
		admin := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		ia := svc.Interactors(admin)
		product, err := ia.TypeDefinitions().Create(admin, apptypedef.CreateInput{InternalName: "product", DisplayName: "Product"})
		So(err, ShouldBeNil)
		typeID := product.ID.String()
		sku, err := ia.Attributes().Create(admin, appattribute.CreateInput{TypeDefinitionID: typeID, InternalName: "sku", DisplayName: "SKU", DataType: "string"})
		So(err, ShouldBeNil)
		cost, err := ia.Attributes().Create(admin, appattribute.CreateInput{TypeDefinitionID: typeID, InternalName: "cost", DisplayName: "Cost", DataType: "float"})
		So(err, ShouldBeNil)

		// Admin seeds both values; each Set is its own unit of work.
		set := func(ctx context.Context, attrID string, v any) error {
			raw, _ := json.Marshal(v)
			_, e := svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: attrID, EntityID: "p1", TypeDefinitionID: typeID, Value: raw,
			})
			return e
		}
		So(set(admin, sku.ID.String(), "ABC"), ShouldBeNil)
		So(set(admin, cost.ID.String(), 250.0), ShouldBeNil)

		readonlyCost := uow.WithAccess(admin, uow.Access{Attr: map[string]uow.Perm{"cost": uow.PermRead}})
		noCost := uow.WithAccess(admin, uow.Access{Attr: map[string]uow.Perm{"cost": uow.PermNone}})

		Convey("A read-only-on-cost principal reads cost but cannot write it", func() {
			vals, err := svc.Interactors(readonlyCost).Values().ListByEntity(readonlyCost, typeID, "p1")
			So(err, ShouldBeNil)
			So(len(vals), ShouldEqual, 2) // sku + cost both visible

			err = set(readonlyCost, cost.ID.String(), 300.0)
			So(err, ShouldNotBeNil)
			So(domainerrors.CodeOf(err), ShouldEqual, domainerrors.CodeForbidden)

			// sku (unrestricted) is still writable.
			So(set(readonlyCost, sku.ID.String(), "XYZ"), ShouldBeNil)
		})

		Convey("Field ACL applies to reading and deleting a value by id", func() {
			vals, err := svc.Interactors(admin).Values().ListByEntity(admin, typeID, "p1")
			So(err, ShouldBeNil)
			var costValueID string
			for _, v := range vals {
				if v.AttributeDefinitionID.String() == cost.ID.String() {
					costValueID = v.ID.String()
				}
			}
			So(costValueID, ShouldNotBeBlank)

			// none-on-cost: Get by id is invisible (NotFound), not leaked.
			_, err = svc.Interactors(noCost).Values().Get(noCost, costValueID)
			So(domainerrors.IsNotFound(err), ShouldBeTrue)

			// read-only-on-cost: delete is a write, so it is forbidden.
			_, err = svc.Interactors(readonlyCost).Values().Remove(readonlyCost, costValueID)
			So(domainerrors.CodeOf(err), ShouldEqual, domainerrors.CodeForbidden)

			// admin reads and deletes it.
			_, err = svc.Interactors(admin).Values().Get(admin, costValueID)
			So(err, ShouldBeNil)
			_, err = svc.Interactors(admin).Values().Remove(admin, costValueID)
			So(err, ShouldBeNil)
		})

		Convey("A none-on-cost principal cannot see or query cost", func() {
			vals, err := svc.Interactors(noCost).Values().ListByEntity(noCost, typeID, "p1")
			So(err, ShouldBeNil)
			So(len(vals), ShouldEqual, 1) // only sku

			eff, err := svc.Interactors(noCost).TypeDefinitions().EffectiveAttributes(noCost, typeID)
			So(err, ShouldBeNil)
			names := []string{}
			for _, e := range eff {
				names = append(names, e.Attribute.InternalName)
			}
			So(names, ShouldContain, "sku")
			So(names, ShouldNotContain, "cost")

			_, err = svc.Interactors(noCost).Query().Execute(noCost, appquery.ExecuteInput{Type: "product", Query: "cost > 100"})
			So(err, ShouldNotBeNil)
			So(domainerrors.CodeOf(err), ShouldEqual, domainerrors.CodeValidation) // unknown attribute
		})

		Convey("The admin principal sees and queries everything", func() {
			vals, err := svc.Interactors(admin).Values().ListByEntity(admin, typeID, "p1")
			So(err, ShouldBeNil)
			So(len(vals), ShouldEqual, 2)
			out, err := svc.Interactors(admin).Query().Execute(admin, appquery.ExecuteInput{Type: "product", Query: "cost > 100"})
			So(err, ShouldBeNil)
			So(len(out.Items), ShouldEqual, 1)
		})

		Convey("A none-on-cost principal cannot leak cost via grid, facets, or export", func() {
			Convey("Grid drops the restricted column and rejects it by name", func() {
				grid, err := svc.Interactors(noCost).Values().GridRows(noCost, typeID, []string{"sku"}, []string{"p1"})
				So(err, ShouldBeNil)
				So(grid.Rows, ShouldHaveLength, 1)
				_, hasCost := grid.Rows[0].Values["cost"]
				So(hasCost, ShouldBeFalse)

				_, err = svc.Interactors(noCost).Values().GridRows(noCost, typeID, []string{"sku", "cost"}, []string{"p1"})
				So(err, ShouldNotBeNil)
				So(domainerrors.CodeOf(err), ShouldEqual, domainerrors.CodeValidation)
			})

			Convey("Facets refuse to bucket the restricted attribute", func() {
				_, err := svc.Interactors(noCost).Values().Facets(noCost, typeID, []string{"cost"}, []string{"p1"})
				So(err, ShouldNotBeNil)
				So(domainerrors.CodeOf(err), ShouldEqual, domainerrors.CodeValidation)
			})

			Convey("Export omits the restricted column by default and rejects it by name", func() {
				out, err := svc.Interactors(noCost).Values().Export(noCost, appvalue.ExportInput{TypeDefinitionID: typeID})
				So(err, ShouldBeNil)
				So(out.Columns, ShouldContain, "sku")
				So(out.Columns, ShouldNotContain, "cost")

				_, err = svc.Interactors(noCost).Values().Export(noCost, appvalue.ExportInput{TypeDefinitionID: typeID, Attributes: []string{"cost"}})
				So(err, ShouldNotBeNil)
				So(domainerrors.CodeOf(err), ShouldEqual, domainerrors.CodeValidation)
			})
		})

		Convey("The admin principal still sees cost in grid and export", func() {
			grid, err := svc.Interactors(admin).Values().GridRows(admin, typeID, []string{"sku", "cost"}, []string{"p1"})
			So(err, ShouldBeNil)
			So(grid.Rows[0].Values["cost"], ShouldEqual, "250")

			out, err := svc.Interactors(admin).Values().Export(admin, appvalue.ExportInput{TypeDefinitionID: typeID})
			So(err, ShouldBeNil)
			So(out.Columns, ShouldContain, "cost")
		})
	})
}
