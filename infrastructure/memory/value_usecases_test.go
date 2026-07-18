package memory_test

import (
	"context"
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	"github.com/zkrebbekx/flexitype/application"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
)

type valFixture struct {
	ctx    context.Context
	it     *application.Interactors
	typeID string
	sku    string
	price  string
	note   string // scopable + localizable
}

func newValFixture() *valFixture {
	ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
	it := flexitype.NewInMemory().Interactors(ctx)

	product, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
		InternalName: "product", DisplayName: "Product",
	})
	So(err, ShouldBeNil)
	typeID := product.ID.String()

	attr := func(in appattribute.CreateInput) string {
		in.TypeDefinitionID = typeID
		in.DisplayName = in.InternalName
		snap, e := it.Attributes().Create(ctx, in)
		So(e, ShouldBeNil)
		return snap.ID.String()
	}

	return &valFixture{
		ctx: ctx, it: it, typeID: typeID,
		sku:   attr(appattribute.CreateInput{InternalName: "sku", DataType: "string"}),
		price: attr(appattribute.CreateInput{InternalName: "price", DataType: "decimal"}),
		note: attr(appattribute.CreateInput{
			InternalName: "note", DataType: "string", Scopable: true, Localizable: true,
		}),
	}
}

func (f *valFixture) set(attrID, entityID string, value any) string {
	raw, err := json.Marshal(value)
	So(err, ShouldBeNil)
	snap, err := f.it.Values().Set(f.ctx, appvalue.SetInput{
		AttributeDefinitionID: attrID, EntityID: entityID,
		TypeDefinitionID: f.typeID, Value: raw,
	})
	So(err, ShouldBeNil)
	return snap.ID.String()
}

func TestValueGet(t *testing.T) {
	Convey("Given a stored value", t, func() {
		f := newValFixture()
		id := f.set(f.sku, "p1", "ABC-1")

		Convey("When it is fetched by ID", func() {
			got, err := f.it.Values().Get(f.ctx, id)

			Convey("Then the stored value comes back with its anchors", func() {
				So(err, ShouldBeNil)
				So(got.AttributeDefinitionID.String(), ShouldEqual, f.sku)
				So(string(got.EntityID), ShouldEqual, "p1")
				So(got.Value.Text(), ShouldEqual, "ABC-1")
			})
		})

		Convey("When a malformed or unknown ID is fetched", func() {
			_, badErr := f.it.Values().Get(f.ctx, "nope")
			_, missingErr := f.it.Values().Get(f.ctx, valueobjects.NewAttributeValueID().String())

			Convey("Then the malformed ID is validation and the unknown one is not-found", func() {
				So(domainerrors.IsValidation(badErr), ShouldBeTrue)
				So(domainerrors.IsNotFound(missingErr), ShouldBeTrue)
			})
		})

		Convey("When another tenant fetches it", func() {
			other := uow.WithTenant(context.Background(), valueobjects.TenantID("other"))
			_, err := f.it.Values().Get(other, id)

			Convey("Then it is invisible across the tenant boundary", func() {
				So(domainerrors.IsNotFound(err), ShouldBeTrue)
			})
		})

		Convey("When a principal without read access on the attribute fetches it", func() {
			restricted := uow.WithAccess(f.ctx, uow.Access{
				Attr: map[string]uow.Perm{"sku": uow.PermNone},
			})
			_, err := f.it.Values().Get(restricted, id)

			Convey("Then the value is reported missing rather than leaked", func() {
				So(domainerrors.IsNotFound(err), ShouldBeTrue)
			})
		})
	})
}

