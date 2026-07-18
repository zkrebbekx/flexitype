package memory_test

import (
	"context"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	"github.com/zkrebbekx/flexitype/application"
	apprelationship "github.com/zkrebbekx/flexitype/application/relationship"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
)

// relFixture is a product ⟶ part "contains" relationship.
type relFixture struct {
	ctx     context.Context
	it      *application.Interactors
	product string
	part    string
	defID   string
}

func newRelFixture() *relFixture {
	ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
	it := flexitype.NewInMemory().Interactors(ctx)

	product, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
		InternalName: "product", DisplayName: "Product",
	})
	So(err, ShouldBeNil)
	part, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
		InternalName: "part", DisplayName: "Part",
	})
	So(err, ShouldBeNil)

	def, err := it.Relationships().CreateDefinition(ctx, apprelationship.CreateDefinitionInput{
		InternalName: "contains", DisplayName: "Contains",
		ParentTypeID: product.ID.String(), ChildTypeID: part.ID.String(),
		ParentLabel: "assembly", ChildLabel: "component",
	})
	So(err, ShouldBeNil)

	return &relFixture{
		ctx: ctx, it: it,
		product: product.ID.String(), part: part.ID.String(), defID: def.ID.String(),
	}
}

func TestRelationshipDefinitionCreateValidation(t *testing.T) {
	Convey("Given a product and a part type", t, func() {
		f := newRelFixture()

		Convey("When an endpoint type ID is malformed", func() {
			_, parentErr := f.it.Relationships().CreateDefinition(f.ctx, apprelationship.CreateDefinitionInput{
				InternalName: "bad_parent", DisplayName: "Bad",
				ParentTypeID: "nope", ChildTypeID: f.part,
			})
			_, childErr := f.it.Relationships().CreateDefinition(f.ctx, apprelationship.CreateDefinitionInput{
				InternalName: "bad_child", DisplayName: "Bad",
				ParentTypeID: f.product, ChildTypeID: "nope",
			})

			Convey("Then each is rejected naming the offending side", func() {
				So(domainerrors.IsValidation(parentErr), ShouldBeTrue)
				So(parentErr.Error(), ShouldContainSubstring, "parent type")
				So(domainerrors.IsValidation(childErr), ShouldBeTrue)
				So(childErr.Error(), ShouldContainSubstring, "child type")
			})
		})

		Convey("When an endpoint type does not exist", func() {
			_, err := f.it.Relationships().CreateDefinition(f.ctx, apprelationship.CreateDefinitionInput{
				InternalName: "missing_type", DisplayName: "Missing",
				ParentTypeID: valueobjects.NewTypeDefinitionID().String(), ChildTypeID: f.part,
			})

			Convey("Then it is a not-found", func() {
				So(domainerrors.IsNotFound(err), ShouldBeTrue)
			})
		})

		Convey("When the internal name is already taken", func() {
			_, err := f.it.Relationships().CreateDefinition(f.ctx, apprelationship.CreateDefinitionInput{
				InternalName: "contains", DisplayName: "Contains Again",
				ParentTypeID: f.product, ChildTypeID: f.part,
			})

			Convey("Then it conflicts", func() {
				So(domainerrors.IsConflict(err), ShouldBeTrue)
				So(domainerrors.DetailsOf(err)["internal_name"], ShouldEqual, "contains")
			})
		})

		Convey("When the internal name or display name is malformed", func() {
			_, nameErr := f.it.Relationships().CreateDefinition(f.ctx, apprelationship.CreateDefinitionInput{
				InternalName: "Not Snake Case", DisplayName: "X",
				ParentTypeID: f.product, ChildTypeID: f.part,
			})
			_, displayErr := f.it.Relationships().CreateDefinition(f.ctx, apprelationship.CreateDefinitionInput{
				InternalName: "no_display", DisplayName: "",
				ParentTypeID: f.product, ChildTypeID: f.part,
			})

			Convey("Then both are rejected as validation", func() {
				So(domainerrors.IsValidation(nameErr), ShouldBeTrue)
				So(domainerrors.IsValidation(displayErr), ShouldBeTrue)
			})
		})

		Convey("When the kind or a version policy is unknown", func() {
			_, kindErr := f.it.Relationships().CreateDefinition(f.ctx, apprelationship.CreateDefinitionInput{
				InternalName: "weird_kind", DisplayName: "Weird",
				ParentTypeID: f.product, ChildTypeID: f.part, Kind: "sideways",
			})
			_, policyErr := f.it.Relationships().CreateDefinition(f.ctx, apprelationship.CreateDefinitionInput{
				InternalName: "weird_policy", DisplayName: "Weird",
				ParentTypeID: f.product, ChildTypeID: f.part, ParentVersionPolicy: "whenever",
			})

			Convey("Then both are rejected as validation", func() {
				So(domainerrors.IsValidation(kindErr), ShouldBeTrue)
				So(domainerrors.IsValidation(policyErr), ShouldBeTrue)
			})
		})

		Convey("When a symmetric definition tries to pin versions or label sides", func() {
			_, pinErr := f.it.Relationships().CreateDefinition(f.ctx, apprelationship.CreateDefinitionInput{
				InternalName: "sym_pinned", DisplayName: "Sym",
				ParentTypeID: f.product, ChildTypeID: f.product,
				Kind: "symmetric", ParentVersionPolicy: "pinned",
			})
			_, labelErr := f.it.Relationships().CreateDefinition(f.ctx, apprelationship.CreateDefinitionInput{
				InternalName: "sym_labelled", DisplayName: "Sym",
				ParentTypeID: f.product, ChildTypeID: f.product,
				Kind: "symmetric", ParentLabel: "left",
			})

			Convey("Then both are refused — symmetric pairs have no direction", func() {
				So(domainerrors.IsValidation(pinErr), ShouldBeTrue)
				So(domainerrors.IsValidation(labelErr), ShouldBeTrue)
			})
		})

		Convey("When an archived type is used as an endpoint", func() {
			doomed, err := f.it.TypeDefinitions().Create(f.ctx, apptypedef.CreateInput{
				InternalName: "doomed", DisplayName: "Doomed",
			})
			So(err, ShouldBeNil)
			_, err = f.it.TypeDefinitions().Archive(f.ctx, doomed.ID.String())
			So(err, ShouldBeNil)

			_, err = f.it.Relationships().CreateDefinition(f.ctx, apprelationship.CreateDefinitionInput{
				InternalName: "to_doomed", DisplayName: "To Doomed",
				ParentTypeID: f.product, ChildTypeID: doomed.ID.String(),
			})

			Convey("Then the definition is refused", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
			})
		})

		Convey("When extends names a malformed or unknown definition", func() {
			_, badErr := f.it.Relationships().CreateDefinition(f.ctx, apprelationship.CreateDefinitionInput{
				InternalName: "bad_extends", DisplayName: "Bad",
				ParentTypeID: f.product, ChildTypeID: f.part, ExtendsID: "nope",
			})
			_, missingErr := f.it.Relationships().CreateDefinition(f.ctx, apprelationship.CreateDefinitionInput{
				InternalName: "missing_extends", DisplayName: "Missing",
				ParentTypeID: f.product, ChildTypeID: f.part,
				ExtendsID: valueobjects.NewRelationshipDefinitionID().String(),
			})

			Convey("Then the malformed one is validation and the unknown one is not-found", func() {
				So(domainerrors.IsValidation(badErr), ShouldBeTrue)
				So(domainerrors.IsNotFound(missingErr), ShouldBeTrue)
			})
		})

		Convey("When a definition extends another", func() {
			child, err := f.it.Relationships().CreateDefinition(f.ctx, apprelationship.CreateDefinitionInput{
				InternalName: "contains_critical", DisplayName: "Contains Critical",
				ParentTypeID: f.product, ChildTypeID: f.part, ExtendsID: f.defID,
			})
			So(err, ShouldBeNil)

			sets, err := f.it.Relationships().AttributeSets(f.ctx, child.ID.String())

			Convey("Then its attribute-set chain runs from its own set to the base-most", func() {
				So(err, ShouldBeNil)
				So(sets, ShouldHaveLength, 2)
				So(sets[0], ShouldEqual, child.AttributeSetID.String())
			})
		})
	})
}

