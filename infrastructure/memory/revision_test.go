package memory_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	apprevision "github.com/zkrebbekx/flexitype/application/revision"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/infrastructure/memory"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

func TestEntityRevisions(t *testing.T) {
	Convey("Given an entity mutated across three revisions", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory()
		it := svc.Interactors(ctx)

		product, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{InternalName: "product", DisplayName: "Product"})
		So(err, ShouldBeNil)
		typeID := product.ID.String()
		attr := func(name, dt string) string {
			s, e := it.Attributes().Create(ctx, appattribute.CreateInput{TypeDefinitionID: typeID, InternalName: name, DisplayName: name, DataType: dt})
			So(e, ShouldBeNil)
			return s.ID.String()
		}
		sku := attr("sku", "string")
		price := attr("price", "integer")
		color := attr("color", "string")

		set := func(attrID, v string) {
			raw := json.RawMessage(v)
			_, e := it.Values().Set(ctx, appvalue.SetInput{AttributeDefinitionID: attrID, EntityID: "e1", TypeDefinitionID: typeID, Value: raw})
			So(e, ShouldBeNil)
		}
		valueID := func(attrID string) string {
			snaps, e := it.Values().ListByEntity(ctx, typeID, "e1")
			So(e, ShouldBeNil)
			for _, s := range snaps {
				if s.AttributeDefinitionID.String() == attrID {
					return s.ID.String()
				}
			}
			return ""
		}

		// r1: {sku=A, price=100}
		set(sku, `"A"`)
		set(price, `100`)
		r1, err := it.Revisions().Create(ctx, typeID, "e1", "v1")
		So(err, ShouldBeNil)

		// r2: {sku=A, price=200, color=red}
		set(price, `200`)
		set(color, `"red"`)
		_, err = it.Revisions().Create(ctx, typeID, "e1", "v2")
		So(err, ShouldBeNil)

		// r3: {price=300, color=red} — sku removed
		set(price, `300`)
		_, err = it.Values().Remove(ctx, valueID(sku))
		So(err, ShouldBeNil)
		r3, err := it.Revisions().Create(ctx, typeID, "e1", "v3")
		So(err, ShouldBeNil)

		Convey("When each revision is read back", func() {
			got1, err := it.Revisions().Get(ctx, r1.ID.String())
			So(err, ShouldBeNil)
			got3, err := it.Revisions().Get(ctx, r3.ID.String())
			So(err, ShouldBeNil)

			Convey("Then it holds the exact historical values", func() {
				vals1 := map[string]string{}
				for _, v := range got1.Values {
					vals1[v.InternalName] = v.Value
				}
				So(vals1, ShouldResemble, map[string]string{"sku": "A", "price": "100"})

				vals3 := map[string]string{}
				for _, v := range got3.Values {
					vals3[v.InternalName] = v.Value
				}
				So(vals3, ShouldResemble, map[string]string{"price": "300", "color": "red"})
			})
		})

		Convey("When r1 and r3 are diffed", func() {
			diff, err := it.Revisions().Diff(ctx, r1.ID.String(), r3.ID.String())
			So(err, ShouldBeNil)

			Convey("Then adds, changes and removes are all listed", func() {
				kinds := map[string]string{}
				for _, c := range diff.Changes {
					kinds[c.InternalName] = c.Kind
				}
				So(kinds["price"], ShouldEqual, "changed")
				So(kinds["color"], ShouldEqual, "added")
				So(kinds["sku"], ShouldEqual, "removed")
			})
		})

		Convey("When r1 is restored", func() {
			_, err := it.Revisions().Restore(ctx, r1.ID.String())
			So(err, ShouldBeNil)

			Convey("Then current state matches r1 and history is retained", func() {
				snaps, err := it.Values().ListByEntity(ctx, typeID, "e1")
				So(err, ShouldBeNil)
				cur := map[string]string{}
				for _, s := range snaps {
					cur[s.AttributeDefinitionID.String()] = s.Value.String()
				}
				So(cur, ShouldResemble, map[string]string{sku: "A", price: "100"})

				revs, err := it.Revisions().List(ctx, typeID, "e1")
				So(err, ShouldBeNil)
				So(revs, ShouldHaveLength, 4) // r1, r2, r3 + the restore revision
			})
		})
	})
}