func TestValueListing(t *testing.T) {
	Convey("Given values across two entities", t, func() {
		f := newValFixture()
		f.set(f.sku, "p1", "ABC-1")
		f.set(f.price, "p1", "10.00")
		f.set(f.sku, "p2", "ABC-2")

		Convey("When one entity's values are listed", func() {
			out, err := f.it.Values().ListByEntity(f.ctx, f.typeID, "p1")

			Convey("Then only that entity's values come back", func() {
				So(err, ShouldBeNil)
				So(out, ShouldHaveLength, 2)
			})
		})

		Convey("When a principal cannot read one attribute", func() {
			restricted := uow.WithAccess(f.ctx, uow.Access{
				Attr: map[string]uow.Perm{"sku": uow.PermRead, "price": uow.PermNone},
			})
			out, err := f.it.Values().ListByEntity(restricted, f.typeID, "p1")

			Convey("Then the unreadable attribute is redacted from the listing", func() {
				So(err, ShouldBeNil)
				So(out, ShouldHaveLength, 1)
				So(out[0].AttributeDefinitionID.String(), ShouldEqual, f.sku)
			})
		})

		Convey("When the type or entity ID is malformed", func() {
			_, typeErr := f.it.Values().ListByEntity(f.ctx, "nope", "p1")
			_, entityErr := f.it.Values().ListByEntity(f.ctx, f.typeID, "")

			Convey("Then each is rejected as validation", func() {
				So(domainerrors.IsValidation(typeErr), ShouldBeTrue)
				So(domainerrors.IsValidation(entityErr), ShouldBeTrue)
			})
		})

		Convey("When several entities are hydrated in one call", func() {
			out, err := f.it.Values().ListByEntities(f.ctx, []string{"p1", "p2"})

			Convey("Then every entity's values come back in one pass", func() {
				So(err, ShouldBeNil)
				So(out, ShouldHaveLength, 3)
			})

			Convey("And a malformed entity ID fails the whole batch", func() {
				_, err := f.it.Values().ListByEntities(f.ctx, []string{"p1", ""})
				So(domainerrors.IsValidation(err), ShouldBeTrue)
			})
		})

		Convey("When entities of the type are summarised", func() {
			out, err := f.it.Values().ListEntities(f.ctx, f.typeID, false, db.PageArgs{})

			Convey("Then each entity appears once with its value count", func() {
				So(err, ShouldBeNil)
				So(out.Items, ShouldHaveLength, 2)
				counts := map[string]int{}
				for _, e := range out.Items {
					counts[e.EntityID] = e.ValueCount
				}
				So(counts["p1"], ShouldEqual, 2)
				So(counts["p2"], ShouldEqual, 1)
			})
		})

		Convey("When entity summaries include descendant types", func() {
			child, err := f.it.TypeDefinitions().Create(f.ctx, apptypedef.CreateInput{
				InternalName: "bike", DisplayName: "Bike", ExtendsID: f.typeID,
			})
			So(err, ShouldBeNil)
			raw, err := json.Marshal("BIKE-1")
			So(err, ShouldBeNil)
			_, err = f.it.Values().Set(f.ctx, appvalue.SetInput{
				AttributeDefinitionID: f.sku, EntityID: "b1",
				TypeDefinitionID: child.ID.String(), Value: raw,
			})
			So(err, ShouldBeNil)

			without, err := f.it.Values().ListEntities(f.ctx, f.typeID, false, db.PageArgs{})
			So(err, ShouldBeNil)
			with, err := f.it.Values().ListEntities(f.ctx, f.typeID, true, db.PageArgs{})
			So(err, ShouldBeNil)

			Convey("Then subtype entities join the roll-up only when asked for", func() {
				So(without.Items, ShouldHaveLength, 2)
				So(with.Items, ShouldHaveLength, 3)
			})
		})

		Convey("When entity summaries are asked for a malformed or unknown type", func() {
			_, badErr := f.it.Values().ListEntities(f.ctx, "nope", false, db.PageArgs{})
			_, missingErr := f.it.Values().ListEntities(f.ctx,
				valueobjects.NewTypeDefinitionID().String(), true, db.PageArgs{})
			zero := 0
			_, pageErr := f.it.Values().ListEntities(f.ctx, f.typeID, false, db.PageArgs{Limit: &zero})

			Convey("Then IDs and page args are each rejected on their own terms", func() {
				So(domainerrors.IsValidation(badErr), ShouldBeTrue)
				So(domainerrors.IsNotFound(missingErr), ShouldBeTrue)
				So(domainerrors.IsValidation(pageErr), ShouldBeTrue)
			})
		})

		Convey("When values are listed with filters", func() {
			byType, err := f.it.Values().List(f.ctx,
				appvalue.ListInput{TypeDefinitionID: f.typeID})
			So(err, ShouldBeNil)
			byAttr, err := f.it.Values().List(f.ctx,
				appvalue.ListInput{AttributeDefinitionID: f.price})
			So(err, ShouldBeNil)
			byEntity, err := f.it.Values().List(f.ctx,
				appvalue.ListInput{EntityID: "p2"})
			So(err, ShouldBeNil)

			Convey("Then each filter narrows exactly as declared", func() {
				So(byType.Items, ShouldHaveLength, 3)
				So(byAttr.Items, ShouldHaveLength, 1)
				So(byEntity.Items, ShouldHaveLength, 1)
			})
		})

		Convey("When a list filter is malformed", func() {
			_, typeErr := f.it.Values().List(f.ctx, appvalue.ListInput{TypeDefinitionID: "nope"})
			_, attrErr := f.it.Values().List(f.ctx, appvalue.ListInput{AttributeDefinitionID: "nope"})
			cursor := "!!!"
			_, pageErr := f.it.Values().List(f.ctx, appvalue.ListInput{Page: db.PageArgs{Cursor: &cursor}})

			Convey("Then each is rejected as validation", func() {
				So(domainerrors.IsValidation(typeErr), ShouldBeTrue)
				So(domainerrors.IsValidation(attrErr), ShouldBeTrue)
				So(domainerrors.IsValidation(pageErr), ShouldBeTrue)
			})
		})

		Convey("When a removed value is listed", func() {
			id := f.set(f.sku, "p3", "ABC-3")
			_, err := f.it.Values().Remove(f.ctx, id)
			So(err, ShouldBeNil)

			live, err := f.it.Values().List(f.ctx, appvalue.ListInput{EntityID: "p3"})
			So(err, ShouldBeNil)
			all, err := f.it.Values().List(f.ctx,
				appvalue.ListInput{EntityID: "p3", IncludeArchived: true})
			So(err, ShouldBeNil)

			Convey("Then it is hidden by default and visible when asked for", func() {
				So(live.Items, ShouldBeEmpty)
				So(all.Items, ShouldHaveLength, 1)
			})
		})

		Convey("When a page smaller than the result set is requested", func() {
			limit := 2
			first, err := f.it.Values().List(f.ctx, appvalue.ListInput{
				Page: db.PageArgs{Limit: &limit, WantTotal: true},
			})
			So(err, ShouldBeNil)

			Convey("Then it carries a cursor and a total, and the cursor resumes", func() {
				So(first.Items, ShouldHaveLength, 2)
				So(first.PageInfo.HasNextPage, ShouldBeTrue)
				So(*first.PageInfo.TotalCount, ShouldEqual, 3)

				second, err := f.it.Values().List(f.ctx, appvalue.ListInput{
					Page: db.PageArgs{Limit: &limit, Cursor: first.PageInfo.NextCursor},
				})
				So(err, ShouldBeNil)
				So(second.Items, ShouldHaveLength, 1)
				So(second.PageInfo.HasPreviousPage, ShouldBeTrue)
			})
		})

		Convey("When another tenant lists values", func() {
			other := uow.WithTenant(context.Background(), valueobjects.TenantID("other"))
			out, err := f.it.Values().List(other, appvalue.ListInput{})

			Convey("Then nothing crosses the tenant boundary", func() {
				So(err, ShouldBeNil)
				So(out.Items, ShouldBeEmpty)
			})
		})
	})
}

