package memory_test

import (
	"context"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

func TestImportExport(t *testing.T) {
	Convey("Given a product type with string, integer and bool attributes", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory()
		it := svc.Interactors(ctx)

		product, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "product", DisplayName: "Product",
		})
		So(err, ShouldBeNil)
		typeID := product.ID.String()
		mk := func(name, dt string) {
			_, e := it.Attributes().Create(ctx, appattribute.CreateInput{
				TypeDefinitionID: typeID, InternalName: name, DisplayName: name, DataType: dt,
			})
			So(e, ShouldBeNil)
		}
		mk("sku", "string")
		mk("qty", "integer")
		mk("active", "bool")

		base := appvalue.ImportInput{
			TypeDefinitionID: typeID,
			KeyColumn:        "id",
			Mapping:          map[string]string{"sku": "sku", "qty": "qty", "active": "active"},
			Columns:          []string{"id", "sku", "qty", "active"},
			Rows: [][]string{
				{"e1", "ABC", "10", "true"},   // valid
				{"e2", "DEF", "20", "false"},  // valid
				{"e3", "GHI", "nope", "true"}, // invalid qty
				{"e4", "JKL", "30", "maybe"},  // invalid active
				{"", "MNO", "40", "true"},     // missing key
			},
			Mode: appvalue.ImportBestEffort,
		}

		Convey("When a dry run is requested", func() {
			in := base
			in.DryRun = true
			rep, err := it.Values().Import(ctx, in)

			Convey("Then it reports exactly the three invalid rows and writes nothing", func() {
				So(err, ShouldBeNil)
				So(rep.RowsTotal, ShouldEqual, 5)
				So(rep.RowsValid, ShouldEqual, 2)
				So(rep.RowsWritten, ShouldEqual, 0)
				So(rep.Errors, ShouldHaveLength, 3)
				rows := map[int]string{}
				for _, e := range rep.Errors {
					rows[e.Row] = e.Attribute
				}
				So(rows[3], ShouldEqual, "qty")
				So(rows[4], ShouldEqual, "active")
				_, hasRow5 := rows[5]
				So(hasRow5, ShouldBeTrue) // missing key

				// Nothing persisted.
				vals, err := it.Values().ListByEntity(ctx, typeID, "e1")
				So(err, ShouldBeNil)
				So(vals, ShouldBeEmpty)
			})
		})

		Convey("When committed best-effort", func() {
			rep, err := it.Values().Import(ctx, base)

			Convey("Then the two valid rows are written and the three invalid ones re-reported", func() {
				So(err, ShouldBeNil)
				So(rep.RowsWritten, ShouldEqual, 2)
				So(rep.Errors, ShouldHaveLength, 3)

				vals, err := it.Values().ListByEntity(ctx, typeID, "e1")
				So(err, ShouldBeNil)
				So(vals, ShouldHaveLength, 3)
			})

			Convey("Then an export of the type round-trips the written values", func() {
				out, err := it.Values().Export(ctx, appvalue.ExportInput{TypeDefinitionID: typeID})
				So(err, ShouldBeNil)
				So(out.Columns[0], ShouldEqual, "entity_id")

				// Locate the sku/qty/active column offsets and read e1's row.
				col := map[string]int{}
				for idx, c := range out.Columns {
					col[c] = idx
				}
				var e1 []string
				for _, row := range out.Rows {
					if row[0] == "e1" {
						e1 = row
					}
				}
				So(e1, ShouldNotBeNil)
				So(e1[col["sku"]], ShouldEqual, "ABC")
				So(e1[col["qty"]], ShouldEqual, "10")
				So(e1[col["active"]], ShouldEqual, "true")
			})
		})

		Convey("When committed transactionally with an invalid row present", func() {
			in := base
			in.Mode = appvalue.ImportTransactional
			rep, err := it.Values().Import(ctx, in)

			Convey("Then nothing is written and the invalid rows are reported", func() {
				So(err, ShouldBeNil)
				So(rep.RowsWritten, ShouldEqual, 0)
				So(rep.Errors, ShouldHaveLength, 3)
				vals, err := it.Values().ListByEntity(ctx, typeID, "e1")
				So(err, ShouldBeNil)
				So(vals, ShouldBeEmpty)
			})
		})
	})
}
