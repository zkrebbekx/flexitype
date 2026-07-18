package http

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/application"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/infrastructure/memory"
	"github.com/zkrebbekx/flexitype/pkg/health"
	"github.com/zkrebbekx/flexitype/pkg/logger"
)

// TestWriteErrorStatusMapping pins the single place domain error codes become
// HTTP statuses. Every client switch depends on this table, so each arm is
// asserted rather than inferred from whichever routes happen to produce it.
func TestWriteErrorStatusMapping(t *testing.T) {
	Convey("Given the domain-error-to-status mapping", t, func() {
		cases := []struct {
			name   string
			err    error
			status int
			code   string
		}{
			{"a validation error", domainerrors.NewValidation("bad input"), http.StatusUnprocessableEntity, "VALIDATION"},
			{"a not-found error", domainerrors.NewNotFound("product", "abc"), http.StatusNotFound, "NOT_FOUND"},
			{"a conflict", domainerrors.NewConflict("already exists"), http.StatusConflict, "CONFLICT"},
			{"an archived aggregate", domainerrors.NewArchived("product", "abc"), http.StatusGone, "ARCHIVED"},
			{"a forbidden action", domainerrors.NewForbidden("nope"), http.StatusForbidden, "FORBIDDEN"},
			{"a dependency violation", domainerrors.NewDependencyViolation("needs a sku"), http.StatusUnprocessableEntity, "DEPENDENCY_VIOLATION"},
		}

		for _, tc := range cases {
			Convey("When writeError is given "+tc.name, func() {
				rec := httptest.NewRecorder()
				writeError(rec, logger.New(logger.Config{Level: "error"}), tc.err)

				Convey("Then it maps to the documented status and machine code", func() {
					So(rec.Code, ShouldEqual, tc.status)
					So(rec.Header().Get("Content-Type"), ShouldEqual, "application/json")
					So(rec.Body.String(), ShouldContainSubstring, `"code":"`+tc.code+`"`)
				})
			})
		}

		Convey("When writeError is given a wrapped domain error", func() {
			rec := httptest.NewRecorder()
			wrapped := errWrap{inner: domainerrors.NewNotFound("product", "abc")}
			writeError(rec, logger.New(logger.Config{Level: "error"}), wrapped)

			Convey("Then errors.As still finds it — wrapping does not degrade to a 500", func() {
				So(rec.Code, ShouldEqual, http.StatusNotFound)
				So(rec.Body.String(), ShouldContainSubstring, `"code":"NOT_FOUND"`)
			})
		})

		Convey("When writeError is given a plain infrastructure error", func() {
			rec := httptest.NewRecorder()
			writeError(rec, logger.New(logger.Config{Level: "error"}), errors.New("connection reset by peer"))

			Convey("Then it is a generic 500 that leaks nothing about the internals", func() {
				So(rec.Code, ShouldEqual, http.StatusInternalServerError)
				So(rec.Body.String(), ShouldContainSubstring, `"code":"INTERNAL"`)
				So(rec.Body.String(), ShouldContainSubstring, `"message":"internal error"`)
				So(rec.Body.String(), ShouldNotContainSubstring, "connection reset")
			})
		})

		Convey("When writeError has no logger (an embedder passed none)", func() {
			rec := httptest.NewRecorder()
			writeError(rec, nil, errors.New("boom"))

			Convey("Then it still answers 500 rather than panicking", func() {
				So(rec.Code, ShouldEqual, http.StatusInternalServerError)
			})
		})
	})
}

// errWrap wraps an error the way an interactor's fmt.Errorf("...: %w") does.
type errWrap struct{ inner error }

func (e errWrap) Error() string { return "context: " + e.inner.Error() }
func (e errWrap) Unwrap() error { return e.inner }

// TestWriteItemsNeverNull guards the list-response invariant: a nil slice must
// still marshal to [], because clients read `.items` as an array (this was a
// real saved-views regression).
func TestWriteItemsNeverNull(t *testing.T) {
	Convey("Given a nil slice of items", t, func() {
		rec := httptest.NewRecorder()
		writeItems[string](rec, nil)

		Convey("Then the response body carries an empty array, not null", func() {
			So(rec.Code, ShouldEqual, http.StatusOK)
			So(rec.Body.String(), ShouldContainSubstring, `"items":[]`)
			So(rec.Body.String(), ShouldNotContainSubstring, "null")
		})
	})

	Convey("Given a populated slice of items", t, func() {
		rec := httptest.NewRecorder()
		writeItems(rec, []string{"a", "b"})

		Convey("Then the items are written in order", func() {
			So(rec.Body.String(), ShouldContainSubstring, `"items":["a","b"]`)
		})
	})
}

// TestRecovererTurnsPanicsInto500 covers the outermost middleware: a handler
// that panics must produce a JSON 500 rather than dropping the connection, so
// a client sees an error instead of a transport failure.
func TestRecovererTurnsPanicsInto500(t *testing.T) {
	Convey("Given a handler that panics", t, func() {
		h := recoverer(logger.New(logger.Config{Level: "error"}))(
			http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				panic("handler exploded")
			}))

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/boom", nil))

		Convey("Then the client gets a generic JSON 500, not a dropped connection", func() {
			So(rec.Code, ShouldEqual, http.StatusInternalServerError)
			So(rec.Body.String(), ShouldContainSubstring, `"code":"INTERNAL"`)
		})

		Convey("Then the panic value is not echoed back to the caller", func() {
			So(rec.Body.String(), ShouldNotContainSubstring, "handler exploded")
		})
	})

	Convey("Given a handler that does not panic", t, func() {
		h := recoverer(logger.New(logger.Config{Level: "error"}))(
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusTeapot)
			}))

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

		Convey("Then the response passes through untouched", func() {
			So(rec.Code, ShouldEqual, http.StatusTeapot)
		})
	})
}

// TestSavedViewsDisabled covers the 501 branch a deployment gives when the
// saved-view store is not wired: the feature is absent, not broken.
func TestSavedViewsDisabled(t *testing.T) {
	Convey("Given an API built without a saved-view store", t, func() {
		store := memory.NewStore()
		factory := application.NewFactory(application.FactoryConfig{
			Transactor:      store.Transactor(),
			NewRepositories: func() application.Repositories { return store.Repositories() },
			ActivityLog:     store.ActivityLog(),
			// SavedViews deliberately left nil.
		})
		srv := httptest.NewServer(buildRouter(ServerConfig{
			Factory: factory,
			Logger:  logger.New(logger.Config{Level: "error"}),
			Health:  health.NewService("flexitype", "test"),
		}))
		t.Cleanup(srv.Close)
		h := &deliveryHarness{t: t, srv: srv}

		Convey("When any saved-view route is called", func() {
			Convey("Then every one is 501 FEATURE_DISABLED", func() {
				for _, call := range []rawResponse{
					h.get("/api/v1/saved-views"),
					h.post("/api/v1/saved-views", map[string]any{"name": "v", "root_type": "product"}),
					h.get("/api/v1/saved-views/" + absentULID),
					h.patch("/api/v1/saved-views/"+absentULID, map[string]any{"name": "v"}),
					h.delete("/api/v1/saved-views/" + absentULID),
				} {
					So(call.Status, ShouldEqual, http.StatusNotImplemented)
					So(call.errorCode(), ShouldEqual, "FEATURE_DISABLED")
					So(string(call.Body), ShouldContainSubstring, "saved views")
				}
			})
		})
	})
}
