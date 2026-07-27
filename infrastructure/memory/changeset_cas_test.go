package memory_test

import (
	"context"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	appchangeset "github.com/zkrebbekx/flexitype/application/changeset"
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
