package webhook

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/internal/safedial"
)

// countingDeliveryStore records how often the worker polled it and can be
// told to fail either the lease-release or the claim step.
type countingDeliveryStore struct {
	fakeDeliveryStore
	mu         sync.Mutex
	passes     chan struct{}
	releaseErr error
	claimErr   error
}

func (s *countingDeliveryStore) ReleaseExpired(ctx context.Context, now time.Time) (int, error) {
	s.mu.Lock()
	err := s.releaseErr
	s.mu.Unlock()
	if err != nil {
		return 0, err
	}
	n, releaseErr := s.fakeDeliveryStore.ReleaseExpired(ctx, now)
	if s.passes != nil {
		select {
		case s.passes <- struct{}{}:
		default:
		}
	}
	return n, releaseErr
}

func (s *countingDeliveryStore) ClaimDue(ctx context.Context, limit int, lease time.Duration, now time.Time) ([]ClaimedDelivery, error) {
	s.mu.Lock()
	err := s.claimErr
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return s.fakeDeliveryStore.ClaimDue(ctx, limit, lease, now)
}

func TestWorkerOptions(t *testing.T) {
	Convey("Given a worker built with explicit options", t, func() {
		var observed []error
		w := NewWorker(&fakeDeliveryStore{},
			WithWorkerInterval(5*time.Millisecond),
			WithWorkerConcurrency(2),
			WithMaxAttempts(7),
			WithWorkerErrorObserver(func(err error) { observed = append(observed, err) }),
			WithHTTPClient(safedial.NewClient(safedial.Options{AllowPrivate: true})),
		)

		Convey("Then each option overrides the corresponding default", func() {
			So(w.interval, ShouldEqual, 5*time.Millisecond)
			So(w.concurrency, ShouldEqual, 2)
			So(w.maxAttempts, ShouldEqual, 7)
			So(w.onError, ShouldNotBeNil)
			So(observed, ShouldBeEmpty)
		})
	})

	Convey("Given a worker with no error observer", t, func() {
		store := &countingDeliveryStore{releaseErr: errors.New("db down")}
		w := NewWorker(store)

		Convey("When a pass fails", func() {
			w.pass(context.Background())

			Convey("Then reporting the failure is a no-op rather than a panic", func() {
				So(store.outcomes, ShouldBeEmpty)
			})
		})
	})
}

func TestWorkerPassErrorPaths(t *testing.T) {
	Convey("Given a worker whose store is failing", t, func() {
		var mu sync.Mutex
		var observed []error
		observe := func(err error) {
			mu.Lock()
			defer mu.Unlock()
			observed = append(observed, err)
		}
		ctx := context.Background()

		Convey("When releasing expired leases fails", func() {
			store := &countingDeliveryStore{releaseErr: errors.New("lease table locked")}
			w := NewWorker(store, WithWorkerErrorObserver(observe))
			w.pass(ctx)

			Convey("Then the pass aborts before claiming anything", func() {
				So(observed, ShouldHaveLength, 1)
				So(observed[0].Error(), ShouldContainSubstring, "release expired leases")
				So(observed[0].Error(), ShouldContainSubstring, "lease table locked")
				So(store.outcomes, ShouldBeEmpty)
			})
		})

		Convey("When claiming due deliveries fails", func() {
			store := &countingDeliveryStore{claimErr: errors.New("claim conflict")}
			w := NewWorker(store, WithWorkerErrorObserver(observe))
			w.pass(ctx)

			Convey("Then the lease sweep still ran and the claim error is reported", func() {
				So(store.released, ShouldEqual, 1)
				So(observed, ShouldHaveLength, 1)
				So(observed[0].Error(), ShouldContainSubstring, "claim deliveries")
				So(store.outcomes, ShouldBeEmpty)
			})
		})

		Convey("When the context is already cancelled", func() {
			store := &countingDeliveryStore{}
			cancelled, cancel := context.WithCancel(ctx)
			cancel()
			w := NewWorker(store, WithWorkerErrorObserver(observe))
			w.pass(cancelled)

			Convey("Then no delivery is claimed and nothing is reported", func() {
				So(store.outcomes, ShouldBeEmpty)
				So(observed, ShouldBeEmpty)
			})
		})
	})
}

func TestWorkerRun(t *testing.T) {
	Convey("Given a running worker on a short poll interval", t, func() {
		store := &countingDeliveryStore{passes: make(chan struct{}, 8)}
		w := NewWorker(store, WithWorkerInterval(time.Millisecond))
		ctx, cancel := context.WithCancel(context.Background())

		done := make(chan struct{})
		go func() {
			w.Run(ctx)
			close(done)
		}()

		Convey("When it is nudged", func() {
			// Drain the pass Run makes on entry, then nudge for another.
			select {
			case <-store.passes:
			case <-time.After(2 * time.Second):
				t.Fatal("worker never made its first pass")
			}
			w.Nudge()

			var second bool
			select {
			case <-store.passes:
				second = true
			case <-time.After(2 * time.Second):
			}

			Convey("Then it makes another pass and stops when the context ends", func() {
				So(second, ShouldBeTrue)

				cancel()
				select {
				case <-done:
				case <-time.After(2 * time.Second):
					t.Fatal("worker did not stop on context cancellation")
				}
			})
		})

		Reset(func() {
			cancel()
			<-done
		})
	})

	Convey("Given an idle worker", t, func() {
		w := NewWorker(&fakeDeliveryStore{})

		Convey("When it is nudged more times than the wake channel can hold", func() {
			w.Nudge()
			w.Nudge()
			w.Nudge()

			Convey("Then the extra nudges are dropped rather than blocking the caller", func() {
				So(len(w.nudge), ShouldEqual, 1)
			})
		})
	})
}
