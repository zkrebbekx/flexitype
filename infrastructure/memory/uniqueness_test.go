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
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

func TestUniquenessStructural(t *testing.T) {
	Convey("Given unique decimal and json attributes", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory()
		it := svc.Interactors(ctx)

		product, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{InternalName: "product", DisplayName: "Product"})
		So(err, ShouldBeNil)
		typeID := product.ID.String()
		price, err := it.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: typeID, InternalName: "price", DisplayName: "Price", DataType: "decimal", Unique: true,
		})
		So(err, ShouldBeNil)
		payload, err := it.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: typeID, InternalName: "payload", DisplayName: "Payload", DataType: "json", Unique: true,
		})
		So(err, ShouldBeNil)

		set := func(attrID, entity, raw string) error {
			_, e := it.Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: attrID, EntityID: entity, TypeDefinitionID: typeID, Value: json.RawMessage(raw),
			})
			return e
		}

		Convey("Decimals collide numerically (1.5 == 1.50)", func() {
			So(set(price.ID.String(), "p1", `"1.5"`), ShouldBeNil)
			So(domainerrors.IsConflict(set(price.ID.String(), "p2", `"1.50"`)), ShouldBeTrue)
		})

		Convey("JSON objects collide by structure regardless of key order", func() {
			So(set(payload.ID.String(), "p1", `{"a":1,"b":2}`), ShouldBeNil)
			So(domainerrors.IsConflict(set(payload.ID.String(), "p2", `{"b":2,"a":1}`)), ShouldBeTrue)
		})
	})
}
