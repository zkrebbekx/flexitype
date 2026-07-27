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
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

// TestEntityAnchorInvariant is the in-memory twin of the Postgres anchor suite,
// so both backends agree on when an entity's type anchor may move.
//
// The anchor keys reads, dependency source loading, completeness scoring,
// revision capture, facets, the grid and PurgeEntity, so a value written under
// a second anchor is invisible to all of them. A backend that resolved the
// anchor differently would make those surfaces disagree.
func TestEntityAnchorInvariant(t *testing.T) {
	Convey("Given a Child type inheriting name from Parent, plus its own sku", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory()
		ia := svc.Interactors(ctx)

		parent, err := ia.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "parent", DisplayName: "Parent",
		})
		So(err, ShouldBeNil)
		child, err := ia.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "child", DisplayName: "Child", ExtendsID: parent.ID.String(),
		})
		So(err, ShouldBeNil)
		other, err := ia.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "other", DisplayName: "Other",
		})
		So(err, ShouldBeNil)

		name, err := ia.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: parent.ID.String(), InternalName: "name", DisplayName: "Name",
			DataType: "string",
		})
		So(err, ShouldBeNil)
		sku, err := ia.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: child.ID.String(), InternalName: "sku", DisplayName: "SKU",
			DataType: "string",
		})
		So(err, ShouldBeNil)

		set := func(attrID, typeID, v string) error {
			raw, _ := json.Marshal(v)
			_, err := svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: attrID, EntityID: "e1",
				TypeDefinitionID: typeID, Value: raw,
			})
			return err
		}

		Convey("When an inherited write is followed by a subtype write", func() {
			So(set(name.ID.String(), "", "Widget"), ShouldBeNil)
			So(set(sku.ID.String(), child.ID.String(), "ABC"), ShouldBeNil)

			Convey("Then the entity narrows to Child and keeps both values", func() {
				underChild, err := svc.Interactors(ctx).Values().ListByEntity(ctx, child.ID.String(), "e1")
				So(err, ShouldBeNil)
				So(underChild, ShouldHaveLength, 2)

				underParent, err := svc.Interactors(ctx).Values().ListByEntity(ctx, parent.ID.String(), "e1")
				So(err, ShouldBeNil)
				So(underParent, ShouldBeEmpty)
			})
		})

		Convey("When a later write supplies no type", func() {
			So(set(sku.ID.String(), child.ID.String(), "ABC"), ShouldBeNil)
			So(set(name.ID.String(), "", "Widget"), ShouldBeNil)

			Convey("Then it adopts the entity's anchor rather than the declaring type", func() {
				vals, err := svc.Interactors(ctx).Values().ListByEntity(ctx, child.ID.String(), "e1")
				So(err, ShouldBeNil)
				So(vals, ShouldHaveLength, 2)
			})
		})

		Convey("When a write names a type unrelated to the anchor", func() {
			So(set(sku.ID.String(), child.ID.String(), "ABC"), ShouldBeNil)

			Convey("Then it is rejected instead of splitting the entity", func() {
				raw, _ := json.Marshal("Widget")
				_, err := svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{
					AttributeDefinitionID: name.ID.String(), EntityID: "e1",
					TypeDefinitionID: other.ID.String(), Value: raw,
				})
				So(err, ShouldNotBeNil)
				So(domainerrors.CodeOf(err), ShouldEqual, domainerrors.CodeValidation)
			})
		})

		Convey("When every value is removed and one is written again", func() {
			So(set(sku.ID.String(), child.ID.String(), "ABC"), ShouldBeNil)
			_, err := svc.Interactors(ctx).Values().RemoveEntity(ctx, child.ID.String(), "e1")
			So(err, ShouldBeNil)
			So(set(name.ID.String(), "", "Widget"), ShouldBeNil)

			Convey("Then the archived rows keep the anchor, so the entity does not move", func() {
				vals, err := svc.Interactors(ctx).Values().ListByEntity(ctx, child.ID.String(), "e1")
				So(err, ShouldBeNil)
				So(vals, ShouldHaveLength, 1)
			})
		})
	})
}