func TestRelationshipDefinitionLifecycle(t *testing.T) {
	Convey("Given a contains relationship definition", t, func() {
		f := newRelFixture()

		Convey("When it is updated", func() {
			snap, err := f.it.Relationships().UpdateDefinition(f.ctx, apprelationship.UpdateDefinitionInput{
				ID: f.defID, DisplayName: "Bill of Materials", Description: "assembly structure",
				ParentLabel: "assembly", ChildLabel: "component",
				MaxChildren: intp(5),
			})

			Convey("Then the mutable fields change and the version bumps", func() {
				So(err, ShouldBeNil)
				So(snap.DisplayName, ShouldEqual, "Bill of Materials")
				So(snap.Description, ShouldEqual, "assembly structure")
				So(snap.Version, ShouldEqual, 2)
				So(snap.MaxChildren, ShouldNotBeNil)
				So(*snap.MaxChildren, ShouldEqual, 5)

				got, err := f.it.Relationships().GetDefinition(f.ctx, f.defID)
				So(err, ShouldBeNil)
				So(got.DisplayName, ShouldEqual, "Bill of Materials")
			})
		})

		Convey("When an update changes nothing", func() {
			_, err := f.it.Relationships().UpdateDefinition(f.ctx, apprelationship.UpdateDefinitionInput{
				ID: f.defID, DisplayName: "Contains",
				ParentLabel: "assembly", ChildLabel: "component",
			})

			Convey("Then the version does not bump", func() {
				So(err, ShouldBeNil)
				got, err := f.it.Relationships().GetDefinition(f.ctx, f.defID)
				So(err, ShouldBeNil)
				So(got.Version, ShouldEqual, 1)
			})
		})

		Convey("When an update drops the display name", func() {
			_, err := f.it.Relationships().UpdateDefinition(f.ctx, apprelationship.UpdateDefinitionInput{
				ID: f.defID, DisplayName: "",
			})

			Convey("Then it is rejected as validation", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
			})
		})

		Convey("When an update names a malformed or unknown definition", func() {
			_, badErr := f.it.Relationships().UpdateDefinition(f.ctx,
				apprelationship.UpdateDefinitionInput{ID: "nope", DisplayName: "X"})
			_, missingErr := f.it.Relationships().UpdateDefinition(f.ctx,
				apprelationship.UpdateDefinitionInput{
					ID: valueobjects.NewRelationshipDefinitionID().String(), DisplayName: "X",
				})

			Convey("Then the malformed ID is validation and the unknown one is not-found", func() {
				So(domainerrors.IsValidation(badErr), ShouldBeTrue)
				So(domainerrors.IsNotFound(missingErr), ShouldBeTrue)
			})
		})

		Convey("When it is archived and restored", func() {
			archived, err := f.it.Relationships().ArchiveDefinition(f.ctx, f.defID)
			So(err, ShouldBeNil)
			So(archived.ArchivedAt, ShouldNotBeNil)

			Convey("Then archiving twice is refused", func() {
				_, err := f.it.Relationships().ArchiveDefinition(f.ctx, f.defID)
				So(domainerrors.IsArchived(err), ShouldBeTrue)
			})

			Convey("And updating an archived definition is refused", func() {
				_, err := f.it.Relationships().UpdateDefinition(f.ctx,
					apprelationship.UpdateDefinitionInput{ID: f.defID, DisplayName: "X"})
				So(domainerrors.IsArchived(err), ShouldBeTrue)
			})

			Convey("And it disappears from the default listing", func() {
				out, err := f.it.Relationships().ListDefinitions(f.ctx, apprelationship.DefinitionListInput{})
				So(err, ShouldBeNil)
				So(out.Items, ShouldBeEmpty)

				out, err = f.it.Relationships().ListDefinitions(f.ctx,
					apprelationship.DefinitionListInput{IncludeArchived: true})
				So(err, ShouldBeNil)
				So(out.Items, ShouldHaveLength, 1)
			})

			Convey("And restoring brings it back", func() {
				restored, err := f.it.Relationships().RestoreDefinition(f.ctx, f.defID)
				So(err, ShouldBeNil)
				So(restored.ArchivedAt, ShouldBeNil)

				out, err := f.it.Relationships().ListDefinitions(f.ctx, apprelationship.DefinitionListInput{})
				So(err, ShouldBeNil)
				So(out.Items, ShouldHaveLength, 1)
			})
		})

		Convey("When a live definition is restored", func() {
			_, err := f.it.Relationships().RestoreDefinition(f.ctx, f.defID)

			Convey("Then it is refused — there is nothing to restore", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
			})
		})

		Convey("When archive or restore names a malformed or unknown definition", func() {
			_, badErr := f.it.Relationships().ArchiveDefinition(f.ctx, "nope")
			_, missingErr := f.it.Relationships().RestoreDefinition(f.ctx,
				valueobjects.NewRelationshipDefinitionID().String())

			Convey("Then the malformed ID is validation and the unknown one is not-found", func() {
				So(domainerrors.IsValidation(badErr), ShouldBeTrue)
				So(domainerrors.IsNotFound(missingErr), ShouldBeTrue)
			})
		})
	})
}

