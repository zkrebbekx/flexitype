package relationship

import (
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/typedef"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

func newEntityType(t *testing.T, name string) *typedef.TypeDefinition {
	t.Helper()
	td, _, err := typedef.New(typedef.NewInput{TenantID: valueobjects.DefaultTenant, InternalName: name, DisplayName: name}, time.Now())
	if err != nil {
		t.Fatalf("new type: %v", err)
	}
	return td
}

func newAttrSet(t *testing.T, name string) *typedef.TypeDefinition {
	t.Helper()
	td, _, err := typedef.NewAttributeSet(valueobjects.DefaultTenant, name, name, time.Now())
	if err != nil {
		t.Fatalf("new attribute set: %v", err)
	}
	return td
}

func TestRelationshipDefinition(t *testing.T) {
	Convey("Given parent and child entity types and an attribute set", t, func() {
		now := time.Date(2026, 7, 11, 10, 0, 0, 0, time.UTC)
		assembly := newEntityType(t, "assembly")
		part := newEntityType(t, "part")
		attrSet := newAttrSet(t, "rel_uses_attrs")

		input := NewDefinitionInput{
			InternalName: "uses",
			DisplayName:  "Uses",
			ParentType:   assembly,
			ChildType:    part,
			AttributeSet: attrSet,
		}

		Convey("When a definition is created with defaults", func() {
			def, evts, err := NewDefinition(input, now)

			Convey("Then both sides default to the latest-version policy and a created event is emitted", func() {
				So(err, ShouldBeNil)
				So(def.ParentVersionPolicy(), ShouldEqual, PolicyLatest)
				So(def.ChildVersionPolicy(), ShouldEqual, PolicyLatest)
				So(evts, ShouldHaveLength, 1)
				So(evts[0].EventType(), ShouldEqual, EventDefinitionCreated)
			})
		})

		Convey("When the attribute set is an ordinary entity type", func() {
			input.AttributeSet = newEntityType(t, "not_a_set")
			_, _, err := NewDefinition(input, now)

			Convey("Then creation is rejected", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
			})
		})

		Convey("When a definition extends another with the same endpoints", func() {
			base, _, err := NewDefinition(input, now)
			So(err, ShouldBeNil)

			extended := input
			extended.InternalName = "uses_certified"
			extended.DisplayName = "Uses (certified)"
			extended.AttributeSet = newAttrSet(t, "rel_uses_certified_attrs")
			extended.Extends = base

			def, _, err := NewDefinition(extended, now)

			Convey("Then inheritance is recorded", func() {
				So(err, ShouldBeNil)
				So(def.ExtendsID(), ShouldNotBeNil)
				So(def.ExtendsID().Equals(base.ID()), ShouldBeTrue)
			})
		})

		Convey("When a definition extends another with different endpoints", func() {
			base, _, err := NewDefinition(input, now)
			So(err, ShouldBeNil)

			other := input
			other.InternalName = "contains"
			other.ParentType = part // flipped
			other.ChildType = assembly
			other.AttributeSet = newAttrSet(t, "rel_contains_attrs")
			other.Extends = base

			_, _, err = NewDefinition(other, now)

			Convey("Then creation is rejected", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
			})
		})
	})
}

