package flexitype_test

import (
	"context"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	"github.com/zkrebbekx/flexitype/application/uow"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// TestTenantRedactionChunksAndVerifies is the regression for #486.
//
// Both tenant redactions were one unbounded UPDATE over an unindexed JSON
// path, inside the erasure transaction. They now run in bounded chunks over
// the primary key, and confirm the predicate is empty rather than trusting an
// empty chunk. The row count here exceeds one chunk, so the loop is exercised
// rather than assumed.
func TestTenantRedactionChunksAndVerifies(t *testing.T) {
	pool := openTestDB(t)
	defer func() { _ = pool.Close() }()

	svc := flexitype.New(pool)
	if err := svc.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	Convey("Given more outbox and activity rows than one redaction chunk", t, func() {
		truncateAll(t, pool)
		const rows = 5200 // redactChunk is 5000
		tenant := valueobjects.TenantID("tenant-purge")
		ctx := uow.WithTenant(context.Background(), tenant)

		for i := 0; i < rows; i++ {
			_, err := pool.Exec(`INSERT INTO flexitype_event_outbox
				(id, tenant_id, actor, event_type, aggregate_type, aggregate_id, payload,
				 occurred_at, recorded_at, attempts, last_error, next_attempt_at)
				VALUES ($1, $2, 'test', 'value.set', 'attribute_value', 'e1', $3,
				 now(), now(), 0, '', now())`,
				ulid.New().String(), tenant.String(),
				`{"entity_id":"e1","value":"secret"}`)
			So(err, ShouldBeNil)
			_, err = pool.Exec(`INSERT INTO flexitype_activity_log
				(id, tenant_id, actor, entity, entity_id, action, before_state, after_state, occurred_at)
				VALUES ($1, $2, 'test', 'attribute_value', 'e1', 'updated', $3, $3, now())`,
				ulid.New().String(), tenant.String(), `{"entity_id":"e1","value":"secret"}`)
			So(err, ShouldBeNil)
		}

		Convey("When the tenant is purged", func() {
			_, err := svc.Interactors(ctx).Erasure().PurgeTenant(ctx)
			So(err, ShouldBeNil)

			Convey("Then no row of either table still carries an erasable value", func() {
				var outboxLeft, activityLeft int
				So(pool.Get(&outboxLeft, `SELECT count(*) FROM flexitype_event_outbox
					 WHERE tenant_id = $1 AND payload->>'entity_id' IS NOT NULL
					   AND COALESCE(payload->>'erased','false') <> 'true'`, tenant.String()), ShouldBeNil)
				So(outboxLeft, ShouldEqual, 0)
				So(pool.Get(&activityLeft, `SELECT count(*) FROM flexitype_activity_log
					 WHERE tenant_id = $1 AND (before_state->>'entity_id' IS NOT NULL
					    OR after_state->>'entity_id' IS NOT NULL)`, tenant.String()), ShouldBeNil)
				So(activityLeft, ShouldEqual, 0)
			})

			Convey("And the payloads no longer hold the value", func() {
				var withValue int
				So(pool.Get(&withValue, `SELECT count(*) FROM flexitype_event_outbox
					 WHERE tenant_id = $1 AND payload->>'value' IS NOT NULL`, tenant.String()), ShouldBeNil)
				So(withValue, ShouldEqual, 0)
			})
		})
	})
}
