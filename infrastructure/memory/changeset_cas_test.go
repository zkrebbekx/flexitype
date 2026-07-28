package memory_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	appchangeset "github.com/zkrebbekx/flexitype/application/changeset"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/infrastructure/memory"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// TestChangeSetCompareAndSwap covers the lost update the store used to allow.
//
// Update wrote the whole record unconditionally, so a caller holding a stale
// read overwrote whatever landed in between: two reviewers editing one set
// lost each other's mutations, and an edit racing an approval wrote the
// pre-approval state back — reverting the approval with no error and no trace.
func TestChangeSetCompareAndSwap(t *testing.T) {
	Convey("Given a stored change-set at version 1", t, func() {
		ctx := context.Background()
		store := memory.NewChangeSetStore()
		now := time.Now().UTC()
		cs := appchangeset.ChangeSet{
			ID: ulid.New(), TenantID: valueobjects.DefaultTenant,
			Name: "spring pricing", State: appchangeset.StateDraft,
			CreatedAt: now, UpdatedAt: now, Version: 1,
		}
		So(store.Create(ctx, cs), ShouldBeNil)

		first, err := store.Get(ctx, valueobjects.DefaultTenant, cs.ID)
		So(err, ShouldBeNil)
		second, err := store.Get(ctx, valueobjects.DefaultTenant, cs.ID)
		So(err, ShouldBeNil)

		Convey("When the first writer commits", func() {
			first.Name = "spring pricing v2"
			So(store.Update(ctx, first), ShouldBeNil)

			Convey("Then the stored version advanced", func() {
				got, gerr := store.Get(ctx, valueobjects.DefaultTenant, cs.ID)
				So(gerr, ShouldBeNil)
				So(got.Version, ShouldEqual, first.Version+1)
				So(got.Name, ShouldEqual, "spring pricing v2")
			})

			Convey("Then the second writer's stale view is refused", func() {
				second.Name = "written from a stale read"
				err := store.Update(ctx, second)
				So(err, ShouldNotBeNil)
				So(domainerrors.IsConflict(err), ShouldBeTrue)
			})

			Convey("Then the stale write left the record untouched", func() {
				second.Name = "written from a stale read"
				_ = store.Update(ctx, second)
				got, gerr := store.Get(ctx, valueobjects.DefaultTenant, cs.ID)
				So(gerr, ShouldBeNil)
				So(got.Name, ShouldEqual, "spring pricing v2")
			})

			Convey("Then re-reading and retrying succeeds", func() {
				fresh, gerr := store.Get(ctx, valueobjects.DefaultTenant, cs.ID)
				So(gerr, ShouldBeNil)
				fresh.Name = "written after a re-read"
				So(store.Update(ctx, fresh), ShouldBeNil)
			})
		})

		Convey("When the set belongs to another tenant", func() {
			other := first
			other.TenantID = valueobjects.TenantID("other")
			err := store.Update(ctx, other)

			Convey("Then it is not found rather than cross-tenant written", func() {
				So(err, ShouldNotBeNil)
				So(domainerrors.IsNotFound(err), ShouldBeTrue)
			})
		})
	})
}

