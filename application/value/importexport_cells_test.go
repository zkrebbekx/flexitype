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
		entries, ok := scopedCell(`[{"value":"Widget","locale":"en"},{"value":"Gadget","locale":"fr"}]`, valueobjects.DataTypeString, true)

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
			_, ok := scopedCell(`{"magnitude":"10","unit":"kg"}`, valueobjects.DataTypeString, false)
			So(ok, ShouldBeFalse)
		})
	})

	Convey("Given ordinary cells", t, func() {
		for _, cell := range []string{"", "Widget", "10", "true", "  "} {
			_, ok := scopedCell(cell, valueobjects.DataTypeString, false)

			Convey("Then "+cell+" is a plain cell", func() {
				So(ok, ShouldBeFalse)
			})
		}
	})

	Convey("Given a malformed array cell", t, func() {
		Convey("Then it falls back to a plain cell rather than erroring", func() {
			_, ok := scopedCell(`[{"value":`, valueobjects.DataTypeString, true)
			So(ok, ShouldBeFalse)
		})

		Convey("Then an array member with no value is refused", func() {
			_, ok := scopedCell(`[{"locale":"en"}]`, valueobjects.DataTypeString, true)
			So(ok, ShouldBeFalse)
		})

		Convey("Then an empty array is not a multi-value cell", func() {
			_, ok := scopedCell(`[]`, valueobjects.DataTypeString, true)
			So(ok, ShouldBeFalse)
		})
	})

	Convey("Given a member holding a JSON object", t, func() {
		entries, ok := scopedCell(`[{"value":{"magnitude":"1","unit":"kg"},"channel":"web"}]`, valueobjects.DataTypeString, true)

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
			valueobjects.DataTypeString, true)

		Convey("Then it decodes for a non-json column, once legacy cells are opted in", func() {
			So(ok, ShouldBeTrue)
			So(entries, ShouldHaveLength, 2)
			So(entries[0].text(), ShouldEqual, "Widget")
		})
	})

	Convey("Given a JSON payload shaped like the old untagged format", t, func() {
		cell := `[{"value":{"x":1}},{"value":{"y":2}}]`

		Convey("When the column is json", func() {
			_, ok := scopedCell(cell, valueobjects.DataTypeJSON, true)

			Convey("Then it is one value, not two members", func() {
				So(ok, ShouldBeFalse)
			})
		})

		Convey("When the column is not json", func() {
			_, ok := scopedCell(cell, valueobjects.DataTypeString, true)

			Convey("Then the untagged form still loads, so an older export imports", func() {
				So(ok, ShouldBeTrue)
			})
		})
	})

	Convey("Given a tagged cell with an empty member list", t, func() {
		Convey("Then it is not a multi-value cell", func() {
			_, ok := scopedCell(`{"values":[]}`, valueobjects.DataTypeString, true)
			So(ok, ShouldBeFalse)
		})
	})

	Convey("Given an ordinary JSON object cell", t, func() {
		Convey("Then a quantity is still one value", func() {
			_, ok := scopedCell(`{"magnitude":"10","unit":"kg"}`, valueobjects.DataTypeQuantity, false)
			So(ok, ShouldBeFalse)
		})
	})
}

