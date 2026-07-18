package outbox

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/events"
)

// fakeStore is an in-memory outbox honouring the claim/finalize contract:
// Claim leases pending rows (so a concurrent relay skips them) and Finalize
// records the dispatch outcome, retiring successes and un-leasing failures.
type fakeStore struct {
	mu      sync.Mutex
	pending []events.Envelope
	leased  map[string]bool
	done    []string
	fails   map[string]int
}

func (s *fakeStore) Write(_ context.Context, _ db.Tx, envs []events.Envelope) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending = append(s.pending, envs...)
	return nil
}

func (s *fakeStore) Claim(_ context.Context, _ string, limit int, _ time.Duration) ([]events.Envelope, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.leased == nil {
		s.leased = map[string]bool{}
	}
	claimed := make([]events.Envelope, 0, limit)
	for _, env := range s.pending {
		if len(claimed) == limit {
			break
		}
		if s.leased[env.ID] {
			continue
		}
		s.leased[env.ID] = true
		claimed = append(claimed, env)
	}
	return claimed, nil
}

func (s *fakeStore) Finalize(_ context.Context, results []Result) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fails == nil {
		s.fails = map[string]int{}
	}
	for _, res := range results {
		delete(s.leased, res.EnvelopeID)
		if res.Err == nil {
			s.done = append(s.done, res.EnvelopeID)
			for i, env := range s.pending {
				if env.ID == res.EnvelopeID {
					s.pending = append(s.pending[:i], s.pending[i+1:]...)
					break
				}
			}
		} else {
			s.fails[res.EnvelopeID]++
		}
	}
	return nil
}

// captureStore records exactly how the relay called Claim and Finalize and
// can be made to fail either call, so the relay's lease parameters and error
// paths are observable.
type captureStore struct {
	mu sync.Mutex
	// batches is the sequence of envelope batches Claim will serve.
	batches     [][]events.Envelope
	claims      int
	claimIDs    []string
	claimLimits []int
	claimTTLs   []time.Duration
	claimErr    error
	finalizeErr error
	finalized   [][]Result
}

func (s *captureStore) Write(context.Context, db.Tx, []events.Envelope) error { return nil }

func (s *captureStore) Claim(_ context.Context, relayID string, limit int, leaseTTL time.Duration) ([]events.Envelope, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.claims++
	s.claimIDs = append(s.claimIDs, relayID)
	s.claimLimits = append(s.claimLimits, limit)
	s.claimTTLs = append(s.claimTTLs, leaseTTL)
	if s.claimErr != nil {
		return nil, s.claimErr
	}
	if len(s.batches) == 0 {
		return nil, nil
	}
	batch := s.batches[0]
	s.batches = s.batches[1:]
	return batch, nil
}

