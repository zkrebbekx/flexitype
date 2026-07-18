package typedef

import (
	"strings"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/events"
)

func TestTypeDefinitionConstruction(t *testing.T) {
	Convey("Given the type-definition constructor", t, func() {
		now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)

		Convey("When the internal name is malformed", func() {
			Convey("Then non-snake_case, too-short and too-long names are rejected", func() {
				for _, name := range []string{"", "P", "Product", "1product", "pro-duct", "pro duct",
					strings.Repeat("a", 64)} {
					_, _, err := New(NewInput{InternalName: name, DisplayName: "X"}, now)
					So(domainerrors.IsValidation(err), ShouldBeTrue)
				}
			})
		})

		Convey("When the display name is missing", func() {
			_, _, err := New(NewInput{InternalName: "product"}, now)

			Convey("Then construction is rejected", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "display name is required")
			})
		})

		Convey("When a valid type is created", func() {
			td, evts, err := New(NewInput{
				InternalName: "product", DisplayName: "Product", Description: "sellable thing",
			}, now)

			Convey("Then it defaults to the default tenant, an entity kind and version 1", func() {
				So(err, ShouldBeNil)
				So(td.TenantID(), ShouldEqual, valueobjects.DefaultTenant)
				So(td.Kind(), ShouldEqual, KindEntity)
				So(td.InternalName(), ShouldEqual, "product")
				So(td.DisplayName(), ShouldEqual, "Product")
				So(td.Description(), ShouldEqual, "sellable thing")
				So(td.ExtendsID(), ShouldBeNil)
				So(td.Version(), ShouldEqual, 1)
				So(td.CreatedAt(), ShouldEqual, now)
				So(td.UpdatedAt(), ShouldEqual, now)
				So(td.ArchivedAt(), ShouldBeNil)
				So(td.IsArchived(), ShouldBeFalse)

				So(evts, ShouldHaveLength, 1)
				So(evts[0].AggregateType(), ShouldEqual, AggregateType)
				So(evts[0].AggregateID(), ShouldEqual, td.ID().String())
				So(evts[0].OccurredWhen(), ShouldEqual, now)
			})
		})

		Convey("When a type extends another", func() {
			parent, _, err := New(NewInput{InternalName: "product", DisplayName: "Product"}, now)
			So(err, ShouldBeNil)

			child, _, err := New(NewInput{
				InternalName: "bike", DisplayName: "Bike", Extends: parent,
			}, now)

			Convey("Then the parent is recorded on the subtype", func() {
				So(err, ShouldBeNil)
				So(child.ExtendsID(), ShouldNotBeNil)
				So(child.ExtendsID().Equals(parent.ID()), ShouldBeTrue)
			})
		})

		Convey("When the extended type is unsuitable", func() {
			parent, _, err := New(NewInput{InternalName: "product", DisplayName: "Product"}, now)
			So(err, ShouldBeNil)

			Convey("Then attribute sets, other tenants and archived types are all refused", func() {
				set, _, err := NewAttributeSet(valueobjects.DefaultTenant, "rel_x_attrs", "X", now)
				So(err, ShouldBeNil)
				_, _, err = New(NewInput{InternalName: "bike", DisplayName: "Bike", Extends: set}, now)
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "only entity types can be extended")

				foreign, _, err := New(NewInput{
					TenantID: valueobjects.TenantID("acme"), InternalName: "product", DisplayName: "P",
				}, now)
				So(err, ShouldBeNil)
				_, _, err = New(NewInput{InternalName: "bike", DisplayName: "Bike", Extends: foreign}, now)
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "same tenant")

				_, err = parent.Archive(now)
				So(err, ShouldBeNil)
				_, _, err = New(NewInput{InternalName: "bike", DisplayName: "Bike", Extends: parent}, now)
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "archived")
			})
		})

		Convey("When a relationship attribute set is created", func() {
			set, evts, err := NewAttributeSet(valueobjects.TenantID("acme"), "rel_contains_attrs", "Contains", now)

			Convey("Then it carries the hidden kind and its own tenant", func() {
				So(err, ShouldBeNil)
				So(set.Kind(), ShouldEqual, KindRelationshipAttributes)
				So(set.TenantID(), ShouldEqual, valueobjects.TenantID("acme"))
				So(evts, ShouldHaveLength, 1)
			})

			Convey("And a malformed name is still rejected", func() {
				_, _, err := NewAttributeSet(valueobjects.DefaultTenant, "Rel Attrs", "X", now)
				So(domainerrors.IsValidation(err), ShouldBeTrue)
			})
		})
	})
}

