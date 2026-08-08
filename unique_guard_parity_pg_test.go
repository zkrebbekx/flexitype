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
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

// The make-unique guard asks the store whether the attribute's live values
// already contain duplicates. It grouped on the value's RENDERING while the
// write path compares values with typed equality, so the two disagreed in
// both directions (#487):
//
//   - Decimal "1.5" and "1.50", and quantity "5 kg" and "5000 g", render
//     differently but ARE equal. The guard saw distinct values and allowed
//     the flip; every later writer of that value was then refused forever,
//     with no way back except deleting real data.
//   - The rendering ignored (locale, channel), but uniqueness is per scope.
//     One value held in two locales read as a duplicate of itself and
//     blocked a flip the data supports perfectly.
//
// Both backends now key on the same typed identity the write path uses:
// Value.EqualityKey in memory, and the matching SQL expressions in Postgres.
func runUniqueGuardParity(t *testing.T, label string, setup func() *flexitype.Service) {
	t.Helper()

	Convey("Given stored values that differ only in rendering ("+label+")", t, func() {
		svc := setup()
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		it := svc.Interactors(ctx)

		product, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "product", DisplayName: "Product",
		})
		So(err, ShouldBeNil)
		typeID := product.ID.String()
		fam, err := svc.Interactors(ctx).Units().Create(ctx, appunit.CreateInput{
			Name: "mass", BaseUnit: "g", Units: map[string]float64{"g": 1, "kg": 1000},
		})
		So(err, ShouldBeNil)

		attr := func(in appattribute.CreateInput) string {
			in.TypeDefinitionID = typeID
			in.DisplayName = in.InternalName
			a, aerr := svc.Interactors(ctx).Attributes().Create(ctx, in)
			So(aerr, ShouldBeNil)
			return a.ID.String()
		}
		set := func(attrID, entity, raw string, scope appvalue.SetInput) error {
			scope.AttributeDefinitionID = attrID
			scope.EntityID = entity
			scope.TypeDefinitionID = typeID
			scope.Value = json.RawMessage(raw)
			_, serr := svc.Interactors(ctx).Values().Set(ctx, scope)
			return serr
		}
		// Update REPLACES the structural flags, so a caller flipping unique
		// must resend the ones it keeps — otherwise this asks to turn
		// localizable off as well, and a different guard answers.
		makeUnique := func(id string, localizable bool) error {
			_, uerr := svc.Interactors(ctx).Attributes().Update(ctx, appattribute.UpdateInput{
				ID: id, DisplayName: "x", Unique: true, Localizable: localizable,
			})
			return uerr
		}

		Convey("When two entities hold the same decimal written differently", func() {
			price := attr(appattribute.CreateInput{InternalName: "price", DataType: "decimal"})
			So(set(price, "p1", `"1.5"`, appvalue.SetInput{}), ShouldBeNil)
			So(set(price, "p2", `"1.50"`, appvalue.SetInput{}), ShouldBeNil)

			Convey("Then making it unique is refused: the write path calls them duplicates", func() {
				err := makeUnique(price, false)
				So(domainerrors.IsConflict(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "duplicates")
			})
		})

		Convey("When two entities hold the same quantity in different units", func() {
			weight := attr(appattribute.CreateInput{
				InternalName: "weight", DataType: "quantity",
				UnitFamilyID: fam.ID.String(), DisplayUnit: "g",
			})
			So(set(weight, "p1", `{"magnitude":"5","unit":"kg"}`, appvalue.SetInput{}), ShouldBeNil)
			So(set(weight, "p2", `{"magnitude":"5000","unit":"g"}`, appvalue.SetInput{}), ShouldBeNil)

			Convey("Then making it unique is refused: both store the same base magnitude", func() {
				err := makeUnique(weight, false)
				So(domainerrors.IsConflict(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "duplicates")
			})
		})

		Convey("When two entities hold the same text in DIFFERENT locales", func() {
			name := attr(appattribute.CreateInput{
				InternalName: "name", DataType: "string", Localizable: true,
			})
			So(set(name, "p1", `"Widget"`, appvalue.SetInput{Locale: "en"}), ShouldBeNil)
			So(set(name, "p2", `"Widget"`, appvalue.SetInput{Locale: "de"}), ShouldBeNil)

			Convey("Then making it unique is allowed: uniqueness is per scope", func() {
				So(makeUnique(name, true), ShouldBeNil)
			})

			Convey("And the write path then agrees with the guard", func() {
				So(makeUnique(name, true), ShouldBeNil)
				// Same value, same scope: refused.
				So(domainerrors.IsConflict(
					set(name, "p3", `"Widget"`, appvalue.SetInput{Locale: "en"})), ShouldBeTrue)
				// Same value, a scope nobody holds it in: accepted.
				So(set(name, "p3", `"Widget"`, appvalue.SetInput{Locale: "fr"}), ShouldBeNil)
			})
		})

		Convey("When two entities hold genuinely different decimals", func() {
			cost := attr(appattribute.CreateInput{InternalName: "cost", DataType: "decimal"})
			So(set(cost, "p1", `"1.5"`, appvalue.SetInput{}), ShouldBeNil)
			So(set(cost, "p2", `"2.5"`, appvalue.SetInput{}), ShouldBeNil)

			Convey("Then making it unique is allowed", func() {
				So(makeUnique(cost, false), ShouldBeNil)
			})
		})
	})
}

// TestUniqueGuardTypedComparison runs the guard scenarios against the
// in-memory backend.
func TestUniqueGuardTypedComparison(t *testing.T) {
	runUniqueGuardParity(t, "memory", func() *flexitype.Service {
		return flexitype.NewInMemory()
	})
}

// TestUniqueGuardTypedComparisonPostgres re-runs them against Postgres, where
// the grouping is SQL rather than a Go map.
func TestUniqueGuardTypedComparisonPostgres(t *testing.T) {
	pool := openTestDB(t)
	defer func() { _ = pool.Close() }()

	svc := flexitype.New(pool)
	if err := svc.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	runUniqueGuardParity(t, "postgres", func() *flexitype.Service {
		truncateAll(t, pool)
		return svc
	})
}
