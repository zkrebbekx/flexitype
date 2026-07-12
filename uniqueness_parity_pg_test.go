package flexitype_test

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

// TestUniquenessParityPostgres pins the memory↔Postgres uniqueness semantics
// for decimal and json values: decimals compare numerically and json compares
// structurally on BOTH backends, so a logical duplicate that the memory twin
// (and its tests) reject cannot slip into Postgres.
func TestUniquenessParityPostgres(t *testing.T) {
	pool := openTestDB(t)
	defer func() { _ = pool.Close() }()

	svc := flexitype.New(pool)
	if err := svc.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	Convey("Given unique decimal and json attributes in Postgres", t, func() {
		truncateAll(t, pool)
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
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
			_, e := svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: attrID, EntityID: entity, TypeDefinitionID: typeID, Value: json.RawMessage(raw),
			})
			return e
		}

		Convey("Then 1.5 and 1.50 collide (numeric equality)", func() {
			So(set(price.ID.String(), "p1", `"1.5"`), ShouldBeNil)
			So(domainerrors.IsConflict(set(price.ID.String(), "p2", `"1.50"`)), ShouldBeTrue)
		})

		Convey("Then json objects differing only in key order collide", func() {
			So(set(payload.ID.String(), "p1", `{"a":1,"b":2}`), ShouldBeNil)
			So(domainerrors.IsConflict(set(payload.ID.String(), "p2", `{"b":2,"a":1}`)), ShouldBeTrue)
		})
	})
}
