package flexitype_test

import (
	"context"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	apprelationship "github.com/zkrebbekx/flexitype/application/relationship"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

func cardIntp(n int) *int { return &n }

// TestRelationshipCardinalityParityPostgres re-runs the cardinality suite
// (infrastructure/memory/relationship_cardinality_test.go) against Postgres,
// exercising the live-link counting SQL that backs max-child enforcement and
// the unmet-minimum requirements report.
func TestRelationshipCardinalityParityPostgres(t *testing.T) {
	pool := openTestDB(t)
	defer func() { _ = pool.Close() }()

	svc := flexitype.New(pool)
	if err := svc.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	Convey("Given a directed definition with max 2 children and min 1 child (Postgres)", t, func() {
		truncateAll(t, pool)
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		it := svc.Interactors(ctx)

		item, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{InternalName: "item", DisplayName: "Item"})
		So(err, ShouldBeNil)
		def, err := it.Relationships().CreateDefinition(ctx, apprelationship.CreateDefinitionInput{
			InternalName: "contains", DisplayName: "Contains",
			ParentTypeID: item.ID.String(), ChildTypeID: item.ID.String(),
			MaxChildren: cardIntp(2), MinChildren: cardIntp(1),
		})
		So(err, ShouldBeNil)

		link := func(parent, child string) error {
			_, e := it.Relationships().Link(ctx, apprelationship.LinkInput{
				DefinitionID: def.ID.String(), ParentEntity: parent, ChildEntity: child,
			})
			return e
		}

		Convey("When a parent is linked to a third child", func() {
			So(link("p", "c1"), ShouldBeNil)
			So(link("p", "c2"), ShouldBeNil)
			err := link("p", "c3")

			Convey("Then it is rejected as a validation error with a machine-readable constraint", func() {
				So(err, ShouldNotBeNil)
				So(domainerrors.CodeOf(err), ShouldEqual, domainerrors.CodeValidation)
				So(domainerrors.DetailsOf(err)["constraint"], ShouldEqual, "max_children")
			})
		})

		Convey("When requirements are read for an entity with no links", func() {
			reqs, err := it.Relationships().RelationshipRequirements(ctx, "lonely")

			Convey("Then the unmet minimum is reported", func() {
				So(err, ShouldBeNil)
				So(reqs, ShouldHaveLength, 1)
				So(reqs[0].Side, ShouldEqual, "children")
				So(reqs[0].Required, ShouldEqual, 1)
				So(reqs[0].Current, ShouldEqual, 0)
			})
		})

		Convey("When an entity meets the minimum", func() {
			So(link("q", "qc"), ShouldBeNil)
			reqs, err := it.Relationships().RelationshipRequirements(ctx, "q")

			Convey("Then it has no unmet requirements", func() {
				So(err, ShouldBeNil)
				So(reqs, ShouldBeEmpty)
			})
		})
	})
}
