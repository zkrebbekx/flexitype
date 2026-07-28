package value

import (
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/domain/valueobjects"
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
		entries, ok := scopedCell(`[{"value":"Widget","locale":"en"},{"value":"Gadget","locale":"fr"}]`, valueobjects.DataTypeString)

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
			_, ok := scopedCell(`{"magnitude":"10","unit":"kg"}`, valueobjects.DataTypeString)
			So(ok, ShouldBeFalse)
		})
	})

	Convey("Given ordinary cells", t, func() {
		for _, cell := range []string{"", "Widget", "10", "true", "  "} {
			_, ok := scopedCell(cell, valueobjects.DataTypeString)

			Convey("Then "+cell+" is a plain cell", func() {
				So(ok, ShouldBeFalse)
			})
		}
	})

	Convey("Given a malformed array cell", t, func() {
		Convey("Then it falls back to a plain cell rather than erroring", func() {
			_, ok := scopedCell(`[{"value":`, valueobjects.DataTypeString)
			So(ok, ShouldBeFalse)
		})

		Convey("Then an array member with no value is refused", func() {
			_, ok := scopedCell(`[{"locale":"en"}]`, valueobjects.DataTypeString)
			So(ok, ShouldBeFalse)
		})

		Convey("Then an empty array is not a multi-value cell", func() {
			_, ok := scopedCell(`[]`, valueobjects.DataTypeString)
			So(ok, ShouldBeFalse)
		})
	})

	Convey("Given a member holding a JSON object", t, func() {
		entries, ok := scopedCell(`[{"value":{"magnitude":"1","unit":"kg"},"channel":"web"}]`, valueobjects.DataTypeString)

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

// TestScopedCellIsTagged covers the ambiguity the untagged format created.
//
// A bare array of {"value",…} objects is a perfectly ordinary JSON payload,
// so exporting and re-importing `[{"value":{"x":1}},{"value":{"y":2}}]` in a
// json column read the cell as two scoped members and stored the last one —
// silently, with zero errors reported.
func TestScopedCellIsTagged(t *testing.T) {
	Convey("Given the tagged multi-value format the export now writes", t, func() {
		entries, ok := scopedCell(
			`{"values":[{"value":"Widget","locale":"en"},{"value":"Gadget","locale":"fr"}]}`,
			valueobjects.DataTypeString)

		Convey("Then it decodes, whatever the column's type", func() {
			So(ok, ShouldBeTrue)
			So(entries, ShouldHaveLength, 2)
			So(entries[0].text(), ShouldEqual, "Widget")
		})
	})

	Convey("Given a JSON payload shaped like the old untagged format", t, func() {
		cell := `[{"value":{"x":1}},{"value":{"y":2}}]`

		Convey("When the column is json", func() {
			_, ok := scopedCell(cell, valueobjects.DataTypeJSON)

			Convey("Then it is one value, not two members", func() {
				So(ok, ShouldBeFalse)
			})
		})

		Convey("When the column is not json", func() {
			_, ok := scopedCell(cell, valueobjects.DataTypeString)

			Convey("Then the untagged form still loads, so an older export imports", func() {
				So(ok, ShouldBeTrue)
			})
		})
	})

	Convey("Given a tagged cell with an empty member list", t, func() {
		Convey("Then it is not a multi-value cell", func() {
			_, ok := scopedCell(`{"values":[]}`, valueobjects.DataTypeString)
			So(ok, ShouldBeFalse)
		})
	})

	Convey("Given an ordinary JSON object cell", t, func() {
		Convey("Then a quantity is still one value", func() {
			_, ok := scopedCell(`{"magnitude":"10","unit":"kg"}`, valueobjects.DataTypeQuantity)
			So(ok, ShouldBeFalse)
		})
	})
}
