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

// TestComputedCannotBeMultiValued is the regression for #490.
//
// The combination was accepted and then accumulated: the materializer writes
// one result per entity, but a multi-valued write APPENDS rather than
// replaces, and the clear-stale path tracks a single value id — so
// `total = qty * 2` went to [2, 4, 6] as qty moved 1, 2, 3, with nothing able
// to remove the earlier results and nothing reported at creation.
func TestComputedCannotBeMultiValued(t *testing.T) {
	Convey("Given a type with a source attribute", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory()
		ia := svc.Interactors(ctx)

		product, err := ia.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "product", DisplayName: "Product",
		})
		So(err, ShouldBeNil)
		_, err = svc.Interactors(ctx).Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: product.ID.String(), InternalName: "qty",
			DisplayName: "Qty", DataType: "integer",
		})
		So(err, ShouldBeNil)

		create := func(name string, multi bool, formula string) error {
			in := appattribute.CreateInput{
				TypeDefinitionID: product.ID.String(), InternalName: name,
				DisplayName: name, DataType: "integer", MultiValued: multi,
			}
			if formula != "" {
				in.Computed = json.RawMessage(`{"kind":"formula","formula":"` + formula + `"}`)
			}
			_, cerr := svc.Interactors(ctx).Attributes().Create(ctx, in)
			return cerr
		}

		Convey("When a computed attribute is created multi-valued", func() {
			err := create("total", true, "qty * 2")

			Convey("Then it is refused, saying why the results would accumulate", func() {
				So(domainerrors.CodeOf(err), ShouldEqual, domainerrors.CodeValidation)
				So(err.Error(), ShouldContainSubstring, "cannot be multi-valued")
			})
		})

		Convey("When a computed single-valued attribute is created", func() {
			Convey("Then it is accepted", func() {
				So(create("total", false, "qty * 2"), ShouldBeNil)
			})
		})

		Convey("When a plain multi-valued attribute is created", func() {
			Convey("Then it is accepted", func() {
				So(create("tags", true, ""), ShouldBeNil)
			})
		})

		Convey("When an existing computed attribute is updated to multi-valued", func() {
			So(create("total", false, "qty * 2"), ShouldBeNil)
			list, lerr := svc.Interactors(ctx).Attributes().List(ctx, appattribute.ListInput{
				TypeDefinitionID: product.ID.String(),
			})
			So(lerr, ShouldBeNil)
			var totalID string
			for _, a := range list.Items {
				if a.InternalName == "total" {
					totalID = a.ID.String()
				}
			}
			So(totalID, ShouldNotBeEmpty)

			_, uerr := svc.Interactors(ctx).Attributes().Update(ctx, appattribute.UpdateInput{
				ID: totalID, DisplayName: "Total", MultiValued: true,
				Computed: json.RawMessage(`{"kind":"formula","formula":"qty * 2"}`),
			})

			Convey("Then the transition is refused too", func() {
				So(domainerrors.CodeOf(uerr), ShouldEqual, domainerrors.CodeValidation)
				So(uerr.Error(), ShouldContainSubstring, "cannot be multi-valued")
			})
		})
	})
}

// TestRestoreKeepsAnEqualButDifferentlyWrittenValue is the regression
// for #493.
//
// ApplySnapshot built its target set from Value.String while the set pass
// skips a write when the stored value is Equal to the cell. An
// Equal-but-differently-rendered value therefore split the two passes: the
// set pass no-opped, the archive pass did not find the stored rendering in
// the target, and restoring a snapshot that HELD a value left the entity
// with none.
func TestRestoreKeepsAnEqualButDifferentlyWrittenValue(t *testing.T) {
	Convey("Given a multi-valued attribute whose value is rewritten in another form", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory()
		ia := svc.Interactors(ctx)

		product, err := ia.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "product", DisplayName: "Product",
		})
		So(err, ShouldBeNil)
		mass, err := svc.Interactors(ctx).Units().Create(ctx, appunit.CreateInput{
			Name: "mass", BaseUnit: "g", Units: map[string]float64{"g": 1, "kg": 1000},
		})
		So(err, ShouldBeNil)

		attr := func(in appattribute.CreateInput) string {
			in.TypeDefinitionID = product.ID.String()
			in.DisplayName = in.InternalName
			a, aerr := svc.Interactors(ctx).Attributes().Create(ctx, in)
			So(aerr, ShouldBeNil)
			return a.ID.String()
		}
		set := func(attrID, raw string) string {
			snap, serr := svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: attrID, EntityID: "p1",
				TypeDefinitionID: product.ID.String(), Value: json.RawMessage(raw),
			})
			So(serr, ShouldBeNil)
			return snap.ID.String()
		}
		live := func(attrID string) []string {
			vals, verr := svc.Interactors(ctx).Values().ListByEntity(ctx, product.ID.String(), "p1")
			So(verr, ShouldBeNil)
			out := []string{}
			for _, v := range vals {
				if v.AttributeDefinitionID.String() == attrID {
					out = append(out, v.Value.String())
				}
			}
			return out
		}

		for _, tc := range []struct {
			label      string
			in         appattribute.CreateInput
			captured   string
			rewrittenA string
		}{
			{
				label:      "a decimal with trailing zeros",
				in:         appattribute.CreateInput{InternalName: "price", DataType: "decimal", MultiValued: true},
				captured:   `"1.50"`,
				rewrittenA: `"1.5"`,
			},
			{
				label:      "a quantity in another unit",
				in:         appattribute.CreateInput{InternalName: "weight", DataType: "quantity", MultiValued: true},
				captured:   `{"magnitude":"5","unit":"kg"}`,
				rewrittenA: `{"magnitude":"5000","unit":"g"}`,
			},
		} {
			Convey("When the snapshot holds "+tc.label, func() {
				in := tc.in
				if in.DataType == "quantity" {
					in.UnitFamilyID = mass.ID.String()
					in.DisplayUnit = "g"
				}
				id := attr(in)

				valueID := set(id, tc.captured)
				rev, rerr := svc.Interactors(ctx).Revisions().Create(ctx, product.ID.String(), "p1", "before")
				So(rerr, ShouldBeNil)
				So(rev.Values, ShouldHaveLength, 1)

				// Rewrite the same value in the other form.
				_, remErr := svc.Interactors(ctx).Values().Remove(ctx, valueID)
				So(remErr, ShouldBeNil)
				set(id, tc.rewrittenA)
				So(live(id), ShouldHaveLength, 1)

				_, rserr := svc.Interactors(ctx).Revisions().Restore(ctx, rev.ID.String())
				So(rserr, ShouldBeNil)

				Convey("Then the restore keeps it, rather than archiving what it restored", func() {
					So(live(id), ShouldHaveLength, 1)
				})
			})
		}
	})
}
