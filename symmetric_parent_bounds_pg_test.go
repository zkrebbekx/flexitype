package flexitype_test

import (
	"context"
	"os"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	apprelationship "github.com/zkrebbekx/flexitype/application/relationship"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

// TestSymmetricRelationshipRefusesParentBounds is the regression for #506.
//
// A symmetric link has no parent side: enforceCardinality and
// RelationshipRequirements read the CHILDREN bounds for a symmetric kind. A
// stored min_parents/max_parents was therefore accepted and then never
// enforced — a symmetric spouse_of declared max_parents 1 admitted a-b, a-c
// and a-d, and an unmet min_parents was invisible.
func TestSymmetricRelationshipRefusesParentBounds(t *testing.T) {
	Convey("Given two person types to relate", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory()
		ia := svc.Interactors(ctx)

		person, err := ia.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "person", DisplayName: "Person",
		})
		So(err, ShouldBeNil)
		one, two := 1, 2

		create := func(in apprelationship.CreateDefinitionInput) error {
			in.ParentTypeID = person.ID.String()
			in.ChildTypeID = person.ID.String()
			in.DisplayName = in.InternalName
			_, cerr := svc.Interactors(ctx).Relationships().CreateDefinition(ctx, in)
			return cerr
		}

		Convey("When a symmetric definition declares a parents cap", func() {
			err := create(apprelationship.CreateDefinitionInput{
				InternalName: "spouse_of", Kind: "symmetric", MaxParents: &one,
			})

			Convey("Then it is refused, pointing at the bound that is enforced", func() {
				So(domainerrors.CodeOf(err), ShouldEqual, domainerrors.CodeValidation)
				So(err.Error(), ShouldContainSubstring, "min_children/max_children")
			})
		})

		Convey("When a symmetric definition declares a parents minimum", func() {
			err := create(apprelationship.CreateDefinitionInput{
				InternalName: "peer_of", Kind: "symmetric", MinParents: &one,
			})

			Convey("Then it is refused too", func() {
				So(domainerrors.CodeOf(err), ShouldEqual, domainerrors.CodeValidation)
			})
		})

		Convey("When a symmetric definition uses the children bounds", func() {
			err := create(apprelationship.CreateDefinitionInput{
				InternalName: "spouse_of", Kind: "symmetric", MaxChildren: &one,
			})

			Convey("Then it is accepted, and the cap is the one that is enforced", func() {
				So(err, ShouldBeNil)

				defs, lerr := svc.Interactors(ctx).Relationships().ListDefinitions(ctx, apprelationship.DefinitionListInput{})
				So(lerr, ShouldBeNil)
				var defID string
				for _, d := range defs.Items {
					if d.InternalName == "spouse_of" {
						defID = d.ID.String()
					}
				}
				So(defID, ShouldNotBeEmpty)

				_, lerr = svc.Interactors(ctx).Relationships().Link(ctx, apprelationship.LinkInput{
					DefinitionID: defID, ParentEntity: "a", ChildEntity: "b",
				})
				So(lerr, ShouldBeNil)
				_, lerr = svc.Interactors(ctx).Relationships().Link(ctx, apprelationship.LinkInput{
					DefinitionID: defID, ParentEntity: "a", ChildEntity: "c",
				})
				So(lerr, ShouldNotBeNil)
				So(lerr.Error(), ShouldContainSubstring, "cardinality")

				// The cap counts an entity's links on EITHER side, which is
				// what makes it the right home for a symmetric limit.
				_, lerr = svc.Interactors(ctx).Relationships().Link(ctx, apprelationship.LinkInput{
					DefinitionID: defID, ParentEntity: "d", ChildEntity: "b",
				})
				So(lerr, ShouldNotBeNil)
				So(lerr.Error(), ShouldContainSubstring, "cardinality")
			})
		})

		Convey("When a DIRECTED definition declares parent bounds", func() {
			other, oerr := svc.Interactors(ctx).TypeDefinitions().Create(ctx, apptypedef.CreateInput{
				InternalName: "company", DisplayName: "Company",
			})
			So(oerr, ShouldBeNil)
			_, err := svc.Interactors(ctx).Relationships().CreateDefinition(ctx, apprelationship.CreateDefinitionInput{
				InternalName: "employs", DisplayName: "Employs",
				ParentTypeID: other.ID.String(), ChildTypeID: person.ID.String(),
				MaxParents: &two,
			})

			Convey("Then they are still accepted: a directed link has both sides", func() {
				So(err, ShouldBeNil)
			})
		})
	})
}

