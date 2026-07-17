package gql

import (
	"fmt"
	"testing"

	"github.com/graphql-go/graphql"
	. "github.com/smartystreets/goconvey/convey"
)

// TestEngineSchemaCacheLRUBound proves the tenant|access schema cache is
// LRU-bounded so it cannot grow without limit (issue #192), evicting the
// least-recently-used entry once the cap is exceeded.
func TestEngineSchemaCacheLRUBound(t *testing.T) {
	Convey("Given an engine with a small schema-cache cap", t, func() {
		e := NewEngine(WithCacheCap(3))

		put := func(key string) {
			e.mu.Lock()
			e.cachePut(key, 1, graphql.Schema{})
			e.mu.Unlock()
		}
		has := func(key string) bool {
			e.mu.Lock()
			defer e.mu.Unlock()
			_, ok := e.cache[key]
			return ok
		}

		Convey("When more distinct keys are inserted than the cap", func() {
			put("a")
			put("b")
			put("c")
			put("d") // overflows: evicts the least-recently-used ("a")

			Convey("Then the cache never exceeds the cap and evicts LRU-first", func() {
				So(len(e.cache), ShouldEqual, 3)
				So(e.ll.Len(), ShouldEqual, 3)
				So(has("a"), ShouldBeFalse)
				So(has("b"), ShouldBeTrue)
				So(has("c"), ShouldBeTrue)
				So(has("d"), ShouldBeTrue)
			})
		})

		Convey("When an existing key is touched before overflowing", func() {
			put("a")
			put("b")
			put("c")
			// A read marks "a" most-recently-used, so "b" becomes the true LRU.
			e.mu.Lock()
			_, _ = e.cacheGet("a")
			e.mu.Unlock()
			put("d") // evicts "b", not "a"

			Convey("Then the recently-touched key survives and the true LRU is evicted", func() {
				So(len(e.cache), ShouldEqual, 3)
				So(has("b"), ShouldBeFalse)
				So(has("a"), ShouldBeTrue)
				So(has("c"), ShouldBeTrue)
				So(has("d"), ShouldBeTrue)
			})
		})

		Convey("When re-inserting an existing key at a new version", func() {
			put("a")
			put("b")
			put("c")
			e.mu.Lock()
			e.cachePut("a", 2, graphql.Schema{}) // refresh, not a new entry
			e.mu.Unlock()

			Convey("Then it refreshes in place without growing the cache", func() {
				So(len(e.cache), ShouldEqual, 3)
				So(e.ll.Len(), ShouldEqual, 3)
				e.mu.Lock()
				ent, ok := e.cacheGet("a")
				e.mu.Unlock()
				So(ok, ShouldBeTrue)
				So(ent.gen, ShouldEqual, 2)
			})
		})

		Convey("When far more keys than the cap are inserted", func() {
			for i := 0; i < 100; i++ {
				put(fmt.Sprintf("k%d", i))
			}

			Convey("Then the cache and its LRU list stay bounded at the cap", func() {
				So(len(e.cache), ShouldEqual, 3)
				So(e.ll.Len(), ShouldEqual, 3)
			})
		})
	})
}