func TestRelationshipDefinitionReads(t *testing.T) {
	Convey("Given a contains relationship definition", t, func() {
		f := newRelFixture()

		Convey("When it is fetched", func() {
			got, err := f.it.Relationships().GetDefinition(f.ctx, f.defID)

			Convey("Then both endpoints and labels come back", func() {
				So(err, ShouldBeNil)
				So(got.InternalName, ShouldEqual, "contains")
				So(got.ParentTypeID.String(), ShouldEqual, f.product)
				So(got.ChildTypeID.String(), ShouldEqual, f.part)
				So(got.ParentLabel, ShouldEqual, "assembly")
			})
		})

		Convey("When a malformed or unknown definition is fetched", func() {
			_, badErr := f.it.Relationships().GetDefinition(f.ctx, "nope")
			_, missingErr := f.it.Relationships().GetDefinition(f.ctx,
				valueobjects.NewRelationshipDefinitionID().String())

			Convey("Then the malformed ID is validation and the unknown one is not-found", func() {
				So(domainerrors.IsValidation(badErr), ShouldBeTrue)
				So(domainerrors.IsNotFound(missingErr), ShouldBeTrue)
			})
		})

		Convey("When another tenant reads it", func() {
			other := uow.WithTenant(context.Background(), valueobjects.TenantID("other"))
			_, getErr := f.it.Relationships().GetDefinition(other, f.defID)
			_, setsErr := f.it.Relationships().AttributeSets(other, f.defID)

			Convey("Then it is invisible across the tenant boundary", func() {
				So(domainerrors.IsNotFound(getErr), ShouldBeTrue)
				So(domainerrors.IsNotFound(setsErr), ShouldBeTrue)
			})
		})

		Convey("When definitions are listed for a participating type", func() {
			out, err := f.it.Relationships().ListDefinitions(f.ctx,
				apprelationship.DefinitionListInput{TypeDefinitionID: f.product})

			Convey("Then the definition touching that type is returned", func() {
				So(err, ShouldBeNil)
				So(out.Items, ShouldHaveLength, 1)
				So(out.Items[0].ID.String(), ShouldEqual, f.defID)
			})
		})

		Convey("When definitions are listed for a subtype of a participating type", func() {
			sub, err := f.it.TypeDefinitions().Create(f.ctx, apptypedef.CreateInput{
				InternalName: "kit", DisplayName: "Kit", ExtendsID: f.product,
			})
			So(err, ShouldBeNil)

			out, err := f.it.Relationships().ListDefinitions(f.ctx,
				apprelationship.DefinitionListInput{TypeDefinitionID: sub.ID.String()})

			Convey("Then endpoints are polymorphic — the ancestor's definition is included", func() {
				So(err, ShouldBeNil)
				So(out.Items, ShouldHaveLength, 1)
				So(out.Items[0].ID.String(), ShouldEqual, f.defID)
			})
		})

		Convey("When definitions are listed for an uninvolved type", func() {
			other, err := f.it.TypeDefinitions().Create(f.ctx, apptypedef.CreateInput{
				InternalName: "vendor", DisplayName: "Vendor",
			})
			So(err, ShouldBeNil)

			out, err := f.it.Relationships().ListDefinitions(f.ctx,
				apprelationship.DefinitionListInput{TypeDefinitionID: other.ID.String()})

			Convey("Then nothing matches", func() {
				So(err, ShouldBeNil)
				So(out.Items, ShouldBeEmpty)
			})
		})

		Convey("When the type filter is malformed or unknown", func() {
			_, badErr := f.it.Relationships().ListDefinitions(f.ctx,
				apprelationship.DefinitionListInput{TypeDefinitionID: "nope"})
			_, missingErr := f.it.Relationships().ListDefinitions(f.ctx,
				apprelationship.DefinitionListInput{TypeDefinitionID: valueobjects.NewTypeDefinitionID().String()})

			Convey("Then the malformed one is validation and the unknown one is not-found", func() {
				So(domainerrors.IsValidation(badErr), ShouldBeTrue)
				So(domainerrors.IsNotFound(missingErr), ShouldBeTrue)
			})
		})

		Convey("When the page arguments are invalid", func() {
			zero := 0
			_, err := f.it.Relationships().ListDefinitions(f.ctx,
				apprelationship.DefinitionListInput{Page: db.PageArgs{Limit: &zero}})

			Convey("Then they are rejected as validation", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
			})
		})

		Convey("When the attribute-set chain is requested for a malformed or unknown definition", func() {
			_, badErr := f.it.Relationships().AttributeSets(f.ctx, "nope")
			_, missingErr := f.it.Relationships().AttributeSets(f.ctx,
				valueobjects.NewRelationshipDefinitionID().String())

			Convey("Then the malformed ID is validation and the unknown one is not-found", func() {
				So(domainerrors.IsValidation(badErr), ShouldBeTrue)
				So(domainerrors.IsNotFound(missingErr), ShouldBeTrue)
			})
		})
	})
}

