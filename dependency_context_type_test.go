package flexitype_test

import (
	"context"
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	appdependency "github.com/zkrebbekx/flexitype/application/dependency"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

// TestContextConditionsAreTypedAgainstTheFact is the regression for #482.
//
// A context condition's subject is the caller-supplied fact, but Validate
// checked its operands against the SOURCE attribute's type and evaluation
// compared the fact raw. A numeric rule over a context fact was unbuildable
// unless the unrelated source attribute happened to be ordered and of the
// operand's type, and a fact arriving as a different type turned every write
// to the target into an unclassified comparison error (500). Conditions now
// declare context_type, operands validate against it, and a fact of a
// different runtime type is a non-match — the same fail-safe as an absent
// fact.
func TestContextConditionsAreTypedAgainstTheFact(t *testing.T) {
	Convey("Given an order type with integer qty and string approver", t, func() {
		admin := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory()
		ia := svc.Interactors(admin)

		order, err := ia.TypeDefinitions().Create(admin, apptypedef.CreateInput{
			InternalName: "order", DisplayName: "Order",
		})
		So(err, ShouldBeNil)
		mk := func(name, dt string) string {
			a, aerr := ia.Attributes().Create(admin, appattribute.CreateInput{
				TypeDefinitionID: order.ID.String(), InternalName: name,
				DisplayName: name, DataType: dt,
			})
			So(aerr, ShouldBeNil)
			return a.ID.String()
		}
		qty := mk("qty", "integer")
		approver := mk("approver", "string")

		createRule := func(conditions string) error {
			_, cerr := ia.Dependencies().Create(admin, appdependency.CreateInput{
				SourceAttributeID: qty,
				TargetAttributeID: approver,
				Conditions:        json.RawMessage(conditions),
				Effect:            json.RawMessage(`{"required":true}`),
			})
			return cerr
		}

		Convey("When a float range over a context fact is authored on an integer source", func() {
			err := createRule(`[{"kind":"range","context_key":"order_total","context_type":"float",` +
				`"min":{"type":"float","value":5000},"min_exclusive":true}]`)

			Convey("Then the rule is accepted: operands validate against the declared fact type", func() {
				So(err, ShouldBeNil)
			})

			Convey("And the rule fires only on a fact of the declared type", func() {
				So(err, ShouldBeNil)
				required := func(ctx context.Context) bool {
					eff, eerr := svc.Interactors(ctx).Dependencies().EffectiveSchema(ctx, approver, "o1")
					So(eerr, ShouldBeNil)
					return eff.Required
				}

				high := uow.WithContextValues(admin, map[string]valueobjects.Value{
					"order_total": valueobjects.NewFloatValue(9000),
				})
				So(required(high), ShouldBeTrue)

				low := uow.WithContextValues(admin, map[string]valueobjects.Value{
					"order_total": valueobjects.NewFloatValue(100),
				})
				So(required(low), ShouldBeFalse)

				// The host sends the fact as a string: wrong type, so the
				// rule does not fire — and nothing returns an error.
				mistyped := uow.WithContextValues(admin, map[string]valueobjects.Value{
					"order_total": valueobjects.NewStringValue("9000"),
				})
				So(required(mistyped), ShouldBeFalse)
			})

			Convey("And a write to the target under a mistyped fact succeeds instead of failing", func() {
				So(err, ShouldBeNil)
				mistyped := uow.WithContextValues(admin, map[string]valueobjects.Value{
					"order_total": valueobjects.NewStringValue("9000"),
				})
				raw, _ := json.Marshal("alice")
				_, serr := svc.Interactors(mistyped).Values().Set(mistyped, appvalue.SetInput{
					AttributeDefinitionID: approver, EntityID: "o1",
					TypeDefinitionID: order.ID.String(), Value: raw,
				})
				So(serr, ShouldBeNil)
			})
		})

		Convey("When a context condition is authored without context_type", func() {
			err := createRule(`[{"kind":"equals","context_key":"customer_tier",` +
				`"value":{"type":"string","value":"enterprise"}}]`)

			Convey("Then it is refused with a validation error naming context_type", func() {
				So(domainerrors.CodeOf(err), ShouldEqual, domainerrors.CodeValidation)
				So(err.Error(), ShouldContainSubstring, "context_type")
			})
		})

		Convey("When context_type is set without context_key", func() {
			err := createRule(`[{"kind":"equals","context_type":"string",` +
				`"value":{"type":"string","value":"x"}}]`)

			Convey("Then it is refused", func() {
				So(domainerrors.CodeOf(err), ShouldEqual, domainerrors.CodeValidation)
			})
		})

		Convey("When context_type is not a data type", func() {
			err := createRule(`[{"kind":"equals","context_key":"tier","context_type":"money",` +
				`"value":{"type":"string","value":"x"}}]`)

			Convey("Then it is refused", func() {
				So(domainerrors.CodeOf(err), ShouldEqual, domainerrors.CodeValidation)
				So(err.Error(), ShouldContainSubstring, "context_type")
			})
		})
	})
}
