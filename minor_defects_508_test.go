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

// TestExclusiveFlagsBelongToRange covers the first item of #508.
//
// The "an exclusive flag without its bound is an error" check lived inside
// the range arm, so min_exclusive on an equals, in, pattern or dynamic
// condition validated, stored and was then ignored — while the OpenAPI
// schema documents both flags on the shared condition object with no
// restriction, so an author had no way to learn it did nothing.
func TestExclusiveFlagsBelongToRange(t *testing.T) {
	Convey("Given a dependency between two attributes", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory()
		ia := svc.Interactors(ctx)

		order, err := ia.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "order", DisplayName: "Order",
		})
		So(err, ShouldBeNil)
		mk := func(name, dt string) string {
			a, aerr := svc.Interactors(ctx).Attributes().Create(ctx, appattribute.CreateInput{
				TypeDefinitionID: order.ID.String(), InternalName: name,
				DisplayName: name, DataType: dt,
			})
			So(aerr, ShouldBeNil)
			return a.ID.String()
		}
		qty := mk("qty", "integer")
		approver := mk("approver", "string")

		rule := func(conditions string) error {
			_, cerr := svc.Interactors(ctx).Dependencies().Create(ctx, appdependency.CreateInput{
				SourceAttributeID: qty, TargetAttributeID: approver,
				Conditions: json.RawMessage(conditions),
				Effect:     json.RawMessage(`{"required":true}`),
			})
			return cerr
		}

		Convey("When an equals condition carries min_exclusive", func() {
			err := rule(`[{"kind":"equals","value":{"type":"integer","value":5},"min_exclusive":true}]`)

			Convey("Then it is refused rather than stored and ignored", func() {
				So(domainerrors.CodeOf(err), ShouldEqual, domainerrors.CodeValidation)
				So(err.Error(), ShouldContainSubstring, "range condition only")
			})
		})

		Convey("When an in condition carries max_exclusive", func() {
			err := rule(`[{"kind":"in","values":[{"type":"integer","value":5}],"max_exclusive":true}]`)

			Convey("Then it is refused too", func() {
				So(domainerrors.CodeOf(err), ShouldEqual, domainerrors.CodeValidation)
			})
		})

		Convey("When a range condition carries them", func() {
			err := rule(`[{"kind":"range","min":{"type":"integer","value":5},"min_exclusive":true}]`)

			Convey("Then it is accepted: the flags belong to a range", func() {
				So(err, ShouldBeNil)
			})
		})
	})
}

// TestGridKeepsEveryMember covers the fourth item of #508.
//
// GridRows assigned one cell per (entity, attribute), so the last row read
// won and the grid showed ONE arbitrary member of a multi-valued or scoped
// attribute — while the facet counts beside it counted them all. The two
// surfaces disagreed about the same data, and which member showed could
// change with an unrelated write.
func TestGridKeepsEveryMember(t *testing.T) {
	Convey("Given an entity holding several members of one attribute", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory()
		ia := svc.Interactors(ctx)

		product, err := ia.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "product", DisplayName: "Product",
		})
		So(err, ShouldBeNil)
		tags, err := svc.Interactors(ctx).Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: product.ID.String(), InternalName: "tags",
			DisplayName: "Tags", DataType: "string", MultiValued: true,
		})
		So(err, ShouldBeNil)
		for _, v := range []string{`"sale"`, `"clearance"`} {
			_, serr := svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: tags.ID.String(), EntityID: "p1",
				TypeDefinitionID: product.ID.String(), Value: json.RawMessage(v),
			})
			So(serr, ShouldBeNil)
		}

		Convey("When the grid is rendered", func() {
			grid, gerr := svc.Interactors(ctx).Values().GridRows(ctx,
				product.ID.String(), []string{"tags"}, []string{"p1"})
			So(gerr, ShouldBeNil)
			So(grid.Rows, ShouldHaveLength, 1)

			Convey("Then the cell holds every member, not an arbitrary one", func() {
				So(grid.Rows[0].Values["tags"], ShouldContainSubstring, "sale")
				So(grid.Rows[0].Values["tags"], ShouldContainSubstring, "clearance")
			})
		})
	})
}
