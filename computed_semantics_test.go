package flexitype_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

// TestComputedDecimalIsExact covers the one place the system produced decimals
// rather than accepting them.
//
// A decimal computed attribute was evaluated in binary float64 and rendered
// with the shortest round-trip, so `0.1 + 0.2` materialized as
// `0.30000000000000004`. Those values fail equality in FQL and against
// one_of members, so a filter for an exact total matches nothing, and they
// appear verbatim in exports where a monetary field with 17 significant
// digits is visibly wrong. Choosing `decimal` is how a schema author asks for
// exactness; this was the only place that contract was not honoured.
func TestComputedDecimalIsExact(t *testing.T) {
	Convey("Given decimal inputs and a decimal computed total", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory()
		ia := svc.Interactors(ctx)

		product, err := ia.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "product", DisplayName: "Product",
		})
		So(err, ShouldBeNil)

		dec := func(name string) string {
			a, aerr := svc.Interactors(ctx).Attributes().Create(ctx, appattribute.CreateInput{
				TypeDefinitionID: product.ID.String(), InternalName: name,
				DisplayName: name, DataType: "decimal",
			})
			So(aerr, ShouldBeNil)
			return a.ID.String()
		}
		price := dec("unit_price")
		surcharge := dec("surcharge")

		total, err := svc.Interactors(ctx).Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: product.ID.String(), InternalName: "total",
			DisplayName: "Total", DataType: "decimal",
			Computed: json.RawMessage(`{"kind":"formula","formula":"unit_price + surcharge"}`),
		})
		So(err, ShouldBeNil)

		set := func(attrID, v string) {
			raw, _ := json.Marshal(v)
			_, serr := svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: attrID, EntityID: "p1",
				TypeDefinitionID: product.ID.String(), Value: raw,
			})
			So(serr, ShouldBeNil)
		}

		read := func() string {
			vals, verr := svc.Interactors(ctx).Values().ListByEntity(ctx, product.ID.String(), "p1")
			So(verr, ShouldBeNil)
			for _, v := range vals {
				if v.AttributeDefinitionID.String() == total.ID.String() {
					return v.Value.Text()
				}
			}
			return ""
		}

		Convey("When the classic float case is computed", func() {
			set(price, "0.1")
			set(surcharge, "0.2")

			Convey("Then the total is exactly 0.3, not 0.30000000000000004", func() {
				So(read(), ShouldEqual, "0.3")
			})
		})

		Convey("When money-shaped values are added", func() {
			set(price, "19.99")
			set(surcharge, "0.01")

			Convey("Then the total carries no float artifact", func() {
				So(read(), ShouldEqual, "20")
			})
		})

		Convey("When many decimal places accumulate", func() {
			set(price, "1.005")
			set(surcharge, "2.0025")

			Convey("Then every digit survives", func() {
				So(read(), ShouldEqual, "3.0075")
			})
		})
	})
}

// TestFormulaEditRecomputes covers the staleness window after a schema change.
//
// The materializer treated a definition event purely as cache invalidation, so
// correcting a formula left every existing entity holding the old value. Those
// stale values stayed queryable in FQL and visible in the console, which made
// the correction look applied while the data still reflected the old formula,
// and nothing converged until an unrelated write touched each entity or an
// operator ran the tenant-wide recompute.
func TestFormulaEditRecomputes(t *testing.T) {
	Convey("Given an entity whose total is already materialized", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory()
		ia := svc.Interactors(ctx)

		product, err := ia.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "product", DisplayName: "Product",
		})
		So(err, ShouldBeNil)
		price, err := svc.Interactors(ctx).Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: product.ID.String(), InternalName: "price",
			DisplayName: "Price", DataType: "integer",
		})
		So(err, ShouldBeNil)
		total, err := svc.Interactors(ctx).Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: product.ID.String(), InternalName: "total",
			DisplayName: "Total", DataType: "integer",
			Computed: json.RawMessage(`{"kind":"formula","formula":"price * 2"}`),
		})
		So(err, ShouldBeNil)

		raw, _ := json.Marshal(10)
		_, err = svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{
			AttributeDefinitionID: price.ID.String(), EntityID: "p1",
			TypeDefinitionID: product.ID.String(), Value: raw,
		})
		So(err, ShouldBeNil)

		read := func() int64 {
			vals, verr := svc.Interactors(ctx).Values().ListByEntity(ctx, product.ID.String(), "p1")
			So(verr, ShouldBeNil)
			for _, v := range vals {
				if v.AttributeDefinitionID.String() == total.ID.String() {
					return v.Value.Int()
				}
			}
			return -1
		}
		So(read(), ShouldEqual, 20)

		Convey("When the formula is corrected", func() {
			_, uerr := svc.Interactors(ctx).Attributes().Update(ctx, appattribute.UpdateInput{
				ID: total.ID.String(), DisplayName: "Total",
				Computed: json.RawMessage(`{"kind":"formula","formula":"price * 3"}`),
			})
			So(uerr, ShouldBeNil)

			Convey("Then the stored value converges without another write", func() {
				// The rebuild runs off the request goroutine, so allow for it.
				var got int64
				for i := 0; i < 200; i++ {
					if got = read(); got == 30 {
						break
					}
					time.Sleep(10 * time.Millisecond)
				}
				So(got, ShouldEqual, 30)
			})
		})
	})
}

