package flexitype_test

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

// TestImportChunkBoundaryPostgres re-runs the chunked-import semantics over the
// real Postgres repositories, where a failed statement aborts the surrounding
// transaction — so the chunk-then-fallback path and the one-transaction
// transactional path are exercised against true SQL transaction behavior, not
// just the memory twin.
func TestImportChunkBoundaryPostgres(t *testing.T) {
	pool := openTestDB(t)
	defer func() { _ = pool.Close() }()

	svc := flexitype.New(pool)
	if err := svc.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	Convey("Given a product type with a unique code in Postgres", t, func() {
		truncateAll(t, pool)
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		it := svc.Interactors(ctx)

		product, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{InternalName: "product", DisplayName: "Product"})
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

		Convey("When a best-effort import spans several chunks (250 rows)", func() {
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
			So(err, ShouldBeNil)

			Convey("Then every row is written across chunk boundaries", func() {
				So(rep.RowsWritten, ShouldEqual, 250)
				So(rep.RowsValid, ShouldEqual, 250)
				So(rep.Errors, ShouldBeEmpty)
				for _, id := range []string{"e1", "e100", "e101", "e250"} {
					vals, verr := it.Values().ListByEntity(ctx, typeID, id)
					So(verr, ShouldBeNil)
					So(vals, ShouldHaveLength, 2)
				}
			})
		})

		Convey("When a best-effort chunk holds a row conflicting with committed data", func() {
			seed, err := it.Values().Import(ctx, appvalue.ImportInput{
				TypeDefinitionID: typeID, KeyColumn: "id",
				Mapping: map[string]string{"code": "code"}, Columns: []string{"id", "code"},
				Rows: [][]string{{"seed", "CODE-1"}}, Mode: appvalue.ImportBestEffort,
			})
			So(err, ShouldBeNil)
			So(seed.RowsWritten, ShouldEqual, 1)

			rep, err := it.Values().Import(ctx, appvalue.ImportInput{
				TypeDefinitionID: typeID, KeyColumn: "id",
				Mapping: map[string]string{"code": "code"}, Columns: []string{"id", "code"},
				Rows: [][]string{
					{"a1", "CODE-10"},
					{"a2", "CODE-1"}, // conflicts with committed seed
					{"a3", "CODE-11"},
				},
				Mode: appvalue.ImportBestEffort,
			})
			So(err, ShouldBeNil)

			Convey("Then the good rows land and only the conflict is reported (fallback path)", func() {
				So(rep.RowsWritten, ShouldEqual, 2)
				So(rep.Errors, ShouldHaveLength, 1)
				So(rep.Errors[0].Row, ShouldEqual, 2)
				a2, _ := it.Values().ListByEntity(ctx, typeID, "a2")
				So(a2, ShouldBeEmpty)
				a1, _ := it.Values().ListByEntity(ctx, typeID, "a1")
				So(a1, ShouldHaveLength, 1)
			})
		})

		Convey("When a transactional import has an intra-import conflict", func() {
			rep, err := it.Values().Import(ctx, appvalue.ImportInput{
				TypeDefinitionID: typeID, KeyColumn: "id",
				Mapping: map[string]string{"code": "code"}, Columns: []string{"id", "code"},
				Rows: [][]string{
					{"c1", "T1"},
					{"c2", "T1"}, // conflict -> whole set rolls back
					{"c3", "T3"},
				},
				Mode: appvalue.ImportTransactional,
			})
			So(err, ShouldBeNil)

			Convey("Then nothing is written (all-or-nothing preserved over real SQL)", func() {
				So(rep.RowsWritten, ShouldEqual, 0)
				So(rep.Errors, ShouldNotBeEmpty)
				for _, id := range []string{"c1", "c2", "c3"} {
					vals, _ := it.Values().ListByEntity(ctx, typeID, id)
					So(vals, ShouldBeEmpty)
				}
			})
		})
	})
}