// TestPublishClaimsBeforeApplying covers the ordering that made a failed
// compare-and-swap leave the data written.
//
// Publish applied the mutations and only then performed the version-guarded
// Update. Once that call could fail, any concurrent touch of the set — a
// reviewer rejecting it, a second publish, the scheduler tick — committed the
// values and left the record saying something else. Through PublishDue it
// compounded: the set stayed approved with publish_at in the past, so every
// tick re-applied the same mutations over whatever had been written in
// between, and a REJECTED set could have its contents applied on a timer.
func TestPublishClaimsBeforeApplying(t *testing.T) {
	Convey("Given an approved change-set whose record moves under it", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory()
		it := svc.Interactors(ctx)

		product, err := it.TypeDefinitions().Create(ctx,
			apptypedef.CreateInput{InternalName: "product", DisplayName: "Product"})
		So(err, ShouldBeNil)
		name, err := it.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: product.ID.String(), InternalName: "name",
			DisplayName: "Name", DataType: "string",
		})
		So(err, ShouldBeNil)

		cs, err := it.ChangeSets().Create(ctx, appchangeset.CreateInput{Name: "rename"})
		So(err, ShouldBeNil)
		_, err = it.ChangeSets().AddMutation(ctx, cs.ID.String(), appvalue.Mutation{
			Kind: appvalue.MutationSet, AttributeDefinitionID: name.ID.String(),
			EntityID: "p1", TypeDefinitionID: product.ID.String(),
			Value: json.RawMessage(`"after"`),
		})
		So(err, ShouldBeNil)

		Convey("When it publishes normally", func() {
			out, perr := svc.Interactors(ctx).ChangeSets().Publish(ctx, cs.ID.String())

			Convey("Then the set is published and the value is written", func() {
				So(perr, ShouldBeNil)
				So(out.State, ShouldEqual, appchangeset.StatePublished)

				vals, verr := svc.Interactors(ctx).Values().ListByEntity(ctx, product.ID.String(), "p1")
				So(verr, ShouldBeNil)
				So(vals, ShouldHaveLength, 1)
			})
		})

		Convey("When it is published twice concurrently", func() {
			// The second publish loses the compare-and-swap. What matters is
			// that it loses it BEFORE the mutations are applied, so the data
			// is written exactly once and the record matches it.
			var wg sync.WaitGroup
			errs := make([]error, 2)
			wg.Add(2)
			for i := 0; i < 2; i++ {
				go func(i int) {
					defer wg.Done()
					_, errs[i] = svc.Interactors(ctx).ChangeSets().Publish(ctx, cs.ID.String())
				}(i)
			}
			wg.Wait()

			Convey("Then exactly one succeeds", func() {
				failed := 0
				for _, e := range errs {
					if e != nil {
						failed++
					}
				}
				So(failed, ShouldEqual, 1)
			})

			Convey("Then the entity holds one value, not two applications of the same set", func() {
				vals, verr := svc.Interactors(ctx).Values().ListByEntity(ctx, product.ID.String(), "p1")
				So(verr, ShouldBeNil)
				So(vals, ShouldHaveLength, 1)
			})

			Convey("Then the record says published, matching the data", func() {
				got, gerr := svc.Interactors(ctx).ChangeSets().Get(ctx, cs.ID.String())
				So(gerr, ShouldBeNil)
				So(got.State, ShouldEqual, appchangeset.StatePublished)
			})
		})

		Convey("When publishing fails on a constraint", func() {
			bad, cerr := svc.Interactors(ctx).ChangeSets().Create(ctx,
				appchangeset.CreateInput{Name: "bad"})
			So(cerr, ShouldBeNil)
			_, aerr := svc.Interactors(ctx).ChangeSets().AddMutation(ctx, bad.ID.String(),
				appvalue.Mutation{
					Kind: appvalue.MutationSet, AttributeDefinitionID: name.ID.String(),
					EntityID: "p2", TypeDefinitionID: product.ID.String(),
					Value: json.RawMessage(`12345`), // not a string
				})
			So(aerr, ShouldBeNil)
			_, perr := svc.Interactors(ctx).ChangeSets().Publish(ctx, bad.ID.String())

			Convey("Then the error is reported and nothing is written", func() {
				So(perr, ShouldNotBeNil)
				vals, verr := svc.Interactors(ctx).Values().ListByEntity(ctx, product.ID.String(), "p2")
				So(verr, ShouldBeNil)
				So(vals, ShouldBeEmpty)
			})

			Convey("Then the claim is handed back, so it can publish again once fixed", func() {
				got, gerr := svc.Interactors(ctx).ChangeSets().Get(ctx, bad.ID.String())
				So(gerr, ShouldBeNil)
				So(got.State, ShouldNotEqual, appchangeset.StatePublishing)
				So(got.State, ShouldNotEqual, appchangeset.StatePublished)
			})
		})
	})
}

// TestPublishingStateIsInert covers what a change-set left mid-publish can and
// cannot do.
//
// The state exists so the claim can be taken before the side effects. It is
// deliberately not something the scheduler picks up: a set that began
// publishing and did not finish is visible and inert rather than silently
// repeating its mutations on every tick.
func TestPublishingStateIsInert(t *testing.T) {
	Convey("Given a change-set left in the publishing state", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		store := memory.NewChangeSetStore()
		now := time.Now().UTC()
		past := now.Add(-time.Hour)
		cs := appchangeset.ChangeSet{
			ID: ulid.New(), TenantID: valueobjects.DefaultTenant, Name: "stuck",
			State: appchangeset.StatePublishing, PublishAt: &past,
			CreatedAt: now, UpdatedAt: now, Version: 1,
		}
		So(store.Create(ctx, cs), ShouldBeNil)

		Convey("When the scheduler looks for work", func() {
			due, err := store.DueForPublish(ctx, now)

			Convey("Then it is not picked up, so its mutations are not re-applied", func() {
				So(err, ShouldBeNil)
				for _, d := range due {
					So(d.ID, ShouldNotEqual, cs.ID)
				}
			})
		})

		Convey("When it is read back", func() {
			got, err := store.Get(ctx, valueobjects.DefaultTenant, cs.ID)

			Convey("Then the state names what happened rather than lying", func() {
				So(err, ShouldBeNil)
				So(got.State, ShouldEqual, appchangeset.StatePublishing)
			})
		})
	})
}

