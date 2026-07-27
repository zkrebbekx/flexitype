package flexitype_test

import (
	"context"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	appchangeset "github.com/zkrebbekx/flexitype/application/changeset"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

// TestChangeSetTransitionGuards pins the state machine's refusals.
//
// A change-set is a review artifact, so the transitions are the contract: a
// reviewer must not be able to approve a draft, publish a rejected set, or
// stage a mutation onto something already published. Each refusal is a
// message a person reads, so each is asserted rather than inferred.
func TestChangeSetTransitionGuards(t *testing.T) {
	Convey("Given a draft change-set with one staged mutation", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory()

		created, err := svc.Interactors(ctx).ChangeSets().Create(ctx, appchangeset.CreateInput{
			Name: "spring pricing",
		})
		So(err, ShouldBeNil)

		mutation := appvalue.Mutation{
			Kind:                  appvalue.MutationRemove,
			AttributeDefinitionID: valueobjects.NewAttributeDefinitionID().String(),
			EntityID:              "e1",
		}
		_, err = svc.Interactors(ctx).ChangeSets().AddMutation(ctx, created.ID.String(), mutation)
		So(err, ShouldBeNil)

		Convey("Then a name is required at creation", func() {
			_, cerr := svc.Interactors(ctx).ChangeSets().Create(ctx, appchangeset.CreateInput{})
			So(cerr, ShouldNotBeNil)
			So(domainerrors.IsValidation(cerr), ShouldBeTrue)
		})

		Convey("Then an unknown id is not found", func() {
			_, gerr := svc.Interactors(ctx).ChangeSets().Get(ctx, "01JBQ8Z0000000000000000000")
			So(gerr, ShouldNotBeNil)
		})

		Convey("Then a malformed id is a validation error, not a lookup", func() {
			_, gerr := svc.Interactors(ctx).ChangeSets().Get(ctx, "not-an-id")
			So(gerr, ShouldNotBeNil)
			So(domainerrors.IsValidation(gerr), ShouldBeTrue)
		})

		Convey("When it is submitted for review", func() {
			submitted, serr := svc.Interactors(ctx).ChangeSets().Submit(ctx, created.ID.String())
			So(serr, ShouldBeNil)
			So(submitted.State, ShouldEqual, appchangeset.StateInReview)

			Convey("Then staging another mutation is refused: it is no longer a draft", func() {
				_, aerr := svc.Interactors(ctx).ChangeSets().AddMutation(ctx, created.ID.String(), mutation)
				So(aerr, ShouldNotBeNil)
				So(aerr.Error(), ShouldContainSubstring, "not a draft")
			})

			Convey("Then submitting again is refused", func() {
				_, aerr := svc.Interactors(ctx).ChangeSets().Submit(ctx, created.ID.String())
				So(aerr, ShouldNotBeNil)
			})

			Convey("Then it can be rejected", func() {
				rejected, rerr := svc.Interactors(ctx).ChangeSets().Reject(ctx, created.ID.String())
				So(rerr, ShouldBeNil)
				So(rejected.State, ShouldEqual, appchangeset.StateRejected)

				Convey("And a rejected set cannot be approved", func() {
					_, aerr := svc.Interactors(ctx).ChangeSets().Approve(ctx, created.ID.String())
					So(aerr, ShouldNotBeNil)
				})
			})
		})

		Convey("When it is listed", func() {
			sets, lerr := svc.Interactors(ctx).ChangeSets().List(ctx)

			Convey("Then it appears with its version", func() {
				So(lerr, ShouldBeNil)
				So(sets, ShouldHaveLength, 1)
				So(sets[0].Version, ShouldBeGreaterThan, 0)
			})
		})

		Convey("When a publish time is set in the future", func() {
			at := time.Now().UTC().Add(time.Hour)
			scheduled, cerr := svc.Interactors(ctx).ChangeSets().Create(ctx, appchangeset.CreateInput{
				Name: "later", PublishAt: &at,
			})

			Convey("Then it is stored on the set", func() {
				So(cerr, ShouldBeNil)
				So(scheduled.PublishAt, ShouldNotBeNil)
			})

			Convey("Then the scheduler publishes nothing yet", func() {
				n, perr := svc.Interactors(ctx).ChangeSets().PublishDue(ctx)
				So(perr, ShouldBeNil)
				So(n, ShouldEqual, 0)
			})
		})
	})
}
