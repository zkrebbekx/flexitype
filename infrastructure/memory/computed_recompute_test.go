package memory_test

import (
	"context"
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

// TestRecomputeComputedRecovery covers the recovery path for #211's in-UoW
// projection maintenance: a computed value left unmaterialized (as a
// mid-post-commit crash would) is rebuilt by the tenant-wide recompute.
func TestRecomputeComputedRecovery(t *testing.T) {
	Convey("Given values written before a computed attribute exists — so its value is unmaterialized", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory()
		it := svc.Interactors(ctx)

		product, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{InternalName: "product", DisplayName: "Product"})
		So(err, ShouldBeNil)
		typeID := product.ID.String()

		mk := func(name string) string {
			a, e := it.Attributes().Create(ctx, appattribute.CreateInput{
				TypeDefinitionID: typeID, InternalName: name, DisplayName: name, DataType: "float",
			})
			So(e, ShouldBeNil)
			return a.ID.String()
		}
		priceID, costID := mk("price"), mk("cost")

		set := func(attrID string, v float64) {
			raw, _ := json.Marshal(v)
			_, e := svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: attrID, EntityID: "p1", TypeDefinitionID: typeID, Value: raw,
			})
			So(e, ShouldBeNil)
		}
		set(priceID, 100)
		set(costID, 40)

		// Adding the computed attribute AFTER the values only fires a definition
		// event, which invalidates the has-computed cache but does not recompute
		// existing entities — so p1's margin stays unmaterialized, mirroring a
		// projection a crash left stale.
		margin, err := it.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: typeID, InternalName: "margin", DisplayName: "Margin", DataType: "float",
			Computed: json.RawMessage(`{"kind":"formula","formula":"(price - cost) / price"}`),
		})
		So(err, ShouldBeNil)

		marginOf := func() (float64, bool) {
			vals, e := svc.Interactors(ctx).Values().ListByEntity(ctx, typeID, "p1")
			So(e, ShouldBeNil)
			for _, v := range vals {
				if v.AttributeDefinitionID.String() == margin.ID.String() {
					return v.Value.Float(), true
				}
			}
			return 0, false
		}

		Convey("The computed value is initially absent", func() {
			_, ok := marginOf()
			So(ok, ShouldBeFalse)
		})

		Convey("RecomputeComputed rebuilds it for the tenant", func() {
			n, err := svc.RecomputeComputed(ctx, valueobjects.DefaultTenant)
			So(err, ShouldBeNil)
			So(n, ShouldBeGreaterThanOrEqualTo, 1)

			m, ok := marginOf()
			So(ok, ShouldBeTrue)
			So(m, ShouldAlmostEqual, 0.6)
		})
	})
}
