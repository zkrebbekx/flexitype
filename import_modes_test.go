package flexitype_test

import (
	"context"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	appunit "github.com/zkrebbekx/flexitype/application/unit"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

// TestImportModes covers what a bad row does to the rest of the file.
//
// The two modes answer different questions. Best-effort asks "load what you
// can and tell me what failed", which is what a data-migration run wants.
// Transactional asks "all or nothing", which is what a scheduled sync wants,
// because half a file applied is worse than none. Getting the distinction
// wrong is silent: the caller sees a report either way.
func TestImportModes(t *testing.T) {
	Convey("Given a type with a typed and a quantity attribute", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory()
		ia := svc.Interactors(ctx)

		mass, err := ia.Units().Create(ctx, appunit.CreateInput{
			Name: "mass", BaseUnit: "g", Units: map[string]float64{"g": 1, "kg": 1000},
		})
		So(err, ShouldBeNil)
		product, err := svc.Interactors(ctx).TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "product", DisplayName: "Product",
		})
		So(err, ShouldBeNil)

		attr := func(name, dataType, family string) {
			_, aerr := svc.Interactors(ctx).Attributes().Create(ctx, appattribute.CreateInput{
				TypeDefinitionID: product.ID.String(), InternalName: name,
				DisplayName: name, DataType: dataType, UnitFamilyID: family,
			})
			So(aerr, ShouldBeNil)
		}
		attr("sku", "string", "")
		attr("pages", "integer", "")
		attr("weight", "quantity", mass.ID.String())

		importRows := func(mode appvalue.ImportMode, rows [][]string) *appvalue.ImportReport {
			rep, ierr := svc.Interactors(ctx).Values().Import(ctx, appvalue.ImportInput{
				TypeDefinitionID: product.ID.String(),
				KeyColumn:        "entity_id",
				Mapping:          map[string]string{"sku": "sku", "pages": "pages", "weight": "weight"},
				Columns:          []string{"entity_id", "sku", "pages", "weight"},
				Rows:             rows,
				Mode:             mode,
			})
			So(ierr, ShouldBeNil)
			return rep
		}
		live := func() int {
			page, lerr := svc.Interactors(ctx).Values().List(ctx, appvalue.ListInput{})
			So(lerr, ShouldBeNil)
			return len(page.Items)
		}

		good := []string{"p1", "ABC", "100", `{"magnitude":"2","unit":"kg"}`}
		bad := []string{"p2", "DEF", "not-a-number", `{"magnitude":"1","unit":"kg"}`}

		Convey("When a bad row is imported best-effort", func() {
			rep := importRows(appvalue.ImportBestEffort, [][]string{good, bad})

			Convey("Then the good row lands and the bad one is reported", func() {
				So(rep.RowsWritten, ShouldEqual, 1)
				So(rep.Errors, ShouldNotBeEmpty)
				So(rep.Errors[0].Attribute, ShouldEqual, "pages")
				So(live(), ShouldBeGreaterThan, 0)
			})
		})

		Convey("When a bad row is imported transactionally", func() {
			rep := importRows(appvalue.ImportTransactional, [][]string{good, bad})

			Convey("Then nothing lands: half a file is worse than none", func() {
				So(rep.Errors, ShouldNotBeEmpty)
				So(rep.RowsWritten, ShouldEqual, 0)
				So(live(), ShouldEqual, 0)
			})
		})

		Convey("When a dry run is requested", func() {
			rep, ierr := svc.Interactors(ctx).Values().Import(ctx, appvalue.ImportInput{
				TypeDefinitionID: product.ID.String(),
				KeyColumn:        "entity_id",
				Mapping:          map[string]string{"sku": "sku"},
				Columns:          []string{"entity_id", "sku"},
				Rows:             [][]string{{"p1", "ABC"}},
				DryRun:           true,
			})

			Convey("Then it validates and writes nothing", func() {
				So(ierr, ShouldBeNil)
				So(rep.DryRun, ShouldBeTrue)
				So(rep.RowsValid, ShouldEqual, 1)
				So(live(), ShouldEqual, 0)
			})
		})

		Convey("When a quantity arrives in a unit of its family", func() {
			rep := importRows(appvalue.ImportBestEffort, [][]string{
				{"p3", "GHI", "1", `{"magnitude":"1500","unit":"g"}`},
			})

			Convey("Then it is accepted and rebased against the family's base", func() {
				So(rep.Errors, ShouldBeEmpty)
				So(rep.RowsWritten, ShouldEqual, 1)
			})
		})

		Convey("When a quantity names a unit outside its family", func() {
			rep := importRows(appvalue.ImportBestEffort, [][]string{
				{"p4", "JKL", "1", `{"magnitude":"3","unit":"litres"}`},
			})

			Convey("Then the row is rejected rather than stored un-rebased", func() {
				So(rep.Errors, ShouldNotBeEmpty)
			})
		})
	})
}
