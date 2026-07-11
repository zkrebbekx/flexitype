package dataloader

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
)

func TestLoader(t *testing.T) {
	Convey("Given a loader over a counting batch function", t, func() {
		var calls atomic.Int64
		var lastBatch []int
		var mu sync.Mutex

		fetch := func(_ context.Context, keys []int) (map[int]string, error) {
			calls.Add(1)
			mu.Lock()
			lastBatch = append([]int(nil), keys...)
			mu.Unlock()
			out := make(map[int]string, len(keys))
			for _, k := range keys {
				if k > 0 {
					out[k] = fmt.Sprintf("v%d", k)
				}
			}
			return out, nil
		}
		loader := NewLoader(fetch, Config{Wait: 5 * time.Millisecond, MaxBatch: 100})
		ctx := context.Background()

		Convey("When many concurrent loads arrive inside one batch window", func() {
			var wg sync.WaitGroup
			results := make([]string, 10)
			errs := make([]error, 10)
			for i := 0; i < 10; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					// 5 distinct keys, each requested twice.
					results[i], errs[i] = loader.Load(ctx, i%5+1)
				}(i)
			}
			wg.Wait()

			Convey("Then every caller gets its value from a single batched query", func() {
				for i := range results {
					So(errs[i], ShouldBeNil)
					So(results[i], ShouldStartWith, "v")
				}
				So(calls.Load(), ShouldEqual, 1)
				mu.Lock()
				So(len(lastBatch), ShouldEqual, 5)
				mu.Unlock()
			})
		})

		Convey("When the same key is loaded twice sequentially", func() {
			first, err1 := loader.Load(ctx, 7)
			second, err2 := loader.Load(ctx, 7)

			Convey("Then the second load is served from cache", func() {
				So(err1, ShouldBeNil)
				So(err2, ShouldBeNil)
				So(first, ShouldEqual, "v7")
				So(second, ShouldEqual, "v7")
				So(calls.Load(), ShouldEqual, 1)
			})
		})

		Convey("When a key is missing from the batch result", func() {
			_, err := loader.Load(ctx, -1)

			Convey("Then the miss policy returns an error", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "not found")
			})
		})

		Convey("When a key is primed", func() {
			loader.Prime(42, "primed")
			v, err := loader.Load(ctx, 42)

			Convey("Then it is served without hitting the batch function", func() {
				So(err, ShouldBeNil)
				So(v, ShouldEqual, "primed")
				So(calls.Load(), ShouldEqual, 0)
			})
		})

		Convey("When a cached key is cleared", func() {
			_, _ = loader.Load(ctx, 3)
			So(calls.Load(), ShouldEqual, 1)
			loader.Clear(3)
			_, _ = loader.Load(ctx, 3)

			Convey("Then the next load re-fetches", func() {
				So(calls.Load(), ShouldEqual, 2)
			})
		})
	})

	Convey("Given a zero-miss loader", t, func() {
		fetch := func(_ context.Context, _ []string) (map[string]int, error) {
			return map[string]int{}, nil
		}
		loader := NewZeroLoader(fetch, Config{})

		Convey("When a missing key is loaded", func() {
			v, err := loader.Load(context.Background(), "absent")

			Convey("Then it resolves to the zero value without error", func() {
				So(err, ShouldBeNil)
				So(v, ShouldEqual, 0)
			})
		})
	})

	Convey("Given a loader whose batch function fails", t, func() {
		fetch := func(_ context.Context, _ []int) (map[int]string, error) {
			return nil, fmt.Errorf("boom")
		}
		loader := NewLoader(fetch, Config{})

		Convey("When a load runs", func() {
			_, err := loader.Load(context.Background(), 1)

			Convey("Then the batch error propagates", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "boom")
			})
		})
	})

	Convey("Given LoadMany over mixed keys", t, func() {
		fetch := func(_ context.Context, keys []int) (map[int]string, error) {
			out := make(map[int]string)
			for _, k := range keys {
				out[k] = fmt.Sprintf("v%d", k)
			}
			return out, nil
		}
		loader := NewLoader(fetch, Config{})

		Convey("When loading several keys", func() {
			values, err := loader.LoadMany(context.Background(), []int{3, 1, 2})

			Convey("Then values come back in key order", func() {
				So(err, ShouldBeNil)
				So(values, ShouldResemble, []string{"v3", "v1", "v2"})
			})
		})
	})
}