func TestValueScopeRules(t *testing.T) {
	Convey("Given a scopable attribute and a plain one", t, func() {
		f := newValFixture()

		scoped := func(attrID, entityID, locale, channel string, v any) error {
			raw, err := json.Marshal(v)
			So(err, ShouldBeNil)
			_, err = f.it.Values().Set(f.ctx, appvalue.SetInput{
				AttributeDefinitionID: attrID, EntityID: entityID,
				TypeDefinitionID: f.typeID, Locale: locale, Channel: channel, Value: raw,
			})
			return err
		}

		Convey("When a scope is supplied for a non-scopable attribute", func() {
			localeErr := scoped(f.sku, "p1", "en_AU", "", "ABC")
			channelErr := scoped(f.sku, "p1", "", "web", "ABC")

			Convey("Then both are refused as validation", func() {
				So(domainerrors.IsValidation(localeErr), ShouldBeTrue)
				So(domainerrors.IsValidation(channelErr), ShouldBeTrue)
			})
		})

		Convey("When one attribute holds a different value per scope", func() {
			So(scoped(f.note, "p1", "", "", "base"), ShouldBeNil)
			So(scoped(f.note, "p1", "en_AU", "", "aussie"), ShouldBeNil)
			So(scoped(f.note, "p1", "en_AU", "web", "aussie web"), ShouldBeNil)

			out, err := f.it.Values().ListByEntity(f.ctx, f.typeID, "p1")

			Convey("Then each (locale, channel) is a distinct value", func() {
				So(err, ShouldBeNil)
				So(out, ShouldHaveLength, 3)
				byScope := map[string]string{}
				for _, s := range out {
					byScope[s.Locale+"|"+s.Channel] = s.Value.Text()
				}
				So(byScope["|"], ShouldEqual, "base")
				So(byScope["en_AU|"], ShouldEqual, "aussie")
				So(byScope["en_AU|web"], ShouldEqual, "aussie web")
			})
		})
	})
}

