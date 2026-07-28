package flexitype_test

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

// TestEntityAnchorInvariantPostgres covers the split that a per-write,
// caller-supplied type anchor produced.
//
// The anchor defaulted to the attribute's DECLARING type, so writing an
// inherited attribute (declared on the parent) and an own attribute (declared
// on the subtype) put one entity's values under two anchors. Reads, dependency
// source loading, completeness scoring, revision capture, facets, the grid and
// PurgeEntity are all keyed on the anchor, so each saw only part of the entity
// — and PurgeEntity, the right-to-erasure primitive, reported success while
// permanently missing the rows under the other anchor.
func TestEntityAnchorInvariantPostgres(t *testing.T) {
	pool := openTestDB(t)
	svc := flexitype.New(pool)
	if err := svc.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	Convey("Given a Child type inheriting name from Parent, plus its own sku", t, func() {
		truncateAll(t, pool)
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		ia := svc.Interactors(ctx)

		parent, err := ia.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "parent", DisplayName: "Parent",
		})
		So(err, ShouldBeNil)
		child, err := ia.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "child", DisplayName: "Child", ExtendsID: parent.ID.String(),
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

		Convey("When the inherited attribute is written without a type, then the own one with Child", func() {
			// This is the exact sequence from the report: the first write used
			// to anchor to Parent (the declaring type) and the second to
			// Child, splitting the entity.
			So(set(name.ID.String(), "", "Widget"), ShouldBeNil)
			So(set(sku.ID.String(), child.ID.String(), "ABC"), ShouldBeNil)

			Convey("Then both values live under one anchor, narrowed to Child", func() {
				underChild, err := svc.Interactors(ctx).Values().ListByEntity(ctx, child.ID.String(), "e1")
				So(err, ShouldBeNil)
				So(underChild, ShouldHaveLength, 2)

				underParent, err := svc.Interactors(ctx).Values().ListByEntity(ctx, parent.ID.String(), "e1")
				So(err, ShouldBeNil)
				So(underParent, ShouldBeEmpty)
			})

			Convey("Then purging the entity removes everything it holds", func() {
				report, err := svc.Interactors(ctx).Erasure().PurgeEntity(ctx, child.ID.String(), "e1")
				So(err, ShouldBeNil)
				So(report.ValuesPurged, ShouldEqual, 2)

				left, err := svc.Interactors(ctx).Values().List(ctx, appvalue.ListInput{
					EntityID: "e1", IncludeArchived: true,
				})
				So(err, ShouldBeNil)
				So(left.Items, ShouldBeEmpty)
			})
		})

		Convey("When the first write names Child explicitly", func() {
			So(set(sku.ID.String(), child.ID.String(), "ABC"), ShouldBeNil)

			Convey("Then a later inherited write joins the same anchor without being told", func() {
				So(set(name.ID.String(), "", "Widget"), ShouldBeNil)
				vals, err := svc.Interactors(ctx).Values().ListByEntity(ctx, child.ID.String(), "e1")
				So(err, ShouldBeNil)
				So(vals, ShouldHaveLength, 2)
			})

			Convey("Then a write naming the parent is satisfied where the entity already is", func() {
				// The supplied type is an ANCESTOR of the current anchor, so
				// the entity is already anchored to something more specific
				// and the write belongs to that type's inherited schema. It
				// is accepted WITHOUT widening: nothing moves back to the
				// parent.
				//
				// Refusing it broke restoring a pre-narrowing revision
				// outright — Restore replays with the captured parent type —
				// and the error called the parent "an unrelated type", which
				// it is not.
				So(set(name.ID.String(), parent.ID.String(), "Widget"), ShouldBeNil)

				underChild, err := svc.Interactors(ctx).Values().ListByEntity(ctx, child.ID.String(), "e1")
				So(err, ShouldBeNil)
				So(underChild, ShouldHaveLength, 2)

				underParent, err := svc.Interactors(ctx).Values().ListByEntity(ctx, parent.ID.String(), "e1")
				So(err, ShouldBeNil)
				So(underParent, ShouldBeEmpty)
			})

			Convey("Then a write naming an unrelated type is still refused", func() {
				other, oerr := svc.Interactors(ctx).TypeDefinitions().Create(ctx,
					apptypedef.CreateInput{InternalName: "unrelated", DisplayName: "Unrelated"})
				So(oerr, ShouldBeNil)
				otherAttr, aerr := svc.Interactors(ctx).Attributes().Create(ctx, appattribute.CreateInput{
					TypeDefinitionID: other.ID.String(), InternalName: "other_name",
					DisplayName: "Other", DataType: "string",
				})
				So(aerr, ShouldBeNil)

				err := set(otherAttr.ID.String(), other.ID.String(), "Nope")
				So(err, ShouldNotBeNil)
				So(domainerrors.CodeOf(err), ShouldEqual, domainerrors.CodeValidation)
				So(err.Error(), ShouldContainSubstring, "unrelated type")
			})
		})

		Convey("When an entity's values are all removed and one is written again", func() {
			So(set(sku.ID.String(), child.ID.String(), "ABC"), ShouldBeNil)
			_, err := svc.Interactors(ctx).Values().RemoveEntity(ctx, child.ID.String(), "e1")
			So(err, ShouldBeNil)

			So(set(name.ID.String(), "", "Widget"), ShouldBeNil)

			Convey("Then the anchor survives, so the entity does not move types", func() {
				// Archived rows count for the anchor lookup precisely so that
				// re-adding a value cannot silently re-home the entity.
				vals, err := svc.Interactors(ctx).Values().ListByEntity(ctx, child.ID.String(), "e1")
				So(err, ShouldBeNil)
				So(vals, ShouldHaveLength, 1)
				So(vals[0].AttributeDefinitionID.String(), ShouldEqual, name.ID.String())
			})
		})

		Convey("When two entities of different types are written", func() {
			raw, _ := json.Marshal("Other")
			_, err := svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: name.ID.String(), EntityID: "e2",
				TypeDefinitionID: parent.ID.String(), Value: raw,
			})
			So(err, ShouldBeNil)
			So(set(sku.ID.String(), child.ID.String(), "ABC"), ShouldBeNil)

			Convey("Then each keeps its own anchor", func() {
				e1, err := svc.Interactors(ctx).Values().ListByEntity(ctx, child.ID.String(), "e1")
				So(err, ShouldBeNil)
				So(e1, ShouldHaveLength, 1)

				e2, err := svc.Interactors(ctx).Values().ListByEntity(ctx, parent.ID.String(), "e2")
				So(err, ShouldBeNil)
				So(e2, ShouldHaveLength, 1)
			})
		})
	})
}

