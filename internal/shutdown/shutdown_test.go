package shutdown

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/pkg/logger"
)

// recorder collects the order tasks actually ran in.
type recorder struct {
	mu    sync.Mutex
	order []string
}

func (r *recorder) task(name string, priority int) Task {
	return Task{Name: name, Priority: priority, Handler: func(context.Context) error {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.order = append(r.order, name)
		return nil
	}}
}

func (r *recorder) seen() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.order))
	copy(out, r.order)
	return out
}

// waitFor runs h.Wait in a goroutine and blocks until it returns, failing the
// test if teardown hangs.
func waitFor(ctx context.Context, t *testing.T, h *Handler) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.Wait(ctx)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Wait did not return")
	}
}

func TestNew(t *testing.T) {
	Convey("Given a shutdown handler configuration", t, func() {
		Convey("When the timeout is left unset", func() {
			h := New(Config{})

			Convey("Then it falls back to 30 seconds", func() {
				So(h, ShouldNotBeNil)
				So(h.cfg.Timeout, ShouldEqual, 30*time.Second)
			})
		})

		Convey("When the timeout is negative", func() {
			h := New(Config{Timeout: -5 * time.Second})

			Convey("Then it also falls back to 30 seconds", func() {
				So(h.cfg.Timeout, ShouldEqual, 30*time.Second)
			})
		})

		Convey("When an explicit timeout is given", func() {
			h := New(Config{Timeout: 2 * time.Second})

			Convey("Then it is kept as configured, with no tasks registered yet", func() {
				So(h.cfg.Timeout, ShouldEqual, 2*time.Second)
				So(h.tasks, ShouldBeEmpty)
			})
		})
	})
}

func TestWaitRunsTasksByPriority(t *testing.T) {
	Convey("Given a handler with teardown tasks registered out of priority order", t, func() {
		h := New(Config{Timeout: 2 * time.Second})
		rec := &recorder{}

		h.RegisterTask(rec.task("close-database", 10))
		h.RegisterTask(rec.task("drain-servers", 90))
		h.RegisterTask(rec.task("flush-telemetry", 50))

		So(h.tasks, ShouldHaveLength, 3)

		Convey("When the context is cancelled instead of a signal arriving", func() {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			waitFor(ctx, t, h)

			Convey("Then every task ran highest priority first", func() {
				So(rec.seen(), ShouldResemble, []string{"drain-servers", "flush-telemetry", "close-database"})
			})
		})
	})

	Convey("Given tasks that share a priority", t, func() {
		h := New(Config{Timeout: 2 * time.Second})
		rec := &recorder{}
		h.RegisterTask(rec.task("first", 50))
		h.RegisterTask(rec.task("second", 50))
		h.RegisterTask(rec.task("third", 50))

		Convey("When teardown runs", func() {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			waitFor(ctx, t, h)

			Convey("Then registration order is preserved (stable sort)", func() {
				So(rec.seen(), ShouldResemble, []string{"first", "second", "third"})
			})
		})
	})

	Convey("Given a handler with no tasks", t, func() {
		h := New(Config{Timeout: time.Second})

		Convey("When teardown runs", func() {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()

			Convey("Then Wait returns promptly without error", func() {
				waitFor(ctx, t, h)
			})
		})
	})
}

func TestWaitContinuesPastFailingTasks(t *testing.T) {
	Convey("Given a handler whose middle task fails, with a logger attached", t, func() {
		h := New(Config{
			Timeout: 2 * time.Second,
			Logger:  logger.New(logger.Config{Level: "error", Format: "json"}),
		})
		rec := &recorder{}

		h.RegisterTask(rec.task("drain-servers", 90))
		h.RegisterTask(Task{Name: "flush-telemetry", Priority: 50, Handler: func(context.Context) error {
			rec.mu.Lock()
			rec.order = append(rec.order, "flush-telemetry")
			rec.mu.Unlock()
			return fmt.Errorf("exporter unreachable")
		}})
		h.RegisterTask(rec.task("close-database", 10))

		Convey("When teardown runs", func() {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			waitFor(ctx, t, h)

			Convey("Then the failure is logged and the remaining tasks still run", func() {
				So(rec.seen(), ShouldResemble, []string{"drain-servers", "flush-telemetry", "close-database"})
			})
		})
	})
}

