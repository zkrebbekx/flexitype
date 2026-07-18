package relationship

import (
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

// baseDefinitionInput builds a valid directed definition input.
func baseDefinitionInput(t *testing.T) NewDefinitionInput {
	t.Helper()
	return NewDefinitionInput{
		InternalName: "uses",
		DisplayName:  "Uses",
		ParentType:   newEntityType(t, "assembly"),
		ChildType:    newEntityType(t, "part"),
		AttributeSet: newAttrSet(t, "rel_uses_attrs"),
	}
}

func TestNewDefinitionEndpointGuards(t *testing.T) {
	Convey("Given a relationship definition being created", t, func() {
		now := time.Date(2026, 7, 18, 10, 0, 0, 0, time.UTC)

		Convey("When an endpoint type is missing", func() {
			in := baseDefinitionInput(t)
			in.ParentType = nil
			_, _, err := NewDefinition(in, now)

			Convey("Then creation is rejected", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "parent and child types are required")
			})
		})

		Convey("When the child endpoint is missing", func() {
			in := baseDefinitionInput(t)
			in.ChildType = nil
			_, _, err := NewDefinition(in, now)

			Convey("Then creation is rejected", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
			})
		})

		Convey("When an endpoint is a relationship attribute set rather than an entity type", func() {
			in := baseDefinitionInput(t)
			in.ParentType = newAttrSet(t, "rel_other_attrs")
			_, _, err := NewDefinition(in, now)

			Convey("Then creation is rejected", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "endpoints must be entity types")
			})
		})

		Convey("When an endpoint type is archived", func() {
			in := baseDefinitionInput(t)
			_, err := in.ChildType.Archive(now)
			So(err, ShouldBeNil)

			_, _, err2 := NewDefinition(in, now)

			Convey("Then creation is rejected", func() {
				So(domainerrors.IsValidation(err2), ShouldBeTrue)
				So(err2.Error(), ShouldContainSubstring, "must not be archived")
			})
		})

		Convey("When the display name is missing", func() {
			in := baseDefinitionInput(t)
			in.DisplayName = ""
			_, _, err := NewDefinition(in, now)

			Convey("Then creation is rejected", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
			})
		})
	})
}

func TestNewDefinitionInheritanceGuards(t *testing.T) {
	Convey("Given a base relationship definition", t, func() {
		now := time.Date(2026, 7, 18, 10, 0, 0, 0, time.UTC)
		in := baseDefinitionInput(t)
		base, _, err := NewDefinition(in, now)
		So(err, ShouldBeNil)

		Convey("When an extending definition reuses the same endpoints and kind", func() {
			ext := in
			ext.InternalName = "uses_special"
			ext.AttributeSet = newAttrSet(t, "rel_uses_special_attrs")
			ext.Extends = base

			def, _, err := NewDefinition(ext, now)

			Convey("Then it is accepted and records its base", func() {
				So(err, ShouldBeNil)
				So(def.ExtendsID(), ShouldNotBeNil)
				So(def.ExtendsID().Equals(base.ID()), ShouldBeTrue)
			})
		})

		Convey("When the base definition is archived", func() {
			_, err := base.Archive(now)
			So(err, ShouldBeNil)

			ext := in
			ext.InternalName = "uses_special"
			ext.AttributeSet = newAttrSet(t, "rel_uses_special_attrs")
			ext.Extends = base

			_, _, err2 := NewDefinition(ext, now)

			Convey("Then extending it is rejected", func() {
				So(domainerrors.IsValidation(err2), ShouldBeTrue)
				So(err2.Error(), ShouldContainSubstring, "archived")
			})
		})

		Convey("When the extending definition has a different kind", func() {
			ext := baseDefinitionInput(t)
			ext.InternalName = "uses_special"
			ext.Kind = KindSymmetric
			ext.Extends = base

			_, _, err := NewDefinition(ext, now)

			Convey("Then the kind mismatch is rejected", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "same kind")
			})
		})

		Convey("When the extending definition connects different endpoint types", func() {
			ext := baseDefinitionInput(t) // fresh, unrelated endpoint types
			ext.InternalName = "uses_special"
			ext.Extends = base

			_, _, err := NewDefinition(ext, now)

			Convey("Then the endpoint mismatch is rejected", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "same parent and child types")
			})
		})
	})
}

