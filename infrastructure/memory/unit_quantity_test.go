package memory_test

import (
	"context"
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	appunit "github.com/zkrebbekx/flexitype/application/unit"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/infrastructure/memory"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

func TestQuantityConstraintRebasing(t *testing.T) {
	Convey("Given a mass unit family (g, kg, lb)", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory()
		it := svc.Interactors(ctx)

		fam, err := it.Units().Create(ctx, appunit.CreateInput{
			Name: "mass", BaseUnit: "g", Units: map[string]float64{"g": 1, "kg": 1000, "lb": 453.592},
		})
		So(err, ShouldBeNil)
		product, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{InternalName: "product", DisplayName: "Product"})
		So(err, ShouldBeNil)
		typeID := product.ID.String()

		setW := func(attrID, magnitude, unit string) error {
			raw := json.RawMessage(`{"magnitude":"` + magnitude + `","unit":"` + unit + `"}`)
			_, e := it.Values().Set(ctx, appvalue.SetInput{AttributeDefinitionID: attrID, EntityID: "e1", TypeDefinitionID: typeID, Value: raw})
			return e
		}

		Convey("A quantity one_of set is enforced on the base value", func() {
			w, err := it.Attributes().Create(ctx, appattribute.CreateInput{
				TypeDefinitionID: typeID, InternalName: "weight", DisplayName: "Weight",
				DataType: "quantity", UnitFamilyID: fam.ID.String(), DisplayUnit: "g",
				Constraints: json.RawMessage(`[{"kind":"one_of","values":[` +
					`{"type":"quantity","value":{"magnitude":"1","unit":"kg"}},` +
					`{"type":"quantity","value":{"magnitude":"2","unit":"kg"}}]}]`),
			})
			So(err, ShouldBeNil)
			So(setW(w.ID.String(), "1000", "g"), ShouldBeNil)    // 1 kg — a member
			So(setW(w.ID.String(), "2", "kg"), ShouldBeNil)      // 2 kg — a member
			So(setW(w.ID.String(), "1500", "g"), ShouldNotBeNil) // 1.5 kg — not a member
		})

		Convey("A quantity default within min/max lets the attribute create", func() {
			_, err := it.Attributes().Create(ctx, appattribute.CreateInput{
				TypeDefinitionID: typeID, InternalName: "shipwt", DisplayName: "Ship weight",
				DataType: "quantity", UnitFamilyID: fam.ID.String(), DisplayUnit: "g",
				Constraints:  json.RawMessage(`[{"kind":"min_value","value":{"type":"quantity","value":{"magnitude":"1","unit":"kg"}}}]`),
				DefaultValue: json.RawMessage(`{"static":{"type":"quantity","value":{"magnitude":"5","unit":"kg"}}}`),
			})
			So(err, ShouldBeNil)
		})

		Convey("Equal quantities in different units fold to the same base", func() {
			w, err := it.Attributes().Create(ctx, appattribute.CreateInput{
				TypeDefinitionID: typeID, InternalName: "exact", DisplayName: "Exact",
				DataType: "quantity", UnitFamilyID: fam.ID.String(), DisplayUnit: "g",
				Constraints: json.RawMessage(`[{"kind":"one_of","values":[{"type":"quantity","value":{"magnitude":"2.2","unit":"lb"}}]}]`),
			})
			So(err, ShouldBeNil)
			// 997.9024 g == 2.2 lb (2.2 * 453.592) exactly; float multiplication
			// would leave them differing in the last ~1e-13 and reject this.
			So(setW(w.ID.String(), "997.9024", "g"), ShouldBeNil)
		})
	})
}

// TestUnitFamilyStoreDirect pins the unit-family store port: families list
// alphabetically within a tenant, and every read/write is tenant-scoped.
func TestUnitFamilyStoreDirect(t *testing.T) {
	Convey("Given unit families across two tenants", t, func() {
		ctx := context.Background()
		store := memory.NewUnitFamilyStore()

		mk := func(id string, tenant valueobjects.TenantID, name, base string) appunit.Family {
			return appunit.Family{
				ID: ulid.MustParse(id), TenantID: tenant, Name: name, BaseUnit: base,
				Units: map[string]float64{base: 1},
			}
		}
		mass := mk(ulidAt('1'), tenantA, "mass", "g")
		length := mk(ulidAt('2'), tenantA, "length", "mm")
		foreign := mk(ulidAt('3'), tenantB, "mass", "g")

		for _, f := range []appunit.Family{mass, length, foreign} {
			So(store.Create(ctx, f), ShouldBeNil)
		}

		familyNames := func(fs []appunit.Family) []string {
			out := make([]string, 0, len(fs))
			for _, f := range fs {
				out = append(out, f.Name)
			}
			return out
		}

		Convey("When a tenant lists its families", func() {
			got, err := store.List(ctx, tenantA)
			So(err, ShouldBeNil)

			Convey("Then they are name-ordered and the other tenant's family is invisible", func() {
				So(familyNames(got), ShouldResemble, []string{"length", "mass"})
			})
		})

		Convey("When a tenant with no families lists them", func() {
			got, err := store.List(ctx, valueobjects.TenantID("empty"))
			So(err, ShouldBeNil)

			Convey("Then an empty slice is returned rather than nil", func() {
				So(got, ShouldNotBeNil)
				So(got, ShouldBeEmpty)
			})
		})

		Convey("When a family is fetched under the wrong tenant", func() {
			_, err := store.Get(ctx, tenantA, foreign.ID)

			Convey("Then it is not found — the id alone does not cross the boundary", func() {
				So(domainerrors.IsNotFound(err), ShouldBeTrue)
			})
		})

		Convey("When a family is deleted by its owner", func() {
			So(store.Delete(ctx, tenantA, mass.ID), ShouldBeNil)

			Convey("Then it is gone and the tenant's other family is untouched", func() {
				_, err := store.Get(ctx, tenantA, mass.ID)
				So(domainerrors.IsNotFound(err), ShouldBeTrue)

				remaining, err := store.List(ctx, tenantA)
				So(err, ShouldBeNil)
				So(familyNames(remaining), ShouldResemble, []string{"length"})
			})
		})

		Convey("When a tenant tries to delete another tenant's family", func() {
			So(store.Delete(ctx, tenantA, foreign.ID), ShouldBeNil)

			Convey("Then the call is a silent no-op and the family survives", func() {
				got, err := store.Get(ctx, tenantB, foreign.ID)
				So(err, ShouldBeNil)
				So(got.Name, ShouldEqual, "mass")
			})
		})

		Convey("When an unknown family id is deleted", func() {
			err := store.Delete(ctx, tenantA, ulid.MustParse(ulidAt('9')))

			Convey("Then deletion is idempotent — no error for an absent family", func() {
				So(err, ShouldBeNil)
			})
		})
	})
}