func TestRelationshipLinks(t *testing.T) {
	Convey("Given a relationship definition with a pinned parent policy", t, func() {
		now := time.Date(2026, 7, 11, 10, 0, 0, 0, time.UTC)
		assembly := newEntityType(t, "assembly")
		part := newEntityType(t, "part")

		def, _, err := NewDefinition(NewDefinitionInput{
			InternalName:        "uses",
			DisplayName:         "Uses",
			ParentType:          assembly,
			ChildType:           part,
			AttributeSet:        newAttrSet(t, "rel_uses_attrs"),
			ParentVersionPolicy: PolicyPinned,
			ChildVersionPolicy:  PolicyLatest,
		}, now)
		So(err, ShouldBeNil)

		pin := 3

		Convey("When linking with the required parent pin", func() {
			rel, evts, err := Link(LinkInput{
				Definition:    def,
				ParentEntity:  "asm-1",
				ChildEntity:   "part-9",
				ParentVersion: &pin,
			}, now)

			Convey("Then the link records the pin and follows latest on the child", func() {
				So(err, ShouldBeNil)
				So(*rel.ParentVersion(), ShouldEqual, 3)
				So(rel.ChildVersion(), ShouldBeNil)
				So(evts[0].EventType(), ShouldEqual, EventLinked)
			})
		})

		Convey("When linking without the required parent pin", func() {
			_, _, err := Link(LinkInput{
				Definition:   def,
				ParentEntity: "asm-1",
				ChildEntity:  "part-9",
			}, now)

			Convey("Then linking is rejected", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
			})
		})

		Convey("When pinning the latest-policy child side", func() {
			_, _, err := Link(LinkInput{
				Definition:    def,
				ParentEntity:  "asm-1",
				ChildEntity:   "part-9",
				ParentVersion: &pin,
				ChildVersion:  &pin,
			}, now)

			Convey("Then linking is rejected", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
			})
		})

		Convey("When re-pinning an existing link", func() {
			rel, _, err := Link(LinkInput{
				Definition:    def,
				ParentEntity:  "asm-1",
				ChildEntity:   "part-9",
				ParentVersion: &pin,
			}, now)
			So(err, ShouldBeNil)

			newPin := 4
			evts, err := rel.RePin(def, &newPin, nil, now.Add(time.Hour))

			Convey("Then the pin moves and a repinned event is emitted", func() {
				So(err, ShouldBeNil)
				So(*rel.ParentVersion(), ShouldEqual, 4)
				So(evts[0].EventType(), ShouldEqual, EventRePinned)
			})
		})

		Convey("When unlinking", func() {
			rel, _, err := Link(LinkInput{
				Definition:    def,
				ParentEntity:  "asm-1",
				ChildEntity:   "part-9",
				ParentVersion: &pin,
			}, now)
			So(err, ShouldBeNil)

			evts, err := rel.Unlink(now.Add(time.Hour))

			Convey("Then the link archives and a second unlink fails", func() {
				So(err, ShouldBeNil)
				So(rel.IsArchived(), ShouldBeTrue)
				So(evts[0].EventType(), ShouldEqual, EventUnlinked)

				_, err := rel.Unlink(now.Add(2 * time.Hour))
				So(domainerrors.IsArchived(err), ShouldBeTrue)
			})
		})

		Convey("When linking an entity to itself under a same-type definition", func() {
			person := newEntityType(t, "person")
			selfDef, _, err := NewDefinition(NewDefinitionInput{
				InternalName: "manages",
				DisplayName:  "Manages",
				ParentType:   person,
				ChildType:    person,
				AttributeSet: newAttrSet(t, "rel_manages_attrs"),
			}, now)
			So(err, ShouldBeNil)

			_, _, err = Link(LinkInput{Definition: selfDef, ParentEntity: "p-1", ChildEntity: "p-1"}, now)

			Convey("Then linking is rejected", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
			})
		})
	})
}

func TestSymmetricRelationships(t *testing.T) {
	Convey("Given two entity types and an attribute set", t, func() {
		now := time.Date(2026, 7, 11, 10, 0, 0, 0, time.UTC)
		product := newEntityType(t, "product")
		accessory := newEntityType(t, "accessory")
		attrSet := newAttrSet(t, "rel_compatible_attrs")

		input := NewDefinitionInput{
			InternalName: "compatible_with",
			DisplayName:  "Compatible with",
			Kind:         KindSymmetric,
			ParentType:   product,
			ChildType:    accessory,
			AttributeSet: attrSet,
		}

		Convey("When a symmetric definition is created", func() {
			def, _, err := NewDefinition(input, now)

			Convey("Then it is symmetric with latest-only policies", func() {
				So(err, ShouldBeNil)
				So(def.IsSymmetric(), ShouldBeTrue)
				So(def.ParentVersionPolicy(), ShouldEqual, PolicyLatest)
			})

			Convey("And links canonicalize the unordered pair", func() {
				rel, _, err := Link(LinkInput{Definition: def, ParentEntity: "zz-9", ChildEntity: "aa-1"}, now)
				So(err, ShouldBeNil)
				So(rel.ParentEntityID().String(), ShouldEqual, "aa-1")
				So(rel.ChildEntityID().String(), ShouldEqual, "zz-9")
			})
		})

		Convey("When a symmetric definition tries to pin versions", func() {
			input.ParentVersionPolicy = PolicyPinned
			_, _, err := NewDefinition(input, now)

			Convey("Then it is rejected — pinning is directional", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
			})
		})

		Convey("When a symmetric definition supplies side labels", func() {
			input.ParentLabel = "left"
			_, _, err := NewDefinition(input, now)

			Convey("Then it is rejected — roles are undefined on an unordered pair", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
			})
		})

		Convey("When a directed definition declares role labels", func() {
			directed := NewDefinitionInput{
				InternalName: "uses",
				DisplayName:  "Uses",
				ParentType:   product,
				ChildType:    accessory,
				ParentLabel:  "assembly",
				ChildLabel:   "component",
				AttributeSet: newAttrSet(t, "rel_uses_attrs"),
			}
			def, _, err := NewDefinition(directed, now)

			Convey("Then the labels persist for display", func() {
				So(err, ShouldBeNil)
				So(def.ParentLabel(), ShouldEqual, "assembly")
				So(def.ChildLabel(), ShouldEqual, "component")
			})
		})

		Convey("When a definition extends a base of a different kind", func() {
			base, _, err := NewDefinition(NewDefinitionInput{
				InternalName: "related_to",
				DisplayName:  "Related to",
				ParentType:   product,
				ChildType:    accessory,
				AttributeSet: newAttrSet(t, "rel_related_attrs"),
			}, now)
			So(err, ShouldBeNil)

			input.Extends = base
			_, _, err = NewDefinition(input, now)

			Convey("Then it is rejected", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
			})
		})
	})
}
