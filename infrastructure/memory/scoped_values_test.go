package memory_test

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

func TestScopedValues(t *testing.T) {
	Convey("Given a localizable+scopable description across locale × channel", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory(flexitype.WithSearchIndex())
		it := svc.Interactors(ctx)

		product, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{InternalName: "product", DisplayName: "Product"})
		So(err, ShouldBeNil)
		typeID := product.ID.String()
		desc, err := it.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: typeID, InternalName: "description", DisplayName: "Description",
			DataType: "string", Localizable: true, Scopable: true,
		})
		So(err, ShouldBeNil)
		sku, err := it.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: typeID, InternalName: "sku", DisplayName: "SKU", DataType: "string",
		})
		So(err, ShouldBeNil)

		set := func(entity, locale, channel, v string) error {
			raw, _ := json.Marshal(v)
			_, e := it.Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: desc.ID.String(), EntityID: entity, TypeDefinitionID: typeID,
				Locale: locale, Channel: channel, Value: raw,
			})
			return e
		}
		So(set("p1", "en", "web", "Hello"), ShouldBeNil)
		So(set("p1", "de", "web", "Hallo"), ShouldBeNil)
		So(set("p1", "en", "print", "Hi"), ShouldBeNil)
		So(set("p1", "de", "print", "Hoi"), ShouldBeNil)

		Convey("When the entity's values are read", func() {
			vals, err := it.Values().ListByEntity(ctx, typeID, "p1")
			So(err, ShouldBeNil)

			Convey("Then each scope reads back independently", func() {
				byScope := map[string]string{}
				for _, v := range vals {
					byScope[v.Locale+"/"+v.Channel] = v.Value.String()
				}
				So(byScope["en/web"], ShouldEqual, "Hello")
				So(byScope["de/web"], ShouldEqual, "Hallo")
				So(byScope["en/print"], ShouldEqual, "Hi")
				So(byScope["de/print"], ShouldEqual, "Hoi")
			})
		})

		Convey("When a scope is set on a non-localizable attribute", func() {
			raw, _ := json.Marshal("ABC")
			_, err := it.Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: sku.ID.String(), EntityID: "p1", TypeDefinitionID: typeID,
				Locale: "en", Value: raw,
			})

			Convey("Then it is rejected", func() {
				So(err, ShouldNotBeNil)
				So(domainerrors.CodeOf(err), ShouldEqual, domainerrors.CodeValidation)
			})
		})

		Convey("When FQL filters within a selected scope", func() {
			match := func(locale, channel, q string) []string {
				out, e := it.Query().Execute(ctx, appquery.ExecuteInput{
					Type: "product", Query: q, Scope: valueobjects.Scope{Locale: locale, Channel: channel},
				})
				So(e, ShouldBeNil)
				ids := []string{}
				for _, it := range out.Items {
					ids = append(ids, it.EntityID)
				}
				return ids
			}

			Convey("Then it matches only within that scope", func() {
				So(match("en", "web", `description = "Hello"`), ShouldResemble, []string{"p1"})
				So(match("de", "web", `description = "Hello"`), ShouldBeEmpty)
				So(match("de", "web", `description = "Hallo"`), ShouldResemble, []string{"p1"})
				So(match("en", "print", `description = "Hi"`), ShouldResemble, []string{"p1"})
			})
		})

		Convey("When a unique attribute is scoped", func() {
			code, err := it.Attributes().Create(ctx, appattribute.CreateInput{
				TypeDefinitionID: typeID, InternalName: "code", DisplayName: "Code",
				DataType: "string", Localizable: true, Unique: true,
			})
			So(err, ShouldBeNil)
			setCode := func(entity, locale, v string) error {
				raw, _ := json.Marshal(v)
				_, e := it.Values().Set(ctx, appvalue.SetInput{
					AttributeDefinitionID: code.ID.String(), EntityID: entity, TypeDefinitionID: typeID,
					Locale: locale, Value: raw,
				})
				return e
			}

			Convey("Then uniqueness applies per scope", func() {
				So(setCode("p1", "en", "X"), ShouldBeNil)
				So(setCode("p2", "de", "X"), ShouldBeNil)                           // different scope: allowed
				So(domainerrors.IsConflict(setCode("p2", "en", "X")), ShouldBeTrue) // same scope: conflict
			})
		})
	})
}
