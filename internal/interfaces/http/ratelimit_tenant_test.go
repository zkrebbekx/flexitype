package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/application/uow"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/metrics"
	"github.com/zkrebbekx/flexitype/pkg/ratelimit"
)

// TestTenantRateLimit covers the ceiling the per-account limiter cannot
// provide.
//
// Keying only on the service account is right — the account id is not
// spoofable — but it leaves a tenant's effective rate proportional to the
// number of accounts it creates, because the per-account buckets have no view
// of the total.
func TestTenantRateLimit(t *testing.T) {
	// ask sends one request as the named account of the named tenant.
	ask := func(h http.Handler, tenant, account string) int {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/anything", nil)
		ctx := uow.WithTenant(r.Context(), valueobjects.TenantID(tenant))
		ctx = uow.WithActor(ctx, uow.Actor{ID: account, Kind: uow.ActorServiceAccount})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r.WithContext(ctx))
		return w.Code
	}
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	Convey("Given a tenant ceiling of two requests and a generous per-account limit", t, func() {
		m := metrics.New()
		perAccount := ratelimit.New(1000, 1000)
		perTenant := ratelimit.New(0.001, 2) // effectively two, then empty
		h := rateLimit(perAccount, perTenant, m)(ok)

		Convey("When one tenant spreads its requests over several accounts", func() {
			first := ask(h, "acme", "account-1")
			second := ask(h, "acme", "account-2")
			third := ask(h, "acme", "account-3")

			Convey("Then the ceiling still applies across all of them", func() {
				So(first, ShouldEqual, http.StatusOK)
				So(second, ShouldEqual, http.StatusOK)
				So(third, ShouldEqual, http.StatusTooManyRequests)
			})

			Convey("Then another tenant is unaffected", func() {
				So(ask(h, "other", "account-9"), ShouldEqual, http.StatusOK)
			})
		})
	})

	Convey("Given only a per-account limit", t, func() {
		m := metrics.New()
		h := rateLimit(ratelimit.New(0.001, 1), nil, m)(ok)

		Convey("When one account exhausts its bucket", func() {
			So(ask(h, "acme", "account-1"), ShouldEqual, http.StatusOK)
			So(ask(h, "acme", "account-1"), ShouldEqual, http.StatusTooManyRequests)

			Convey("Then a second account of the same tenant still proceeds", func() {
				// This is the multiplication the tenant ceiling exists to stop.
				So(ask(h, "acme", "account-2"), ShouldEqual, http.StatusOK)
			})
		})
	})

	Convey("Given both limiters are disabled", t, func() {
		h := rateLimit(nil, nil, metrics.New())(ok)

		Convey("Then requests pass through untouched", func() {
			So(ask(h, "acme", "account-1"), ShouldEqual, http.StatusOK)
			So(ask(h, "acme", "account-1"), ShouldEqual, http.StatusOK)
		})
	})
}
