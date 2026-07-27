package flexitype_test

import (
	"context"
	"encoding/json"
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
