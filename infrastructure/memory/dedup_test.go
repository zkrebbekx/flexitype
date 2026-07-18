package memory_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	appdedup "github.com/zkrebbekx/flexitype/application/dedup"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/infrastructure/memory"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

func TestDuplicateDetection(t *testing.T) {
	Convey("Given products whose names nearly match", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory()
		it := svc.Interactors(ctx)

		product, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "product", DisplayName: "Product",
		})
		So(err, ShouldBeNil)
		typeID := product.ID.String()
		name, err := it.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: typeID, InternalName: "name", DisplayName: "Name", DataType: "string",
		})
		So(err, ShouldBeNil)

		setName := func(entity, v string) {
			raw, _ := json.Marshal(v)
			_, e := it.Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: name.ID.String(), EntityID: entity, TypeDefinitionID: typeID, Value: raw,
			})
			So(e, ShouldBeNil)
		}
		setName("e1", "Trail Bike 500")
		setName("e2", "trail bike 500 ") // trailing space + different case
		setName("e3", "Road Helmet XL")  // unrelated

		rule, err := it.Dedup().CreateRule(ctx, appdedup.CreateRuleInput{
			TypeDefinitionID:      typeID,
			AttributeDefinitionID: name.ID.String(),
			Strategy:              appdedup.StrategyTrigram,
			Threshold:             0.7,
		})
		So(err, ShouldBeNil)

		Convey("When the rule is scanned", func() {
			out, err := it.Dedup().Scan(ctx, rule.ID.String())

			Convey("Then the near-duplicate pairs and only they are reported", func() {
				So(err, ShouldBeNil)
				So(out.Candidates, ShouldHaveLength, 1)
				So(out.Candidates[0].EntityA, ShouldEqual, "e1")
				So(out.Candidates[0].EntityB, ShouldEqual, "e2")
				So(out.Candidates[0].Score, ShouldBeGreaterThanOrEqualTo, 0.7)
			})

			Convey("Then a second scan is idempotent", func() {
				So(err, ShouldBeNil)
				again, err := it.Dedup().Scan(ctx, rule.ID.String())
				So(err, ShouldBeNil)
				So(again.Candidates, ShouldResemble, out.Candidates)
			})
		})

		Convey("When a candidate pair is dismissed", func() {
			So(it.Dedup().Dismiss(ctx, rule.ID.String(), "e2", "e1"), ShouldBeNil)
			out, err := it.Dedup().Scan(ctx, rule.ID.String())

			Convey("Then it stays dismissed on re-scan", func() {
				So(err, ShouldBeNil)
				So(out.Candidates, ShouldBeEmpty)
			})
		})

		Convey("When an exact-match rule is scanned against differing case", func() {
			exact, err := it.Dedup().CreateRule(ctx, appdedup.CreateRuleInput{
				TypeDefinitionID: typeID, AttributeDefinitionID: name.ID.String(), Strategy: appdedup.StrategyExact,
			})
			So(err, ShouldBeNil)
			out, err := it.Dedup().Scan(ctx, exact.ID.String())

			Convey("Then no pair matches (values differ by case and space)", func() {
				So(err, ShouldBeNil)
				So(out.Candidates, ShouldBeEmpty)
			})
		})

		Convey("When two content-free values are trigram-scanned", func() {
			setName("e4", "!!!")
			setName("e5", "@@@")
			out, err := it.Dedup().Scan(ctx, rule.ID.String())

			Convey("Then they are not flagged as a perfect duplicate", func() {
				So(err, ShouldBeNil)
				for _, c := range out.Candidates {
					pair := c.EntityA + "," + c.EntityB
					So(pair, ShouldNotContainSubstring, "e4")
					So(pair, ShouldNotContainSubstring, "e5")
				}
			})
		})
	})
}