func TestDefinitionUpdateGuards(t *testing.T) {
	Convey("Given an existing relationship definition", t, func() {
		now := time.Date(2026, 7, 18, 10, 0, 0, 0, time.UTC)
		def, _, err := NewDefinition(baseDefinitionInput(t), now)
		So(err, ShouldBeNil)

		Convey("When it is updated with an invalid version policy", func() {
			_, err := def.Update(UpdateDefinitionInput{
				DisplayName: "Uses", ParentVersionPolicy: VersionPolicy("sometimes"),
			}, now.Add(time.Hour))

			Convey("Then the update is rejected", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "version policies")
			})
		})

		Convey("When it is updated with no changes at all", func() {
			evts, err := def.Update(UpdateDefinitionInput{DisplayName: "Uses"}, now.Add(time.Hour))

			Convey("Then it is a no-op emitting no event", func() {
				So(err, ShouldBeNil)
				So(evts, ShouldBeEmpty)
			})
		})

		Convey("When cardinality bounds are set", func() {
			minC, maxC := 1, 5
			evts, err := def.Update(UpdateDefinitionInput{
				DisplayName: "Uses", MinChildren: &minC, MaxChildren: &maxC,
			}, now.Add(time.Hour))

			Convey("Then the bounds are stored and an updated event is emitted", func() {
				So(err, ShouldBeNil)
				So(evts, ShouldHaveLength, 1)
				So(*def.MinChildren(), ShouldEqual, 1)
				So(*def.MaxChildren(), ShouldEqual, 5)
				So(def.UpdatedAt(), ShouldEqual, now.Add(time.Hour))
			})
		})
	})
}

func TestSymmetricDefinitionUpdateGuards(t *testing.T) {
	Convey("Given a symmetric relationship definition", t, func() {
		now := time.Date(2026, 7, 18, 10, 0, 0, 0, time.UTC)
		in := baseDefinitionInput(t)
		in.Kind = KindSymmetric
		def, _, err := NewDefinition(in, now)
		So(err, ShouldBeNil)

		Convey("When an update tries to pin a version", func() {
			_, err := def.Update(UpdateDefinitionInput{
				DisplayName: "Uses", ParentVersionPolicy: PolicyPinned,
			}, now.Add(time.Hour))

			Convey("Then it is rejected: pinning is inherently directional", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "cannot pin versions")
			})
		})

		Convey("When an update tries to set a side label", func() {
			_, err := def.Update(UpdateDefinitionInput{
				DisplayName: "Uses", ParentLabel: "parents",
			}, now.Add(time.Hour))

			Convey("Then it is rejected: roles are undefined on an unordered pair", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "side labels")
			})
		})
	})
}

func TestLinkGuards(t *testing.T) {
	Convey("Given a relationship definition and two entities", t, func() {
		now := time.Date(2026, 7, 18, 10, 0, 0, 0, time.UTC)
		def, _, err := NewDefinition(baseDefinitionInput(t), now)
		So(err, ShouldBeNil)

		Convey("When the definition is missing", func() {
			_, _, err := Link(LinkInput{
				ParentEntity: "a", ChildEntity: "b",
			}, now)

			Convey("Then linking is rejected", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "definition is required")
			})
		})

		Convey("When the definition is archived", func() {
			_, err := def.Archive(now)
			So(err, ShouldBeNil)

			_, _, err2 := Link(LinkInput{
				Definition: def, ParentEntity: "a", ChildEntity: "b",
			}, now)

			Convey("Then linking is rejected as archived", func() {
				So(domainerrors.IsArchived(err2), ShouldBeTrue)
			})
		})

		Convey("When an entity id is missing", func() {
			_, _, err := Link(LinkInput{
				Definition: def, ParentEntity: "", ChildEntity: "b",
			}, now)

			Convey("Then linking is rejected", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "entity IDs are required")
			})
		})

		Convey("When a pinned side supplies no version", func() {
			in := baseDefinitionInput(t)
			in.ParentVersionPolicy = PolicyPinned
			pinned, _, err := NewDefinition(in, now)
			So(err, ShouldBeNil)

			_, _, err2 := Link(LinkInput{
				Definition: pinned, ParentEntity: "a", ChildEntity: "b",
			}, now)

			Convey("Then linking is rejected", func() {
				So(domainerrors.IsValidation(err2), ShouldBeTrue)
				So(err2.Error(), ShouldContainSubstring, "must pin a type version")
			})
		})

		Convey("When a pinned side supplies a version below 1", func() {
			in := baseDefinitionInput(t)
			in.ParentVersionPolicy = PolicyPinned
			pinned, _, err := NewDefinition(in, now)
			So(err, ShouldBeNil)

			zero := 0
			_, _, err2 := Link(LinkInput{
				Definition: pinned, ParentEntity: "a", ChildEntity: "b", ParentVersion: &zero,
			}, now)

			Convey("Then linking is rejected", func() {
				So(domainerrors.IsValidation(err2), ShouldBeTrue)
				So(err2.Error(), ShouldContainSubstring, "at least 1")
			})
		})

		Convey("When a latest-policy side supplies a version pin", func() {
			v := 3
			_, _, err := Link(LinkInput{
				Definition: def, ParentEntity: "a", ChildEntity: "b", ParentVersion: &v,
			}, now)

			Convey("Then linking is rejected", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "cannot pin")
			})
		})
	})
}