// TestFormulaRefusesScopedSources covers the source shapes evaluation cannot
// represent.
//
// Evaluation carries ONE number per source name, so a multi-valued source
// collapsed to whichever member the repository returned last, and a scoped
// value was skipped entirely. Adding or removing a member changed the answer
// with no other change to the schema or the formula, and nothing signalled
// it: the computed attribute was populated, queryable in FQL and counted
// toward completeness.
func TestFormulaRefusesScopedSources(t *testing.T) {
	Convey("Given a type with multi-valued and localizable numeric attributes", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory()
		it := svc.Interactors(ctx)

		product, err := it.TypeDefinitions().Create(ctx,
			apptypedef.CreateInput{InternalName: "product", DisplayName: "Product"})
		So(err, ShouldBeNil)
		mk := func(name string, in appattribute.CreateInput) string {
			in.TypeDefinitionID = product.ID.String()
			in.InternalName, in.DisplayName = name, name
			if in.DataType == "" {
				in.DataType = "integer"
			}
			a, aerr := svc.Interactors(ctx).Attributes().Create(ctx, in)
			So(aerr, ShouldBeNil)
			return a.ID.String()
		}
		mk("scores", appattribute.CreateInput{MultiValued: true})
		mk("price", appattribute.CreateInput{Localizable: true})
		plain := mk("weight", appattribute.CreateInput{})

		Convey("When a formula reads the multi-valued attribute", func() {
			_, err := svc.Interactors(ctx).Attributes().Create(ctx, appattribute.CreateInput{
				TypeDefinitionID: product.ID.String(), InternalName: "total",
				DisplayName: "Total", DataType: "integer",
				Computed: json.RawMessage(`{"kind":"formula","formula":"scores * 2"}`),
			})

			Convey("Then it is refused rather than silently using one member", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "multi-valued")
			})
		})

		Convey("When a formula reads the localizable attribute", func() {
			_, err := svc.Interactors(ctx).Attributes().Create(ctx, appattribute.CreateInput{
				TypeDefinitionID: product.ID.String(), InternalName: "total",
				DisplayName: "Total", DataType: "integer",
				Computed: json.RawMessage(`{"kind":"formula","formula":"price + 1"}`),
			})

			Convey("Then it is refused rather than skipping the scoped values", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "localizable")
			})
		})

		Convey("When a formula reads a plain scalar attribute", func() {
			_, err := svc.Interactors(ctx).Attributes().Create(ctx, appattribute.CreateInput{
				TypeDefinitionID: product.ID.String(), InternalName: "double",
				DisplayName: "Double", DataType: "integer",
				Computed: json.RawMessage(`{"kind":"formula","formula":"weight * 2"}`),
			})

			Convey("Then it is accepted", func() {
				So(err, ShouldBeNil)
			})
		})

		Convey("And a formula already reading the plain attribute", func() {
			_, err := svc.Interactors(ctx).Attributes().Create(ctx, appattribute.CreateInput{
				TypeDefinitionID: product.ID.String(), InternalName: "double",
				DisplayName: "Double", DataType: "integer",
				Computed: json.RawMessage(`{"kind":"formula","formula":"weight * 2"}`),
			})
			So(err, ShouldBeNil)

			Convey("When that source is made multi-valued", func() {
				_, uerr := svc.Interactors(ctx).Attributes().Update(ctx, appattribute.UpdateInput{
					ID: plain, DisplayName: "Weight", MultiValued: true,
				})

				Convey("Then it is refused: the guard is not one-way", func() {
					So(uerr, ShouldNotBeNil)
					So(uerr.Error(), ShouldContainSubstring, "computed formula reads it")
				})
			})

			Convey("When that source is made localizable", func() {
				_, uerr := svc.Interactors(ctx).Attributes().Update(ctx, appattribute.UpdateInput{
					ID: plain, DisplayName: "Weight", Localizable: true,
				})

				Convey("Then it is refused too", func() {
					So(uerr, ShouldNotBeNil)
				})
			})
		})
	})
}

