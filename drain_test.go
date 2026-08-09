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

// TestDrainWaitsForDetachedWork covers the one piece of work a caller cannot
// otherwise wait for.
//
// A schema change schedules a rebuild that deliberately outlives the request,
// on a context detached from it. Nothing the caller holds can await that, so
// "is the database quiet?" had no answer: an operator could close the pool
// under a running rebuild, and a test could not tell a quiet moment from a
// rebuild about to start — which is how a background recompute came to land
// inside the next test's TRUNCATE.
func TestDrainWaitsForDetachedWork(t *testing.T) {
	Convey("Given a type whose schema change schedules a rebuild", t, func() {
		svc := flexitype.NewInMemory()
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		ia := svc.Interactors(ctx)

		product, err := ia.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "product", DisplayName: "Product",
		})
		So(err, ShouldBeNil)
		price, err := ia.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: product.ID.String(), InternalName: "price",
			DisplayName: "Price", DataType: "decimal",
		})
		So(err, ShouldBeNil)
		raw, _ := json.Marshal("10.00")
		_, err = ia.Values().Set(ctx, appvalue.SetInput{
			AttributeDefinitionID: price.ID.String(), EntityID: "p1",
			TypeDefinitionID: product.ID.String(), Value: raw,
		})
		So(err, ShouldBeNil)

		Convey("When a computed attribute is added and the service is drained", func() {
			// Creating this schedules the detached rebuild.
			_, cerr := ia.Attributes().Create(ctx, appattribute.CreateInput{
				TypeDefinitionID: product.ID.String(), InternalName: "double_price",
				DisplayName: "Double", DataType: "decimal",
				Computed: json.RawMessage(`{"kind":"formula","formula":"price * 2"}`),
			})
			So(cerr, ShouldBeNil)

			drainCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			So(svc.Drain(drainCtx), ShouldBeNil)

			Convey("Then the rebuild has finished, not merely been scheduled", func() {
				// The rebuild settles before it runs, so an undrained service
				// would still be asleep here and this value would be absent.
				values, verr := svc.Interactors(ctx).Values().ListByEntity(ctx, product.ID.String(), "p1")
				So(verr, ShouldBeNil)
				var doubled string
				for _, v := range values {
					if v.AttributeDefinitionID.String() != price.ID.String() {
						doubled = v.Value.String()
					}
				}
				So(doubled, ShouldEqual, "20")
			})
		})

		Convey("When a service with no background work is drained", func() {
			quiet := flexitype.NewInMemory()

			Convey("Then it returns immediately", func() {
				So(quiet.Drain(context.Background()), ShouldBeNil)
			})
		})

		Convey("When Drain is called twice", func() {
			So(svc.Drain(context.Background()), ShouldBeNil)

			Convey("Then the second call is a no-op", func() {
				So(svc.Drain(context.Background()), ShouldBeNil)
			})
		})
	})
}
