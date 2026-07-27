package flexitype_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	appunit "github.com/zkrebbekx/flexitype/application/unit"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/blob"
)

// TestRevisionRoundTripPostgres covers the data types a restore could not
// reconstruct, and the two ways a restore silently did not restore.
//
// Revisions stored each value as its DISPLAY string, which is not JSON for
// media (a bare object key) or quantity ("10 kg"). Feeding either back through
// the import decoder failed and aborted the whole unit of work, so any entity
// holding one of those two types had no working restore path at all — from the
// primitive an operator reaches for precisely when something has gone wrong.
func TestRevisionRoundTripPostgres(t *testing.T) {
	pool := openTestDB(t)
	svc := flexitype.New(pool, flexitype.WithBlobStore(blob.NewMemoryStore()))
	if err := svc.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	Convey("Given an entity holding media, quantity and a multi-valued attribute", t, func() {
		truncateAll(t, pool)
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		ia := svc.Interactors(ctx)

		mass, err := ia.Units().Create(ctx, appunit.CreateInput{
			Name: "mass", BaseUnit: "g",
			Units: map[string]float64{"g": 1, "kg": 1000},
		})
		So(err, ShouldBeNil)

		product, err := ia.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "product", DisplayName: "Product",
		})
		So(err, ShouldBeNil)
		typeID := product.ID.String()

		weight, err := ia.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: typeID, InternalName: "weight", DisplayName: "Weight",
			DataType: "quantity", UnitFamilyID: mass.ID.String(),
		})
		So(err, ShouldBeNil)
		datasheet, err := ia.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: typeID, InternalName: "datasheet", DisplayName: "Datasheet",
			DataType: "media",
		})
		So(err, ShouldBeNil)
		tags, err := ia.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: typeID, InternalName: "tags", DisplayName: "Tags",
			DataType: "string", MultiValued: true,
		})
		So(err, ShouldBeNil)

		set := func(attrID string, v any) error {
			raw, _ := json.Marshal(v)
			_, err := svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: attrID, EntityID: "p1", TypeDefinitionID: typeID, Value: raw,
			})
			return err
		}
		So(set(weight.ID.String(), map[string]any{"magnitude": "10", "unit": "kg"}), ShouldBeNil)
		So(set(tags.ID.String(), "alpha"), ShouldBeNil)
		_, err = svc.Interactors(ctx).Values().UploadMedia(ctx, typeID, "p1", datasheet.ID.String(),
			strings.NewReader("datasheet bytes"), "sheet.pdf")
		So(err, ShouldBeNil)

		rev, err := svc.Interactors(ctx).Revisions().Create(ctx, typeID, "p1", "baseline")
		So(err, ShouldBeNil)
		So(rev.Values, ShouldHaveLength, 3)

		Convey("Then the capture carries a round-trippable form for every value", func() {
			for _, v := range rev.Values {
				So(len(v.Typed), ShouldBeGreaterThan, 0)
				So(json.Valid(v.Typed), ShouldBeTrue)
			}
		})

		Convey("When the entity is changed and then restored", func() {
			So(set(weight.ID.String(), map[string]any{"magnitude": "25", "unit": "kg"}), ShouldBeNil)
			So(set(tags.ID.String(), "beta"), ShouldBeNil)

			_, err := svc.Interactors(ctx).Revisions().Restore(ctx, rev.ID.String())

			Convey("Then the restore succeeds instead of aborting on media or quantity", func() {
				So(err, ShouldBeNil)
			})

			Convey("Then every value is back to its captured state", func() {
				So(err, ShouldBeNil)
				vals, lerr := svc.Interactors(ctx).Values().ListByEntity(ctx, typeID, "p1")
				So(lerr, ShouldBeNil)

				byAttr := map[string][]string{}
				for _, v := range vals {
					byAttr[v.AttributeDefinitionID.String()] = append(
						byAttr[v.AttributeDefinitionID.String()], v.Value.String())
				}
				So(byAttr[weight.ID.String()], ShouldResemble, []string{"10 kg"})
				So(byAttr[tags.ID.String()], ShouldResemble, []string{"alpha"})
				So(byAttr[datasheet.ID.String()], ShouldHaveLength, 1)
			})
		})

		Convey("When a multi-valued attribute has GAINED a member since the capture", func() {
			So(set(tags.ID.String(), "beta"), ShouldBeNil)
			vals, err := svc.Interactors(ctx).Values().ListByEntity(ctx, typeID, "p1")
			So(err, ShouldBeNil)
			tagCount := 0
			for _, v := range vals {
				if v.AttributeDefinitionID.String() == tags.ID.String() {
					tagCount++
				}
			}
			So(tagCount, ShouldEqual, 2)

			_, err = svc.Interactors(ctx).Revisions().Restore(ctx, rev.ID.String())
			So(err, ShouldBeNil)

			Convey("Then the extra member is archived, not silently kept", func() {
				// Keying the target set on (attribute, scope) alone meant the
				// extra member satisfied the key, so nothing archived it and
				// the restore did not restore.
				after, err := svc.Interactors(ctx).Values().ListByEntity(ctx, typeID, "p1")
				So(err, ShouldBeNil)
				got := []string{}
				for _, v := range after {
					if v.AttributeDefinitionID.String() == tags.ID.String() {
						got = append(got, v.Value.String())
					}
				}
				So(got, ShouldResemble, []string{"alpha"})
			})
		})

		Convey("When two revisions differ by one member of a multi-valued attribute", func() {
			So(set(tags.ID.String(), "beta"), ShouldBeNil)
			rev2, err := svc.Interactors(ctx).Revisions().Create(ctx, typeID, "p1", "after")
			So(err, ShouldBeNil)

			diff, err := svc.Interactors(ctx).Revisions().Diff(ctx, rev.ID.String(), rev2.ID.String())
			So(err, ShouldBeNil)

			Convey("Then the added member is reported", func() {
				// Keying one Value per scope collapsed the members, so only
				// the last survived and the addition produced no change.
				added := []string{}
				for _, c := range diff.Changes {
					if c.InternalName == "tags" && c.Kind == "added" {
						added = append(added, c.After)
					}
				}
				So(added, ShouldResemble, []string{"beta"})
			})

			Convey("Then the member that did not change is not reported", func() {
				for _, c := range diff.Changes {
					So(c.After, ShouldNotEqual, "alpha")
				}
			})
		})
	})
}
