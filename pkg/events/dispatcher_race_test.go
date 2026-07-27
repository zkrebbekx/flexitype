package events_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/pkg/events"
)

// TestRegisterDuringDispatch covers registering a handler while the dispatcher
// is delivering.
//
// The facade hands the dispatcher out through Service.Dispatcher, and its
// godoc invited registering hooks after construction. Register appended to the
// registration slice while deliverAll ranged over it, with no synchronisation,
// so following that advice on a serving process was a data race: under -race
// it reports a write at the slice header against a concurrent read, and
// without -race the append can reallocate under the reader.
//
// Run this file with -race for it to mean anything.
func TestRegisterDuringDispatch(t *testing.T) {
	Convey("Given a dispatcher already delivering events", t, func() {
		d := events.NewDispatcher()
		var delivered atomic.Int64
		d.RegisterFunc("counter", func(_ context.Context, _ events.Envelope) error {
			delivered.Add(1)
			return nil
		})

		Convey("When handlers are registered while dispatches run", func() {
			ctx := context.Background()
			var wg sync.WaitGroup
			start := make(chan struct{})

			for i := 0; i < 8; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					for j := 0; j < 50; j++ {
						_ = d.Dispatch(ctx, events.Metadata{}, testEvent{})
					}
				}()
			}
			for i := 0; i < 8; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					for j := 0; j < 50; j++ {
						d.RegisterFunc("late", func(_ context.Context, _ events.Envelope) error {
							return nil
						})
					}
				}()
			}
			close(start)
			wg.Wait()

			Convey("Then every dispatch reached the original handler", func() {
				// 8 goroutines x 50 dispatches, one event each. A lost or
				// duplicated registration would move this count.
				So(delivered.Load(), ShouldEqual, 400)
			})
		})
	})
}

// testEvent is the smallest thing Dispatch will envelope.
type testEvent struct{}

func (testEvent) EventType() events.Type  { return events.Type("test.happened") }
func (testEvent) AggregateType() string   { return "test" }
func (testEvent) AggregateID() string     { return "1" }
func (testEvent) OccurredWhen() time.Time { return time.Unix(0, 0).UTC() }
