package safedial_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/internal/safedial"
)

// TestProxyDoesNotBypassTheGuard covers issue #593.
//
// A proxy moves the connect target from the destination to the proxy. The
// guard runs on the address the dialer dials, so with HTTP_PROXY set it
// validated the proxy — public, allowed — and never saw where the request was
// really going. The proxy then made the internal connection on the caller's
// behalf, which is the whole thing the guard exists to prevent.
func TestProxyDoesNotBypassTheGuard(t *testing.T) {
	Convey("Given a proxy that would happily fetch an internal address", t, func() {
		var proxied []string
		proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			proxied = append(proxied, r.URL.String())
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("metadata"))
		}))
		defer proxy.Close()

		// The environment a deployment might legitimately have.
		t.Setenv("HTTP_PROXY", proxy.URL)
		t.Setenv("HTTPS_PROXY", proxy.URL)
		t.Setenv("NO_PROXY", "")

		Convey("When the guarded client is pointed at the metadata service", func() {
			client := safedial.NewClient(safedial.Options{})
			resp, err := client.Get("http://169.254.169.254/latest/meta-data/")
			if err == nil {
				defer func() { _ = resp.Body.Close() }()
			}

			Convey("Then the connection is refused, and the proxy is never asked", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "blocked connection to non-public address")
				So(err.Error(), ShouldNotContainSubstring, "proxyconnect")
				So(proxied, ShouldBeEmpty)
			})
		})

		Convey("When a caller opts in to the environment proxy", func() {
			client := safedial.NewClient(safedial.Options{UseEnvironmentProxy: true})
			resp, err := client.Get("http://169.254.169.254/latest/meta-data/")
			if err == nil {
				defer func() { _ = resp.Body.Close() }()
			}

			Convey("Then the request goes to the PROXY, not the destination", func() {
				// This is the documented cost of opting in: the guard now sees
				// the proxy's address instead of the destination. Here the
				// proxy is loopback, so the guard blocks it — and that is the
				// tell. The error names the proxy connection, whereas the
				// default client above failed on 169.254.169.254 itself.
				//
				// With a PUBLIC proxy the same path succeeds and the metadata
				// service is reached, which is exactly why this is opt-in.
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "proxyconnect")
			})
		})
	})

	Convey("Given no proxy in the environment", t, func() {
		t.Setenv("HTTP_PROXY", "")
		t.Setenv("HTTPS_PROXY", "")

		Convey("When a private address is requested directly", func() {
			client := safedial.NewClient(safedial.Options{})
			_, err := client.Get("http://127.0.0.1:9/")

			Convey("Then it is still blocked", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "blocked connection to non-public address")
			})
		})
	})
}
