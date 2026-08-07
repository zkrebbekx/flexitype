package flexitype_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

func TestRevisionInputValidation(t *testing.T) {
	Convey("Given an entity-revision interactor over an in-memory service", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory()
		it := svc.Interactors(ctx)
		revs := it.Revisions()

		product, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "product", DisplayName: "Product",
		})
		So(err, ShouldBeNil)

		Convey("When a revision is created for a malformed type id", func() {
			_, err := revs.Create(ctx, "not-a-ulid", "e1", "")

			Convey("Then it is rejected as a validation error", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
			})
		})

		Convey("When a revision is created for a malformed entity id", func() {
			_, err := revs.Create(ctx, product.ID.String(), "", "")

			Convey("Then it is rejected as a validation error", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
			})
		})

		Convey("When a revision is created for a type that does not exist", func() {
			_, err := revs.Create(ctx, valueobjects.NewTypeDefinitionID().String(), "e1", "")

			Convey("Then the missing type surfaces as an error", func() {
				So(err, ShouldNotBeNil)
			})
		})

		Convey("When a revision is fetched by a malformed id", func() {
			_, err := revs.Get(ctx, "not-a-ulid")

			Convey("Then it is rejected as a validation error", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
			})
		})

		Convey("When a diff is requested with a malformed revision id", func() {
			_, err := revs.Diff(ctx, "not-a-ulid", "also-not-a-ulid")

			Convey("Then it is rejected as a validation error", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
			})
		})

		Convey("When a restore is requested with a malformed revision id", func() {
			_, err := revs.Restore(ctx, "not-a-ulid")

			Convey("Then it is rejected as a validation error", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
			})
		})

		Convey("When a point-in-time read finds no revision at or before the instant", func() {
			_, err := revs.AsOf(ctx, product.ID.String(), "e1", time.Now().Add(-24*time.Hour))

			Convey("Then it reports not found", func() {
				So(err, ShouldNotBeNil)
			})
		})

		Convey("When revisions are listed for an entity that has none", func() {
			out, err := revs.List(ctx, product.ID.String(), "nobody")

			Convey("Then the list is empty rather than an error", func() {
				So(err, ShouldBeNil)
				So(out, ShouldBeEmpty)
			})
		})
	})
}

// TestRevisionRestoreAfterNarrowing covers issue #479: a restore after the
// entity's anchor narrows to a subtype.
//
// snapshotValues and the archive pass of ApplySnapshot read the entity
// through the type-keyed ListByEntity, keyed on the revision's captured type.
// Narrowing re-anchors every value row to the subtype, so the captured type
// matched zero rows. The archive pass archived nothing — the restore
// silently kept post-snapshot values — and the follow-up capture recorded an
// empty entity.
func TestRevisionRestoreAfterNarrowing(t *testing.T) {
	Convey("Given a vehicle entity captured before its anchor narrows to car", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory()
		newIt := svc.Interactors // fresh request-scoped interactor per unit of work

		vehicle, err := newIt(ctx).TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "vehicle", DisplayName: "Vehicle",
		})
		So(err, ShouldBeNil)
		car, err := newIt(ctx).TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "car", DisplayName: "Car", ExtendsID: vehicle.ID.String(),
		})
		So(err, ShouldBeNil)
		vin, err := newIt(ctx).Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: vehicle.ID.String(), InternalName: "vin",
			DisplayName: "VIN", DataType: "string",
		})
		So(err, ShouldBeNil)
		trim, err := newIt(ctx).Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: car.ID.String(), InternalName: "trim",
			DisplayName: "Trim", DataType: "string",
		})
		So(err, ShouldBeNil)

		// Write vin with no type: the entity anchors to vehicle, the
		// declaring type.
		_, err = newIt(ctx).Values().Set(ctx, appvalue.SetInput{
			AttributeDefinitionID: vin.ID.String(), EntityID: "e1",
			Value: json.RawMessage(`"VIN123"`),
		})
		So(err, ShouldBeNil)

		r1, err := newIt(ctx).Revisions().Create(ctx, vehicle.ID.String(), "e1", "before narrowing")
		So(err, ShouldBeNil)
		So(r1.Values, ShouldHaveLength, 1)

		// Write trim naming car: the anchor narrows and both rows move.
		_, err = newIt(ctx).Values().Set(ctx, appvalue.SetInput{
			AttributeDefinitionID: trim.ID.String(), EntityID: "e1",
			TypeDefinitionID: car.ID.String(), Value: json.RawMessage(`"GT"`),
		})
		So(err, ShouldBeNil)
		liveBefore, err := newIt(ctx).Values().ListByEntity(ctx, car.ID.String(), "e1")
		So(err, ShouldBeNil)
		So(liveBefore, ShouldHaveLength, 2)

		Convey("When the pre-narrowing revision is restored", func() {
			r2, err := newIt(ctx).Revisions().Restore(ctx, r1.ID.String())
			So(err, ShouldBeNil)

			Convey("Then the live values match the snapshot: vin stays, trim is archived", func() {
				live, err := newIt(ctx).Values().ListByEntity(ctx, car.ID.String(), "e1")
				So(err, ShouldBeNil)
				So(live, ShouldHaveLength, 1)
				So(live[0].AttributeDefinitionID.String(), ShouldEqual, vin.ID.String())
				So(live[0].Value.String(), ShouldEqual, "VIN123")
			})

			Convey("Then the follow-up revision records exactly the restored value set", func() {
				So(r2.Values, ShouldHaveLength, 1)
				So(r2.Values[0].InternalName, ShouldEqual, "vin")
				So(r2.Values[0].Value, ShouldEqual, "VIN123")
			})

			Convey("Then the follow-up revision carries the entity's current anchor", func() {
				So(r2.TypeDefinitionID, ShouldEqual, car.ID.String())
			})

			Convey("Then restoring the follow-up revision round-trips", func() {
				r3, err := newIt(ctx).Revisions().Restore(ctx, r2.ID.String())
				So(err, ShouldBeNil)
				So(r3.Values, ShouldHaveLength, 1)
				So(r3.Values[0].InternalName, ShouldEqual, "vin")
				So(r3.Values[0].Value, ShouldEqual, "VIN123")

				live, err := newIt(ctx).Values().ListByEntity(ctx, car.ID.String(), "e1")
				So(err, ShouldBeNil)
				So(live, ShouldHaveLength, 1)
				So(live[0].Value.String(), ShouldEqual, "VIN123")
			})
		})
	})
}

