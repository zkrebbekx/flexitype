package postgres

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

// TestResidualEraserNames pins the names an erasure report and an error use
// to say WHICH copy of the erased values could not be redacted. A failure
// that does not name the store leaves an operator with a failed
// right-to-erasure request and nowhere to look.
func TestResidualEraserNames(t *testing.T) {
	Convey("Given the residual erasers", t, func() {
		Convey("Then the event-log eraser names itself", func() {
			So(NewOutboxEraser().Name(), ShouldEqual, "event log")
		})

		Convey("Then the activity-log eraser names itself", func() {
			So(NewActivityEraser().Name(), ShouldEqual, "activity log")
		})
	})
}

// TestRedactedPayloadShape pins the redaction to identifiers only.
//
// The payload has to keep enough for a consumer to see that something
// happened to an entity, and none of the value. A future edit that adds a
// field to the projection would leak the value back into a feed that is
// served for as long as retention allows — and forever for rows never
// expanded.
func TestRedactedPayloadShape(t *testing.T) {
	Convey("Given the redacted-payload projection", t, func() {
		Convey("Then it keeps only routing identifiers and the marker", func() {
			for _, key := range []string{
				"'tenant_id'", "'entity_id'", "'type_definition_id'",
				"'attribute_definition_id'", "'erased'",
			} {
				So(redactedPayload, ShouldContainSubstring, key)
			}
		})

		Convey("Then it carries no value field", func() {
			So(redactedPayload, ShouldNotContainSubstring, "'value'")
			So(redactedPayload, ShouldNotContainSubstring, "'value_json'")
		})
	})
}
