package postgres_test

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/application/outbox"
	"github.com/zkrebbekx/flexitype/infrastructure/postgres"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/events"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// openIntegrationDB connects to the DSN in FLEXITYPE_TEST_DSN, skipping the
// test when it is unset so CI (which has no database) stays green. Run
// locally with e.g.
//
//	FLEXITYPE_TEST_DSN='postgres://postgres:postgres@localhost:55433/flexitype_it?sslmode=disable' go test ./infrastructure/postgres/ -run Integration
func openIntegrationDB(t *testing.T) *sqlx.DB {
	t.Helper()
	dsn := os.Getenv("FLEXITYPE_TEST_DSN")
	if dsn == "" {
		t.Skip("FLEXITYPE_TEST_DSN not set; skipping database integration test")
	}
	pool, err := sqlx.Connect("postgres", dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	return pool
}

func writeEnvelopes(ctx context.Context, tx db.Transactor, store outbox.Store, n int) []string {
	ids := make([]string, n)
	envs := make([]events.Envelope, n)
	now := time.Now().UTC()
	for i := 0; i < n; i++ {
		id := ulid.New().String()
		ids[i] = id
		envs[i] = events.Envelope{
			ID:            id,
			Type:          "flexitype.test.happened",
			AggregateType: "test",
			AggregateID:   fmt.Sprintf("agg-%d", i),
			TenantID:      "default",
			Actor:         "tester",
			OccurredAt:    now,
			RecordedAt:    now,
			SchemaVersion: events.SchemaVersion,
			Payload:       []byte(`{}`),
		}
	}
	_ = tx.InTransaction(ctx, func(t db.Transactor) error {
		return store.Write(ctx, t, envs)
	})
	return ids
}

func TestOutboxLeaseIntegration(t *testing.T) {
	pool := openIntegrationDB(t)
	defer func() { _ = pool.Close() }()
	ctx := context.Background()
	transactor := db.NewTransactor(pool)

	if err := postgres.Migrate(ctx, transactor); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// Clean slate for a deterministic run.
	pool.MustExec(`TRUNCATE flexitype_event_outbox, flexitype_webhook_delivery`)

	store := postgres.NewOutboxStore(transactor)

	Convey("Given a batch of pending envelopes and two concurrent relays", t, func() {
		// goconvey re-runs this setup once per leaf assertion, so reset the
		// tables each time to keep every path isolated.
		pool.MustExec(`TRUNCATE flexitype_event_outbox, flexitype_webhook_delivery`)
		ids := writeEnvelopes(ctx, transactor, store, 40)

		var mu sync.Mutex
		delivered := map[string]int{}
		dispatcher := events.NewDispatcher()
		dispatcher.RegisterFunc("counter", func(_ context.Context, e events.Envelope) error {
			// Simulate handler latency: if this ran under the sequencer
			// lock, the second relay could never make progress.
			time.Sleep(2 * time.Millisecond)
			mu.Lock()
			delivered[e.ID]++
			mu.Unlock()
			return nil
		})

		relayA := outbox.NewRelay(store, dispatcher, outbox.WithBatchSize(7), outbox.WithRelayID("relay-A"))
		relayB := outbox.NewRelay(store, dispatcher, outbox.WithBatchSize(7), outbox.WithRelayID("relay-B"))

		Convey("When both drain concurrently", func() {
			var wg sync.WaitGroup
			for _, r := range []*outbox.Relay{relayA, relayB} {
				wg.Add(1)
				go func(rel *outbox.Relay) { defer wg.Done(); rel.DrainOnce(ctx) }(r)
			}
			wg.Wait()
			// A late pass mops up anything a relay released mid-run.
			relayA.DrainOnce(ctx)

			Convey("Then every envelope is delivered exactly once with a unique feed_seq and no leftovers", func() {
				mu.Lock()
				So(len(delivered), ShouldEqual, len(ids))
				for _, id := range ids {
					So(delivered[id], ShouldEqual, 1)
				}
				mu.Unlock()

				var dispatched, distinctSeq, leftClaimed, deliveries int
				So(pool.Get(&dispatched,
					`SELECT count(*) FROM flexitype_event_outbox WHERE dispatched_at IS NOT NULL`), ShouldBeNil)
				So(pool.Get(&distinctSeq,
					`SELECT count(DISTINCT feed_seq) FROM flexitype_event_outbox WHERE feed_seq IS NOT NULL`), ShouldBeNil)
				So(pool.Get(&leftClaimed,
					`SELECT count(*) FROM flexitype_event_outbox WHERE dispatched_at IS NULL`), ShouldBeNil)
				So(pool.Get(&deliveries,
					`SELECT count(*) FROM flexitype_event_outbox WHERE feed_seq IS NOT NULL`), ShouldBeNil)
				So(dispatched, ShouldEqual, len(ids))
				So(distinctSeq, ShouldEqual, len(ids))
				So(deliveries, ShouldEqual, len(ids)) // no envelope stamped twice
				So(leftClaimed, ShouldEqual, 0)
			})
		})
	})

	Convey("Given an abandoned lease from a crashed relay", t, func() {
		// Reset per leaf re-execution (see note above).
		pool.MustExec(`TRUNCATE flexitype_event_outbox, flexitype_webhook_delivery`)
		ids := writeEnvelopes(ctx, transactor, store, 5)

		Convey("When a relay claims but never finalizes (short lease TTL)", func() {
			claimed, err := store.Claim(ctx, "dead-relay", 5, time.Second)
			So(err, ShouldBeNil)
			So(claimed, ShouldHaveLength, 5)

			Convey("Then another relay cannot reclaim them before the lease expires", func() {
				again, err := store.Claim(ctx, "live-relay", 5, time.Second)
				So(err, ShouldBeNil)
				So(again, ShouldBeEmpty)

				Convey("But once the lease expires they are reclaimed and can be finalized", func() {
					time.Sleep(1200 * time.Millisecond)
					reclaimed, err := store.Claim(ctx, "live-relay", 5, time.Second)
					So(err, ShouldBeNil)
					So(reclaimed, ShouldHaveLength, 5)

					results := make([]outbox.Result, len(reclaimed))
					for i, e := range reclaimed {
						results[i] = outbox.Result{EnvelopeID: e.ID}
					}
					So(store.Finalize(ctx, results), ShouldBeNil)

					var dispatched int
					So(pool.Get(&dispatched,
						`SELECT count(*) FROM flexitype_event_outbox WHERE dispatched_at IS NOT NULL`), ShouldBeNil)
					So(dispatched, ShouldEqual, len(ids))
				})
			})
		})
	})
}