func TestRevisionLifecycle(t *testing.T) {
	Convey("Given an entity with scoped and base values", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory()
		it := svc.Interactors(ctx)

		product, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "product", DisplayName: "Product",
		})
		So(err, ShouldBeNil)

		mk := func(name, dt string, localizable bool) string {
			a, e := it.Attributes().Create(ctx, appattribute.CreateInput{
				TypeDefinitionID: product.ID.String(), InternalName: name,
				DisplayName: name, DataType: dt, Localizable: localizable,
			})
			So(e, ShouldBeNil)
			return a.ID.String()
		}
		nameA := mk("name", "string", true)
		skuA := mk("sku", "string", false)

		set := func(attrID, raw, locale string) string {
			v, e := svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: attrID, EntityID: "e1",
				TypeDefinitionID: product.ID.String(),
				Value:            json.RawMessage(raw), Locale: locale,
			})
			So(e, ShouldBeNil)
			return v.ID.String()
		}

		skuValueID := set(skuA, `"SKU-1"`, "")
		set(nameA, `"Widget"`, "")
		set(nameA, `"Gerät"`, "de-DE")

		revs := svc.Interactors(ctx).Revisions()

		Convey("When the entity's current state is captured", func() {
			rev, err := revs.Create(ctx, product.ID.String(), "e1", "first cut")
			So(err, ShouldBeNil)

			Convey("Then the revision is sequence 1 and captures every scope separately", func() {
				So(rev.Seq, ShouldEqual, 1)
				So(rev.Label, ShouldEqual, "first cut")
				So(rev.EntityID, ShouldEqual, "e1")
				So(rev.Values, ShouldHaveLength, 3)

				// Sorted by internal name, then locale, then channel.
				So(rev.Values[0].InternalName, ShouldEqual, "name")
				So(rev.Values[0].Locale, ShouldEqual, "")
				So(rev.Values[0].Value, ShouldEqual, "Widget")
				So(rev.Values[1].InternalName, ShouldEqual, "name")
				So(rev.Values[1].Locale, ShouldEqual, "de-DE")
				So(rev.Values[1].Value, ShouldEqual, "Gerät")
				So(rev.Values[2].InternalName, ShouldEqual, "sku")
			})
		})

		Convey("When a second revision follows a change", func() {
			first, err := revs.Create(ctx, product.ID.String(), "e1", "v1")
			So(err, ShouldBeNil)

			set(nameA, `"Widget Pro"`, "")     // changed
			set(nameA, `"Gerät Pro"`, "de-DE") // changed at its own scope
			skuB := mk("colour", "string", false)
			set(skuB, `"red"`, "") // added

			second, err := revs.Create(ctx, product.ID.String(), "e1", "v2")
			So(err, ShouldBeNil)

			Convey("Then the sequence increments", func() {
				So(second.Seq, ShouldEqual, 2)
			})

			Convey("Then listing returns metadata only, without value payloads", func() {
				list, err := revs.List(ctx, product.ID.String(), "e1")
				So(err, ShouldBeNil)
				So(list, ShouldHaveLength, 2)
				for _, r := range list {
					So(r.Values, ShouldBeNil)
				}
			})

			Convey("Then fetching one revision returns its full value snapshot", func() {
				got, err := revs.Get(ctx, first.ID.String())
				So(err, ShouldBeNil)
				So(got.Seq, ShouldEqual, 1)
				So(got.Values, ShouldHaveLength, 3)
			})

			Convey("Then diffing the two reports each scope's change independently", func() {
				diff, err := revs.Diff(ctx, first.ID.String(), second.ID.String())
				So(err, ShouldBeNil)
				So(diff.FromSeq, ShouldEqual, 1)
				So(diff.ToSeq, ShouldEqual, 2)

				kinds := map[string]string{}
				for _, c := range diff.Changes {
					kinds[c.InternalName+"|"+c.Locale] = c.Kind
				}
				So(kinds["name|"], ShouldEqual, "changed")
				So(kinds["name|de-DE"], ShouldEqual, "changed")
				So(kinds["colour|"], ShouldEqual, "added")

				// Ordering is stable: name, then by locale within the attribute.
				var names []string
				for _, c := range diff.Changes {
					names = append(names, c.InternalName+"|"+c.Locale)
				}
				So(names, ShouldResemble, []string{"colour|", "name|", "name|de-DE"})

				for _, c := range diff.Changes {
					if c.InternalName == "name" && c.Locale == "" {
						So(c.Before, ShouldEqual, "Widget")
						So(c.After, ShouldEqual, "Widget Pro")
					}
				}
			})

			Convey("Then restoring the first revision creates new forward state", func() {
				restored, err := revs.Restore(ctx, first.ID.String())
				So(err, ShouldBeNil)

				Convey("And history is never mutated: a new revision is appended", func() {
					So(restored.Seq, ShouldEqual, 3)
					So(restored.Label, ShouldContainSubstring, "restored from revision 1")

					list, err := revs.List(ctx, product.ID.String(), "e1")
					So(err, ShouldBeNil)
					So(list, ShouldHaveLength, 3)
				})

				Convey("And the live values match the restored revision at every scope", func() {
					diff, err := revs.Diff(ctx, first.ID.String(), restored.ID.String())
					So(err, ShouldBeNil)

					// The only difference may be the attribute added after v1,
					// which restore removes or leaves empty — the shared
					// attributes must all match.
					for _, c := range diff.Changes {
						So(c.InternalName, ShouldEqual, "colour")
					}
				})
			})

			Convey("Then a point-in-time read returns the latest revision at that instant", func() {
				asOf, err := revs.AsOf(ctx, product.ID.String(), "e1", time.Now().Add(time.Minute))
				So(err, ShouldBeNil)
				So(asOf.Seq, ShouldEqual, 2)
			})
		})

		Convey("When two revisions are identical", func() {
			a, err := revs.Create(ctx, product.ID.String(), "e1", "a")
			So(err, ShouldBeNil)
			b, err := revs.Create(ctx, product.ID.String(), "e1", "b")
			So(err, ShouldBeNil)

			Convey("Then their diff is empty, not nil", func() {
				diff, err := revs.Diff(ctx, a.ID.String(), b.ID.String())
				So(err, ShouldBeNil)
				So(diff.Changes, ShouldNotBeNil)
				So(diff.Changes, ShouldBeEmpty)
			})
		})

		Convey("When a value is removed between revisions", func() {
			before, err := revs.Create(ctx, product.ID.String(), "e1", "before")
			So(err, ShouldBeNil)

			_, err = svc.Interactors(ctx).Values().Remove(ctx, skuValueID)
			So(err, ShouldBeNil)

			after, err := revs.Create(ctx, product.ID.String(), "e1", "after")
			So(err, ShouldBeNil)

			Convey("Then the diff reports it as removed, carrying its previous value", func() {
				diff, err := revs.Diff(ctx, before.ID.String(), after.ID.String())
				So(err, ShouldBeNil)

				var found bool
				for _, c := range diff.Changes {
					if c.InternalName == "sku" {
						found = true
						So(c.Kind, ShouldEqual, "removed")
						So(c.Before, ShouldEqual, "SKU-1")
						So(c.After, ShouldEqual, "")
					}
				}
				So(found, ShouldBeTrue)
			})
		})
	})
}
