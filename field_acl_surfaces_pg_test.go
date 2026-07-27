package flexitype_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	"github.com/zkrebbekx/flexitype/application"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	appdedup "github.com/zkrebbekx/flexitype/application/dedup"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/blob"
)

// TestFieldACLReadSurfacesPostgres covers the read and write surfaces that
// returned attribute values without consulting the field ACL: the values
// list, revisions, the activity log, media download and duplicate detection,
// plus the entity cascade delete that archived values the principal could not
// write. Each Convey states the surface and the principal it must hold for.
func TestFieldACLReadSurfacesPostgres(t *testing.T) {
	pool := openTestDB(t)
	defer func() { _ = pool.Close() }()

	svc := flexitype.New(pool, flexitype.WithBlobStore(blob.NewMemoryStore()))
	if err := svc.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	Convey("Given a product with sku and a restricted cost attribute (Postgres)", t, func() {
		truncateAll(t, pool)
		admin := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		ia := svc.Interactors(admin)
		product, err := ia.TypeDefinitions().Create(admin, apptypedef.CreateInput{InternalName: "product", DisplayName: "Product"})
		So(err, ShouldBeNil)
		typeID := product.ID.String()
		sku, err := ia.Attributes().Create(admin, appattribute.CreateInput{
			TypeDefinitionID: typeID, InternalName: "sku", DisplayName: "SKU", DataType: "string",
		})
		So(err, ShouldBeNil)
		cost, err := ia.Attributes().Create(admin, appattribute.CreateInput{
			TypeDefinitionID: typeID, InternalName: "cost", DisplayName: "Cost", DataType: "float",
		})
		So(err, ShouldBeNil)

		set := func(ctx context.Context, attrID, entity string, v any) error {
			raw, _ := json.Marshal(v)
			_, e := svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: attrID, EntityID: entity, TypeDefinitionID: typeID, Value: raw,
			})
			return e
		}
		So(set(admin, sku.ID.String(), "p1", "ABC"), ShouldBeNil)
		So(set(admin, cost.ID.String(), "p1", 250.0), ShouldBeNil)

		noCost := uow.WithAccess(admin, uow.Access{Attr: map[string]uow.Perm{"cost": uow.PermNone}})
		readOnlyCost := uow.WithAccess(admin, uow.Access{Attr: map[string]uow.Perm{"cost": uow.PermRead}})

		Convey("The values list applies the field ACL to a direct attribute filter", func() {
			Convey("A restricted principal asking for cost by id gets nothing", func() {
				out, err := svc.Interactors(noCost).Values().List(noCost, appvalue.ListInput{
					AttributeDefinitionID: cost.ID.String(),
				})
				So(err, ShouldBeNil)
				So(out.Items, ShouldBeEmpty)
			})

			Convey("A restricted principal listing the whole type sees only sku", func() {
				out, err := svc.Interactors(noCost).Values().List(noCost, appvalue.ListInput{
					TypeDefinitionID: typeID,
				})
				So(err, ShouldBeNil)
				So(out.Items, ShouldHaveLength, 1)
				So(out.Items[0].AttributeDefinitionID.String(), ShouldEqual, sku.ID.String())
			})

			Convey("The admin still sees both values", func() {
				out, err := svc.Interactors(admin).Values().List(admin, appvalue.ListInput{TypeDefinitionID: typeID})
				So(err, ShouldBeNil)
				So(out.Items, ShouldHaveLength, 2)
			})
		})

		Convey("Revisions apply the field ACL on read and stay complete for restore", func() {
			rev, err := svc.Interactors(admin).Revisions().Create(admin, typeID, "p1", "baseline")
			So(err, ShouldBeNil)
			So(rev.Values, ShouldHaveLength, 2)

			Convey("A restricted principal reading the revision sees only sku", func() {
				got, err := svc.Interactors(noCost).Revisions().Get(noCost, rev.ID.String())
				So(err, ShouldBeNil)
				So(got.Values, ShouldHaveLength, 1)
				So(got.Values[0].InternalName, ShouldEqual, "sku")
			})

			Convey("A restricted principal diffing two revisions never sees a cost transition", func() {
				So(set(admin, cost.ID.String(), "p1", 300.0), ShouldBeNil)
				So(set(admin, sku.ID.String(), "p1", "XYZ"), ShouldBeNil)
				rev2, err := svc.Interactors(admin).Revisions().Create(admin, typeID, "p1", "after")
				So(err, ShouldBeNil)

				diff, err := svc.Interactors(noCost).Revisions().Diff(noCost, rev.ID.String(), rev2.ID.String())
				So(err, ShouldBeNil)
				names := []string{}
				for _, c := range diff.Changes {
					names = append(names, c.InternalName)
				}
				So(names, ShouldContain, "sku")
				So(names, ShouldNotContain, "cost")

				adminDiff, err := svc.Interactors(admin).Revisions().Diff(admin, rev.ID.String(), rev2.ID.String())
				So(err, ShouldBeNil)
				So(adminDiff.Changes, ShouldHaveLength, 2)
			})

			Convey("The admin restore still replays every captured value", func() {
				So(set(admin, cost.ID.String(), "p1", 999.0), ShouldBeNil)
				_, err := svc.Interactors(admin).Revisions().Restore(admin, rev.ID.String())
				So(err, ShouldBeNil)

				vals, err := svc.Interactors(admin).Values().ListByEntity(admin, typeID, "p1")
				So(err, ShouldBeNil)
				So(vals, ShouldHaveLength, 2)
				for _, v := range vals {
					if v.AttributeDefinitionID.String() == cost.ID.String() {
						So(v.Value.String(), ShouldEqual, "250")
					}
				}
			})
		})

		Convey("The activity log masks the values of unreadable attributes", func() {
			out, err := svc.Interactors(noCost).Activity().List(noCost, application.ActivityListInput{})
			So(err, ShouldBeNil)
			So(out.Items, ShouldNotBeEmpty)

			sawSkuValue, sawCostValue, sawMask := false, false, false
			for _, e := range out.Items {
				for _, raw := range [][]byte{e.Before, e.After} {
					if len(raw) == 0 {
						continue
					}
					var obj map[string]any
					So(json.Unmarshal(raw, &obj), ShouldBeNil)
					id, _ := obj["attribute_definition_id"].(string)
					switch id {
					case cost.ID.String():
						if obj["value"] != nil {
							sawCostValue = true
						}
						if obj["redacted"] == true {
							sawMask = true
						}
					case sku.ID.String():
						if obj["value"] != nil {
							sawSkuValue = true
						}
					}
				}
			}

			Convey("Then no cost value survives and the entry is marked redacted", func() {
				So(sawCostValue, ShouldBeFalse)
				So(sawMask, ShouldBeTrue)
			})

			Convey("Then the readable sku values are untouched", func() {
				So(sawSkuValue, ShouldBeTrue)
			})

			Convey("Then the admin reads the cost values in full", func() {
				adminOut, err := svc.Interactors(admin).Activity().List(admin, application.ActivityListInput{})
				So(err, ShouldBeNil)
				found := false
				for _, e := range adminOut.Items {
					var obj map[string]any
					if len(e.After) == 0 {
						continue
					}
					So(json.Unmarshal(e.After, &obj), ShouldBeNil)
					if obj["attribute_definition_id"] == cost.ID.String() && obj["value"] != nil {
						found = true
					}
				}
				So(found, ShouldBeTrue)
			})
		})

		Convey("Media download is governed by the attribute's read permission", func() {
			file, err := svc.Interactors(admin).Attributes().Create(admin, appattribute.CreateInput{
				TypeDefinitionID: typeID, InternalName: "datasheet", DisplayName: "Datasheet", DataType: "media",
			})
			So(err, ShouldBeNil)
			snap, err := svc.Interactors(admin).Values().UploadMedia(admin, typeID, "p1", file.ID.String(),
				strings.NewReader("hello"), "note.txt")
			So(err, ShouldBeNil)
			key := snap.Value.Media().ObjectKey
			So(key, ShouldNotBeBlank)

			noFile := uow.WithAccess(admin, uow.Access{Attr: map[string]uow.Perm{"datasheet": uow.PermNone}})

			Convey("A principal barred from the attribute cannot fetch the blob", func() {
				ok, err := svc.Interactors(noFile).Values().MediaKeyReadable(noFile, key)
				So(err, ShouldBeNil)
				So(ok, ShouldBeFalse)
			})

			Convey("A principal who may read the attribute can", func() {
				ok, err := svc.Interactors(noCost).Values().MediaKeyReadable(noCost, key)
				So(err, ShouldBeNil)
				So(ok, ShouldBeTrue)
			})

			Convey("An unknown key is unreadable for everyone", func() {
				ok, err := svc.Interactors(admin).Values().MediaKeyReadable(admin, "does-not-exist")
				So(err, ShouldBeNil)
				So(ok, ShouldBeFalse)
			})
		})

		Convey("Duplicate detection requires read permission on the rule's attribute", func() {
			Convey("A restricted principal cannot create a rule on cost", func() {
				_, err := svc.Interactors(noCost).Dedup().CreateRule(noCost, appdedup.CreateRuleInput{
					TypeDefinitionID:      typeID,
					AttributeDefinitionID: cost.ID.String(),
					Strategy:              appdedup.StrategyExact,
				})
				So(domainerrors.IsNotFound(err), ShouldBeTrue)
			})

			Convey("A rule created by an admin is unscannable by a restricted principal", func() {
				rule, err := svc.Interactors(admin).Dedup().CreateRule(admin, appdedup.CreateRuleInput{
					TypeDefinitionID:      typeID,
					AttributeDefinitionID: cost.ID.String(),
					Strategy:              appdedup.StrategyExact,
				})
				So(err, ShouldBeNil)

				_, err = svc.Interactors(noCost).Dedup().Scan(noCost, rule.ID.String())
				So(domainerrors.IsNotFound(err), ShouldBeTrue)

				_, err = svc.Interactors(admin).Dedup().Scan(admin, rule.ID.String())
				So(err, ShouldBeNil)
			})
		})

		Convey("Removing an entity requires write permission on every attribute it holds", func() {
			Convey("A principal who may only read cost cannot cascade-delete the entity", func() {
				_, err := svc.Interactors(readOnlyCost).Values().RemoveEntity(readOnlyCost, typeID, "p1")
				So(domainerrors.CodeOf(err), ShouldEqual, domainerrors.CodeForbidden)

				vals, err := svc.Interactors(admin).Values().ListByEntity(admin, typeID, "p1")
				So(err, ShouldBeNil)
				So(vals, ShouldHaveLength, 2)
			})

			Convey("The admin still removes it", func() {
				out, err := svc.Interactors(admin).Values().RemoveEntity(admin, typeID, "p1")
				So(err, ShouldBeNil)
				So(out.ValuesRemoved, ShouldEqual, 2)
			})
		})
	})
}