func TestRelationshipLinkLifecycle(t *testing.T) {
	Convey("Given a contains relationship definition", t, func() {
		f := newRelFixture()

		link := func(parent, child string) (string, error) {
			snap, err := f.it.Relationships().Link(f.ctx, apprelationship.LinkInput{
				DefinitionID: f.defID, ParentEntity: parent, ChildEntity: child,
			})
			if err != nil {
				return "", err
			}
			return snap.ID.String(), nil
		}

		Convey("When two entities are linked", func() {
			id, err := link("prod-1", "part-1")
			So(err, ShouldBeNil)

			Convey("Then the link is retrievable with both endpoints", func() {
				got, err := f.it.Relationships().Get(f.ctx, id)
				So(err, ShouldBeNil)
				So(string(got.ParentEntityID), ShouldEqual, "prod-1")
				So(string(got.ChildEntityID), ShouldEqual, "part-1")
				So(got.ArchivedAt, ShouldBeNil)
			})

			Convey("And relinking the same pair is refused while the link is live", func() {
				_, err := link("prod-1", "part-1")
				So(err, ShouldNotBeNil)
				So(domainerrors.IsConflict(err) || domainerrors.IsValidation(err), ShouldBeTrue)
			})

			Convey("And it appears on both endpoints with the right role", func() {
				parentSide, err := f.it.Relationships().ListByEntity(f.ctx, "prod-1")
				So(err, ShouldBeNil)
				So(parentSide, ShouldHaveLength, 1)
				So(parentSide[0].Role, ShouldEqual, "parent")
				So(parentSide[0].Definition.InternalName, ShouldEqual, "contains")

				childSide, err := f.it.Relationships().ListByEntity(f.ctx, "part-1")
				So(err, ShouldBeNil)
				So(childSide, ShouldHaveLength, 1)
				So(childSide[0].Role, ShouldEqual, "child")
			})

			Convey("And unlinking archives it and removes it from the entity view", func() {
				snap, err := f.it.Relationships().Unlink(f.ctx, id)
				So(err, ShouldBeNil)
				So(snap.ArchivedAt, ShouldNotBeNil)

				links, err := f.it.Relationships().ListByEntity(f.ctx, "prod-1")
				So(err, ShouldBeNil)
				So(links, ShouldBeEmpty)
			})

			Convey("And unlinking twice is refused", func() {
				_, err := f.it.Relationships().Unlink(f.ctx, id)
				So(err, ShouldBeNil)
				_, err = f.it.Relationships().Unlink(f.ctx, id)
				So(err, ShouldNotBeNil)
			})

			Convey("And the pair can be relinked once the first link is archived", func() {
				_, err := f.it.Relationships().Unlink(f.ctx, id)
				So(err, ShouldBeNil)
				_, err = link("prod-1", "part-1")
				So(err, ShouldBeNil)
			})
		})

		Convey("When linking with a malformed definition or entity", func() {
			_, defErr := f.it.Relationships().Link(f.ctx, apprelationship.LinkInput{
				DefinitionID: "nope", ParentEntity: "p", ChildEntity: "c",
			})
			_, parentErr := f.it.Relationships().Link(f.ctx, apprelationship.LinkInput{
				DefinitionID: f.defID, ParentEntity: "", ChildEntity: "c",
			})
			_, childErr := f.it.Relationships().Link(f.ctx, apprelationship.LinkInput{
				DefinitionID: f.defID, ParentEntity: "p", ChildEntity: "",
			})

			Convey("Then each is rejected as validation", func() {
				So(domainerrors.IsValidation(defErr), ShouldBeTrue)
				So(domainerrors.IsValidation(parentErr), ShouldBeTrue)
				So(domainerrors.IsValidation(childErr), ShouldBeTrue)
			})
		})

		Convey("When linking under an unknown definition", func() {
			_, err := f.it.Relationships().Link(f.ctx, apprelationship.LinkInput{
				DefinitionID: valueobjects.NewRelationshipDefinitionID().String(),
				ParentEntity: "p", ChildEntity: "c",
			})

			Convey("Then it is a not-found", func() {
				So(domainerrors.IsNotFound(err), ShouldBeTrue)
			})
		})

		Convey("When unlinking or fetching a malformed or unknown link", func() {
			_, badGet := f.it.Relationships().Get(f.ctx, "nope")
			_, missingGet := f.it.Relationships().Get(f.ctx, valueobjects.NewRelationshipID().String())
			_, badUnlink := f.it.Relationships().Unlink(f.ctx, "nope")
			_, missingUnlink := f.it.Relationships().Unlink(f.ctx, valueobjects.NewRelationshipID().String())

			Convey("Then malformed IDs are validation and unknown ones are not-found", func() {
				So(domainerrors.IsValidation(badGet), ShouldBeTrue)
				So(domainerrors.IsNotFound(missingGet), ShouldBeTrue)
				So(domainerrors.IsValidation(badUnlink), ShouldBeTrue)
				So(domainerrors.IsNotFound(missingUnlink), ShouldBeTrue)
			})
		})

		Convey("When another tenant reads or unlinks a link", func() {
			id, err := link("prod-1", "part-1")
			So(err, ShouldBeNil)
			other := uow.WithTenant(context.Background(), valueobjects.TenantID("other"))

			_, getErr := f.it.Relationships().Get(other, id)
			_, unlinkErr := f.it.Relationships().Unlink(other, id)

			Convey("Then both are refused across the tenant boundary", func() {
				So(domainerrors.IsNotFound(getErr), ShouldBeTrue)
				So(domainerrors.IsNotFound(unlinkErr), ShouldBeTrue)
			})
		})

		Convey("When links are listed for an entity with a malformed ID", func() {
			_, err := f.it.Relationships().ListByEntity(f.ctx, "")

			Convey("Then it is rejected as validation", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
			})
		})

		Convey("When links are listed for an entity with none", func() {
			out, err := f.it.Relationships().ListByEntity(f.ctx, "unlinked")

			Convey("Then the result is empty rather than an error", func() {
				So(err, ShouldBeNil)
				So(out, ShouldBeEmpty)
			})
		})
	})
}