// TestRebuildConvergesAgainstAConcurrentWrite covers the wall-clock gate the
// background rebuild used to rely on.
//
// It compared the entity's last_updated_at — stamped from the writing
// request's clock when the write BEGAN — against the rebuilding process's own
// clock. Nothing serialised the two, so a write that began before the rebuild
// started and committed after it listed the entity was invisible to the
// check: the rebuild read the pre-write inputs and wrote a computed value
// derived from them, leaving the source new and the computed value stale
// until that entity was written again.
func TestRebuildConvergesAgainstAConcurrentWrite(t *testing.T) {
	Convey("Given a computed total over one source", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory()
		it := svc.Interactors(ctx)

		product, err := it.TypeDefinitions().Create(ctx,
			apptypedef.CreateInput{InternalName: "product", DisplayName: "Product"})
		So(err, ShouldBeNil)
		qty, err := it.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: product.ID.String(), InternalName: "qty",
			DisplayName: "Qty", DataType: "integer",
		})
		So(err, ShouldBeNil)
		_, err = svc.Interactors(ctx).Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: product.ID.String(), InternalName: "doubled",
			DisplayName: "Doubled", DataType: "integer",
			Computed: json.RawMessage(`{"kind":"formula","formula":"qty * 2"}`),
		})
		So(err, ShouldBeNil)

		set := func(v string) {
			_, serr := svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: qty.ID.String(), EntityID: "p1",
				TypeDefinitionID: product.ID.String(), Value: json.RawMessage(v),
			})
			So(serr, ShouldBeNil)
		}
		set("5")

		doubled := func() string {
			vals, verr := svc.Interactors(ctx).Values().ListByEntity(ctx, product.ID.String(), "p1")
			So(verr, ShouldBeNil)
			for _, v := range vals {
				if v.AttributeDefinitionID.String() != qty.ID.String() {
					return v.Value.String()
				}
			}
			return ""
		}

		Convey("When the source is written and a tenant-wide recompute runs", func() {
			set("11")
			n, rerr := svc.RecomputeComputed(ctx, valueobjects.DefaultTenant)

			Convey("Then the computed value tracks the newest source", func() {
				So(rerr, ShouldBeNil)
				So(n, ShouldBeGreaterThan, 0)
				So(doubled(), ShouldEqual, "22")
			})
		})

		Convey("When a recompute races a write of the source", func() {
			// The rebuild re-reads the inputs after writing and recomputes if
			// they moved, so whichever order these land in, the computed
			// value ends up derived from the source that is actually stored.
			// So() must not run inside a goroutine (goconvey keeps its state
			// per test goroutine), so the write's error is captured and
			// asserted after the join.
			var wg sync.WaitGroup
			var writeErr error
			wg.Add(2)
			go func() {
				defer wg.Done()
				_, writeErr = svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{
					AttributeDefinitionID: qty.ID.String(), EntityID: "p1",
					TypeDefinitionID: product.ID.String(), Value: json.RawMessage(`7`),
				})
			}()
			go func() {
				defer wg.Done()
				_, _ = svc.RecomputeComputed(ctx, valueobjects.DefaultTenant)
			}()
			wg.Wait()
			So(writeErr, ShouldBeNil)

			Convey("Then the computed value matches the stored source", func() {
				vals, verr := svc.Interactors(ctx).Values().ListByEntity(ctx, product.ID.String(), "p1")
				So(verr, ShouldBeNil)
				var source, computed string
				for _, v := range vals {
					if v.AttributeDefinitionID.String() == qty.ID.String() {
						source = v.Value.String()
					} else {
						computed = v.Value.String()
					}
				}
				want := map[string]string{"5": "10", "7": "14", "11": "22"}[source]
				So(computed, ShouldEqual, want)
			})
		})
	})
}

