package memory_test

import (
	"context"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	domainattribute "github.com/zkrebbekx/flexitype/domain/attribute"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	domainrelationship "github.com/zkrebbekx/flexitype/domain/relationship"
	domaintypedef "github.com/zkrebbekx/flexitype/domain/typedef"
	"github.com/zkrebbekx/flexitype/infrastructure/memory"
)

// TestMemoryEnforcesTheSameUniquenessAsPostgres is the parity regression.
//
// The interactor checks a name is free and then saves, so two callers past the
// same check both wrote — and the in-memory repositories were a bare map
// assignment. Postgres refuses that at a unique index; memory accepted it and
// left two live rows sharing a natural key, a state Postgres cannot represent
// and that then breaks lookup by name.
//
// Each case here writes the second row directly through the repository, which
// is where the divergence lived: through an interactor the pre-check wins and
// the store is never asked.
func TestMemoryEnforcesTheSameUniquenessAsPostgres(t *testing.T) {
	Convey("Given a store with a type, an attribute and a link", t, func() {
		ctx := context.Background()
		store := memory.NewStore()
		repos := store.Repositories()

		product, _, err := domaintypedef.New(domaintypedef.NewInput{
			TenantID: tenantA, InternalName: "product", DisplayName: "Product",
		}, fixedTime)
		So(err, ShouldBeNil)
		So(repos.TypeDefinitions.Save(ctx, product), ShouldBeNil)

		Convey("When a second type reuses the internal name", func() {
			twin, _, terr := domaintypedef.New(domaintypedef.NewInput{
				TenantID: tenantA, InternalName: "product", DisplayName: "Other",
			}, fixedTime)
			So(terr, ShouldBeNil)
			err := repos.TypeDefinitions.Save(ctx, twin)

			Convey("Then it is refused, as Postgres refuses it", func() {
				So(err, ShouldNotBeNil)
				So(domainerrors.IsConflict(err), ShouldBeTrue)
			})
		})

		Convey("When the SAME type is saved again", func() {
			// An update must not collide with itself.
			err := repos.TypeDefinitions.Save(ctx, product)

			Convey("Then it is allowed", func() {
				So(err, ShouldBeNil)
			})
		})

		Convey("When a second type reuses the name in ANOTHER tenant", func() {
			other, _, terr := domaintypedef.New(domaintypedef.NewInput{
				TenantID: tenantB, InternalName: "product", DisplayName: "Product",
			}, fixedTime)
			So(terr, ShouldBeNil)
			err := repos.TypeDefinitions.Save(ctx, other)

			Convey("Then it is allowed — the index is per tenant", func() {
				So(err, ShouldBeNil)
			})
		})

		Convey("When two attributes on one type share an internal name", func() {
			first, _, aerr := domainattribute.New(domainattribute.NewInput{
				TenantID: tenantA, TypeDefinitionID: product.ID(),
				InternalName: "sku", DisplayName: "SKU", DataType: "string",
			}, fixedTime)
			So(aerr, ShouldBeNil)
			So(repos.Attributes.Save(ctx, first), ShouldBeNil)

			second, _, aerr := domainattribute.New(domainattribute.NewInput{
				TenantID: tenantA, TypeDefinitionID: product.ID(),
				InternalName: "sku", DisplayName: "Stock code", DataType: "string",
			}, fixedTime)
			So(aerr, ShouldBeNil)
			err := repos.Attributes.Save(ctx, second)

			Convey("Then the second is refused", func() {
				So(err, ShouldNotBeNil)
				So(domainerrors.IsConflict(err), ShouldBeTrue)
			})
		})

		Convey("When two relationship definitions share an internal name", func() {
			set, _, serr := domaintypedef.NewAttributeSet(tenantA, "related_attrs", "Attrs", fixedTime)
			So(serr, ShouldBeNil)
			newDefinition := func() *domainrelationship.Definition {
				def, _, derr := domainrelationship.NewDefinition(domainrelationship.NewDefinitionInput{
					TenantID: tenantA, InternalName: "related", DisplayName: "Related",
					ParentType: product, ChildType: product, AttributeSet: set,
				}, fixedTime)
				So(derr, ShouldBeNil)
				return def
			}
			So(repos.RelationshipDefinitions.Save(ctx, newDefinition()), ShouldBeNil)
			err := repos.RelationshipDefinitions.Save(ctx, newDefinition())

			Convey("Then the second is refused", func() {
				So(err, ShouldNotBeNil)
				So(domainerrors.IsConflict(err), ShouldBeTrue)
			})
		})

		Convey("When the same two entities are linked twice", func() {
			set, _, serr := domaintypedef.NewAttributeSet(tenantA, "pair_attrs", "Attrs", fixedTime)
			So(serr, ShouldBeNil)
			def, _, derr := domainrelationship.NewDefinition(domainrelationship.NewDefinitionInput{
				TenantID: tenantA, InternalName: "bundled", DisplayName: "Bundled",
				ParentType: product, ChildType: product, AttributeSet: set,
			}, fixedTime)
			So(derr, ShouldBeNil)

			link := func() *domainrelationship.Relationship {
				rel, _, lerr := domainrelationship.Link(domainrelationship.LinkInput{
					Definition: def, ParentEntity: "a", ChildEntity: "b",
				}, fixedTime)
				So(lerr, ShouldBeNil)
				return rel
			}
			So(repos.Relationships.Save(ctx, link()), ShouldBeNil)
			err := repos.Relationships.Save(ctx, link())

			Convey("Then the second is refused", func() {
				So(err, ShouldNotBeNil)
				So(domainerrors.IsConflict(err), ShouldBeTrue)
			})
		})
	})
}
