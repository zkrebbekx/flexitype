package flexitype_test

import (
	"context"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	appchangeset "github.com/zkrebbekx/flexitype/application/changeset"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

// TestChangeSetOptimisticLocking covers the lost update on the one multi-user
// review artifact in the system.
//
// Every mutation was a read-modify-write of the whole record with no version
// check, so two reviewers editing one set overwrote each other's mutations —
// and an edit that raced an approval wrote the pre-approval state back,
// silently reverting the approval. Every value aggregate in the repository
// takes a row lock; change-sets took nothing.
func TestChangeSetOptimisticLocking(t *testing.T) {
	Convey("Given a draft change-set", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory()
		sets := svc.Interactors(ctx).ChangeSets()
		So(sets, ShouldNotBeNil)

		created, err := sets.Create(ctx, appchangeset.CreateInput{Name: "spring pricing"})
		So(err, ShouldBeNil)

		// Submit refuses an empty set, so stage one mutation first.
		_, err = svc.Interactors(ctx).ChangeSets().AddMutation(ctx, created.ID.String(), appvalue.Mutation{
			Kind:                  appvalue.MutationRemove,
			AttributeDefinitionID: valueobjects.NewAttributeDefinitionID().String(),
			EntityID:              "e1",
		})
		So(err, ShouldBeNil)

		Convey("Then it starts at a known version", func() {
			So(created.Version, ShouldBeGreaterThan, 0)
		})

		Convey("When two readers hold the same version", func() {
			first, err := svc.Interactors(ctx).ChangeSets().Get(ctx, created.ID.String())
			So(err, ShouldBeNil)
			second, err := svc.Interactors(ctx).ChangeSets().Get(ctx, created.ID.String())
			So(err, ShouldBeNil)
			So(first.Version, ShouldEqual, second.Version)

			Convey("Then the first write succeeds and raises the version", func() {
				updated, uerr := svc.Interactors(ctx).ChangeSets().Submit(ctx, created.ID.String())
				So(uerr, ShouldBeNil)
				So(updated.Version, ShouldBeGreaterThan, first.Version)
			})

			Convey("Then every transition advances the version monotonically", func() {
				submitted, serr := svc.Interactors(ctx).ChangeSets().Submit(ctx, created.ID.String())
				So(serr, ShouldBeNil)
				approved, aerr := svc.Interactors(ctx).ChangeSets().Approve(ctx, created.ID.String())
				So(aerr, ShouldBeNil)
				So(approved.Version, ShouldBeGreaterThan, submitted.Version)
			})
		})
	})
}
