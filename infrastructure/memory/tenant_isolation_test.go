package memory_test

import (
	"context"
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	appdependency "github.com/zkrebbekx/flexitype/application/dependency"
	apprelationship "github.com/zkrebbekx/flexitype/application/relationship"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
)

// TestTenantIsolation asserts that no aggregate is reachable — for reading
// or writing — by another tenant's caller, and that the failure reads as
// NotFound (never confirming existence).
func TestTenantIsolation(t *testing.T) {
	Convey("Given tenant A's schema, values and relationships in one service", t, func() {
		svc := flexitype.NewInMemory()
		ctxA := uow.WithTenant(context.Background(), valueobjects.TenantID("tenant-a"))
		ctxB := uow.WithTenant(context.Background(), valueobjects.TenantID("tenant-b"))

		a := svc.Interactors(ctxA)
		typeA, err := a.TypeDefinitions().Create(ctxA, apptypedef.CreateInput{
			InternalName: "product", DisplayName: "Product",
		})
		So(err, ShouldBeNil)
		attrA, err := a.Attributes().Create(ctxA, appattribute.CreateInput{
			TypeDefinitionID: typeA.ID.String(), InternalName: "name", DisplayName: "Name", DataType: "string",
		})
		So(err, ShouldBeNil)
		valA, err := a.Values().Set(ctxA, appvalue.SetInput{
			AttributeDefinitionID: attrA.ID.String(), EntityID: "p-1", Value: json.RawMessage(`"Widget"`),
		})
		So(err, ShouldBeNil)
		relDefA, err := a.Relationships().CreateDefinition(ctxA, apprelationship.CreateDefinitionInput{
			InternalName: "uses", DisplayName: "Uses",
			ParentTypeID: typeA.ID.String(), ChildTypeID: typeA.ID.String(),
		})
		So(err, ShouldBeNil)
		linkA, err := a.Relationships().Link(ctxA, apprelationship.LinkInput{
			DefinitionID: relDefA.ID.String(), ParentEntity: "p-1", ChildEntity: "p-2",
		})
		So(err, ShouldBeNil)

		b := svc.Interactors(ctxB)
		notFound := func(err error) {
			So(err, ShouldNotBeNil)
			So(domainerrors.IsNotFound(err), ShouldBeTrue)
		}

		Convey("Then tenant B cannot read any of tenant A's aggregates by ID", func() {
			_, err := b.TypeDefinitions().Get(ctxB, typeA.ID.String())
			notFound(err)
			_, err = b.TypeDefinitions().EffectiveAttributes(ctxB, typeA.ID.String())
			notFound(err)
			_, err = b.TypeDefinitions().Children(ctxB, typeA.ID.String())
			notFound(err)
			_, err = b.Attributes().Get(ctxB, attrA.ID.String())
			notFound(err)
			_, err = b.Attributes().ListByTypeDefinition(ctxB, typeA.ID.String(), db.PageArgs{})
			notFound(err)
			_, err = b.Values().Get(ctxB, valA.ID.String())
			notFound(err)
			_, err = b.Relationships().GetDefinition(ctxB, relDefA.ID.String())
			notFound(err)
			_, err = b.Relationships().AttributeSets(ctxB, relDefA.ID.String())
			notFound(err)
			_, err = b.Relationships().Get(ctxB, linkA.ID.String())
			notFound(err)
		})

		Convey("Then tenant B cannot mutate tenant A's aggregates", func() {
			_, err := b.TypeDefinitions().Update(ctxB, apptypedef.UpdateInput{
				ID: typeA.ID.String(), DisplayName: "Hijacked",
			})
			notFound(err)
			_, err = b.TypeDefinitions().Archive(ctxB, typeA.ID.String())
			notFound(err)
			_, err = b.Attributes().Update(ctxB, appattribute.UpdateInput{
				ID: attrA.ID.String(), DisplayName: "Hijacked",
			})
			notFound(err)
			_, err = b.Attributes().Archive(ctxB, attrA.ID.String())
			notFound(err)
			_, err = b.Values().Set(ctxB, appvalue.SetInput{
				AttributeDefinitionID: attrA.ID.String(), EntityID: "b-1", Value: json.RawMessage(`"x"`),
			})
			notFound(err)
			_, err = b.Values().Remove(ctxB, valA.ID.String())
			notFound(err)
			_, err = b.Attributes().Create(ctxB, appattribute.CreateInput{
				TypeDefinitionID: typeA.ID.String(), InternalName: "smuggled", DisplayName: "S", DataType: "string",
			})
			notFound(err)
			_, err = b.Relationships().Link(ctxB, apprelationship.LinkInput{
				DefinitionID: relDefA.ID.String(), ParentEntity: "b-1", ChildEntity: "b-2",
			})
			notFound(err)
			_, err = b.Relationships().Unlink(ctxB, linkA.ID.String())
			notFound(err)
			_, err = b.Relationships().ArchiveDefinition(ctxB, relDefA.ID.String())
			notFound(err)

			Convey("And tenant A's data is untouched", func() {
				got, err := a.TypeDefinitions().Get(ctxA, typeA.ID.String())
				So(err, ShouldBeNil)
				So(got.DisplayName, ShouldEqual, "Product")
				So(got.ArchivedAt, ShouldBeNil)
				v, err := a.Values().Get(ctxA, valA.ID.String())
				So(err, ShouldBeNil)
				So(v.ArchivedAt, ShouldBeNil)
			})
		})

		Convey("Then tenant B's own resources remain fully usable", func() {
			typeB, err := b.TypeDefinitions().Create(ctxB, apptypedef.CreateInput{
				InternalName: "product", DisplayName: "B Product",
			})
			So(err, ShouldBeNil)
			got, err := b.TypeDefinitions().Get(ctxB, typeB.ID.String())
			So(err, ShouldBeNil)
			So(got.DisplayName, ShouldEqual, "B Product")
		})

		Convey("Then dependencies cannot span tenants", func() {
			typeB, err := b.TypeDefinitions().Create(ctxB, apptypedef.CreateInput{
				InternalName: "widget", DisplayName: "Widget",
			})
			So(err, ShouldBeNil)
			attrB, err := b.Attributes().Create(ctxB, appattribute.CreateInput{
				TypeDefinitionID: typeB.ID.String(), InternalName: "size", DisplayName: "Size", DataType: "string",
			})
			So(err, ShouldBeNil)

			_, err = b.Dependencies().Create(ctxB, appdependency.CreateInput{
				SourceAttributeID: attrA.ID.String(), // tenant A's attribute
				TargetAttributeID: attrB.ID.String(),
				Conditions:        json.RawMessage(`[{"kind":"equals","value":{"type":"string","value":"x"}}]`),
				Effect:            json.RawMessage(`{}`),
			})
			notFound(err)
		})
	})
}