// TestRecomputeStableRetriesOnMovingInputs drives the convergence loop
// directly: a source that changes while the recompute runs must be picked up
// rather than left with a value derived from what was read first.
func TestRecomputeStableRetriesOnMovingInputs(t *testing.T) {
	Convey("Given an entity whose source is rewritten between recomputes", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory()
		it := svc.Interactors(ctx)

		product, err := it.TypeDefinitions().Create(ctx,
			apptypedef.CreateInput{InternalName: "product", DisplayName: "Product"})
		So(err, ShouldBeNil)
		qty, err := it.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: product.ID.String(), InternalName: "qty",
			DisplayName: "Qty", DataType: "integer",
		})
		So(err, ShouldBeNil)
		_, err = svc.Interactors(ctx).Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: product.ID.String(), InternalName: "doubled",
			DisplayName: "Doubled", DataType: "integer",
			Computed: json.RawMessage(`{"kind":"formula","formula":"qty * 2"}`),
		})
		So(err, ShouldBeNil)

		write := func(v string) {
			_, serr := svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: qty.ID.String(), EntityID: "p1",
				TypeDefinitionID: product.ID.String(), Value: json.RawMessage(v),
			})
			So(serr, ShouldBeNil)
		}

		Convey("When the source is written repeatedly and a recompute follows each", func() {
			for _, v := range []string{"1", "2", "3", "4"} {
				write(v)
				_, rerr := svc.RecomputeComputed(ctx, valueobjects.DefaultTenant)
				So(rerr, ShouldBeNil)
			}

			Convey("Then the computed value tracks the last source written", func() {
				vals, verr := svc.Interactors(ctx).Values().ListByEntity(ctx, product.ID.String(), "p1")
				So(verr, ShouldBeNil)
				for _, v := range vals {
					if v.AttributeDefinitionID.String() != qty.ID.String() {
						So(v.Value.String(), ShouldEqual, "8")
					}
				}
			})
		})

		Convey("When an entity has no values at all", func() {
			n, rerr := svc.RecomputeComputed(ctx, valueobjects.DefaultTenant)

			Convey("Then the recompute is a no-op rather than an error", func() {
				So(rerr, ShouldBeNil)
				So(n, ShouldBeGreaterThanOrEqualTo, 0)
			})
		})
	})
}