func TestRelationshipList(t *testing.T) {
	Convey("Given several links under one definition", t, func() {
		f := newRelFixture()

		var ids []string
		for _, child := range []string{"part-1", "part-2", "part-3"} {
			snap, err := f.it.Relationships().Link(f.ctx, apprelationship.LinkInput{
				DefinitionID: f.defID, ParentEntity: "prod-1", ChildEntity: child,
			})
			So(err, ShouldBeNil)
			ids = append(ids, snap.ID.String())
		}

		Convey("When links are listed unfiltered", func() {
			out, err := f.it.Relationships().List(f.ctx, apprelationship.ListInput{})

			Convey("Then every live link comes back", func() {
				So(err, ShouldBeNil)
				So(out.Items, ShouldHaveLength, 3)
			})
		})

		Convey("When links are filtered by definition and endpoint", func() {
			byDef, err := f.it.Relationships().List(f.ctx,
				apprelationship.ListInput{DefinitionID: f.defID})
			So(err, ShouldBeNil)
			byParent, err := f.it.Relationships().List(f.ctx,
				apprelationship.ListInput{ParentEntityID: "prod-1"})
			So(err, ShouldBeNil)
			byChild, err := f.it.Relationships().List(f.ctx,
				apprelationship.ListInput{ChildEntityID: "part-2"})
			So(err, ShouldBeNil)
			byOtherChild, err := f.it.Relationships().List(f.ctx,
				apprelationship.ListInput{ChildEntityID: "part-99"})
			So(err, ShouldBeNil)

			Convey("Then each filter narrows the set as declared", func() {
				So(byDef.Items, ShouldHaveLength, 3)
				So(byParent.Items, ShouldHaveLength, 3)
				So(byChild.Items, ShouldHaveLength, 1)
				So(byOtherChild.Items, ShouldBeEmpty)
			})
		})

		Convey("When an archived link is listed", func() {
			_, err := f.it.Relationships().Unlink(f.ctx, ids[0])
			So(err, ShouldBeNil)

			live, err := f.it.Relationships().List(f.ctx, apprelationship.ListInput{})
			So(err, ShouldBeNil)
			all, err := f.it.Relationships().List(f.ctx, apprelationship.ListInput{IncludeArchived: true})
			So(err, ShouldBeNil)

			Convey("Then it is hidden by default and visible when asked for", func() {
				So(live.Items, ShouldHaveLength, 2)
				So(all.Items, ShouldHaveLength, 3)
			})
		})

		Convey("When a list filter is malformed", func() {
			_, defErr := f.it.Relationships().List(f.ctx, apprelationship.ListInput{DefinitionID: "nope"})
			zero := 0
			_, pageErr := f.it.Relationships().List(f.ctx,
				apprelationship.ListInput{Page: db.PageArgs{Limit: &zero}})

			Convey("Then it is rejected as validation", func() {
				So(domainerrors.IsValidation(defErr), ShouldBeTrue)
				So(domainerrors.IsValidation(pageErr), ShouldBeTrue)
			})
		})

		Convey("When a page smaller than the result set is requested", func() {
			limit := 2
			first, err := f.it.Relationships().List(f.ctx, apprelationship.ListInput{
				Page: db.PageArgs{Limit: &limit, WantTotal: true},
			})
			So(err, ShouldBeNil)

			Convey("Then it carries a cursor and a total, and the cursor resumes", func() {
				So(first.Items, ShouldHaveLength, 2)
				So(first.PageInfo.HasNextPage, ShouldBeTrue)
				So(first.PageInfo.NextCursor, ShouldNotBeNil)
				So(*first.PageInfo.TotalCount, ShouldEqual, 3)

				second, err := f.it.Relationships().List(f.ctx, apprelationship.ListInput{
					Page: db.PageArgs{Limit: &limit, Cursor: first.PageInfo.NextCursor},
				})
				So(err, ShouldBeNil)
				So(second.Items, ShouldHaveLength, 1)
				So(second.PageInfo.HasNextPage, ShouldBeFalse)
				So(second.PageInfo.HasPreviousPage, ShouldBeTrue)
			})
		})

		Convey("When another tenant lists links", func() {
			other := uow.WithTenant(context.Background(), valueobjects.TenantID("other"))
			out, err := f.it.Relationships().List(other, apprelationship.ListInput{})

			Convey("Then nothing crosses the tenant boundary", func() {
				So(err, ShouldBeNil)
				So(out.Items, ShouldBeEmpty)
			})
		})
	})
}
