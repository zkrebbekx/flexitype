package http

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/pkg/logger"
)

// TestSignedMediaTokenNeverReachesTheLog covers the one request path whose URL
// is itself a credential.
//
// A request path is normally safe to log, and useful. `/media/signed/{token}`
// is the exception: the token is a bearer capability that fetches tenant bytes
// with no other credential, for up to 24 hours. Logging the path handed the
// object to everyone who can read the log — the aggregator, the SIEM, a
// support engineer, a log-shipping vendor. The project rule is that no
// credential reaches a log line.
func TestSignedMediaTokenNeverReachesTheLog(t *testing.T) {
	const token = "eyJ2IjoxfQ.c2VjcmV0LXNpZ25hdHVyZS12YWx1ZQ"

	logged := func(path string, handler http.HandlerFunc) string {
		var buf bytes.Buffer
		log := &logger.Logger{Logger: zerolog.New(&buf)}
		rec := httptest.NewRecorder()
		requestLogger(log)(handler).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return buf.String()
	}
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	Convey("Given a redemption of a signed media link", t, func() {
		line := logged(signedMediaPrefix+token, ok)

		Convey("Then the request is logged", func() {
			So(line, ShouldContainSubstring, `"path"`)
			So(line, ShouldContainSubstring, signedMediaPrefix)
		})

		Convey("Then the token is not", func() {
			So(line, ShouldNotContainSubstring, token)
			So(line, ShouldContainSubstring, "[redacted]")
		})
	})

	Convey("Given a handler that panics on that route", t, func() {
		// The recoverer logs the path too, and a panic is exactly when
		// somebody goes looking through the log.
		var buf bytes.Buffer
		log := &logger.Logger{Logger: zerolog.New(&buf)}
		rec := httptest.NewRecorder()
		boom := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") })
		recoverer(log)(boom).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, signedMediaPrefix+token, nil))

		Convey("Then the token is still not logged", func() {
			So(rec.Code, ShouldEqual, http.StatusInternalServerError)
			So(buf.String(), ShouldContainSubstring, "handler panic")
			So(buf.String(), ShouldNotContainSubstring, token)
		})
	})

	Convey("Given an ordinary request", t, func() {
		line := logged("/api/v1/attributes/01JBQ8Z0000000000000000001", ok)

		Convey("Then its path is logged in full — an id is not a credential", func() {
			So(line, ShouldContainSubstring, "/api/v1/attributes/01JBQ8Z0000000000000000001")
			So(line, ShouldNotContainSubstring, "[redacted]")
		})
	})

	Convey("Given the signed-media prefix with nothing after it", t, func() {
		// No token means nothing to redact, and the path is still worth
		// logging: it is how a misconfigured caller shows up.
		line := logged(strings.TrimSuffix(signedMediaPrefix, "/"), ok)

		Convey("Then it is logged as it arrived", func() {
			So(line, ShouldNotContainSubstring, "[redacted]")
		})
	})
}