// TestSymmetricParentBoundsFoldedPostgres covers migration 000035, which
// repairs the rows written before construction refused these bounds. The
// bound moves to the side that IS enforced rather than being dropped: an
// unset children bound takes it outright, and a set one keeps whichever is
// tighter.
func TestSymmetricParentBoundsFoldedPostgres(t *testing.T) {
	pool := openTestDB(t)
	defer func() { _ = pool.Close() }()

	svc := flexitype.New(pool)
	if err := svc.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	Convey("Given symmetric definitions stored with parent bounds", t, func() {
		truncateAll(t, pool)
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		person, err := svc.Interactors(ctx).TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "person", DisplayName: "Person",
		})
		So(err, ShouldBeNil)

		// The rows an earlier release accepted. The definition is created
		// through the API — so its attribute set and every other invariant
		// are real — and only the bounds are then written directly, because
		// construction now refuses the shape under test.
		seed := func(name string, minChildren, maxChildren, minParents, maxParents any) {
			_, cerr := svc.Interactors(ctx).Relationships().CreateDefinition(ctx, apprelationship.CreateDefinitionInput{
				InternalName: name, DisplayName: name, Kind: "symmetric",
				ParentTypeID: person.ID.String(), ChildTypeID: person.ID.String(),
			})
			So(cerr, ShouldBeNil)
			_, uerr := pool.Exec(`UPDATE flexitype_relationship_definition
				   SET min_children = $2, max_children = $3, min_parents = $4, max_parents = $5
				 WHERE internal_name = $1`,
				name, minChildren, maxChildren, minParents, maxParents)
			So(uerr, ShouldBeNil)
		}
		seed("only_parents", nil, nil, 1, 3) // children unset: takes the parents bound
		seed("both_set", 2, 5, 1, 3)         // both set: keeps the tighter of each
		seed("no_parent_bounds", nil, 4, nil, nil)

		// Run the SHIPPED migration body against them, rather than a copy of
		// its SQL: Migrate already recorded 000035, so these rows — written
		// after it ran — are exactly the pre-upgrade state it repairs.
		body, rerr := os.ReadFile("infrastructure/postgres/migrations/000035_symmetric_parent_bounds.up.sql")
		So(rerr, ShouldBeNil)
		_, err = pool.Exec(string(body))
		So(err, ShouldBeNil)
		// Applying it twice must not move a bound again.
		_, err = pool.Exec(string(body))
		So(err, ShouldBeNil)

		read := func(name string) (minC, maxC, minP, maxP *int) {
			row := pool.QueryRow(`SELECT min_children, max_children, min_parents, max_parents
				 FROM flexitype_relationship_definition WHERE internal_name = $1`, name)
			So(row.Scan(&minC, &maxC, &minP, &maxP), ShouldBeNil)
			return
		}

		Convey("Then a definition with no children bound takes the parents bound", func() {
			minC, maxC, minP, maxP := read("only_parents")
			So(*minC, ShouldEqual, 1)
			So(*maxC, ShouldEqual, 3)
			So(minP, ShouldBeNil)
			So(maxP, ShouldBeNil)
		})

		Convey("Then a definition with both bounds keeps the tighter of each", func() {
			minC, maxC, minP, maxP := read("both_set")
			So(*minC, ShouldEqual, 2) // the greater minimum
			So(*maxC, ShouldEqual, 3) // the lesser maximum
			So(minP, ShouldBeNil)
			So(maxP, ShouldBeNil)
		})

		Convey("Then a definition that declared no parent bound is untouched", func() {
			minC, maxC, minP, maxP := read("no_parent_bounds")
			So(minC, ShouldBeNil)
			So(*maxC, ShouldEqual, 4)
			So(minP, ShouldBeNil)
			So(maxP, ShouldBeNil)
		})
	})
}
