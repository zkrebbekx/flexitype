package memory_test

import (
	"context"
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

func TestEntityRevisions(t *testing.T) {
	Convey("Given an entity mutated across three revisions", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory()
		it := svc.Interactors(ctx)

		product, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{InternalName: "product", DisplayName: "Product"})
		So(err, ShouldBeNil)
		typeID := product.ID.String()
		attr := func(name, dt string) string {
			s, e := it.Attributes().Create(ctx, appattribute.CreateInput{TypeDefinitionID: typeID, InternalName: name, DisplayName: name, DataType: dt})
			So(e, ShouldBeNil)
			return s.ID.String()
		}
		sku := attr("sku", "string")
		price := attr("price", "integer")
		color := attr("color", "string")

		set := func(attrID, v string) {
			raw := json.RawMessage(v)
			_, e := it.Values().Set(ctx, appvalue.SetInput{AttributeDefinitionID: attrID, EntityID: "e1", TypeDefinitionID: typeID, Value: raw})
			So(e, ShouldBeNil)
		}
		valueID := func(attrID string) string {
			snaps, e := it.Values().ListByEntity(ctx, typeID, "e1")
			So(e, ShouldBeNil)
			for _, s := range snaps {
				if s.AttributeDefinitionID.String() == attrID {
					return s.ID.String()
				}
			}
			return ""
		}

		// r1: {sku=A, price=100}
		set(sku, `"A"`)
		set(price, `100`)
		r1, err := it.Revisions().Create(ctx, typeID, "e1", "v1")
		So(err, ShouldBeNil)

		// r2: {sku=A, price=200, color=red}
		set(price, `200`)
		set(color, `"red"`)
		_, err = it.Revisions().Create(ctx, typeID, "e1", "v2")
		So(err, ShouldBeNil)

		// r3: {price=300, color=red} — sku removed
		set(price, `300`)
		_, err = it.Values().Remove(ctx, valueID(sku))
		So(err, ShouldBeNil)
		r3, err := it.Revisions().Create(ctx, typeID, "e1", "v3")
		So(err, ShouldBeNil)

		Convey("When each revision is read back", func() {
			got1, err := it.Revisions().Get(ctx, r1.ID.String())
			So(err, ShouldBeNil)
			got3, err := it.Revisions().Get(ctx, r3.ID.String())
			So(err, ShouldBeNil)

			Convey("Then it holds the exact historical values", func() {
				vals1 := map[string]string{}
				for _, v := range got1.Values {
					vals1[v.InternalName] = v.Value
				}
				So(vals1, ShouldResemble, map[string]string{"sku": "A", "price": "100"})

				vals3 := map[string]string{}
				for _, v := range got3.Values {
					vals3[v.InternalName] = v.Value
				}
				So(vals3, ShouldResemble, map[string]string{"price": "300", "color": "red"})
			})
		})

		Convey("When r1 and r3 are diffed", func() {
			diff, err := it.Revisions().Diff(ctx, r1.ID.String(), r3.ID.String())
			So(err, ShouldBeNil)

			Convey("Then adds, changes and removes are all listed", func() {
				kinds := map[string]string{}
				for _, c := range diff.Changes {
					kinds[c.InternalName] = c.Kind
				}
				So(kinds["price"], ShouldEqual, "changed")
				So(kinds["color"], ShouldEqual, "added")
				So(kinds["sku"], ShouldEqual, "removed")
			})
		})

		Convey("When r1 is restored", func() {
			_, err := it.Revisions().Restore(ctx, r1.ID.String())
			So(err, ShouldBeNil)

			Convey("Then current state matches r1 and history is retained", func() {
				snaps, err := it.Values().ListByEntity(ctx, typeID, "e1")
				So(err, ShouldBeNil)
				cur := map[string]string{}
				for _, s := range snaps {
					cur[s.AttributeDefinitionID.String()] = s.Value.String()
				}
				So(cur, ShouldResemble, map[string]string{sku: "A", price: "100"})

				revs, err := it.Revisions().List(ctx, typeID, "e1")
				So(err, ShouldBeNil)
				So(revs, ShouldHaveLength, 4) // r1, r2, r3 + the restore revision
			})
		})
	})
}

func TestEntityRevisionsScoped(t *testing.T) {
	Convey("Given an entity with locale/channel-scoped values", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory()
		it := svc.Interactors(ctx)

		product, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{InternalName: "product", DisplayName: "Product"})
		So(err, ShouldBeNil)
		typeID := product.ID.String()
		desc, err := it.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: typeID, InternalName: "description", DisplayName: "Description",
			DataType: "string", Localizable: true, Scopable: true,
		})
		So(err, ShouldBeNil)

		set := func(locale, channel, v string) {
			raw, _ := json.Marshal(v)
			_, e := it.Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: desc.ID.String(), EntityID: "e1", TypeDefinitionID: typeID,
				Locale: locale, Channel: channel, Value: raw,
			})
			So(e, ShouldBeNil)
		}
		set("en", "web", "Hello")
		set("de", "web", "Hallo")
		set("en", "print", "Hi")
		set("de", "print", "Hoi")

		r1, err := it.Revisions().Create(ctx, typeID, "e1", "v1")
		So(err, ShouldBeNil)
		So(r1.Values, ShouldHaveLength, 4) // every scope captured distinctly

		// Mutate a single scope after the snapshot.
		set("en", "web", "CHANGED")

		Convey("When r1 is restored", func() {
			_, err := it.Revisions().Restore(ctx, r1.ID.String())
			So(err, ShouldBeNil)

			Convey("Then every scope returns to the snapshot with no collapse or spurious base row", func() {
				snaps, err := it.Values().ListByEntity(ctx, typeID, "e1")
				So(err, ShouldBeNil)
				byScope := map[string]string{}
				for _, s := range snaps {
					byScope[s.Locale+"/"+s.Channel] = s.Value.String()
				}
				So(len(snaps), ShouldEqual, 4) // exactly the four scopes — no base "" row
				So(byScope, ShouldResemble, map[string]string{
					"en/web":   "Hello",
					"de/web":   "Hallo",
					"en/print": "Hi",
					"de/print": "Hoi",
				})
			})
		})

		Convey("When the snapshot is diffed against the mutated state", func() {
			r2, err := it.Revisions().Create(ctx, typeID, "e1", "v2")
			So(err, ShouldBeNil)
			diff, err := it.Revisions().Diff(ctx, r1.ID.String(), r2.ID.String())
			So(err, ShouldBeNil)

			Convey("Then only the mutated scope is reported changed", func() {
				So(diff.Changes, ShouldHaveLength, 1)
				c := diff.Changes[0]
				So(c.InternalName, ShouldEqual, "description")
				So(c.Locale, ShouldEqual, "en")
				So(c.Channel, ShouldEqual, "web")
				So(c.Kind, ShouldEqual, "changed")
				So(c.Before, ShouldEqual, "Hello")
				So(c.After, ShouldEqual, "CHANGED")
			})
		})
	})
}
