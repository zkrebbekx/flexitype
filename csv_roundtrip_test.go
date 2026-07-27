package flexitype_test

import (
	"context"
	"encoding/json"
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

// TestCSVRoundTrip covers the export whose own doc promised it re-imports
// unchanged.
//
// Export rendered a quantity as "10 kg" and a media value as a bare object
// key — Value.String is a DISPLAY rendering, and neither form re-imports:
// "10 kg" reached the importer's default arm as a quoted string the quantity
// decoder rejects, and a bare key failed the media arm's JSON check. Worse,
// the export wrote one cell per (entity, attribute) by assignment, so a
// multi-valued attribute kept whichever row came last and every locale
// variant overwrote the others — data dropped with no error at all.
func TestCSVRoundTrip(t *testing.T) {
	Convey("Given a type with a quantity, a multi-valued and a localizable attribute", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory()
		ia := svc.Interactors(ctx)

		product, err := ia.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "product", DisplayName: "Product",
		})
		So(err, ShouldBeNil)
		mass, err := svc.Interactors(ctx).Units().Create(ctx, appunit.CreateInput{
			Name: "mass", BaseUnit: "g", Units: map[string]float64{"g": 1, "kg": 1000},
		})
		So(err, ShouldBeNil)

		attr := func(in appattribute.CreateInput) string {
			in.TypeDefinitionID = product.ID.String()
			in.DisplayName = in.InternalName
			a, aerr := svc.Interactors(ctx).Attributes().Create(ctx, in)
			So(aerr, ShouldBeNil)
			return a.ID.String()
		}
		weight := attr(appattribute.CreateInput{
			InternalName: "weight", DataType: "quantity", UnitFamilyID: mass.ID.String(),
		})
		tags := attr(appattribute.CreateInput{InternalName: "tags", DataType: "string", MultiValued: true})
		title := attr(appattribute.CreateInput{InternalName: "title", DataType: "string", Localizable: true})

		set := func(in appvalue.SetInput) {
			in.EntityID = "p1"
			in.TypeDefinitionID = product.ID.String()
			_, serr := svc.Interactors(ctx).Values().Set(ctx, in)
			So(serr, ShouldBeNil)
		}
		set(appvalue.SetInput{AttributeDefinitionID: weight,
			Value: json.RawMessage(`{"magnitude":"10","unit":"kg"}`)})
		set(appvalue.SetInput{AttributeDefinitionID: tags, Value: json.RawMessage(`"sale"`)})
		set(appvalue.SetInput{AttributeDefinitionID: tags, Value: json.RawMessage(`"clearance"`)})
		set(appvalue.SetInput{AttributeDefinitionID: title, Locale: "en", Value: json.RawMessage(`"Widget"`)})
		set(appvalue.SetInput{AttributeDefinitionID: title, Locale: "fr", Value: json.RawMessage(`"Gadget"`)})

		exported, err := svc.Interactors(ctx).Values().Export(ctx, appvalue.ExportInput{
			TypeDefinitionID: product.ID.String(),
		})
		So(err, ShouldBeNil)
		So(exported.Rows, ShouldHaveLength, 1)

		cell := func(name string) string {
			for i, c := range exported.Columns {
				if c == name {
					return exported.Rows[0][i]
				}
			}
			return ""
		}

		Convey("Then a quantity exports as the JSON the API accepts", func() {
			So(cell("weight"), ShouldContainSubstring, `"magnitude"`)
			So(cell("weight"), ShouldContainSubstring, `"unit"`)
			So(cell("weight"), ShouldNotEqual, "10 kg")
		})

		Convey("Then every member of a multi-valued attribute survives", func() {
			So(cell("tags"), ShouldContainSubstring, "sale")
			So(cell("tags"), ShouldContainSubstring, "clearance")
		})

		Convey("Then every locale of a localizable attribute survives", func() {
			So(cell("title"), ShouldContainSubstring, "Widget")
			So(cell("title"), ShouldContainSubstring, "Gadget")
			So(cell("title"), ShouldContainSubstring, `"fr"`)
		})

		Convey("When the export is imported into a fresh entity", func() {
			report, ierr := svc.Interactors(ctx).Values().Import(ctx, appvalue.ImportInput{
				TypeDefinitionID: product.ID.String(),
				KeyColumn:        "entity_id",
				Mapping: map[string]string{
					"weight": "weight", "tags": "tags", "title": "title",
				},
				Columns: exported.Columns,
				Rows:    [][]string{{"p2", cell("weight"), cell("tags"), cell("title")}},
			})
			So(ierr, ShouldBeNil)

			Convey("Then every row is accepted", func() {
				So(report.Errors, ShouldBeEmpty)
				So(report.RowsWritten, ShouldEqual, 1)
			})

			Convey("Then the values match what was exported", func() {
				vals, verr := svc.Interactors(ctx).Values().ListByEntity(ctx, product.ID.String(), "p2")
				So(verr, ShouldBeNil)

				byAttr := map[string][]string{}
				for _, v := range vals {
					byAttr[v.AttributeDefinitionID.String()] = append(
						byAttr[v.AttributeDefinitionID.String()], v.Value.String())
				}
				So(byAttr[weight], ShouldHaveLength, 1)
				So(byAttr[weight][0], ShouldEqual, "10 kg")
				So(byAttr[tags], ShouldHaveLength, 2)
				So(byAttr[title], ShouldHaveLength, 2)
			})
		})
	})
}