// TestPublishDueDoesNotRepeatAFailedSet covers the scheduler tick.
//
// A failed publish used to leave the set approved with publish_at in the
// past, so every tick re-applied the same mutations over whatever had been
// written in between — and a rejected set could have its contents applied on
// a timer. The claim is taken first, so a set that cannot publish reports its
// failure and does not silently repeat.
func TestPublishDueDoesNotRepeatAFailedSet(t *testing.T) {
	Convey("Given a scheduled change-set whose mutation cannot apply", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory()
		it := svc.Interactors(ctx)

		product, err := it.TypeDefinitions().Create(ctx,
			apptypedef.CreateInput{InternalName: "product", DisplayName: "Product"})
		So(err, ShouldBeNil)
		name, err := it.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: product.ID.String(), InternalName: "name",
			DisplayName: "Name", DataType: "string",
		})
		So(err, ShouldBeNil)

		past := time.Now().UTC().Add(-time.Hour)
		cs, err := svc.Interactors(ctx).ChangeSets().Create(ctx, appchangeset.CreateInput{
			Name: "scheduled", PublishAt: &past,
		})
		So(err, ShouldBeNil)
		_, err = svc.Interactors(ctx).ChangeSets().AddMutation(ctx, cs.ID.String(),
			appvalue.Mutation{
				Kind: appvalue.MutationSet, AttributeDefinitionID: name.ID.String(),
				EntityID: "p9", TypeDefinitionID: product.ID.String(),
				Value: json.RawMessage(`404`), // not a string
			})
		So(err, ShouldBeNil)
		_, err = svc.Interactors(ctx).ChangeSets().Submit(ctx, cs.ID.String())
		So(err, ShouldBeNil)
		_, err = svc.Interactors(ctx).ChangeSets().Approve(ctx, cs.ID.String())
		So(err, ShouldBeNil)

		Convey("When the scheduler ticks twice", func() {
			first, ferr := svc.Interactors(ctx).ChangeSets().PublishDue(ctx)
			second, serr := svc.Interactors(ctx).ChangeSets().PublishDue(ctx)

			Convey("Then neither tick publishes it", func() {
				So(ferr, ShouldBeNil)
				So(serr, ShouldBeNil)
				So(first, ShouldEqual, 0)
				So(second, ShouldEqual, 0)
			})

			Convey("Then nothing was written for the entity", func() {
				vals, verr := svc.Interactors(ctx).Values().ListByEntity(ctx, product.ID.String(), "p9")
				So(verr, ShouldBeNil)
				So(vals, ShouldBeEmpty)
			})
		})
	})

	Convey("Given a scheduled change-set that can publish", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory()
		it := svc.Interactors(ctx)

		product, err := it.TypeDefinitions().Create(ctx,
			apptypedef.CreateInput{InternalName: "product", DisplayName: "Product"})
		So(err, ShouldBeNil)
		name, err := it.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: product.ID.String(), InternalName: "name",
			DisplayName: "Name", DataType: "string",
		})
		So(err, ShouldBeNil)

		past := time.Now().UTC().Add(-time.Hour)
		cs, err := svc.Interactors(ctx).ChangeSets().Create(ctx, appchangeset.CreateInput{
			Name: "scheduled", PublishAt: &past,
		})
		So(err, ShouldBeNil)
		_, err = svc.Interactors(ctx).ChangeSets().AddMutation(ctx, cs.ID.String(),
			appvalue.Mutation{
				Kind: appvalue.MutationSet, AttributeDefinitionID: name.ID.String(),
				EntityID: "p8", TypeDefinitionID: product.ID.String(),
				Value: json.RawMessage(`"scheduled"`),
			})
		So(err, ShouldBeNil)
		_, err = svc.Interactors(ctx).ChangeSets().Submit(ctx, cs.ID.String())
		So(err, ShouldBeNil)
		_, err = svc.Interactors(ctx).ChangeSets().Approve(ctx, cs.ID.String())
		So(err, ShouldBeNil)

		Convey("When the scheduler ticks twice", func() {
			first, ferr := svc.Interactors(ctx).ChangeSets().PublishDue(ctx)
			second, serr := svc.Interactors(ctx).ChangeSets().PublishDue(ctx)

			Convey("Then it publishes once and is not picked up again", func() {
				So(ferr, ShouldBeNil)
				So(serr, ShouldBeNil)
				So(first, ShouldEqual, 1)
				So(second, ShouldEqual, 0)
			})

			Convey("Then the entity holds exactly one value", func() {
				vals, verr := svc.Interactors(ctx).Values().ListByEntity(ctx, product.ID.String(), "p8")
				So(verr, ShouldBeNil)
				So(vals, ShouldHaveLength, 1)
			})
		})
	})
}
