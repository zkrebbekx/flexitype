package flexitype_test

import (
	"context"
	"encoding/json"
	"errors"
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
)

// The two ways a quantity attribute's unit family could be pulled out from
// under its stored values (#489). Both left the values readable and never
// writable, and neither reported anything at the moment an operator could
// still have chosen otherwise.
//
//   - Clearing the family on the attribute. The structural guard required a
//     non-empty NEW family, so a clear was unguarded — and a REST PUT that
//     omits the omitempty unit_family_id field sends the empty string, which
//     makes an ordinary edit unpin the family.
//   - Deleting the family itself, which checked only that it existed.
func runUnitFamilyStranding(t *testing.T, label string, setup func() *flexitype.Service) {
	t.Helper()

	Convey("Given a quantity attribute holding a value in a mass family ("+label+")", t, func() {
		svc := setup()
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		it := svc.Interactors(ctx)

		fam, err := it.Units().Create(ctx, appunit.CreateInput{
			Name: "mass", BaseUnit: "g", Units: map[string]float64{"g": 1, "kg": 1000},
		})
		So(err, ShouldBeNil)
		product, err := svc.Interactors(ctx).TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "product", DisplayName: "Product",
		})
		So(err, ShouldBeNil)
		weight, err := svc.Interactors(ctx).Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: product.ID.String(), InternalName: "weight", DisplayName: "Weight",
			DataType: "quantity", UnitFamilyID: fam.ID.String(), DisplayUnit: "g",
		})
		So(err, ShouldBeNil)
		_, err = svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{
			AttributeDefinitionID: weight.ID.String(), EntityID: "p1",
			TypeDefinitionID: product.ID.String(),
			Value:            json.RawMessage(`{"magnitude":"2","unit":"kg"}`),
		})
		So(err, ShouldBeNil)

		Convey("When an update omits the unit family, as a REST PUT does", func() {
			_, uerr := svc.Interactors(ctx).Attributes().Update(ctx, appattribute.UpdateInput{
				ID: weight.ID.String(), DisplayName: "Weight (kg)", DisplayUnit: "g",
			})

			Convey("Then clearing it is refused rather than stranding the value", func() {
				So(domainerrors.IsConflict(uerr), ShouldBeTrue)
				So(uerr.Error(), ShouldContainSubstring, "clear the unit family")
			})

			Convey("And the value is still writable afterwards", func() {
				_, serr := svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{
					AttributeDefinitionID: weight.ID.String(), EntityID: "p1",
					TypeDefinitionID: product.ID.String(),
					Value:            json.RawMessage(`{"magnitude":"3","unit":"kg"}`),
				})
				So(serr, ShouldBeNil)
			})
		})

		Convey("When the family itself is deleted", func() {
			derr := svc.Interactors(ctx).Units().Delete(ctx, fam.ID.String())

			Convey("Then it is refused, naming the attribute that pins it", func() {
				So(domainerrors.IsConflict(derr), ShouldBeTrue)
				So(derr.Error(), ShouldContainSubstring, "still pinned by quantity attributes")
				var de *domainerrors.Error
				So(errors.As(derr, &de), ShouldBeTrue)
				So(de.Details["attributes"], ShouldEqual, 1)
				So(de.Details["example_attributes"], ShouldEqual, "weight")
			})

			Convey("And the value is still writable afterwards", func() {
				_, serr := svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{
					AttributeDefinitionID: weight.ID.String(), EntityID: "p1",
					TypeDefinitionID: product.ID.String(),
					Value:            json.RawMessage(`{"magnitude":"4","unit":"kg"}`),
				})
				So(serr, ShouldBeNil)
			})
		})

		Convey("When the update resends the family it means to keep", func() {
			_, uerr := svc.Interactors(ctx).Attributes().Update(ctx, appattribute.UpdateInput{
				ID: weight.ID.String(), DisplayName: "Weight (kg)",
				UnitFamilyID: fam.ID.String(), DisplayUnit: "g",
			})

			Convey("Then the edit is accepted", func() {
				So(uerr, ShouldBeNil)
			})
		})

		Convey("When nothing references a family", func() {
			spare, cerr := svc.Interactors(ctx).Units().Create(ctx, appunit.CreateInput{
				Name: "length", BaseUnit: "m", Units: map[string]float64{"m": 1, "km": 1000},
			})
			So(cerr, ShouldBeNil)

			Convey("Then deleting it still works", func() {
				So(svc.Interactors(ctx).Units().Delete(ctx, spare.ID.String()), ShouldBeNil)
			})
		})
	})
}

// TestUnitFamilyStranding runs the scenarios against the in-memory backend.
func TestUnitFamilyStranding(t *testing.T) {
	runUnitFamilyStranding(t, "memory", func() *flexitype.Service {
		return flexitype.NewInMemory()
	})
}

// TestUnitFamilyStrandingPostgres re-runs them against Postgres, where the
// reference lookup is a SQL filter rather than a scan of a map.
func TestUnitFamilyStrandingPostgres(t *testing.T) {
	pool := openTestDB(t)
	defer func() { _ = pool.Close() }()

	svc := flexitype.New(pool)
	if err := svc.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	runUnitFamilyStranding(t, "postgres", func() *flexitype.Service {
		truncateAll(t, pool)
		return svc
	})
}