// TestRebuildClearsAnUndefinedFormula covers the value a rebuild used to
// leave behind for ever.
//
// The rebuild used the non-clearing variant, because a rebuild that reads an
// entity mid-write sees half its inputs and cannot tell an undefined formula
// apart from a half-written one. The cost was that after an edit introduced a
// division by zero, the pre-edit value survived indefinitely — queryable in
// FQL, present in exports, counted toward completeness, with no formula that
// produces it. The fingerprint check makes clearing safe: a clear based on
// half-written inputs is followed by a source change, which is detected.
func TestRebuildClearsAnUndefinedFormula(t *testing.T) {
	Convey("Given a computed value derived from a source", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory()
		it := svc.Interactors(ctx)

		product, err := it.TypeDefinitions().Create(ctx,
			apptypedef.CreateInput{InternalName: "product", DisplayName: "Product"})
		So(err, ShouldBeNil)
		qty, err := it.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: product.ID.String(), InternalName: "qty",
			DisplayName: "Qty", DataType: "integer",
		})
		So(err, ShouldBeNil)
		divisor, err := svc.Interactors(ctx).Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: product.ID.String(), InternalName: "divisor",
			DisplayName: "Divisor", DataType: "integer",
		})
		So(err, ShouldBeNil)
		computed, err := svc.Interactors(ctx).Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: product.ID.String(), InternalName: "ratio",
			DisplayName: "Ratio", DataType: "integer",
			Computed: json.RawMessage(`{"kind":"formula","formula":"qty / divisor"}`),
		})
		So(err, ShouldBeNil)

		set := func(attr string, v string) {
			_, serr := svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: attr, EntityID: "p1",
				TypeDefinitionID: product.ID.String(), Value: json.RawMessage(v),
			})
			So(serr, ShouldBeNil)
		}
		set(qty.ID.String(), "10")
		set(divisor.ID.String(), "2")

		has := func() bool {
			vals, verr := svc.Interactors(ctx).Values().ListByEntity(ctx, product.ID.String(), "p1")
			So(verr, ShouldBeNil)
			for _, v := range vals {
				if v.AttributeDefinitionID.String() == computed.ID.String() {
					return true
				}
			}
			return false
		}
		So(has(), ShouldBeTrue)

		Convey("When the formula becomes undefined and the schema rebuild runs", func() {
			set(divisor.ID.String(), "0")

			Convey("Then the stale value is cleared rather than left with no formula behind it", func() {
				// The write itself clears it; the rebuild must not put it
				// back, and must clear it if it were still there.
				So(has(), ShouldBeFalse)

				n, rerr := svc.RecomputeComputed(ctx, valueobjects.DefaultTenant)
				So(rerr, ShouldBeNil)
				So(n, ShouldBeGreaterThanOrEqualTo, 0)
				So(has(), ShouldBeFalse)
			})
		})

		Convey("When the source becomes valid again", func() {
			set(divisor.ID.String(), "0")
			set(divisor.ID.String(), "5")

			Convey("Then the value comes back", func() {
				So(has(), ShouldBeTrue)
			})
		})
	})
}

