package value

import (
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/domain/attribute"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

func TestAttributeValueConstructionGuards(t *testing.T) {
	Convey("Given an attribute definition and a candidate value", t, func() {
		now := time.Date(2026, 7, 18, 9, 0, 0, 0, time.UTC)
		def := newDef(t)
		typeID := def.TypeDefinitionID()
		entity := valueobjects.EntityID("order-123")
		val := valueobjects.NewStringValue("SN-001")

		Convey("When the entity id is missing", func() {
			_, _, err := New(def, typeID, valueobjects.EntityID(""), valueobjects.Scope{}, val, now)

			Convey("Then creation is rejected", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "entity ID")
			})
		})

		Convey("When the entity type is missing", func() {
			_, _, err := New(def, valueobjects.TypeDefinitionID{}, entity, valueobjects.Scope{}, val, now)

			Convey("Then creation is rejected", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "entity type")
			})
		})

		Convey("When the value is the zero value", func() {
			_, _, err := New(def, typeID, entity, valueobjects.Scope{}, valueobjects.Value{}, now)

			Convey("Then creation is rejected", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "value is required")
			})
		})

		Convey("When the value violates the definition's constraints", func() {
			_, _, err := New(def, typeID, entity, valueobjects.Scope{},
				valueobjects.NewStringValue("far too long to fit"), now)

			Convey("Then the definition's own validation rejects it", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
			})
		})

		Convey("When the value carries a locale and channel scope", func() {
			scope := valueobjects.Scope{Locale: "en-GB", Channel: "web"}
			av, _, err := New(def, typeID, entity, scope, val, now)

			Convey("Then the scope and timestamps are recorded on the aggregate", func() {
				So(err, ShouldBeNil)
				So(av.Scope(), ShouldResemble, scope)
				So(av.CreatedAt(), ShouldEqual, now)
				So(av.UpdatedAt(), ShouldEqual, now)
				So(av.ArchivedAt(), ShouldBeNil)
				So(av.IsArchived(), ShouldBeFalse)
			})
		})
	})
}

func TestAttributeValueUpdateGuards(t *testing.T) {
	Convey("Given a stored attribute value", t, func() {
		now := time.Date(2026, 7, 18, 9, 0, 0, 0, time.UTC)
		def := newDef(t)
		av, _, err := New(def, def.TypeDefinitionID(), valueobjects.EntityID("order-123"),
			valueobjects.Scope{}, valueobjects.NewStringValue("SN-001"), now)
		So(err, ShouldBeNil)

		Convey("When it is updated using a different attribute's definition", func() {
			other := newDef(t) // a distinct definition, hence a distinct ID
			_, err := av.UpdateValue(other, valueobjects.NewStringValue("SN-002"), now.Add(time.Hour))

			Convey("Then the mismatch is rejected so a value cannot be re-pointed", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "does not match")
			})
		})

		Convey("When it is updated to the zero value", func() {
			_, err := av.UpdateValue(def, valueobjects.Value{}, now.Add(time.Hour))

			Convey("Then the update is rejected — removal is a separate operation", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "value is required")
			})
		})

		Convey("When it is updated to a value violating the constraints", func() {
			_, err := av.UpdateValue(def, valueobjects.NewStringValue("far too long to fit"), now.Add(time.Hour))

			Convey("Then the update is rejected", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
			})
		})

		Convey("When it is updated to the identical value at the same definition version", func() {
			evts, err := av.UpdateValue(def, valueobjects.NewStringValue("SN-001"), now.Add(time.Hour))

			Convey("Then it is a no-op: no event and no timestamp churn", func() {
				So(err, ShouldBeNil)
				So(evts, ShouldBeEmpty)
				So(av.UpdatedAt(), ShouldEqual, now)
			})
		})

		Convey("When the value is removed", func() {
			removedAt := now.Add(2 * time.Hour)
			_, err := av.Remove(removedAt)
			So(err, ShouldBeNil)

			Convey("Then it reports the archive instant", func() {
				So(av.IsArchived(), ShouldBeTrue)
				So(*av.ArchivedAt(), ShouldEqual, removedAt)
			})

			Convey("Then removing again is rejected as already archived", func() {
				_, err := av.Remove(removedAt.Add(time.Hour))
				So(domainerrors.IsArchived(err), ShouldBeTrue)
			})

			Convey("Then updating an archived value is rejected", func() {
				_, err := av.UpdateValue(def, valueobjects.NewStringValue("SN-002"), removedAt.Add(time.Hour))
				So(domainerrors.IsArchived(err), ShouldBeTrue)
			})
		})
	})
}

func TestAttributeValueRepinsDefinitionVersion(t *testing.T) {
	Convey("Given a value stored against version 1 of its definition", t, func() {
		now := time.Date(2026, 7, 18, 9, 0, 0, 0, time.UTC)
		def := newDef(t)
		av, _, err := New(def, def.TypeDefinitionID(), valueobjects.EntityID("order-123"),
			valueobjects.Scope{}, valueobjects.NewStringValue("SN-001"), now)
		So(err, ShouldBeNil)
		So(av.DefinitionVersion(), ShouldEqual, 1)

		Convey("When the definition is updated and the value re-written", func() {
			_, err := def.Update(attribute.UpdateInput{
				DisplayName: "Serial Number",
				Constraints: attribute.Constraints{attribute.MaxLength{N: 20}},
			}, now.Add(time.Hour))
			So(err, ShouldBeNil)
			So(def.Version(), ShouldEqual, 2)

			evts, err := av.UpdateValue(def, valueobjects.NewStringValue("SN-002"), now.Add(2*time.Hour))

			Convey("Then the value re-pins to the newer definition version", func() {
				So(err, ShouldBeNil)
				So(evts, ShouldHaveLength, 1)
				So(av.DefinitionVersion(), ShouldEqual, 2)
				So(av.Value().Text(), ShouldEqual, "SN-002")
				So(av.UpdatedAt(), ShouldEqual, now.Add(2*time.Hour))
			})
		})

		Convey("When only the definition version moves and the value is unchanged", func() {
			_, err := def.Update(attribute.UpdateInput{
				DisplayName: "Serial Number",
				Constraints: attribute.Constraints{attribute.MaxLength{N: 20}},
			}, now.Add(time.Hour))
			So(err, ShouldBeNil)

			evts, err := av.UpdateValue(def, valueobjects.NewStringValue("SN-001"), now.Add(2*time.Hour))

			Convey("Then it still re-pins, because the pinned version is now stale", func() {
				So(err, ShouldBeNil)
				So(evts, ShouldHaveLength, 1)
				So(av.DefinitionVersion(), ShouldEqual, 2)
			})
		})
	})
}
