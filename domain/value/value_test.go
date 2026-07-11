package value

import (
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/domain/attribute"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/events"
)

func newDef(t *testing.T) *attribute.Definition {
	t.Helper()
	def, _, err := attribute.New(attribute.NewInput{
		TypeDefinitionID: valueobjects.NewTypeDefinitionID(),
		InternalName:     "serial_number",
		DisplayName:      "Serial Number",
		DataType:         valueobjects.DataTypeString,
		Constraints:      attribute.Constraints{attribute.MaxLength{N: 10}},
	}, time.Now())
	if err != nil {
		t.Fatalf("new definition: %v", err)
	}
	return def
}

func TestAttributeValue(t *testing.T) {
	Convey("Given an attribute definition and an entity", t, func() {
		now := time.Date(2026, 7, 11, 9, 0, 0, 0, time.UTC)
		def := newDef(t)
		entity := valueobjects.EntityID("order-123")

		Convey("When a valid value is created", func() {
			av, evts, err := New(def, def.TypeDefinitionID(), entity, valueobjects.NewStringValue("SN-001"), now)

			Convey("Then it pins the definition version and emits a set event", func() {
				So(err, ShouldBeNil)
				So(av.DefinitionVersion(), ShouldEqual, def.Version())
				So(evts, ShouldHaveLength, 1)
				So(evts[0].EventType(), ShouldEqual, events.Type("flexitype.attribute_value.set"))
			})
		})

		Convey("When a constraint-violating value is created", func() {
			_, _, err := New(def, def.TypeDefinitionID(), entity, valueobjects.NewStringValue("way-too-long-serial"), now)

			Convey("Then creation fails validation", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
			})
		})

		Convey("When a value of the wrong type is created", func() {
			_, _, err := New(def, def.TypeDefinitionID(), entity, valueobjects.NewIntegerValue(5), now)

			Convey("Then creation fails validation", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
			})
		})

		Convey("When an existing value is updated after the definition evolved", func() {
			av, _, err := New(def, def.TypeDefinitionID(), entity, valueobjects.NewStringValue("SN-001"), now)
			So(err, ShouldBeNil)

			_, err = def.Update(attribute.UpdateInput{
				DisplayName: "Serial Number",
				Constraints: attribute.Constraints{attribute.MaxLength{N: 8}},
			}, now.Add(time.Hour))
			So(err, ShouldBeNil)
			So(def.Version(), ShouldEqual, 2)

			evts, err := av.UpdateValue(def, valueobjects.NewStringValue("SN-002"), now.Add(2*time.Hour))

			Convey("Then the value re-pins to the new definition version with old/new in the event", func() {
				So(err, ShouldBeNil)
				So(av.DefinitionVersion(), ShouldEqual, 2)
				So(evts, ShouldHaveLength, 1)
				updated := evts[0].(Updated)
				So(updated.OldValue.Text(), ShouldEqual, "SN-001")
				So(updated.NewValue.Text(), ShouldEqual, "SN-002")
			})
		})

		Convey("When updating with an identical value on the same definition version", func() {
			av, _, err := New(def, def.TypeDefinitionID(), entity, valueobjects.NewStringValue("SN-001"), now)
			So(err, ShouldBeNil)

			evts, err := av.UpdateValue(def, valueobjects.NewStringValue("SN-001"), now.Add(time.Hour))

			Convey("Then it is a no-op with no events", func() {
				So(err, ShouldBeNil)
				So(evts, ShouldBeEmpty)
			})
		})

		Convey("When a value is removed", func() {
			av, _, err := New(def, def.TypeDefinitionID(), entity, valueobjects.NewStringValue("SN-001"), now)
			So(err, ShouldBeNil)

			evts, err := av.Remove(now.Add(time.Hour))

			Convey("Then it archives and emits a removed event; a second removal fails", func() {
				So(err, ShouldBeNil)
				So(av.IsArchived(), ShouldBeTrue)
				So(evts[0].EventType(), ShouldEqual, events.Type("flexitype.attribute_value.removed"))

				_, err := av.Remove(now.Add(2 * time.Hour))
				So(domainerrors.IsArchived(err), ShouldBeTrue)
			})
		})
	})
}