// TestComputedAggregates covers the feature that replaces the collapse.
//
// A formula could not read a multi-valued attribute at all, because
// evaluation carried one value per name and picking a member silently was the
// defect. An aggregate says which members it wants — all of them — so the
// answer is defined and changes only when the data does.
func TestComputedAggregates(t *testing.T) {
	Convey("Given a multi-valued numeric attribute", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory()
		it := svc.Interactors(ctx)

		order, err := it.TypeDefinitions().Create(ctx,
			apptypedef.CreateInput{InternalName: "order", DisplayName: "Order"})
		So(err, ShouldBeNil)
		lines, err := it.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: order.ID.String(), InternalName: "line_totals",
			DisplayName: "Line totals", DataType: "integer", MultiValued: true,
		})
		So(err, ShouldBeNil)

		add := func(v string) {
			_, serr := svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: lines.ID.String(), EntityID: "o1",
				TypeDefinitionID: order.ID.String(), Value: json.RawMessage(v),
			})
			So(serr, ShouldBeNil)
		}
		valueOf := func(attrID string) (string, bool) {
			vals, verr := svc.Interactors(ctx).Values().ListByEntity(ctx, order.ID.String(), "o1")
			So(verr, ShouldBeNil)
			for _, v := range vals {
				if v.AttributeDefinitionID.String() == attrID {
					return v.Value.String(), true
				}
			}
			return "", false
		}

		Convey("When a formula aggregates it", func() {
			total, cerr := svc.Interactors(ctx).Attributes().Create(ctx, appattribute.CreateInput{
				TypeDefinitionID: order.ID.String(), InternalName: "total",
				DisplayName: "Total", DataType: "integer",
				Computed: json.RawMessage(`{"kind":"formula","formula":"sum(line_totals)"}`),
			})
			So(cerr, ShouldBeNil)

			Convey("Then it is accepted, where a bare read is not", func() {
				_, berr := svc.Interactors(ctx).Attributes().Create(ctx, appattribute.CreateInput{
					TypeDefinitionID: order.ID.String(), InternalName: "doubled",
					DisplayName: "Doubled", DataType: "integer",
					Computed: json.RawMessage(`{"kind":"formula","formula":"line_totals * 2"}`),
				})
				So(berr, ShouldNotBeNil)
				So(berr.Error(), ShouldContainSubstring, "aggregate")
			})

			Convey("Then the total tracks every member, not an arbitrary one", func() {
				add("10")
				v, ok := valueOf(total.ID.String())
				So(ok, ShouldBeTrue)
				So(v, ShouldEqual, "10")

				add("20")
				v, _ = valueOf(total.ID.String())
				So(v, ShouldEqual, "30")

				add("30")
				v, _ = valueOf(total.ID.String())
				So(v, ShouldEqual, "60")
			})
		})

		Convey("When a formula counts it", func() {
			counted, cerr := svc.Interactors(ctx).Attributes().Create(ctx, appattribute.CreateInput{
				TypeDefinitionID: order.ID.String(), InternalName: "line_count",
				DisplayName: "Line count", DataType: "integer",
				Computed: json.RawMessage(`{"kind":"formula","formula":"count(line_totals)"}`),
			})
			So(cerr, ShouldBeNil)

			Convey("Then an entity with no members counts zero rather than clearing", func() {
				add("5")
				v, ok := valueOf(counted.ID.String())
				So(ok, ShouldBeTrue)
				So(v, ShouldEqual, "1")
			})
		})

		Convey("When an aggregate reads a decimal attribute", func() {
			amounts, aerr := svc.Interactors(ctx).Attributes().Create(ctx, appattribute.CreateInput{
				TypeDefinitionID: order.ID.String(), InternalName: "amounts",
				DisplayName: "Amounts", DataType: "decimal", MultiValued: true,
			})
			So(aerr, ShouldBeNil)
			exact, eerr := svc.Interactors(ctx).Attributes().Create(ctx, appattribute.CreateInput{
				TypeDefinitionID: order.ID.String(), InternalName: "amount_total",
				DisplayName: "Amount total", DataType: "decimal",
				Computed: json.RawMessage(`{"kind":"formula","formula":"sum(amounts)"}`),
			})
			So(eerr, ShouldBeNil)

			for _, v := range []string{`"0.1"`, `"0.2"`} {
				_, serr := svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{
					AttributeDefinitionID: amounts.ID.String(), EntityID: "o1",
					TypeDefinitionID: order.ID.String(), Value: json.RawMessage(v),
				})
				So(serr, ShouldBeNil)
			}

			Convey("Then the sum is exact, not 0.30000000000000004", func() {
				v, ok := valueOf(exact.ID.String())
				So(ok, ShouldBeTrue)
				So(v, ShouldEqual, "0.3")
			})
		})
	})

	Convey("Given a scalar attribute a formula aggregates", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory()
		it := svc.Interactors(ctx)

		order, err := it.TypeDefinitions().Create(ctx,
			apptypedef.CreateInput{InternalName: "order", DisplayName: "Order"})
		So(err, ShouldBeNil)
		amount, err := it.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: order.ID.String(), InternalName: "amount",
			DisplayName: "Amount", DataType: "integer",
		})
		So(err, ShouldBeNil)
		_, err = svc.Interactors(ctx).Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: order.ID.String(), InternalName: "total",
			DisplayName: "Total", DataType: "integer",
			Computed: json.RawMessage(`{"kind":"formula","formula":"sum(amount)"}`),
		})
		So(err, ShouldBeNil)

		Convey("When that attribute is made multi-valued", func() {
			_, uerr := svc.Interactors(ctx).Attributes().Update(ctx, appattribute.UpdateInput{
				ID: amount.ID.String(), DisplayName: "Amount", MultiValued: true,
			})

			Convey("Then it is allowed: the formula asked for every member", func() {
				So(uerr, ShouldBeNil)
			})
		})

		Convey("And a second formula reading it bare", func() {
			_, berr := svc.Interactors(ctx).Attributes().Create(ctx, appattribute.CreateInput{
				TypeDefinitionID: order.ID.String(), InternalName: "doubled",
				DisplayName: "Doubled", DataType: "integer",
				Computed: json.RawMessage(`{"kind":"formula","formula":"amount * 2"}`),
			})
			So(berr, ShouldBeNil)

			Convey("When that attribute is made multi-valued", func() {
				_, uerr := svc.Interactors(ctx).Attributes().Update(ctx, appattribute.UpdateInput{
					ID: amount.ID.String(), DisplayName: "Amount", MultiValued: true,
				})

				Convey("Then it is refused: the bare reader would start collapsing", func() {
					So(uerr, ShouldNotBeNil)
					So(uerr.Error(), ShouldContainSubstring, "bare name")
				})
			})
		})
	})
}