func TestRePinGuards(t *testing.T) {
	Convey("Given a link on a pinned-policy definition", t, func() {
		now := time.Date(2026, 7, 18, 10, 0, 0, 0, time.UTC)
		in := baseDefinitionInput(t)
		in.ParentVersionPolicy = PolicyPinned
		def, _, err := NewDefinition(in, now)
		So(err, ShouldBeNil)

		v1 := 1
		rel, _, err := Link(LinkInput{
			Definition: def, ParentEntity: "a", ChildEntity: "b", ParentVersion: &v1,
		}, now)
		So(err, ShouldBeNil)

		Convey("When it is re-pinned to a new version", func() {
			v2 := 2
			evts, err := rel.RePin(def, &v2, nil, now.Add(time.Hour))

			Convey("Then the pin moves and a re-pinned event is emitted", func() {
				So(err, ShouldBeNil)
				So(evts, ShouldHaveLength, 1)
				So(*rel.ParentVersion(), ShouldEqual, 2)
			})
		})

		Convey("When it is re-pinned to the identical versions", func() {
			same := 1
			evts, err := rel.RePin(def, &same, nil, now.Add(time.Hour))

			Convey("Then it is a no-op emitting no event", func() {
				So(err, ShouldBeNil)
				So(evts, ShouldBeEmpty)
			})
		})

		Convey("When it is re-pinned with a mismatched definition", func() {
			other, _, err := NewDefinition(baseDefinitionInput(t), now)
			So(err, ShouldBeNil)

			v2 := 2
			_, err2 := rel.RePin(other, &v2, nil, now.Add(time.Hour))

			Convey("Then it is rejected so a link cannot be re-pointed", func() {
				So(domainerrors.IsValidation(err2), ShouldBeTrue)
				So(err2.Error(), ShouldContainSubstring, "does not match")
			})
		})

		Convey("When it is re-pinned with a nil definition", func() {
			v2 := 2
			_, err := rel.RePin(nil, &v2, nil, now.Add(time.Hour))

			Convey("Then it is rejected", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
			})
		})

		Convey("When it is re-pinned violating the policy", func() {
			_, err := rel.RePin(def, nil, nil, now.Add(time.Hour))

			Convey("Then the policy check rejects the missing pin", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "must pin")
			})
		})

		Convey("When the link is unlinked", func() {
			_, err := rel.Unlink(now.Add(time.Hour))
			So(err, ShouldBeNil)

			Convey("Then it reports the archive instant", func() {
				So(rel.IsArchived(), ShouldBeTrue)
				So(*rel.ArchivedAt(), ShouldEqual, now.Add(time.Hour))
				So(rel.UpdatedAt(), ShouldEqual, now.Add(time.Hour))
			})

			Convey("Then unlinking again is rejected", func() {
				_, err := rel.Unlink(now.Add(2 * time.Hour))
				So(domainerrors.IsArchived(err), ShouldBeTrue)
			})

			Convey("Then re-pinning an archived link is rejected", func() {
				v2 := 2
				_, err := rel.RePin(def, &v2, nil, now.Add(2*time.Hour))
				So(domainerrors.IsArchived(err), ShouldBeTrue)
			})
		})
	})
}

func TestSymmetricLinkCanonicalOrdering(t *testing.T) {
	Convey("Given a symmetric relationship definition", t, func() {
		now := time.Date(2026, 7, 18, 10, 0, 0, 0, time.UTC)
		in := baseDefinitionInput(t)
		in.Kind = KindSymmetric
		def, _, err := NewDefinition(in, now)
		So(err, ShouldBeNil)

		Convey("When a pair is supplied in reverse lexical order", func() {
			rel, _, err := Link(LinkInput{
				Definition: def, ParentEntity: "zeta", ChildEntity: "alpha",
			}, now)

			Convey("Then the endpoints are stored in canonical order", func() {
				So(err, ShouldBeNil)
				So(rel.ParentEntityID(), ShouldEqual, valueobjects.EntityID("alpha"))
				So(rel.ChildEntityID(), ShouldEqual, valueobjects.EntityID("zeta"))
			})
		})

		Convey("When the same pair is supplied in forward order", func() {
			rel, _, err := Link(LinkInput{
				Definition: def, ParentEntity: "alpha", ChildEntity: "zeta",
			}, now)

			Convey("Then it stores identically, making the unordered pair unique", func() {
				So(err, ShouldBeNil)
				So(rel.ParentEntityID(), ShouldEqual, valueobjects.EntityID("alpha"))
				So(rel.ChildEntityID(), ShouldEqual, valueobjects.EntityID("zeta"))
			})
		})
	})
}