func TestWaitBoundsTasksByTimeout(t *testing.T) {
	Convey("Given a handler with a short shutdown timeout", t, func() {
		h := New(Config{Timeout: 40 * time.Millisecond})

		var deadlineWithin time.Duration
		var deadlineSet bool
		var slowErr error
		var ranAfterSlow bool

		h.RegisterTask(Task{Name: "slow-drain", Priority: 90, Handler: func(ctx context.Context) error {
			deadline, ok := ctx.Deadline()
			deadlineSet = ok
			deadlineWithin = time.Until(deadline)
			// Outlive the budget; the task observes cancellation itself.
			select {
			case <-ctx.Done():
				slowErr = ctx.Err()
			case <-time.After(5 * time.Second):
			}
			return slowErr
		}})
		h.RegisterTask(Task{Name: "after", Priority: 10, Handler: func(ctx context.Context) error {
			ranAfterSlow = true
			return ctx.Err()
		}})

		Convey("When teardown runs", func() {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()

			start := time.Now()
			waitFor(ctx, t, h)
			elapsed := time.Since(start)

			Convey("Then tasks get a deadline-bounded context and teardown is not open-ended", func() {
				So(deadlineSet, ShouldBeTrue)
				So(deadlineWithin, ShouldBeLessThanOrEqualTo, 40*time.Millisecond)
				So(slowErr, ShouldEqual, context.DeadlineExceeded)
				So(elapsed, ShouldBeLessThan, 2*time.Second)
			})

			Convey("Then later tasks still run, under the already-expired context", func() {
				So(ranAfterSlow, ShouldBeTrue)
			})
		})
	})
}

func TestWaitOnSignal(t *testing.T) {
	Convey("Given a handler waiting on SIGTERM", t, func() {
		// Keep a notification registered for the whole test so the signal can
		// never fall through to the default disposition and kill the process.
		guard := make(chan os.Signal, 1)
		signal.Notify(guard, syscall.SIGTERM)
		defer signal.Stop(guard)

		h := New(Config{
			Timeout: 2 * time.Second,
			Logger:  logger.New(logger.Config{Level: "error", Format: "json"}),
		})
		rec := &recorder{}
		h.RegisterTask(rec.task("drain-servers", 90))
		h.RegisterTask(rec.task("close-database", 10))

		Convey("When SIGTERM is delivered to the process", func() {
			done := make(chan struct{})
			go func() {
				defer close(done)
				h.Wait(context.Background())
			}()

			// Wait registers its own Notify before blocking; re-send until it
			// has, so the test never races ahead of the registration.
			deadline := time.After(5 * time.Second)
			proc, err := os.FindProcess(os.Getpid())
			So(err, ShouldBeNil)

		send:
			for {
				So(proc.Signal(syscall.SIGTERM), ShouldBeNil)
				select {
				case <-done:
					break send
				case <-deadline:
					t.Fatal("Wait did not return after SIGTERM")
				case <-time.After(20 * time.Millisecond):
				}
			}

			Convey("Then Wait unblocks and runs teardown in priority order", func() {
				So(rec.seen(), ShouldResemble, []string{"drain-servers", "close-database"})
			})
		})
	})
}

func TestRegisterTaskIsConcurrencySafe(t *testing.T) {
	Convey("Given tasks registered from many goroutines at once", t, func() {
		h := New(Config{Timeout: 2 * time.Second})

		var wg sync.WaitGroup
		for i := 0; i < 50; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				h.RegisterTask(Task{
					Name:     fmt.Sprintf("task-%d", i),
					Priority: i,
					Handler:  func(context.Context) error { return nil },
				})
			}(i)
		}
		wg.Wait()

		Convey("When teardown runs", func() {
			var ran int
			var mu sync.Mutex
			h.mu.Lock()
			for i := range h.tasks {
				h.tasks[i].Handler = func(context.Context) error {
					mu.Lock()
					ran++
					mu.Unlock()
					return nil
				}
			}
			h.mu.Unlock()

			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			waitFor(ctx, t, h)

			Convey("Then every registration survived and ran exactly once", func() {
				So(h.tasks, ShouldHaveLength, 50)
				So(ran, ShouldEqual, 50)
			})
		})
	})
}
