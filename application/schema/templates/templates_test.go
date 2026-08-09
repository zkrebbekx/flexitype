package templates

import (
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

// TestCuratedTemplates pins the templates a first-touch user applies.
//
// They ship embedded, so a malformed one is not a runtime surprise in one
// deployment — it is broken for everybody, on a path (apply a template to see
// the product work) where the user has no way to tell a bad template from a
// bad product.
func TestCuratedTemplates(t *testing.T) {
	Convey("Given the curated schema templates", t, func() {
		all := List()

		Convey("Then there are some, and each is identified", func() {
			So(all, ShouldNotBeEmpty)
			for _, tmpl := range all {
				So(tmpl.Name, ShouldNotBeEmpty)
				So(tmpl.Title, ShouldNotBeEmpty)
				So(tmpl.Description, ShouldNotBeEmpty)
			}
		})

		Convey("Then every one is retrievable by name and carries a bundle", func() {
			for _, summary := range all {
				tmpl, ok := Get(summary.Name)
				So(ok, ShouldBeTrue)
				So(tmpl.Name, ShouldEqual, summary.Name)
				So(tmpl.Bundle, ShouldNotBeNil)

				Convey("And the bundle carries a schema to apply: "+summary.Name, func() {
					raw, err := json.Marshal(tmpl.Bundle)
					So(err, ShouldBeNil)
					So(len(raw), ShouldBeGreaterThan, 2)
				})
			}
		})

		Convey("Then an unknown name is reported rather than returning an empty template", func() {
			_, ok := Get("no-such-template")
			So(ok, ShouldBeFalse)
		})

		Convey("Then the listing is stable, so the console does not reorder", func() {
			again := List()
			So(len(again), ShouldEqual, len(all))
			for i := range all {
				So(again[i].Name, ShouldEqual, all[i].Name)
			}
		})
	})
}