func TestTypeDefinitionLifecycle(t *testing.T) {
	Convey("Given a live type definition", t, func() {
		now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
		later := now.Add(time.Hour)
		td, _, err := New(NewInput{
			InternalName: "product", DisplayName: "Product", Description: "original",
		}, now)
		So(err, ShouldBeNil)

		Convey("When its human-facing fields change", func() {
			evts, err := td.Update("Catalogue Product", "revised", later)

			Convey("Then the version bumps and an update event is emitted", func() {
				So(err, ShouldBeNil)
				So(td.DisplayName(), ShouldEqual, "Catalogue Product")
				So(td.Description(), ShouldEqual, "revised")
				So(td.Version(), ShouldEqual, 2)
				So(td.UpdatedAt(), ShouldEqual, later)
				So(evts, ShouldHaveLength, 1)
				So(evts[0].AggregateID(), ShouldEqual, td.ID().String())
				So(evts[0].OccurredWhen(), ShouldEqual, later)
			})
		})

		Convey("When an update changes nothing", func() {
			evts, err := td.Update("Product", "original", later)

			Convey("Then no event is emitted and the version holds", func() {
				So(err, ShouldBeNil)
				So(evts, ShouldBeEmpty)
				So(td.Version(), ShouldEqual, 1)
				So(td.UpdatedAt(), ShouldEqual, now)
			})
		})

		Convey("When an update drops the display name", func() {
			_, err := td.Update("", "x", later)

			Convey("Then it is rejected and the aggregate is untouched", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(td.DisplayName(), ShouldEqual, "Product")
			})
		})

		Convey("When a live type is restored", func() {
			_, err := td.Restore(later)

			Convey("Then it is refused — there is nothing to restore", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "not archived")
			})
		})

		Convey("When it is archived", func() {
			evts, err := td.Archive(later)

			Convey("Then it is marked archived and an event is emitted", func() {
				So(err, ShouldBeNil)
				So(td.IsArchived(), ShouldBeTrue)
				So(*td.ArchivedAt(), ShouldEqual, later)
				So(evts, ShouldHaveLength, 1)
				So(evts[0].EventType(), ShouldNotEqual, events.Type(""))
			})

			Convey("And archiving or updating again is refused", func() {
				_, err := td.Archive(later)
				So(domainerrors.IsArchived(err), ShouldBeTrue)
				_, err = td.Update("X", "", later)
				So(domainerrors.IsArchived(err), ShouldBeTrue)
			})

			Convey("And restoring clears the archive marker and emits an event", func() {
				evts, err := td.Restore(later.Add(time.Hour))
				So(err, ShouldBeNil)
				So(td.IsArchived(), ShouldBeFalse)
				So(td.ArchivedAt(), ShouldBeNil)
				So(evts, ShouldHaveLength, 1)
				So(evts[0].AggregateType(), ShouldEqual, AggregateType)

				// Mutations work again once restored.
				_, err = td.Update("Restored", "", later.Add(2*time.Hour))
				So(err, ShouldBeNil)
			})
		})

		Convey("When it round-trips through a snapshot", func() {
			_, err := td.Update("Catalogue Product", "revised", later)
			So(err, ShouldBeNil)
			restored := Rehydrate(td.Snapshot())

			Convey("Then every field survives storage", func() {
				So(restored.ID().Equals(td.ID()), ShouldBeTrue)
				So(restored.TenantID(), ShouldEqual, td.TenantID())
				So(restored.Kind(), ShouldEqual, KindEntity)
				So(restored.InternalName(), ShouldEqual, "product")
				So(restored.DisplayName(), ShouldEqual, "Catalogue Product")
				So(restored.Description(), ShouldEqual, "revised")
				So(restored.Version(), ShouldEqual, 2)
				So(restored.CreatedAt(), ShouldEqual, now)
				So(restored.UpdatedAt(), ShouldEqual, later)
				So(restored.IsArchived(), ShouldBeFalse)
				So(restored.ExtendsID(), ShouldBeNil)
			})
		})

		Convey("When an archived subtype round-trips through a snapshot", func() {
			child, _, err := New(NewInput{InternalName: "bike", DisplayName: "Bike", Extends: td}, now)
			So(err, ShouldBeNil)
			_, err = child.Archive(later)
			So(err, ShouldBeNil)

			restored := Rehydrate(child.Snapshot())

			Convey("Then the parent pointer and archive marker both survive", func() {
				So(restored.ExtendsID(), ShouldNotBeNil)
				So(restored.ExtendsID().Equals(td.ID()), ShouldBeTrue)
				So(restored.IsArchived(), ShouldBeTrue)
				So(*restored.ArchivedAt(), ShouldEqual, later)
			})
		})
	})
}
