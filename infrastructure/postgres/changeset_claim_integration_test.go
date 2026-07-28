package postgres_test

import (
	"context"
	"testing"
	"time"

	_ "github.com/lib/pq"
	. "github.com/smartystreets/goconvey/convey"

	appchangeset "github.com/zkrebbekx/flexitype/application/changeset"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/infrastructure/postgres"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// TestStalePublishingIntegration proves the store finds change-sets stranded
// by a publish that never finished.
//
// A publish claims the set (state publishing) before it applies the
// mutations. A request that ends mid-publish left the claim behind, and every
// exit was closed: Reject refuses publishing, the scheduler selected only
// approved. The scheduler now reclaims a claim older than the TTL, and this
// is the query it runs.
func TestStalePublishingIntegration(t *testing.T) {
	pool := openIntegrationDB(t)
	defer func() { _ = pool.Close() }()
	ctx := context.Background()

	if err := postgres.Migrate(ctx, db.NewTransactor(pool)); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	store := postgres.NewChangeSetStore(pool)
	reclaimer, isReclaimer := store.(appchangeset.ClaimReclaimer)
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)

	Convey("Given claims of differing age, an approved set and a second tenant", t, func() {
		pool.MustExec(`TRUNCATE flexitype_changeset CASCADE`)
		So(isReclaimer, ShouldBeTrue)

		mk := func(name string, tenant valueobjects.TenantID, state appchangeset.State, updated time.Time) {
			So(store.Create(ctx, appchangeset.ChangeSet{
				ID: ulid.New(), TenantID: tenant, Name: name, State: state,
				CreatedAt: updated, UpdatedAt: updated,
			}), ShouldBeNil)
		}
		cutoff := now.Add(-appchangeset.PublishClaimTTL)
		mk("stranded", valueobjects.DefaultTenant, appchangeset.StatePublishing, now.Add(-time.Hour))
		mk("foreign-stranded", valueobjects.TenantID("tenant-b"), appchangeset.StatePublishing, now.Add(-2*time.Hour))
		mk("in-flight", valueobjects.DefaultTenant, appchangeset.StatePublishing, now.Add(-time.Minute))
		mk("approved", valueobjects.DefaultTenant, appchangeset.StateApproved, now.Add(-time.Hour))

		Convey("When stale claims are listed", func() {
			got, err := reclaimer.StalePublishing(ctx, cutoff)
			So(err, ShouldBeNil)

			Convey("Then only publishing sets past the cutoff qualify, oldest first, across tenants", func() {
				names := make([]string, len(got))
				for i, cs := range got {
					names[i] = cs.Name
				}
				So(names, ShouldResemble, []string{"foreign-stranded", "stranded"})
			})
		})
	})
}
