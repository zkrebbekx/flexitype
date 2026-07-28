package value

import (
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

// TestScopedCell covers the decoder for a cell holding several values.
//
// The export writes a JSON array when an attribute holds more than one value
// for an entity — a multi-valued attribute, or locale/channel variants. The
// decoder has to tell that apart from an ordinary cell, including a JSON
// OBJECT cell (a quantity or a media reference), or a quantity would be
// mistaken for a multi-value cell and dropped.
func TestScopedCell(t *testing.T) {
	Convey("Given a cell that holds several values", t, func() {
		entries, ok := scopedCell(`[{"value":"Widget","locale":"en"},{"value":"Gadget","locale":"fr"}]`)

		Convey("Then each member is decoded with its scope", func() {
			So(ok, ShouldBeTrue)
			So(entries, ShouldHaveLength, 2)
			So(entries[0].text(), ShouldEqual, "Widget")
			So(entries[0].Locale, ShouldEqual, "en")
			So(entries[1].Channel, ShouldBeEmpty)
		})
	})

	Convey("Given a cell holding one JSON object", t, func() {
		Convey("Then a quantity is NOT read as a multi-value cell", func() {
			_, ok := scopedCell(`{"magnitude":"10","unit":"kg"}`)
			So(ok, ShouldBeFalse)
		})
	})

	Convey("Given ordinary cells", t, func() {
		for _, cell := range []string{"", "Widget", "10", "true", "  "} {
			_, ok := scopedCell(cell)

			Convey("Then "+cell+" is a plain cell", func() {
				So(ok, ShouldBeFalse)
			})
		}
	})

	Convey("Given a malformed array cell", t, func() {
		Convey("Then it falls back to a plain cell rather than erroring", func() {
			_, ok := scopedCell(`[{"value":`)
			So(ok, ShouldBeFalse)
		})

		Convey("Then an array member with no value is refused", func() {
			_, ok := scopedCell(`[{"locale":"en"}]`)
			So(ok, ShouldBeFalse)
		})

		Convey("Then an empty array is not a multi-value cell", func() {
			_, ok := scopedCell(`[]`)
			So(ok, ShouldBeFalse)
		})
	})

	Convey("Given a member holding a JSON object", t, func() {
		entries, ok := scopedCell(`[{"value":{"magnitude":"1","unit":"kg"},"channel":"web"}]`)

		Convey("Then its text is the object, not an unquoted string", func() {
			So(ok, ShouldBeTrue)
			So(entries[0].text(), ShouldContainSubstring, `"magnitude"`)
			So(json.Valid([]byte(entries[0].text())), ShouldBeTrue)
			So(entries[0].Channel, ShouldEqual, "web")
		})
	})
}

// TestExportCellEmpty pins the empty case: an entity with no value for an
// attribute exports a blank cell, and a blank cell writes no value on import.
func TestExportCellEmpty(t *testing.T) {
	Convey("Given no values for an attribute", t, func() {
		Convey("Then the cell is blank", func() {
			So(exportCell(nil), ShouldBeEmpty)
		})
	})
}