func TestValueBatchMutations(t *testing.T) {
	Convey("Given an entity with a live value", t, func() {
		f := newValFixture()
		f.set(f.sku, "p1", "ABC-1")

		Convey("When a batch sets one value and removes another", func() {
			raw, err := json.Marshal("20.00")
			So(err, ShouldBeNil)
			err = f.it.Values().ApplyMutations(f.ctx, []appvalue.Mutation{
				{Kind: appvalue.MutationSet, AttributeDefinitionID: f.price,
					EntityID: "p1", TypeDefinitionID: f.typeID, Value: raw},
				{Kind: appvalue.MutationRemove, AttributeDefinitionID: f.sku, EntityID: "p1"},
			})

			Convey("Then both land together", func() {
				So(err, ShouldBeNil)
				out, err := f.it.Values().ListByEntity(f.ctx, f.typeID, "p1")
				So(err, ShouldBeNil)
				So(out, ShouldHaveLength, 1)
				So(out[0].AttributeDefinitionID.String(), ShouldEqual, f.price)
				So(out[0].Value.Text(), ShouldEqual, "20.00")
			})
		})

		Convey("When an empty batch is applied", func() {
			err := f.it.Values().ApplyMutations(f.ctx, nil)

			Convey("Then it is a no-op", func() {
				So(err, ShouldBeNil)
			})
		})

		Convey("When a batch carries an unknown mutation kind", func() {
			err := f.it.Values().ApplyMutations(f.ctx, []appvalue.Mutation{
				{Kind: "rename", AttributeDefinitionID: f.sku, EntityID: "p1"},
			})

			Convey("Then the batch is rejected as validation", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "unknown mutation kind")
			})
		})

		Convey("When a later mutation in the batch fails", func() {
			raw, err := json.Marshal("20.00")
			So(err, ShouldBeNil)
			err = f.it.Values().ApplyMutations(f.ctx, []appvalue.Mutation{
				{Kind: appvalue.MutationSet, AttributeDefinitionID: f.price,
					EntityID: "p1", TypeDefinitionID: f.typeID, Value: raw},
				{Kind: appvalue.MutationRemove, AttributeDefinitionID: "nope", EntityID: "p1"},
			})

			Convey("Then the whole batch rolls back — the earlier set did not land", func() {
				So(err, ShouldNotBeNil)
				out, err := f.it.Values().ListByEntity(f.ctx, f.typeID, "p1")
				So(err, ShouldBeNil)
				So(out, ShouldHaveLength, 1)
				So(out[0].AttributeDefinitionID.String(), ShouldEqual, f.sku)
			})
		})

		Convey("When a remove mutation names an unknown attribute", func() {
			err := f.it.Values().ApplyMutations(f.ctx, []appvalue.Mutation{
				{Kind: appvalue.MutationRemove, EntityID: "p1",
					AttributeDefinitionID: valueobjects.NewAttributeDefinitionID().String()},
			})

			Convey("Then the batch reports a not-found", func() {
				So(domainerrors.IsNotFound(err), ShouldBeTrue)
			})
		})

		Convey("When a remove mutation carries a malformed entity", func() {
			err := f.it.Values().ApplyMutations(f.ctx, []appvalue.Mutation{
				{Kind: appvalue.MutationRemove, AttributeDefinitionID: f.sku, EntityID: ""},
			})

			Convey("Then it is rejected as validation", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
			})
		})

		Convey("When a principal without write access removes a value", func() {
			restricted := uow.WithAccess(f.ctx, uow.Access{
				Attr: map[string]uow.Perm{"sku": uow.PermRead},
			})
			err := f.it.Values().ApplyMutations(restricted, []appvalue.Mutation{
				{Kind: appvalue.MutationRemove, AttributeDefinitionID: f.sku, EntityID: "p1"},
			})

			Convey("Then it is forbidden and the value survives", func() {
				So(domainerrors.CodeOf(err), ShouldEqual, domainerrors.CodeForbidden)
				out, err := f.it.Values().ListByEntity(f.ctx, f.typeID, "p1")
				So(err, ShouldBeNil)
				So(out, ShouldHaveLength, 1)
			})
		})

		Convey("When a remove mutation targets a scope that holds no value", func() {
			err := f.it.Values().ApplyMutations(f.ctx, []appvalue.Mutation{
				{Kind: appvalue.MutationRemove, AttributeDefinitionID: f.sku,
					EntityID: "p1", Locale: "de_DE"},
			})

			Convey("Then it is a no-op and the base value survives", func() {
				So(err, ShouldBeNil)
				out, err := f.it.Values().ListByEntity(f.ctx, f.typeID, "p1")
				So(err, ShouldBeNil)
				So(out, ShouldHaveLength, 1)
			})
		})
	})
}

