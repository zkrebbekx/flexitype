package http

import (
	"net/http/httptest"
	"strings"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
)

func TestDecodeBodyLimit(t *testing.T) {
	Convey("Given a JSON body over the size limit", t, func() {
		big := `{"x":"` + strings.Repeat("a", maxJSONBody+1024) + `"}`
		r := httptest.NewRequest("POST", "/", strings.NewReader(big))
		var dst map[string]any
		err := decode(r, &dst)

		Convey("Then decode rejects it as too large", func() {
			So(err, ShouldNotBeNil)
			So(domainerrors.CodeOf(err), ShouldEqual, domainerrors.CodeValidation)
			So(err.Error(), ShouldContainSubstring, "too large")
		})
	})

	Convey("Given a small valid body", t, func() {
		r := httptest.NewRequest("POST", "/", strings.NewReader(`{"x":1}`))
		var dst map[string]any

		Convey("Then decode accepts it", func() {
			So(decode(r, &dst), ShouldBeNil)
		})
	})
}
