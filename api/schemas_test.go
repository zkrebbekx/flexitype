package api

import (
	"encoding/json"
	"strings"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

// TestEmbeddedSchemasAreServable pins that the published event contracts are
// present, parse, and identify themselves.
//
// They are embedded so a deployment can serve them: a consumer in another
// language should be able to fetch the contract from the service it is
// consuming, rather than transcribing Go structs by hand and discovering a
// change when their parser fails.
func TestEmbeddedSchemasAreServable(t *testing.T) {
	Convey("Given the embedded event schemas", t, func() {
		for name, raw := range map[string][]byte{
			"envelope": EventEnvelopeSchema,
			"payloads": EventPayloadSchemas,
		} {
			Convey("Then the "+name+" schema is present and parses", func() {
				So(len(raw), ShouldBeGreaterThan, 100)
				var doc map[string]any
				So(json.Unmarshal(raw, &doc), ShouldBeNil)

				Convey("And it declares its dialect and identity", func() {
					So(doc["$schema"], ShouldNotBeNil)
					id, ok := doc["$id"].(string)
					So(ok, ShouldBeTrue)
					So(strings.HasPrefix(id, "https://"), ShouldBeTrue)
					So(doc["title"], ShouldNotBeNil)
				})
			})
		}
	})

	Convey("Given the payload schema", t, func() {
		var doc struct {
			Defs map[string]json.RawMessage `json:"$defs"`
		}
		So(json.Unmarshal(EventPayloadSchemas, &doc), ShouldBeNil)

		Convey("Then every event family a consumer receives has a definition", func() {
			for _, def := range []string{"value", "type_definition", "attribute_definition", "relationship"} {
				So(doc.Defs[def], ShouldNotBeNil)
			}
		})
	})

	Convey("Given the OpenAPI document", t, func() {
		Convey("Then it is embedded alongside the event schemas", func() {
			So(len(SpecYAML), ShouldBeGreaterThan, 1000)
		})
	})
}
