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

// TestStrictEcommerceDiffersOnlyInEnforcement pins the pair.
//
// ecommerce_strict exists so the blocking mode is demonstrated by something a
// user can apply, not only by prose. Its value is that it is the SAME schema:
// if the two drift apart, the pair stops being a demonstration of one decision
// and becomes two templates to maintain.
func TestStrictEcommerceDiffersOnlyInEnforcement(t *testing.T) {
	Convey("Given the ecommerce pair", t, func() {
		lenient, ok := Get("ecommerce")
		So(ok, ShouldBeTrue)
		strict, ok := Get("ecommerce_strict")
		So(ok, ShouldBeTrue)

		Convey("Then the strict one blocks and the other reports", func() {
			So(lenient.Bundle.Dependencies, ShouldHaveLength, 2)
			So(strict.Bundle.Dependencies, ShouldHaveLength, len(lenient.Bundle.Dependencies))
			for i, d := range strict.Bundle.Dependencies {
				var effect map[string]any
				So(json.Unmarshal(d.Effect, &effect), ShouldBeNil)
				So(effect["enforce"], ShouldEqual, "on_write")
				So(effect["required"], ShouldEqual, true)

				var was map[string]any
				So(json.Unmarshal(lenient.Bundle.Dependencies[i].Effect, &was), ShouldBeNil)
				So(was["enforce"], ShouldBeNil)
			}
		})

		Convey("Then the types and attributes are identical", func() {
			// Only the enforcement differs. Anything else drifting means the
			// two templates have become separate schemas by accident.
			leanTypes, _ := json.Marshal(lenient.Bundle.Types)
			strictTypes, _ := json.Marshal(strict.Bundle.Types)
			So(string(strictTypes), ShouldEqual, string(leanTypes))

			leanAttrs, _ := json.Marshal(lenient.Bundle.Attributes)
			strictAttrs, _ := json.Marshal(strict.Bundle.Attributes)
			So(string(strictAttrs), ShouldEqual, string(leanAttrs))

			leanRels, _ := json.Marshal(lenient.Bundle.RelationshipDefinitions)
			strictRels, _ := json.Marshal(strict.Bundle.RelationshipDefinitions)
			So(string(strictRels), ShouldEqual, string(leanRels))
		})

		Convey("Then each rule pairs with the same source and target", func() {
			for i, d := range strict.Bundle.Dependencies {
				was := lenient.Bundle.Dependencies[i]
				So(d.SourceType, ShouldEqual, was.SourceType)
				So(d.SourceAttribute, ShouldEqual, was.SourceAttribute)
				So(d.TargetType, ShouldEqual, was.TargetType)
				So(d.TargetAttribute, ShouldEqual, was.TargetAttribute)
			}
		})
	})
}
