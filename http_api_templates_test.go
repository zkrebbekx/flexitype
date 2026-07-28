package flexitype_test

import (
	"net/http"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
)

// TestTemplateEndpoints covers the first-touch path: list the curated
// templates, read one, apply it.
//
// It is the path a new user takes to see the product do something, so a
// failure here reads as "the product is broken" rather than "that template
// is broken" — and applying twice has to be safe, because a user who is not
// sure whether the first click worked will click again.
func TestTemplateEndpoints(t *testing.T) {
	Convey("Given a service with the schema templates", t, func() {
		a := newAPI(t, flexitype.APIConfig{})

		Convey("When the templates are listed", func() {
			resp := a.get("/api/v1/schema/templates")

			Convey("Then the curated set comes back", func() {
				So(resp.Status, ShouldEqual, http.StatusOK)
				So(len(resp.items(t)), ShouldBeGreaterThan, 0)
			})
		})

		Convey("When one is read by name", func() {
			name := a.get("/api/v1/schema/templates").items(t)[0].(map[string]any)["name"].(string)
			resp := a.get("/api/v1/schema/templates/" + name)

			Convey("Then it is returned", func() {
				So(resp.Status, ShouldEqual, http.StatusOK)
				So(resp.str(t, "name"), ShouldEqual, name)
			})
		})

		Convey("When an unknown template is read", func() {
			resp := a.get("/api/v1/schema/templates/no-such-template")

			Convey("Then it is a 404, not an empty template", func() {
				So(resp.Status, ShouldEqual, http.StatusNotFound)
			})
		})

		Convey("When a template is applied", func() {
			name := a.get("/api/v1/schema/templates").items(t)[0].(map[string]any)["name"].(string)
			resp := a.post("/api/v1/schema/templates/"+name+"/apply", nil)

			Convey("Then the schema is created", func() {
				So(resp.Status, ShouldEqual, http.StatusOK)
				So(len(a.get("/api/v1/type-definitions").items(t)), ShouldBeGreaterThan, 0)
			})

			Convey("Then applying it again is safe: import is idempotent", func() {
				again := a.post("/api/v1/schema/templates/"+name+"/apply", nil)
				So(again.Status, ShouldEqual, http.StatusOK)
			})
		})

		Convey("When an unknown template is applied", func() {
			resp := a.post("/api/v1/schema/templates/no-such-template/apply", nil)

			Convey("Then it is a 404 rather than a silent no-op reported as success", func() {
				So(resp.Status, ShouldEqual, http.StatusNotFound)
			})
		})
	})
}
