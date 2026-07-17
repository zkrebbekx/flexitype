package memory_test

import (
	"context"
	"strconv"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

// TestImportChunkBoundary exercises the chunked write path: imports that span
// more than one chunk transaction, a failure inside a chunk that must fall back
// to per-row writes, and an intra-chunk uniqueness conflict.
func TestImportChunkBoundary(t *testing.T) {
	Convey("Given a product type with a plain sku and a unique code", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory()
		it := svc.Interactors(ctx)

		product, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "product", DisplayName: "Product",
		})
		So(err, ShouldBeNil)
		typeID := product.ID.String()

		_, err = it.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: typeID, InternalName: "sku", DisplayName: "SKU", DataType: "string",
		})
		So(err, ShouldBeNil)
		_, err = it.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: typeID, InternalName: "code", DisplayName: "Code", DataType: "string", Unique: true,
		})
		So(err, ShouldBeNil)

		Convey("When a best-effort import spans several chunks (250 valid rows)", func() {
			rows := make([][]string, 0, 250)
			for n := 1; n <= 250; n++ {
				id := "e" + strconv.Itoa(n)
				rows = append(rows, []string{id, "SKU-" + strconv.Itoa(n), "CODE-" + strconv.Itoa(n)})
			}
			rep, err := it.Values().Import(ctx, appvalue.ImportInput{
				TypeDefinitionID: typeID,
				KeyColumn:        "id",
				Mapping:          map[string]string{"sku": "sku", "code": "code"},
				Columns:          []string{"id", "sku", "code"},
				Rows:             rows,
				Mode:             appvalue.ImportBestEffort,
			})

			Convey("Then every row is written across the chunk boundaries", func() {
				So(err, ShouldBeNil)
				So(rep.RowsTotal, ShouldEqual, 250)
				So(rep.RowsWritten, ShouldEqual, 250)
				So(rep.RowsValid, ShouldEqual, 250)
				So(rep.Errors, ShouldBeEmpty)

				// Spot-check rows either side of a chunk boundary (chunk size 100).
				for _, id := range []string{"e1", "e100", "e101", "e200", "e250"} {
					vals, verr := it.Values().ListByEntity(ctx, typeID, id)
					So(verr, ShouldBeNil)
					So(vals, ShouldHaveLength, 2)
				}
			})
		})

		Convey("When a chunk contains one invalid row (bad qty in a required-free set is n/a, use a unique clash against committed data)", func() {
			// Seed a committed entity holding CODE-1.
			seedRep, err := it.Values().Import(ctx, appvalue.ImportInput{
				TypeDefinitionID: typeID,
				KeyColumn:        "id",
				Mapping:          map[string]string{"code": "code"},
				Columns:          []string{"id", "code"},
				Rows:             [][]string{{"seed", "CODE-1"}},
				Mode:             appvalue.ImportBestEffort,
			})
			So(err, ShouldBeNil)
			So(seedRep.RowsWritten, ShouldEqual, 1)

			// Import a fresh chunk where one row re-uses CODE-1 (a uniqueness
			// conflict against the committed seed) and the rest are fine.
			rep, err := it.Values().Import(ctx, appvalue.ImportInput{
				TypeDefinitionID: typeID,
				KeyColumn:        "id",
				Mapping:          map[string]string{"code": "code"},
				Columns:          []string{"id", "code"},
				Rows: [][]string{
					{"a1", "CODE-10"},
					{"a2", "CODE-1"}, // conflicts with the committed seed
					{"a3", "CODE-11"},
				},
				Mode: appvalue.ImportBestEffort,
			})

			Convey("Then the good rows are written and only the conflicting row is reported", func() {
				So(err, ShouldBeNil)
				So(rep.RowsWritten, ShouldEqual, 2)
				So(rep.Errors, ShouldHaveLength, 1)
				So(rep.Errors[0].Row, ShouldEqual, 2)

				a1, _ := it.Values().ListByEntity(ctx, typeID, "a1")
				So(a1, ShouldHaveLength, 1)
				a3, _ := it.Values().ListByEntity(ctx, typeID, "a3")
				So(a3, ShouldHaveLength, 1)
				a2, _ := it.Values().ListByEntity(ctx, typeID, "a2")
				So(a2, ShouldBeEmpty) // the conflicting row wrote nothing
			})
		})

		Convey("When two rows in the same import claim the same unique code", func() {
			rep, err := it.Values().Import(ctx, appvalue.ImportInput{
				TypeDefinitionID: typeID,
				KeyColumn:        "id",
				Mapping:          map[string]string{"code": "code"},
				Columns:          []string{"id", "code"},
				Rows: [][]string{
					{"b1", "DUP"},
					{"b2", "DUP"}, // intra-import conflict with b1
				},
				Mode: appvalue.ImportBestEffort,
			})

			Convey("Then the first wins and the second is reported (later sees earlier)", func() {
				So(err, ShouldBeNil)
				So(rep.RowsWritten, ShouldEqual, 1)
				So(rep.Errors, ShouldHaveLength, 1)
				So(rep.Errors[0].Row, ShouldEqual, 2)

				b1, _ := it.Values().ListByEntity(ctx, typeID, "b1")
				So(b1, ShouldHaveLength, 1)
				b2, _ := it.Values().ListByEntity(ctx, typeID, "b2")
				So(b2, ShouldBeEmpty)
			})
		})

		Convey("When a transactional import contains an intra-import unique conflict", func() {
			rep, err := it.Values().Import(ctx, appvalue.ImportInput{
				TypeDefinitionID: typeID,
				KeyColumn:        "id",
				Mapping:          map[string]string{"code": "code"},
				Columns:          []string{"id", "code"},
				Rows: [][]string{
					{"c1", "T1"},
					{"c2", "T1"}, // conflict -> whole import rolls back
					{"c3", "T3"},
				},
				Mode: appvalue.ImportTransactional,
			})

			Convey("Then nothing is written (all-or-nothing preserved)", func() {
				So(err, ShouldBeNil)
				So(rep.RowsWritten, ShouldEqual, 0)
				So(rep.Errors, ShouldNotBeEmpty)
				for _, id := range []string{"c1", "c2", "c3"} {
					vals, _ := it.Values().ListByEntity(ctx, typeID, id)
					So(vals, ShouldBeEmpty)
				}
			})
		})

		Convey("When a large transactional import is fully valid", func() {
			rows := make([][]string, 0, 150)
			for n := 1; n <= 150; n++ {
				rows = append(rows, []string{"t" + strconv.Itoa(n), "TC-" + strconv.Itoa(n)})
			}
			rep, err := it.Values().Import(ctx, appvalue.ImportInput{
				TypeDefinitionID: typeID,
				KeyColumn:        "id",
				Mapping:          map[string]string{"code": "code"},
				Columns:          []string{"id", "code"},
				Rows:             rows,
				Mode:             appvalue.ImportTransactional,
			})

			Convey("Then all rows commit as one unit", func() {
				So(err, ShouldBeNil)
				So(rep.RowsWritten, ShouldEqual, 150)
				So(rep.RowsValid, ShouldEqual, 150)
				So(rep.Errors, ShouldBeEmpty)
			})
		})
	})
}
