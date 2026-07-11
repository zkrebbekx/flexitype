package api

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	. "github.com/smartystreets/goconvey/convey"
)

// TestOpenAPISpec keeps the committed spec honest: it must be valid
// OpenAPI 3 and convert cleanly to JSON, so generators and mock servers
// pointed at /api/v1/openapi.json never receive a broken document.
func TestOpenAPISpec(t *testing.T) {
	Convey("Given the embedded OpenAPI document", t, func() {
		Convey("It is valid OpenAPI 3", func() {
			loader := openapi3.NewLoader()
			doc, err := loader.LoadFromData(SpecYAML)
			So(err, ShouldBeNil)
			So(doc.Validate(context.Background()), ShouldBeNil)
			So(doc.Info.Version, ShouldEqual, "v1")
			So(len(doc.Paths.Map()), ShouldBeGreaterThan, 20)
		})

		Convey("It converts to well-formed JSON", func() {
			b, err := SpecJSON()
			So(err, ShouldBeNil)
			var v map[string]any
			So(json.Unmarshal(b, &v), ShouldBeNil)
			So(v["openapi"], ShouldNotBeNil)
		})
	})
}
