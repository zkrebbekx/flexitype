package flexitype_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	appdependency "github.com/zkrebbekx/flexitype/application/dependency"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

// TestRecomputeWithoutSearchIndex covers a maintenance endpoint that a whole
// class of deployment could not reach.
//
// Recompute and reindex were wired behind one guard, `s.indexer != nil`. A
// deployment that uses computed attributes and does not configure the search
// index got a "feature disabled" answer from POST /computed/recompute, with
// nothing in the response naming search as the reason. The two features are
// unrelated: recompute walks entities and re-evaluates formulas, and never
// touches the index.
func TestRecomputeWithoutSearchIndex(t *testing.T) {
	Convey("Given a service with no search index configured", t, func() {
		svc := flexitype.NewInMemory()
		// No WithSearchIndex option, which is what leaves s.indexer nil.
		handler := svc.APIHandler(flexitype.APIConfig{AllowAnonymous: true})

		Convey("When an admin posts to the recompute endpoint", func() {
			// A nil authenticator runs as the system actor with admin scope,
			// so this reaches the handler body rather than stopping at auth.
			req := httptest.NewRequest(http.MethodPost, "/api/v1/computed/recompute", nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			Convey("Then it is served, not reported as a disabled feature", func() {
				So(rec.Code, ShouldEqual, http.StatusOK)
			})
		})
	})
}

// TestInheritedRequirednessAgrees pins the agreement between the two surfaces
// that answer "is this attribute required for this entity".
//
// Effective schema keyed its source-value lookup on the attribute's declaring
// type, while completeness read the entity's whole value set. For an entity
// whose anchor is a subtype and whose rule source is declared on the parent,
// the lookup found no source value, so effective schema answered
// required=false while completeness listed the same attribute as missing.
// A client that reads effective schema to drive a form showed no requirement,
// then failed the completeness gate with no field to point at.
func TestInheritedRequirednessAgrees(t *testing.T) {
	Convey("Given Book extends Product, with both attributes declared on Product", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory()
		ia := svc.Interactors(ctx)

		product, err := ia.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "product", DisplayName: "Product",
		})
		So(err, ShouldBeNil)
		book, err := ia.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "book", DisplayName: "Book", ExtendsID: product.ID.String(),
		})
		So(err, ShouldBeNil)

		status, err := ia.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: product.ID.String(), InternalName: "status",
			DisplayName: "Status", DataType: "string",
		})
		So(err, ShouldBeNil)
		sku, err := ia.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: product.ID.String(), InternalName: "sku",
			DisplayName: "SKU", DataType: "string",
		})
		So(err, ShouldBeNil)

		// "status = active makes sku required" — the catalog example's rule.
		_, err = ia.Dependencies().Create(ctx, appdependency.CreateInput{
			SourceAttributeID: status.ID.String(),
			TargetAttributeID: sku.ID.String(),
			Conditions:        json.RawMessage(`[{"kind":"equals","value":{"type":"string","value":"active"}}]`),
			Effect:            json.RawMessage(`{"required":true}`),
		})
		So(err, ShouldBeNil)

		Convey("When an entity anchored to Book is set active", func() {
			raw, _ := json.Marshal("active")
			_, err := svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: status.ID.String(), EntityID: "b1",
				TypeDefinitionID: book.ID.String(), Value: raw,
			})
			So(err, ShouldBeNil)

			Convey("Then effective schema reports sku as required", func() {
				eff, err := svc.Interactors(ctx).Dependencies().EffectiveSchema(ctx, sku.ID.String(), "b1")
				So(err, ShouldBeNil)
				So(eff.Required, ShouldBeTrue)
			})

			Convey("Then completeness lists the same attribute as missing", func() {
				comp, err := svc.Interactors(ctx).Dependencies().Completeness(ctx, book.ID.String(), "b1")
				So(err, ShouldBeNil)
				So(comp.Required, ShouldEqual, 1)
				So(comp.Missing, ShouldHaveLength, 1)
				So(comp.Missing[0].InternalName, ShouldEqual, "sku")
			})

			Convey("Then the write itself succeeds, as the example now documents", func() {
				// Requiredness is a completeness signal. Each write validates
				// only the attribute it carries, so writing status while sku
				// is absent is accepted; the gate is completeness.
				comp, err := svc.Interactors(ctx).Dependencies().Completeness(ctx, book.ID.String(), "b1")
				So(err, ShouldBeNil)
				So(comp.Filled, ShouldEqual, 0)
			})
		})
	})
}
