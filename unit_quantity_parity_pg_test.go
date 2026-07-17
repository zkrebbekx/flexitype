package flexitype_test

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
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

// TestQuantityConstraintRebasingParityPostgres re-runs the quantity suite
// (infrastructure/memory/unit_quantity_test.go) against Postgres: constraints
// and defaults enforced on the base-unit magnitude, and equal quantities in
// different units folding to the same stored base — all through the Postgres
// unit-family store and quantity value columns.
func TestQuantityConstraintRebasingParityPostgres(t *testing.T) {
	pool := openTestDB(t)
	defer func() { _ = pool.Close() }()

	svc := flexitype.New(pool)
	if err := svc.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	Convey("Given a mass unit family (g, kg, lb) (Postgres)", t, func() {
		truncateAll(t, pool)
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
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
