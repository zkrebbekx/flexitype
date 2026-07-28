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

// TestImportOrdersEntitiesCanonically covers the lock ordering the CSV write
// paths now apply.
//
// A file's rows arrive in whatever order it has, so two imports over the same
// entities took the entity-summary rows in opposite order. The ordering is
// applied inside the unit of work, and the row number an error reports is
// still the file's — this pins both.
func TestImportOrdersEntitiesCanonically(t *testing.T) {
	Convey("Given a type with one attribute", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory()
		it := svc.Interactors(ctx)

		product, err := it.TypeDefinitions().Create(ctx,
			apptypedef.CreateInput{InternalName: "product", DisplayName: "Product"})
		So(err, ShouldBeNil)
		_, err = it.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: product.ID.String(), InternalName: "title",
			DisplayName: "Title", DataType: "string",
		})
		So(err, ShouldBeNil)

		Convey("When rows arrive in reverse entity order", func() {
			report, ierr := svc.Interactors(ctx).Values().Import(ctx, appvalue.ImportInput{
				TypeDefinitionID: product.ID.String(),
				KeyColumn:        "entity_id",
				Mapping:          map[string]string{"title": "title"},
				Columns:          []string{"entity_id", "title"},
				Rows: [][]string{
					{"p-c", "third"}, {"p-a", "first"}, {"p-b", "second"},
				},
			})

			Convey("Then every row is written, whatever the file order", func() {
				So(ierr, ShouldBeNil)
				So(report.Errors, ShouldBeEmpty)
				So(report.RowsWritten, ShouldEqual, 3)

				for entity, want := range map[string]string{
					"p-a": "first", "p-b": "second", "p-c": "third",
				} {
					vals, verr := svc.Interactors(ctx).Values().ListByEntity(
						ctx, product.ID.String(), entity)
					So(verr, ShouldBeNil)
					So(vals, ShouldHaveLength, 1)
					So(vals[0].Value.Text(), ShouldEqual, want)
				}
			})
		})

		Convey("When one row in the middle is invalid", func() {
			report, ierr := svc.Interactors(ctx).Values().Import(ctx, appvalue.ImportInput{
				TypeDefinitionID: product.ID.String(),
				KeyColumn:        "entity_id",
				Mapping:          map[string]string{"title": "title"},
				Columns:          []string{"entity_id", "title"},
				Rows: [][]string{
					{"p-z", "ok"}, {"", "missing key"}, {"p-y", "ok"},
				},
			})

			Convey("Then the error names the FILE's row, not the sorted position", func() {
				So(ierr, ShouldBeNil)
				So(report.Errors, ShouldNotBeEmpty)
				So(report.Errors[0].Row, ShouldEqual, 2)
			})
		})
	})
}

// TestJSONColumnSurvivesRoundTrip covers the silent corruption a re-import of
// this tool's own export produced.
//
// The multi-value cell format was in band: first a bare array of
// {"value",…} objects, then {"values":[…]}. Both are ordinary JSON, and the
// second is exactly what an export of a json column looks like — so a
// re-import read one document as two members, wrote both to a single-valued
// attribute, kept the last, and reported one row written with zero errors. A
// bulk migration therefore lost data with a clean report.
func TestJSONColumnSurvivesRoundTrip(t *testing.T) {
	Convey("Given a json attribute holding a document shaped like the cell format", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory()
		ia := svc.Interactors(ctx)

		product, err := ia.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "product", DisplayName: "Product",
		})
		So(err, ShouldBeNil)
		doc, err := svc.Interactors(ctx).Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: product.ID.String(), InternalName: "payload",
			DisplayName: "Payload", DataType: "json",
		})
		So(err, ShouldBeNil)

		// The shape the tagged format used, as ordinary data.
		const payload = `{"values":[{"value":{"x":1}},{"value":{"y":2}}]}`
		_, err = svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{
			AttributeDefinitionID: doc.ID.String(), EntityID: "p1",
			TypeDefinitionID: product.ID.String(), Value: json.RawMessage(payload),
		})
		So(err, ShouldBeNil)

		exported, eerr := svc.Interactors(ctx).Values().Export(ctx, appvalue.ExportInput{
			TypeDefinitionID: product.ID.String(),
		})
		So(eerr, ShouldBeNil)
		So(exported.Rows, ShouldHaveLength, 1)
		cell := func(name string) string {
			for i, c := range exported.Columns {
				if c == name {
					return exported.Rows[0][i]
				}
			}
			return ""
		}

		Convey("When the export is re-imported into a fresh entity", func() {
			report, ierr := svc.Interactors(ctx).Values().Import(ctx, appvalue.ImportInput{
				TypeDefinitionID: product.ID.String(),
				KeyColumn:        "entity_id",
				Mapping:          map[string]string{"payload": "payload"},
				Columns:          exported.Columns,
				Rows:             [][]string{{"p2", cell("payload")}},
			})
			So(ierr, ShouldBeNil)
			So(report.Errors, ShouldBeEmpty)

			Convey("Then the document comes back whole, not as its last member", func() {
				vals, verr := svc.Interactors(ctx).Values().ListByEntity(ctx, product.ID.String(), "p2")
				So(verr, ShouldBeNil)
				So(vals, ShouldHaveLength, 1)

				var got, want any
				So(json.Unmarshal([]byte(vals[0].Value.String()), &got), ShouldBeNil)
				So(json.Unmarshal([]byte(payload), &want), ShouldBeNil)
				So(got, ShouldResemble, want)
			})
		})
	})
}