func (s *captureStore) Finalize(_ context.Context, results []Result) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.finalized = append(s.finalized, results)
	return s.finalizeErr
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

		Convey("When DrainOnce is called", func() {
			relay := NewRelay(store, dispatcher, WithBatchSize(10))
			relay.DrainOnce(context.Background())

			Convey("Then it drains the outbox exactly like a Run pass", func() {
				So(delivered, ShouldResemble, []string{"e1", "e2", "e3"})
				So(store.pending, ShouldBeEmpty)
			})
		})

		Convey("When the context is already cancelled", func() {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			relay := NewRelay(store, dispatcher, WithBatchSize(10))
			relay.DrainOnce(ctx)

			Convey("Then nothing is claimed or dispatched", func() {
				So(delivered, ShouldBeEmpty)
				So(store.pending, ShouldHaveLength, 3)
			})
		})

		Convey("When a batch expands and an after-expand hook is installed", func() {
			expanded := 0
			relay := NewRelay(store, dispatcher, WithBatchSize(10), WithAfterExpand(func() { expanded++ }))
			relay.DrainOnce(context.Background())

			Convey("Then the hook fires once for the pass that expanded", func() {
				So(expanded, ShouldEqual, 1)
			})
		})

		Convey("When a pass claims nothing", func() {
			empty := &fakeStore{}
			expanded := 0
			relay := NewRelay(empty, dispatcher, WithAfterExpand(func() { expanded++ }))
			relay.DrainOnce(context.Background())

			Convey("Then the after-expand hook does not fire", func() {
				So(expanded, ShouldEqual, 0)
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

func TestRelayOptions(t *testing.T) {
	Convey("Given a store that records the lease parameters of each claim", t, func() {
		store := &captureStore{}
		dispatcher := events.NewDispatcher()

		Convey("When the relay is built with defaults", func() {
			relay := NewRelay(store, dispatcher)
			relay.DrainOnce(context.Background())

			Convey("Then it claims 100 at a time under a one-minute lease with a ULID id", func() {
				So(store.claimLimits, ShouldResemble, []int{100})
				So(store.claimTTLs, ShouldResemble, []time.Duration{time.Minute})
				So(store.claimIDs[0], ShouldNotBeBlank)
				So(store.claimIDs[0], ShouldHaveLength, 26) // ULID
				So(relay.interval, ShouldEqual, 2*time.Second)
			})
		})

		Convey("When the lease TTL, batch size and relay id are overridden", func() {
			relay := NewRelay(store, dispatcher,
				WithLeaseTTL(90*time.Second),
				WithBatchSize(7),
				WithRelayID("relay-under-test"),
				WithInterval(5*time.Second))
			relay.DrainOnce(context.Background())

			Convey("Then exactly those values reach the store's Claim call", func() {
				So(store.claimTTLs, ShouldResemble, []time.Duration{90 * time.Second})
				So(store.claimLimits, ShouldResemble, []int{7})
				So(store.claimIDs, ShouldResemble, []string{"relay-under-test"})
				So(relay.interval, ShouldEqual, 5*time.Second)
			})
		})
	})
}

func TestRelayErrorObserver(t *testing.T) {
	Convey("Given a relay with an error observer", t, func() {
		store := &captureStore{}
		dispatcher := events.NewDispatcher()
		var observed []error
		observer := WithErrorObserver(func(err error) { observed = append(observed, err) })

		Convey("When claiming a batch fails", func() {
			store.claimErr = errors.New("claim failed")
			relay := NewRelay(store, dispatcher, observer, WithRelayID("r1"))
			relay.DrainOnce(context.Background())

			Convey("Then the observer sees the claim error and the pass stops", func() {
				So(observed, ShouldHaveLength, 1)
				So(errors.Is(observed[0], store.claimErr), ShouldBeTrue)
				So(store.claims, ShouldEqual, 1)
				So(store.finalized, ShouldBeEmpty)
			})
		})

		Convey("When finalizing a dispatched batch fails", func() {
			store.batches = [][]events.Envelope{{env("e1")}}
			store.finalizeErr = errors.New("finalize failed")
			relay := NewRelay(store, dispatcher, observer, WithBatchSize(1))
			relay.DrainOnce(context.Background())

			Convey("Then the observer sees the finalize error and no further batch is claimed", func() {
				So(observed, ShouldHaveLength, 1)
				So(errors.Is(observed[0], store.finalizeErr), ShouldBeTrue)
				So(store.claims, ShouldEqual, 1)
				So(store.finalized, ShouldHaveLength, 1)
			})
		})

		Convey("When a handler fails for an envelope", func() {
			handlerErr := errors.New("subscriber down")
			dispatcher.RegisterFunc("flaky", func(context.Context, events.Envelope) error { return handlerErr })
			store.batches = [][]events.Envelope{{env("e1")}}
			relay := NewRelay(store, dispatcher, observer, WithBatchSize(1))
			relay.DrainOnce(context.Background())

			Convey("Then the failure is recorded per envelope, not on the relay observer", func() {
				So(observed, ShouldBeEmpty)
				So(store.finalized, ShouldHaveLength, 1)
				So(store.finalized[0][0].EnvelopeID, ShouldEqual, "e1")
				So(errors.Is(store.finalized[0][0].Err, handlerErr), ShouldBeTrue)
			})
		})

		Convey("When no observer is configured and claiming fails", func() {
			store.claimErr = errors.New("claim failed")
			relay := NewRelay(store, dispatcher)

			Convey("Then the drain returns quietly without panicking", func() {
				So(func() { relay.DrainOnce(context.Background()) }, ShouldNotPanic)
			})
		})
	})
}

func TestRelayBatchBoundaries(t *testing.T) {
	Convey("Given a relay claiming batches of two", t, func() {
		store := &captureStore{}
		dispatcher := events.NewDispatcher()
		var delivered []string
		dispatcher.RegisterFunc("recorder", func(_ context.Context, e events.Envelope) error {
			delivered = append(delivered, e.ID)
			return nil
		})

		Convey("When a full batch is followed by a short one", func() {
			store.batches = [][]events.Envelope{
				{env("e1"), env("e2")},
				{env("e3")},
			}
			relay := NewRelay(store, dispatcher, WithBatchSize(2))
			relay.DrainOnce(context.Background())

			Convey("Then it keeps claiming until the short batch ends the pass", func() {
				So(delivered, ShouldResemble, []string{"e1", "e2", "e3"})
				So(store.claims, ShouldEqual, 2)
				So(store.finalized, ShouldHaveLength, 2)
				So(store.finalized[0], ShouldHaveLength, 2)
				So(store.finalized[1], ShouldHaveLength, 1)
			})
		})

		Convey("When every batch is full until the outbox empties", func() {
			store.batches = [][]events.Envelope{
				{env("e1"), env("e2")},
				{env("e3"), env("e4")},
			}
			relay := NewRelay(store, dispatcher, WithBatchSize(2))
			relay.DrainOnce(context.Background())

			Convey("Then a third, empty claim ends the pass", func() {
				So(delivered, ShouldResemble, []string{"e1", "e2", "e3", "e4"})
				So(store.claims, ShouldEqual, 3)
				So(store.finalized, ShouldHaveLength, 2)
			})
		})
	})
}

func TestRelayNudgeCoalescing(t *testing.T) {
	Convey("Given a relay that has not started", t, func() {
		store := &captureStore{}
		relay := NewRelay(store, events.NewDispatcher())

		Convey("When Nudge is called more times than the buffer holds", func() {
			relay.Nudge()
			relay.Nudge()
			relay.Nudge()

			Convey("Then the extra nudges are dropped rather than blocking", func() {
				So(len(relay.nudge), ShouldEqual, 1)
			})
		})
	})
}
