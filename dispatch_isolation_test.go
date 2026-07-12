package flexitype_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/events"
)

func TestDispatchIsolation(t *testing.T) {
	Convey("Given a post-commit subscriber that always fails", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		var observed error
		svc := flexitype.NewInMemory(
			flexitype.WithHandlerFunc("boom", func(context.Context, events.Envelope) error {
				return errors.New("subscriber exploded")
			}),
			flexitype.WithDispatchObserver(func(_ context.Context, err error) {
				observed = err
			}),
		)
		it := svc.Interactors(ctx)

		product, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{InternalName: "product", DisplayName: "Product"})
		So(err, ShouldBeNil)
		name, err := it.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: product.ID.String(), InternalName: "name", DisplayName: "Name", DataType: "string",
		})
		So(err, ShouldBeNil)

		Convey("When a value is written", func() {
			_, err := it.Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: name.ID.String(), EntityID: "e1", TypeDefinitionID: product.ID.String(),
				Value: json.RawMessage(`"hello"`),
			})

			Convey("Then the committed write succeeds and the failure is observed, not surfaced", func() {
				So(err, ShouldBeNil) // committed change is not failed by the subscriber
				So(observed, ShouldNotBeNil)
				So(observed.Error(), ShouldContainSubstring, "subscriber exploded")

				// The value really is persisted.
				vals, e := it.Values().ListByEntity(ctx, product.ID.String(), "e1")
				So(e, ShouldBeNil)
				So(vals, ShouldHaveLength, 1)
			})
		})
	})
}