func TestEntityRevisionsScoped(t *testing.T) {
	Convey("Given an entity with locale/channel-scoped values", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory()
		it := svc.Interactors(ctx)

		product, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{InternalName: "product", DisplayName: "Product"})
		So(err, ShouldBeNil)
		typeID := product.ID.String()
		desc, err := it.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: typeID, InternalName: "description", DisplayName: "Description",
			DataType: "string", Localizable: true, Scopable: true,
		})
		So(err, ShouldBeNil)

		set := func(locale, channel, v string) {
			raw, _ := json.Marshal(v)
			_, e := it.Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: desc.ID.String(), EntityID: "e1", TypeDefinitionID: typeID,
				Locale: locale, Channel: channel, Value: raw,
			})
			So(e, ShouldBeNil)
		}
		set("en", "web", "Hello")
		set("de", "web", "Hallo")
		set("en", "print", "Hi")
		set("de", "print", "Hoi")

		r1, err := it.Revisions().Create(ctx, typeID, "e1", "v1")
		So(err, ShouldBeNil)
		So(r1.Values, ShouldHaveLength, 4) // every scope captured distinctly

		// Mutate a single scope after the snapshot.
		set("en", "web", "CHANGED")

		Convey("When r1 is restored", func() {
			_, err := it.Revisions().Restore(ctx, r1.ID.String())
			So(err, ShouldBeNil)

			Convey("Then every scope returns to the snapshot with no collapse or spurious base row", func() {
				snaps, err := it.Values().ListByEntity(ctx, typeID, "e1")
				So(err, ShouldBeNil)
				byScope := map[string]string{}
				for _, s := range snaps {
					byScope[s.Locale+"/"+s.Channel] = s.Value.String()
				}
				So(len(snaps), ShouldEqual, 4) // exactly the four scopes — no base "" row
				So(byScope, ShouldResemble, map[string]string{
					"en/web":   "Hello",
					"de/web":   "Hallo",
					"en/print": "Hi",
					"de/print": "Hoi",
				})
			})
		})

		Convey("When the snapshot is diffed against the mutated state", func() {
			r2, err := it.Revisions().Create(ctx, typeID, "e1", "v2")
			So(err, ShouldBeNil)
			diff, err := it.Revisions().Diff(ctx, r1.ID.String(), r2.ID.String())
			So(err, ShouldBeNil)

			Convey("Then only the mutated scope is reported changed", func() {
				So(diff.Changes, ShouldHaveLength, 1)
				c := diff.Changes[0]
				So(c.InternalName, ShouldEqual, "description")
				So(c.Locale, ShouldEqual, "en")
				So(c.Channel, ShouldEqual, "web")
				So(c.Kind, ShouldEqual, "changed")
				So(c.Before, ShouldEqual, "Hello")
				So(c.After, ShouldEqual, "CHANGED")
			})
		})
	})
}