func TestValuePreview(t *testing.T) {
	Convey("Given an entity with two live values", t, func() {
		f := newValFixture()
		f.set(f.sku, "p1", "ABC-1")
		f.set(f.price, "p1", "10.00")

		Convey("When a set and a remove are previewed", func() {
			raw, err := json.Marshal("99.00")
			So(err, ShouldBeNil)
			out, err := f.it.Values().Preview(f.ctx, f.typeID, "p1", []appvalue.Mutation{
				{Kind: appvalue.MutationSet, AttributeDefinitionID: f.price,
					EntityID: "p1", Value: raw},
				{Kind: appvalue.MutationRemove, AttributeDefinitionID: f.sku, EntityID: "p1"},
			})

			Convey("Then the overlay is returned and the live data is untouched", func() {
				So(err, ShouldBeNil)
				So(out, ShouldHaveLength, 1)
				So(out[0].AttributeDefinitionID.String(), ShouldEqual, f.price)
				So(out[0].Value.Text(), ShouldEqual, "99.00")

				live, err := f.it.Values().ListByEntity(f.ctx, f.typeID, "p1")
				So(err, ShouldBeNil)
				So(live, ShouldHaveLength, 2)
			})
		})

		Convey("When a preview adds a value the entity does not yet hold", func() {
			raw, err := json.Marshal("new note")
			So(err, ShouldBeNil)
			out, err := f.it.Values().Preview(f.ctx, f.typeID, "p1", []appvalue.Mutation{
				{Kind: appvalue.MutationSet, AttributeDefinitionID: f.note,
					EntityID: "p1", Locale: "en_AU", Value: raw},
			})

			Convey("Then it is appended with its scope", func() {
				So(err, ShouldBeNil)
				So(out, ShouldHaveLength, 3)
				So(out[2].AttributeDefinitionID.String(), ShouldEqual, f.note)
				So(out[2].Locale, ShouldEqual, "en_AU")
			})
		})

		Convey("When a mutation belongs to a different entity", func() {
			raw, err := json.Marshal("99.00")
			So(err, ShouldBeNil)
			out, err := f.it.Values().Preview(f.ctx, f.typeID, "p1", []appvalue.Mutation{
				{Kind: appvalue.MutationSet, AttributeDefinitionID: f.price,
					EntityID: "p2", Value: raw},
			})

			Convey("Then it is ignored", func() {
				So(err, ShouldBeNil)
				So(out, ShouldHaveLength, 2)
				for _, s := range out {
					if s.AttributeDefinitionID.String() == f.price {
						So(s.Value.Text(), ShouldEqual, "10.00")
					}
				}
			})
		})

		Convey("When a previewed mutation is malformed", func() {
			raw, err := json.Marshal("99.00")
			So(err, ShouldBeNil)
			_, attrErr := f.it.Values().Preview(f.ctx, f.typeID, "p1", []appvalue.Mutation{
				{Kind: appvalue.MutationSet, AttributeDefinitionID: "nope",
					EntityID: "p1", Value: raw},
			})
			_, missingErr := f.it.Values().Preview(f.ctx, f.typeID, "p1", []appvalue.Mutation{
				{Kind: appvalue.MutationSet, EntityID: "p1", Value: raw,
					AttributeDefinitionID: valueobjects.NewAttributeDefinitionID().String()},
			})
			badValue, err := json.Marshal("not-a-decimal")
			So(err, ShouldBeNil)
			_, valueErr := f.it.Values().Preview(f.ctx, f.typeID, "p1", []appvalue.Mutation{
				{Kind: appvalue.MutationSet, AttributeDefinitionID: f.price,
					EntityID: "p1", Value: badValue},
			})

			Convey("Then each failure surfaces with its own error code", func() {
				So(domainerrors.IsValidation(attrErr), ShouldBeTrue)
				So(domainerrors.IsNotFound(missingErr), ShouldBeTrue)
				So(domainerrors.IsValidation(valueErr), ShouldBeTrue)
			})
		})

		Convey("When the preview target entity is malformed", func() {
			_, err := f.it.Values().Preview(f.ctx, f.typeID, "", nil)

			Convey("Then it is rejected as validation", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
			})
		})
	})
}

func TestValueQuantityWithoutUnitFamily(t *testing.T) {
	Convey("Given a quantity attribute declared without a unit family", t, func() {
		f := newValFixture()
		snap, err := f.it.Attributes().Create(f.ctx, appattribute.CreateInput{
			TypeDefinitionID: f.typeID, InternalName: "weight",
			DisplayName: "Weight", DataType: "quantity",
		})
		So(err, ShouldBeNil)

		Convey("When a quantity value is written", func() {
			_, err := f.it.Values().Set(f.ctx, appvalue.SetInput{
				AttributeDefinitionID: snap.ID.String(), EntityID: "p1",
				TypeDefinitionID: f.typeID,
				Value:            json.RawMessage(`{"magnitude":"5","unit":"kg"}`),
			})

			Convey("Then it is refused — there is no family to fold the unit into", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "unit family")
			})
		})
	})
}
