package outbox

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/events"
)

// fakeStore is an in-memory outbox honouring the claim/mark contract.
type fakeStore struct {
	mu      sync.Mutex
	pending []events.Envelope
	done    []string
	fails   map[string]int
}

func (s *fakeStore) Write(_ context.Context, _ db.QueryExecer, envs []events.Envelope) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending = append(s.pending, envs...)
	return nil
}

func (s *fakeStore) Expand(_ context.Context, limit int, fn func(envs []events.Envelope) []Result) error {
	s.mu.Lock()
	batch := s.pending
	if len(batch) > limit {
		batch = batch[:limit]
	}
	claimed := make([]events.Envelope, len(batch))
	copy(claimed, batch)
	s.mu.Unlock()

	if len(claimed) == 0 {
		return nil
	}
	results := fn(claimed)

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, res := range results {
		if res.Err == nil {
			s.done = append(s.done, res.EnvelopeID)
			for i, env := range s.pending {
				if env.ID == res.EnvelopeID {
					s.pending = append(s.pending[:i], s.pending[i+1:]...)
					break
				}
			}
		} else {
			if s.fails == nil {
				s.fails = map[string]int{}
			}
			s.fails[res.EnvelopeID]++
		}
	}
	return nil
}

func env(id string) events.Envelope {
	return events.Envelope{ID: id, Type: "flexitype.test.happened", Payload: []byte(`{}`), OccurredAt: time.Now()}
}

func TestRelay(t *testing.T) {
	Convey("Given a relay over a store with pending envelopes", t, func() {
		store := &fakeStore{pending: []events.Envelope{env("e1"), env("e2"), env("e3")}}
		dispatcher := events.NewDispatcher()

		var delivered []string
		var deliveredMu sync.Mutex
		dispatcher.RegisterFunc("recorder", func(_ context.Context, e events.Envelope) error {
			deliveredMu.Lock()
			defer deliveredMu.Unlock()
			delivered = append(delivered, e.ID)
			return nil
		})

		Convey("When the relay drains", func() {
			relay := NewRelay(store, dispatcher, WithBatchSize(2))
			relay.drain(context.Background())

			Convey("Then every envelope dispatches exactly once and is marked done", func() {
				So(delivered, ShouldResemble, []string{"e1", "e2", "e3"})
				So(store.done, ShouldHaveLength, 3)
				So(store.pending, ShouldBeEmpty)
			})
		})

		Convey("When a handler fails for one envelope", func() {
			dispatcher.RegisterFunc("flaky", func(_ context.Context, e events.Envelope) error {
				if e.ID == "e2" {
					return fmt.Errorf("broker down")
				}
				return nil
			})

			relay := NewRelay(store, dispatcher, WithBatchSize(10))
			relay.drain(context.Background())

			Convey("Then the failed envelope stays pending with an attempt recorded", func() {
				So(store.done, ShouldResemble, []string{"e1", "e3"})
				So(store.pending, ShouldHaveLength, 1)
				So(store.pending[0].ID, ShouldEqual, "e2")
				So(store.fails["e2"], ShouldEqual, 1)
			})
		})

		Convey("When Run is nudged", func() {
			relay := NewRelay(store, dispatcher, WithInterval(time.Hour), WithBatchSize(10))
			ctx, cancel := context.WithCancel(context.Background())
			go relay.Run(ctx)
			relay.Nudge()

			deadline := time.After(2 * time.Second)
			for {
				deliveredMu.Lock()
				n := len(delivered)
				deliveredMu.Unlock()
				if n == 3 {
					break
				}
				select {
				case <-deadline:
					t.Fatal("relay did not drain after nudge")
				case <-time.After(10 * time.Millisecond):
				}
			}
			cancel()

			Convey("Then the nudge drains without waiting for the interval", func() {
				So(store.pending, ShouldBeEmpty)
			})
		})
	})
}