// TestMultiValueMarkerIsOutOfBand covers the corruption an in-band sentinel
// could not close.
//
// The format was a bare array of {"value",…} objects — ordinary JSON.
// Retagging it as {"values":[…]} moved WHICH documents collide rather than
// whether they can, and the tagged shape is exactly what an export of a json
// column looks like: re-importing this tool's own output read one document as
// two members, wrote both to a single-valued attribute, kept the last, and
// reported one row written with zero errors.
//
// A '#' cannot begin a JSON document, so the marker is now outside the
// payload and works for a json column too.
func TestMultiValueMarkerIsOutOfBand(t *testing.T) {
	Convey("Given the marked multi-value format the export now writes", t, func() {
		cell := multiValueCellPrefix + `[{"value":"Widget","locale":"en"},{"value":"Gadget","locale":"fr"}]`

		Convey("Then it decodes for every data type, json included", func() {
			for _, dt := range []valueobjects.DataType{
				valueobjects.DataTypeString, valueobjects.DataTypeJSON,
			} {
				entries, ok := scopedCell(cell, dt, false)
				So(ok, ShouldBeTrue)
				So(entries, ShouldHaveLength, 2)
				So(entries[0].text(), ShouldEqual, "Widget")
			}
		})
	})

	Convey("Given a json document shaped like the tagged format", t, func() {
		cell := `{"values":[{"value":{"x":1}},{"value":{"y":2}}]}`

		Convey("When the column is json", func() {
			_, ok := scopedCell(cell, valueobjects.DataTypeJSON, true)

			Convey("Then it is ONE value, so the document survives the round trip", func() {
				So(ok, ShouldBeFalse)
			})
		})

		Convey("When the column is not json", func() {
			entries, ok := scopedCell(cell, valueobjects.DataTypeString, true)

			Convey("Then the legacy tagged form still loads, so an older export imports", func() {
				So(ok, ShouldBeTrue)
				So(entries, ShouldHaveLength, 2)
			})
		})
	})

	Convey("Given a multi-valued json attribute exported and re-imported", t, func() {
		// The round trip a bulk migration performs.
		cell := multiValueCellPrefix + `[{"value":{"x":1}},{"value":{"y":2}}]`
		entries, ok := scopedCell(cell, valueobjects.DataTypeJSON, false)

		Convey("Then both documents come back, rather than the last one only", func() {
			So(ok, ShouldBeTrue)
			So(entries, ShouldHaveLength, 2)
			So(string(entries[0].Value), ShouldEqual, `{"x":1}`)
			So(string(entries[1].Value), ShouldEqual, `{"y":2}`)
		})
	})

	Convey("Given a marked cell whose payload is not a member list", t, func() {
		Convey("Then it is not a multi-value cell", func() {
			_, ok := scopedCell(multiValueCellPrefix+`{"x":1}`, valueobjects.DataTypeString, false)
			So(ok, ShouldBeFalse)
		})
	})
}

// TestLegacyInBandCellsAreOptIn is the regression for #491.
//
// The in-band forms were excluded from json columns only, so any other type
// still routed a cell starting with '[' into the member decoder. A string
// attribute holding the literal text `[{"value":"a"},{"value":"b"}]`
// exported verbatim and re-imported as TWO values — this tool's own output
// did not round-trip, and the report called it one valid row with no errors.
//
// The exclusion now covers every type: only the out-of-band prefix marks a
// multi-value cell, and the legacy forms load only when a caller opts in.
func TestLegacyInBandCellsAreOptIn(t *testing.T) {
	legacyShapes := map[string]string{
		"a bare array":       `[{"value":"a"},{"value":"b"}]`,
		"an untagged object": `{"values":[{"value":"a"},{"value":"b"}]}`,
	}

	for label, cell := range legacyShapes {
		Convey("Given a string value shaped like "+label, t, func() {
			Convey("Then by default it is ONE value, so the export round-trips", func() {
				_, ok := scopedCell(cell, valueobjects.DataTypeString, false)
				So(ok, ShouldBeFalse)
			})

			Convey("Then opting in reads it as members, so an older file still loads", func() {
				entries, ok := scopedCell(cell, valueobjects.DataTypeString, true)
				So(ok, ShouldBeTrue)
				So(entries, ShouldHaveLength, 2)
			})

			Convey("Then a json column refuses it even when opted in", func() {
				_, ok := scopedCell(cell, valueobjects.DataTypeJSON, true)
				So(ok, ShouldBeFalse)
			})
		})
	}

	Convey("Given the marked form the export writes", t, func() {
		cell := multiValueCellPrefix + `[{"value":"a"},{"value":"b"}]`

		Convey("Then it decodes with no opt-in, because no value can carry the prefix", func() {
			entries, ok := scopedCell(cell, valueobjects.DataTypeString, false)
			So(ok, ShouldBeTrue)
			So(entries, ShouldHaveLength, 2)
		})
	})
}
