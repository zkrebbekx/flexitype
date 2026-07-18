package health

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
)

// decode reads the handler's JSON body back into the wire shape the probes
// actually publish, so assertions are on the contract rather than on a
// substring of the rendered text.
func decode(t *testing.T, w *httptest.ResponseRecorder) response {
	t.Helper()
	var got response
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode health body %q: %v", w.Body.String(), err)
	}
	return got
}

func serve(h http.HandlerFunc) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h(w, httptest.NewRequest(http.MethodGet, "/probe", nil))
	return w
}

func TestLiveHandler(t *testing.T) {
	Convey("Given a health service with a failing dependency check", t, func() {
		svc := NewService("flexitype", "1.0.0")
		svc.RegisterCheckFunc("database", func(context.Context) error {
			return fmt.Errorf("connection refused")
		})

		Convey("When liveness is probed", func() {
			w := serve(svc.LiveHandler())

			Convey("Then it reports the process up regardless of dependencies", func() {
				So(w.Code, ShouldEqual, http.StatusOK)
				So(w.Header().Get("Content-Type"), ShouldEqual, "application/json")

				body := decode(t, w)
				So(body.Service, ShouldEqual, "flexitype")
				So(body.Version, ShouldEqual, "1.0.0")
				So(body.Status, ShouldEqual, StatusOK)
				So(body.Checks, ShouldBeEmpty)
			})
		})
	})
}

func TestReadyHandler(t *testing.T) {
	Convey("Given a health service", t, func() {
		svc := NewService("flexitype", "1.0.0")

		Convey("When no checks are registered", func() {
			w := serve(svc.ReadyHandler())

			Convey("Then readiness is OK with no checks in the body", func() {
				So(w.Code, ShouldEqual, http.StatusOK)
				So(w.Header().Get("Content-Type"), ShouldEqual, "application/json")

				body := decode(t, w)
				So(body.Status, ShouldEqual, StatusOK)
				So(body.Checks, ShouldBeEmpty)
			})
		})

		Convey("When every registered check passes", func() {
			svc.RegisterCheckFunc("database", func(context.Context) error { return nil })
			svc.RegisterCheckFunc("blobstore", func(context.Context) error { return nil })

			w := serve(svc.ReadyHandler())

			Convey("Then it is 200 and each check is reported ok with no error", func() {
				So(w.Code, ShouldEqual, http.StatusOK)

				body := decode(t, w)
				So(body.Service, ShouldEqual, "flexitype")
				So(body.Version, ShouldEqual, "1.0.0")
				So(body.Status, ShouldEqual, StatusOK)
				So(body.Checks, ShouldHaveLength, 2)

				names := make([]string, 0, len(body.Checks))
				for _, c := range body.Checks {
					names = append(names, c.Name)
					So(c.Status, ShouldEqual, StatusOK)
					So(c.Error, ShouldEqual, "")
					So(c.LastChecked.IsZero(), ShouldBeFalse)
				}
				sort.Strings(names)
				So(names, ShouldResemble, []string{"blobstore", "database"})
			})
		})

		Convey("When one of several checks fails", func() {
			svc.RegisterCheckFunc("database", func(context.Context) error { return nil })
			svc.RegisterCheckFunc("blobstore", func(context.Context) error {
				return fmt.Errorf("bucket unreachable")
			})

			w := serve(svc.ReadyHandler())

			Convey("Then the whole service is 503 and only that check is down", func() {
				So(w.Code, ShouldEqual, http.StatusServiceUnavailable)

				body := decode(t, w)
				So(body.Status, ShouldEqual, StatusDown)
				So(body.Checks, ShouldHaveLength, 2)

				byName := map[string]Check{}
				for _, c := range body.Checks {
					byName[c.Name] = c
				}
				So(byName["database"].Status, ShouldEqual, StatusOK)
				So(byName["database"].Error, ShouldEqual, "")
				So(byName["blobstore"].Status, ShouldEqual, StatusDown)
				So(byName["blobstore"].Error, ShouldEqual, "bucket unreachable")
			})
		})

		Convey("When a check is re-registered under the same name", func() {
			svc.RegisterCheckFunc("database", func(context.Context) error {
				return fmt.Errorf("stale check")
			})
			svc.RegisterCheckFunc("database", func(context.Context) error { return nil })

			w := serve(svc.ReadyHandler())

			Convey("Then the latest registration wins and it reports ready", func() {
				So(w.Code, ShouldEqual, http.StatusOK)

				body := decode(t, w)
				So(body.Checks, ShouldHaveLength, 1)
				So(body.Checks[0].Status, ShouldEqual, StatusOK)
			})
		})

		Convey("When a check inspects the context it is handed", func() {
			var hasDeadline bool
			var cancelled bool
			svc.RegisterCheckFunc("ctx", func(ctx context.Context) error {
				_, hasDeadline = ctx.Deadline()
				cancelled = ctx.Err() != nil
				return nil
			})

			serve(svc.ReadyHandler())

			Convey("Then the check runs under a live, deadline-bounded context", func() {
				So(hasDeadline, ShouldBeTrue)
				So(cancelled, ShouldBeFalse)
			})
		})

		Convey("When a check takes measurable time", func() {
			svc.RegisterCheckFunc("slow", func(context.Context) error {
				time.Sleep(2 * time.Millisecond)
				return nil
			})

			w := serve(svc.ReadyHandler())

			Convey("Then its duration is recorded in the response", func() {
				body := decode(t, w)
				So(body.Checks, ShouldHaveLength, 1)
				So(body.Checks[0].Duration, ShouldBeGreaterThan, time.Duration(0))
			})
		})
	})
}