// TestRevisionsSurviveNarrowingPostgres covers the erasure hole the anchor
// invariant left behind.
//
// Narrowing moves an entity's VALUE rows onto the subtype. Revision rows were
// keyed on (tenant, type, entity, seq), so they stayed under the old anchor:
// invisible to a read under the new one, and missed by PurgeEntity — which
// purges under the new anchor and reported `revisions_purged: 0` and success
// while a complete snapshot of the entity's values, personal data included,
// stayed readable through Revisions().Get.
func TestRevisionsSurviveNarrowingPostgres(t *testing.T) {
	pool := openTestDB(t)
	svc := flexitype.New(pool)
	if err := svc.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	Convey("Given an entity anchored to a parent type with a captured revision", t, func() {
		truncateAll(t, pool)
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		ia := svc.Interactors(ctx)

		parent, err := ia.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "person", DisplayName: "Person",
		})
		So(err, ShouldBeNil)
		child, err := ia.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "employee", DisplayName: "Employee", ExtendsID: parent.ID.String(),
		})
		So(err, ShouldBeNil)
		personName, err := ia.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: parent.ID.String(), InternalName: "person_name",
			DisplayName: "Name", DataType: "string",
		})
		So(err, ShouldBeNil)
		badge, err := ia.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: child.ID.String(), InternalName: "badge",
			DisplayName: "Badge", DataType: "string",
		})
		So(err, ShouldBeNil)

		// Anchor to the parent by writing the inherited attribute with no type.
		raw, _ := json.Marshal("Alice Smith")
		_, err = ia.Values().Set(ctx, appvalue.SetInput{
			AttributeDefinitionID: personName.ID.String(), EntityID: "e1", Value: raw,
		})
		So(err, ShouldBeNil)
		_, err = svc.Interactors(ctx).Revisions().Create(ctx, parent.ID.String(), "e1", "before narrowing")
		So(err, ShouldBeNil)

		// Narrow: name the subtype on the next write.
		raw, _ = json.Marshal("A-1")
		_, err = svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{
			AttributeDefinitionID: badge.ID.String(), EntityID: "e1",
			TypeDefinitionID: child.ID.String(), Value: raw,
		})
		So(err, ShouldBeNil)

		Convey("When the entity's revisions are listed under the new anchor", func() {
			revs, err := svc.Interactors(ctx).Revisions().List(ctx, child.ID.String(), "e1")

			Convey("Then the pre-narrowing revision is still part of the history", func() {
				So(err, ShouldBeNil)
				So(len(revs), ShouldBeGreaterThanOrEqualTo, 1)
			})
		})

		Convey("When the entity is purged under the new anchor", func() {
			report, err := svc.Interactors(ctx).Erasure().PurgeEntity(ctx, child.ID.String(), "e1")

			Convey("Then the pre-narrowing revision is counted and removed", func() {
				So(err, ShouldBeNil)
				So(report.RevisionsPurged, ShouldBeGreaterThan, 0)

				var left int
				So(pool.Get(&left,
					`SELECT count(*) FROM flexitype_entity_revision WHERE entity_id = 'e1'`), ShouldBeNil)
				So(left, ShouldEqual, 0)
			})
		})
	})
}