// TestMatchStoreDirect pins the matching-rule store port: rules are listed
// oldest-first per type, tenant boundaries hold on every operation, and deleting
// a rule takes its dismissals with it (there is nothing left to dismiss).
func TestMatchStoreDirect(t *testing.T) {
	Convey("Given matching rules on two types across two tenants", t, func() {
		ctx := context.Background()
		store := memory.NewMatchStore()

		const productType, supplierType = "type-product", "type-supplier"
		t0 := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
		mk := func(id string, tenant valueobjects.TenantID, typeID string, created time.Time) appdedup.Rule {
			return appdedup.Rule{
				ID: ulid.MustParse(id), TenantID: tenant, TypeDefinitionID: typeID,
				AttributeDefinitionID: "attr-name", Strategy: appdedup.StrategyExact,
				CreatedAt: created,
			}
		}

		second := mk(ulidAt('1'), tenantA, productType, t0.Add(time.Hour)) // created later
		first := mk(ulidAt('2'), tenantA, productType, t0)                 // created earlier
		onSupplier := mk(ulidAt('3'), tenantA, supplierType, t0)
		foreign := mk(ulidAt('4'), tenantB, productType, t0)

		for _, r := range []appdedup.Rule{second, first, onSupplier, foreign} {
			So(store.CreateRule(ctx, r), ShouldBeNil)
		}
		So(store.Dismiss(ctx, appdedup.Dismissal{
			RuleID: first.ID, TenantID: tenantA, EntityA: "e1", EntityB: "e2",
		}), ShouldBeNil)
		So(store.Dismiss(ctx, appdedup.Dismissal{
			RuleID: second.ID, TenantID: tenantA, EntityA: "e3", EntityB: "e4",
		}), ShouldBeNil)

		ruleIDs := func(rules []appdedup.Rule) []string {
			out := make([]string, 0, len(rules))
			for _, r := range rules {
				out = append(out, r.ID.String())
			}
			return out
		}

		Convey("When a type's rules are listed", func() {
			got, err := store.ListRules(ctx, tenantA, productType)
			So(err, ShouldBeNil)

			Convey("Then they are oldest-first and scoped to both the tenant and the type", func() {
				So(ruleIDs(got), ShouldResemble, []string{first.ID.String(), second.ID.String()})
			})
		})

		Convey("When a type with no rules is listed", func() {
			got, err := store.ListRules(ctx, tenantA, "type-unknown")
			So(err, ShouldBeNil)

			Convey("Then an empty slice is returned rather than nil", func() {
				So(got, ShouldNotBeNil)
				So(got, ShouldBeEmpty)
			})
		})

		Convey("When the other tenant lists the same type's rules", func() {
			got, err := store.ListRules(ctx, tenantB, productType)
			So(err, ShouldBeNil)

			Convey("Then it sees only its own rule", func() {
				So(ruleIDs(got), ShouldResemble, []string{foreign.ID.String()})
			})
		})

		Convey("When a rule is deleted by its owner", func() {
			So(store.DeleteRule(ctx, tenantA, first.ID), ShouldBeNil)

			Convey("Then the rule and its dismissals are gone, and sibling rules keep theirs", func() {
				_, err := store.GetRule(ctx, tenantA, first.ID)
				So(domainerrors.IsNotFound(err), ShouldBeTrue)

				remaining, err := store.ListRules(ctx, tenantA, productType)
				So(err, ShouldBeNil)
				So(ruleIDs(remaining), ShouldResemble, []string{second.ID.String()})

				orphaned, err := store.ListDismissals(ctx, tenantA, first.ID)
				So(err, ShouldBeNil)
				So(orphaned, ShouldBeEmpty)

				kept, err := store.ListDismissals(ctx, tenantA, second.ID)
				So(err, ShouldBeNil)
				So(kept, ShouldHaveLength, 1)
			})
		})

		Convey("When a tenant tries to delete another tenant's rule", func() {
			So(store.DeleteRule(ctx, tenantA, foreign.ID), ShouldBeNil)

			Convey("Then the call is a silent no-op and the rule survives", func() {
				got, err := store.GetRule(ctx, tenantB, foreign.ID)
				So(err, ShouldBeNil)
				So(got.ID.String(), ShouldEqual, foreign.ID.String())
			})
		})

		Convey("When an unknown rule id is deleted", func() {
			err := store.DeleteRule(ctx, tenantA, ulid.MustParse(ulidAt('9')))

			Convey("Then deletion is idempotent — no error for an absent rule", func() {
				So(err, ShouldBeNil)
			})
		})
	})
}