// TestRevisionStoreDirect drives the revision store port itself — the surface
// the erasure and time-travel usecases sit on — so tenant scoping, the "latest
// at or before" as-of rule and the purge row counts are pinned independently of
// the interactors above.
func TestRevisionStoreDirect(t *testing.T) {
	Convey("Given a revision store holding three revisions of one entity plus other rows", t, func() {
		ctx := context.Background()
		store := memory.NewRevisionStore()

		const typeID = "01ARZ3NDEKTSV4RRFFQ69G5FA1"
		rev := func(id string, tenant valueobjects.TenantID, tdID, entity string, seq int, at time.Time) apprevision.Revision {
			return apprevision.Revision{
				ID: ulid.MustParse(id), TenantID: tenant, TypeDefinitionID: tdID,
				EntityID: entity, Seq: seq, Label: id[len(id)-1:], CreatedAt: at,
			}
		}

		t0 := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
		r1 := rev(ulidAt('1'), tenantA, typeID, "e1", 1, t0)
		r2 := rev(ulidAt('2'), tenantA, typeID, "e1", 2, t0.Add(time.Hour))
		r3 := rev(ulidAt('3'), tenantA, typeID, "e1", 3, t0.Add(2*time.Hour))
		other := rev(ulidAt('4'), tenantA, typeID, "e2", 1, t0)
		foreign := rev(ulidAt('5'), tenantB, typeID, "e1", 1, t0)

		for _, r := range []apprevision.Revision{r1, r2, r3, other, foreign} {
			So(store.Create(ctx, r), ShouldBeNil)
		}

		Convey("When the entity's revisions are listed", func() {
			got, err := store.List(ctx, tenantA, typeID, "e1")
			So(err, ShouldBeNil)

			Convey("Then they come back newest-first by sequence, scoped to the tenant", func() {
				So(got, ShouldHaveLength, 3)
				So(got[0].Seq, ShouldEqual, 3)
				So(got[1].Seq, ShouldEqual, 2)
				So(got[2].Seq, ShouldEqual, 1)
			})
		})

		Convey("When another tenant's revision is fetched by id", func() {
			_, err := store.Get(ctx, tenantA, foreign.ID)

			Convey("Then it is not found — the id alone does not grant access", func() {
				So(domainerrors.IsNotFound(err), ShouldBeTrue)
			})
		})

		Convey("When asking for the revision as of an instant between r2 and r3", func() {
			got, err := store.AsOf(ctx, tenantA, typeID, "e1", t0.Add(90*time.Minute))
			So(err, ShouldBeNil)

			Convey("Then the latest revision at or before that instant wins", func() {
				So(got.ID.String(), ShouldEqual, r2.ID.String())
			})
		})

		Convey("When asking as of the exact instant a revision was created", func() {
			got, err := store.AsOf(ctx, tenantA, typeID, "e1", t0.Add(2*time.Hour))
			So(err, ShouldBeNil)

			Convey("Then that revision is included — the bound is inclusive", func() {
				So(got.ID.String(), ShouldEqual, r3.ID.String())
			})
		})

		Convey("When asking as of an instant before the first revision", func() {
			_, err := store.AsOf(ctx, tenantA, typeID, "e1", t0.Add(-time.Second))

			Convey("Then a typed not-found is returned rather than an empty revision", func() {
				So(domainerrors.IsNotFound(err), ShouldBeTrue)
			})
		})

		Convey("When the highest sequence is requested", func() {
			seq, err := store.LastSeq(ctx, tenantA, typeID, "e1")
			So(err, ShouldBeNil)
			missing, err := store.LastSeq(ctx, tenantA, typeID, "never-existed")
			So(err, ShouldBeNil)

			Convey("Then it is the newest revision's sequence, and 0 for an unknown entity", func() {
				So(seq, ShouldEqual, 3)
				So(missing, ShouldEqual, 0)
			})
		})

		Convey("When one entity's revisions are purged", func() {
			n, err := store.PurgeEntity(ctx, tenantA, typeID, "e1")
			So(err, ShouldBeNil)

			Convey("Then exactly that entity's rows are removed; siblings and other tenants remain", func() {
				So(n, ShouldEqual, 3)

				gone, err := store.List(ctx, tenantA, typeID, "e1")
				So(err, ShouldBeNil)
				So(gone, ShouldBeEmpty)

				kept, err := store.List(ctx, tenantA, typeID, "e2")
				So(err, ShouldBeNil)
				So(kept, ShouldHaveLength, 1)

				stillForeign, err := store.Get(ctx, tenantB, foreign.ID)
				So(err, ShouldBeNil)
				So(stillForeign.EntityID, ShouldEqual, "e1")
			})
		})

		Convey("When a whole tenant's revisions are purged", func() {
			n, err := store.PurgeTenant(ctx, tenantA)
			So(err, ShouldBeNil)

			Convey("Then every row of that tenant is erased and the other tenant is untouched", func() {
				So(n, ShouldEqual, 4)

				_, err := store.Get(ctx, tenantA, r1.ID)
				So(domainerrors.IsNotFound(err), ShouldBeTrue)

				survivor, err := store.Get(ctx, tenantB, foreign.ID)
				So(err, ShouldBeNil)
				So(survivor.ID.String(), ShouldEqual, foreign.ID.String())
			})
		})

		Convey("When bound to a handle that is not a transaction", func() {
			bound := store.WithTx(&plainTx{})
			n, err := bound.PurgeEntity(ctx, tenantA, typeID, "e1")
			So(err, ShouldBeNil)

			Convey("Then it is the plain store: the purge happens with no rollback hook to register", func() {
				So(n, ShouldEqual, 3)
				got, err := store.List(ctx, tenantA, typeID, "e1")
				So(err, ShouldBeNil)
				So(got, ShouldBeEmpty)
			})
		})
	})
}
