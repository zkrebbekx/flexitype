package main

import (
	"sync"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

// TestSeenSetConcurrent covers the idempotency map in the reference consumer.
//
// The map was read and written straight from the HTTP handler, which net/http
// runs on a goroutine per request. Two deliveries arriving together raced on
// it: under -race that is a reported write/write conflict, and in production a
// concurrent map write panics the process — so the reference an adopter copies
// crashed exactly when the sender retried under load. The check and the mark
// were also two steps, so two redeliveries of one event could both be treated
// as new and processed twice.
func TestSeenSetConcurrent(t *testing.T) {
	Convey("Given the consumer's seen-event set", t, func() {
		s := &seenSet{ids: map[string]bool{}}

		Convey("When one event id is marked from many goroutines at once", func() {
			var wg sync.WaitGroup
			var mu sync.Mutex
			newCount := 0
			for i := 0; i < 64; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					if s.markNew("01JEVENT") {
						mu.Lock()
						newCount++
						mu.Unlock()
					}
				}()
			}
			wg.Wait()

			Convey("Then exactly one caller sees it as new", func() {
				So(newCount, ShouldEqual, 1)
			})
		})

		Convey("When different event ids are marked concurrently", func() {
			var wg sync.WaitGroup
			ids := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
			for _, id := range ids {
				wg.Add(1)
				go func(id string) {
					defer wg.Done()
					s.markNew(id)
				}(id)
			}
			wg.Wait()

			Convey("Then every one is recorded, and none is new a second time", func() {
				for _, id := range ids {
					So(s.markNew(id), ShouldBeFalse)
				}
			})
		})
	})
}
