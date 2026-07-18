package memory_test

import (
	"context"
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	"github.com/zkrebbekx/flexitype/application"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	appdependency "github.com/zkrebbekx/flexitype/application/dependency"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	"github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// depFixture is a cascading-picklist schema: category → subcategory.
type depFixture struct {
	ctx         context.Context
	it          *application.Interactors
	typeID      string
	category    string
	subcategory string
	depID       string
}

func newDepFixture() *depFixture {
	ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
	svc := flexitype.NewInMemory()
	it := svc.Interactors(ctx)

	product, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
		InternalName: "product", DisplayName: "Product",
	})
	So(err, ShouldBeNil)
	typeID := product.ID.String()

	enumAttr := func(name string, members ...string) string {
		raw, e := json.Marshal([]map[string]any{{"kind": "one_of", "values": func() []map[string]string {
			out := make([]map[string]string, 0, len(members))
			for _, m := range members {
				out = append(out, map[string]string{"type": "enum", "value": m})
			}
			return out
		}()}})
		So(e, ShouldBeNil)
		snap, e := it.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: typeID, InternalName: name, DisplayName: name,
			DataType: "enum", Constraints: raw,
		})
		So(e, ShouldBeNil)
		return snap.ID.String()
	}

	f := &depFixture{ctx: ctx, it: it, typeID: typeID}
	f.category = enumAttr("category", "bike", "car")
	f.subcategory = enumAttr("subcategory", "mountain", "road", "sedan", "suv")

	dep, err := it.Dependencies().Create(ctx, appdependency.CreateInput{
		SourceAttributeID: f.category, TargetAttributeID: f.subcategory,
		Conditions:  json.RawMessage(`[{"kind":"equals","value":{"type":"enum","value":"bike"}}]`),
		Effect:      json.RawMessage(`{"allowed_values":[{"type":"enum","value":"mountain"},{"type":"enum","value":"road"}]}`),
		Description: "bikes get bike subcategories",
	})
	So(err, ShouldBeNil)
	f.depID = dep.ID.String()
	return f
}

func (f *depFixture) setEnum(attrID, entityID, value string) {
	raw, err := json.Marshal(value)
	So(err, ShouldBeNil)
	_, err = f.it.Values().Set(f.ctx, appvalue.SetInput{
		AttributeDefinitionID: attrID, EntityID: entityID,
		TypeDefinitionID: f.typeID, Value: raw,
	})
	So(err, ShouldBeNil)
}

func TestDependencyCreateValidation(t *testing.T) {
	Convey("Given a cascading category → subcategory dependency", t, func() {
		f := newDepFixture()

		Convey("When either attribute ID is malformed", func() {
			_, srcErr := f.it.Dependencies().Create(f.ctx, appdependency.CreateInput{
				SourceAttributeID: "nope", TargetAttributeID: f.subcategory,
				Conditions: json.RawMessage(`[{"kind":"equals","value":{"type":"enum","value":"bike"}}]`),
				Effect:     json.RawMessage(`{"required":true}`),
			})
			_, tgtErr := f.it.Dependencies().Create(f.ctx, appdependency.CreateInput{
				SourceAttributeID: f.category, TargetAttributeID: "nope",
				Conditions: json.RawMessage(`[{"kind":"equals","value":{"type":"enum","value":"bike"}}]`),
				Effect:     json.RawMessage(`{"required":true}`),
			})

			Convey("Then creation is rejected as validation", func() {
				So(errors.IsValidation(srcErr), ShouldBeTrue)
				So(errors.IsValidation(tgtErr), ShouldBeTrue)
			})
		})

		Convey("When an attribute does not exist", func() {
			_, err := f.it.Dependencies().Create(f.ctx, appdependency.CreateInput{
				SourceAttributeID: valueobjects.NewAttributeDefinitionID().String(),
				TargetAttributeID: f.subcategory,
				Conditions:        json.RawMessage(`[{"kind":"equals","value":{"type":"enum","value":"bike"}}]`),
				Effect:            json.RawMessage(`{"required":true}`),
			})

			Convey("Then creation reports the missing attribute", func() {
				So(errors.IsNotFound(err), ShouldBeTrue)
			})
		})

		Convey("When the conditions JSON is malformed", func() {
			_, err := f.it.Dependencies().Create(f.ctx, appdependency.CreateInput{
				SourceAttributeID: f.category, TargetAttributeID: f.subcategory,
				Conditions: json.RawMessage(`[{"kind":`),
				Effect:     json.RawMessage(`{"required":true}`),
			})

			Convey("Then it is rejected as invalid conditions", func() {
				So(errors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "invalid conditions")
			})
		})

		Convey("When the effect JSON is malformed", func() {
			_, err := f.it.Dependencies().Create(f.ctx, appdependency.CreateInput{
				SourceAttributeID: f.category, TargetAttributeID: f.subcategory,
				Conditions: json.RawMessage(`[{"kind":"equals","value":{"type":"enum","value":"bike"}}]`),
				Effect:     json.RawMessage(`{"required":`),
			})

			Convey("Then it is rejected as an invalid effect", func() {
				So(errors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "invalid effect")
			})
		})

		Convey("When the attributes belong to unrelated type hierarchies", func() {
			otherType, err := f.it.TypeDefinitions().Create(f.ctx, apptypedef.CreateInput{
				InternalName: "vendor", DisplayName: "Vendor",
			})
			So(err, ShouldBeNil)
			foreign, err := f.it.Attributes().Create(f.ctx, appattribute.CreateInput{
				TypeDefinitionID: otherType.ID.String(), InternalName: "tier",
				DisplayName: "Tier", DataType: "string",
			})
			So(err, ShouldBeNil)

			_, err = f.it.Dependencies().Create(f.ctx, appdependency.CreateInput{
				SourceAttributeID: f.category, TargetAttributeID: foreign.ID.String(),
				Conditions: json.RawMessage(`[{"kind":"equals","value":{"type":"enum","value":"bike"}}]`),
				Effect:     json.RawMessage(`{"required":true}`),
			})

			Convey("Then the rule is refused — an entity holding the target need not hold the source", func() {
				So(errors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "same type hierarchy")
			})
		})

		Convey("When the attributes sit on one extends chain", func() {
			child, err := f.it.TypeDefinitions().Create(f.ctx, apptypedef.CreateInput{
				InternalName: "bicycle", DisplayName: "Bicycle", ExtendsID: f.typeID,
			})
			So(err, ShouldBeNil)
			frame, err := f.it.Attributes().Create(f.ctx, appattribute.CreateInput{
				TypeDefinitionID: child.ID.String(), InternalName: "frame",
				DisplayName: "Frame", DataType: "string",
			})
			So(err, ShouldBeNil)

			_, err = f.it.Dependencies().Create(f.ctx, appdependency.CreateInput{
				SourceAttributeID: f.category, TargetAttributeID: frame.ID.String(),
				Conditions: json.RawMessage(`[{"kind":"equals","value":{"type":"enum","value":"bike"}}]`),
				Effect:     json.RawMessage(`{"required":true}`),
			})

			Convey("Then a parent attribute may drive a child attribute", func() {
				So(err, ShouldBeNil)
			})
		})
	})
}

func TestDependencyUpdateAndArchive(t *testing.T) {
	Convey("Given an existing cascading dependency", t, func() {
		f := newDepFixture()

		Convey("When its rule is replaced", func() {
			snap, err := f.it.Dependencies().Update(f.ctx, appdependency.UpdateInput{
				ID:          f.depID,
				Conditions:  json.RawMessage(`[{"kind":"equals","value":{"type":"enum","value":"car"}}]`),
				Effect:      json.RawMessage(`{"allowed_values":[{"type":"enum","value":"sedan"}]}`),
				Description: "cars get car subcategories",
			})

			Convey("Then the new rule is persisted and the version bumps", func() {
				So(err, ShouldBeNil)
				So(snap.Version, ShouldEqual, 2)
				So(snap.Description, ShouldEqual, "cars get car subcategories")

				got, err := f.it.Dependencies().Get(f.ctx, f.depID)
				So(err, ShouldBeNil)
				So(got.Version, ShouldEqual, 2)
				So(got.Effect.AllowedValues, ShouldHaveLength, 1)
				So(got.Effect.AllowedValues[0].Text(), ShouldEqual, "sedan")
			})

			Convey("And the effective schema follows the new rule", func() {
				f.setEnum(f.category, "p1", "car")
				out, err := f.it.Dependencies().EffectiveSchema(f.ctx, f.subcategory, "p1")
				So(err, ShouldBeNil)
				So(out.Restricted, ShouldBeTrue)
				So(out.AllowedValues, ShouldHaveLength, 1)
				So(out.AllowedValues[0].Text(), ShouldEqual, "sedan")
			})
		})

		Convey("When an update targets a malformed or unknown ID", func() {
			valid := appdependency.UpdateInput{
				Conditions: json.RawMessage(`[{"kind":"equals","value":{"type":"enum","value":"car"}}]`),
				Effect:     json.RawMessage(`{"required":true}`),
			}
			bad := valid
			bad.ID = "nope"
			_, badErr := f.it.Dependencies().Update(f.ctx, bad)

			missing := valid
			missing.ID = valueobjects.NewDependencyID().String()
			_, missingErr := f.it.Dependencies().Update(f.ctx, missing)

			Convey("Then the malformed ID is validation and the unknown one is not-found", func() {
				So(errors.IsValidation(badErr), ShouldBeTrue)
				So(errors.IsNotFound(missingErr), ShouldBeTrue)
			})
		})

		Convey("When an update carries a rule the domain rejects", func() {
			_, err := f.it.Dependencies().Update(f.ctx, appdependency.UpdateInput{
				ID:         f.depID,
				Conditions: json.RawMessage(`[]`),
				Effect:     json.RawMessage(`{"required":true}`),
			})

			Convey("Then the stored rule is left intact", func() {
				So(errors.IsValidation(err), ShouldBeTrue)
				got, err := f.it.Dependencies().Get(f.ctx, f.depID)
				So(err, ShouldBeNil)
				So(got.Version, ShouldEqual, 1)
			})
		})

		Convey("When it is archived", func() {
			snap, err := f.it.Dependencies().Archive(f.ctx, f.depID)

			Convey("Then it is marked archived", func() {
				So(err, ShouldBeNil)
				So(snap.ArchivedAt, ShouldNotBeNil)
			})

			Convey("And archiving twice is refused", func() {
				_, err := f.it.Dependencies().Archive(f.ctx, f.depID)
				So(errors.IsArchived(err), ShouldBeTrue)
			})

			Convey("And updating an archived dependency is refused", func() {
				_, err := f.it.Dependencies().Update(f.ctx, appdependency.UpdateInput{
					ID:         f.depID,
					Conditions: json.RawMessage(`[{"kind":"equals","value":{"type":"enum","value":"car"}}]`),
					Effect:     json.RawMessage(`{"required":true}`),
				})
				So(errors.IsArchived(err), ShouldBeTrue)
			})

			Convey("And it no longer narrows the target attribute", func() {
				f.setEnum(f.category, "p1", "bike")
				out, err := f.it.Dependencies().EffectiveSchema(f.ctx, f.subcategory, "p1")
				So(err, ShouldBeNil)
				So(out.Restricted, ShouldBeFalse)
			})

			Convey("And it is hidden from the default listing but visible when asked for", func() {
				out, err := f.it.Dependencies().List(f.ctx, appdependency.ListInput{})
				So(err, ShouldBeNil)
				So(out.Items, ShouldBeEmpty)

				out, err = f.it.Dependencies().List(f.ctx, appdependency.ListInput{IncludeArchived: true})
				So(err, ShouldBeNil)
				So(out.Items, ShouldHaveLength, 1)
			})
		})

		Convey("When archiving a malformed or unknown ID", func() {
			_, badErr := f.it.Dependencies().Archive(f.ctx, "nope")
			_, missingErr := f.it.Dependencies().Archive(f.ctx, valueobjects.NewDependencyID().String())

			Convey("Then the malformed ID is validation and the unknown one is not-found", func() {
				So(errors.IsValidation(badErr), ShouldBeTrue)
				So(errors.IsNotFound(missingErr), ShouldBeTrue)
			})
		})
	})
}

func TestDependencyGetAndList(t *testing.T) {
	Convey("Given a tenant with one dependency", t, func() {
		f := newDepFixture()

		Convey("When it is fetched", func() {
			got, err := f.it.Dependencies().Get(f.ctx, f.depID)

			Convey("Then the stored rule comes back whole", func() {
				So(err, ShouldBeNil)
				So(got.ID.String(), ShouldEqual, f.depID)
				So(got.SourceAttributeID.String(), ShouldEqual, f.category)
				So(got.TargetAttributeID.String(), ShouldEqual, f.subcategory)
				So(got.Conditions, ShouldHaveLength, 1)
				So(got.Effect.AllowedValues, ShouldHaveLength, 2)
				So(got.Description, ShouldEqual, "bikes get bike subcategories")
			})
		})

		Convey("When a malformed or unknown ID is fetched", func() {
			_, badErr := f.it.Dependencies().Get(f.ctx, "nope")
			_, missingErr := f.it.Dependencies().Get(f.ctx, valueobjects.NewDependencyID().String())

			Convey("Then the malformed ID is validation and the unknown one is not-found", func() {
				So(errors.IsValidation(badErr), ShouldBeTrue)
				So(errors.IsNotFound(missingErr), ShouldBeTrue)
			})
		})

		Convey("When another tenant fetches it", func() {
			other := uow.WithTenant(context.Background(), valueobjects.TenantID("other"))
			_, err := f.it.Dependencies().Get(other, f.depID)

			Convey("Then it is invisible across the tenant boundary", func() {
				So(errors.IsNotFound(err), ShouldBeTrue)
			})
		})

		Convey("When dependencies are listed by source and by target", func() {
			bySource, err := f.it.Dependencies().List(f.ctx,
				appdependency.ListInput{SourceAttributeID: f.category})
			So(err, ShouldBeNil)
			byTarget, err := f.it.Dependencies().List(f.ctx,
				appdependency.ListInput{TargetAttributeID: f.subcategory})
			So(err, ShouldBeNil)
			byOtherSource, err := f.it.Dependencies().List(f.ctx,
				appdependency.ListInput{SourceAttributeID: f.subcategory})
			So(err, ShouldBeNil)

			Convey("Then each filter selects on the matching side", func() {
				So(bySource.Items, ShouldHaveLength, 1)
				So(byTarget.Items, ShouldHaveLength, 1)
				So(byOtherSource.Items, ShouldBeEmpty)
			})
		})

		Convey("When a list filter is malformed", func() {
			_, srcErr := f.it.Dependencies().List(f.ctx, appdependency.ListInput{SourceAttributeID: "nope"})
			_, tgtErr := f.it.Dependencies().List(f.ctx, appdependency.ListInput{TargetAttributeID: "nope"})

			Convey("Then it is rejected as validation", func() {
				So(errors.IsValidation(srcErr), ShouldBeTrue)
				So(errors.IsValidation(tgtErr), ShouldBeTrue)
			})
		})

		Convey("When the page arguments are invalid", func() {
			zero := 0
			_, limitErr := f.it.Dependencies().List(f.ctx,
				appdependency.ListInput{Page: db.PageArgs{Limit: &zero}})
			cursor := "!!!"
			_, cursorErr := f.it.Dependencies().List(f.ctx,
				appdependency.ListInput{Page: db.PageArgs{Cursor: &cursor}})

			Convey("Then both are rejected as validation", func() {
				So(errors.IsValidation(limitErr), ShouldBeTrue)
				So(errors.IsValidation(cursorErr), ShouldBeTrue)
			})
		})

		Convey("When a page smaller than the result set is requested", func() {
			// Add a second rule so there is something to page over.
			_, err := f.it.Dependencies().Create(f.ctx, appdependency.CreateInput{
				SourceAttributeID: f.category, TargetAttributeID: f.subcategory,
				Conditions: json.RawMessage(`[{"kind":"equals","value":{"type":"enum","value":"car"}}]`),
				Effect:     json.RawMessage(`{"required":true}`),
			})
			So(err, ShouldBeNil)

			limit := 1
			first, err := f.it.Dependencies().List(f.ctx, appdependency.ListInput{
				Page: db.PageArgs{Limit: &limit, WantTotal: true},
			})
			So(err, ShouldBeNil)

			Convey("Then the page carries a cursor and a total, and the cursor resumes", func() {
				So(first.Items, ShouldHaveLength, 1)
				So(first.PageInfo.HasNextPage, ShouldBeTrue)
				So(first.PageInfo.NextCursor, ShouldNotBeNil)
				So(first.PageInfo.TotalCount, ShouldNotBeNil)
				So(*first.PageInfo.TotalCount, ShouldEqual, 2)

				second, err := f.it.Dependencies().List(f.ctx, appdependency.ListInput{
					Page: db.PageArgs{Limit: &limit, Cursor: first.PageInfo.NextCursor},
				})
				So(err, ShouldBeNil)
				So(second.Items, ShouldHaveLength, 1)
				So(second.PageInfo.HasPreviousPage, ShouldBeTrue)
				So(second.Items[0].ID.String(), ShouldNotEqual, first.Items[0].ID.String())
			})
		})
	})
}

func TestDependencyEffectiveSchema(t *testing.T) {
	Convey("Given a cascading category → subcategory dependency", t, func() {
		f := newDepFixture()

		Convey("When the entity's category is bike", func() {
			f.setEnum(f.category, "p1", "bike")
			out, err := f.it.Dependencies().EffectiveSchema(f.ctx, f.subcategory, "p1")

			Convey("Then the subcategory narrows to the bike members", func() {
				So(err, ShouldBeNil)
				So(out.AttributeDefinitionID, ShouldEqual, f.subcategory)
				So(out.EntityID, ShouldEqual, "p1")
				So(out.Restricted, ShouldBeTrue)
				So(out.AllowedValues, ShouldHaveLength, 2)
				So(out.Required, ShouldBeFalse)
			})

			Convey("And a value outside the narrowed set is refused on write", func() {
				raw, err := json.Marshal("sedan")
				So(err, ShouldBeNil)
				_, err = f.it.Values().Set(f.ctx, appvalue.SetInput{
					AttributeDefinitionID: f.subcategory, EntityID: "p1",
					TypeDefinitionID: f.typeID, Value: raw,
				})
				So(errors.IsDependencyViolation(err), ShouldBeTrue)
			})

			Convey("And a value inside the narrowed set is accepted", func() {
				raw, err := json.Marshal("mountain")
				So(err, ShouldBeNil)
				_, err = f.it.Values().Set(f.ctx, appvalue.SetInput{
					AttributeDefinitionID: f.subcategory, EntityID: "p1",
					TypeDefinitionID: f.typeID, Value: raw,
				})
				So(err, ShouldBeNil)
			})
		})

		Convey("When the entity's category does not match the condition", func() {
			f.setEnum(f.category, "p2", "car")
			out, err := f.it.Dependencies().EffectiveSchema(f.ctx, f.subcategory, "p2")

			Convey("Then nothing is narrowed", func() {
				So(err, ShouldBeNil)
				So(out.Restricted, ShouldBeFalse)
				So(out.AllowedValues, ShouldBeEmpty)
			})
		})

		Convey("When the entity has no values at all", func() {
			out, err := f.it.Dependencies().EffectiveSchema(f.ctx, f.subcategory, "unknown-entity")

			Convey("Then the attribute's own schema is returned unnarrowed", func() {
				So(err, ShouldBeNil)
				So(out.Restricted, ShouldBeFalse)
				So(out.Required, ShouldBeFalse)
			})
		})

		Convey("When the attribute has no dependencies targeting it", func() {
			out, err := f.it.Dependencies().EffectiveSchema(f.ctx, f.category, "p1")

			Convey("Then the entity's values are not even loaded and nothing is narrowed", func() {
				So(err, ShouldBeNil)
				So(out.Restricted, ShouldBeFalse)
			})
		})

		Convey("When the attribute or entity ID is malformed", func() {
			_, attrErr := f.it.Dependencies().EffectiveSchema(f.ctx, "nope", "p1")
			_, entityErr := f.it.Dependencies().EffectiveSchema(f.ctx, f.subcategory, "")

			Convey("Then both are rejected as validation", func() {
				So(errors.IsValidation(attrErr), ShouldBeTrue)
				So(errors.IsValidation(entityErr), ShouldBeTrue)
			})
		})

		Convey("When the attribute does not exist", func() {
			_, err := f.it.Dependencies().EffectiveSchema(f.ctx,
				valueobjects.NewAttributeDefinitionID().String(), "p1")

			Convey("Then it is a not-found", func() {
				So(errors.IsNotFound(err), ShouldBeTrue)
			})
		})

		Convey("When another tenant asks for the effective schema", func() {
			other := uow.WithTenant(context.Background(), valueobjects.TenantID("other"))
			_, err := f.it.Dependencies().EffectiveSchema(other, f.subcategory, "p1")

			Convey("Then it is refused across the tenant boundary", func() {
				So(errors.IsNotFound(err), ShouldBeTrue)
			})
		})

		Convey("When a required-override dependency fires", func() {
			_, err := f.it.Dependencies().Create(f.ctx, appdependency.CreateInput{
				SourceAttributeID: f.category, TargetAttributeID: f.subcategory,
				Conditions: json.RawMessage(`[{"kind":"equals","value":{"type":"enum","value":"car"}}]`),
				Effect:     json.RawMessage(`{"required":true}`),
			})
			So(err, ShouldBeNil)
			f.setEnum(f.category, "p3", "car")

			out, err := f.it.Dependencies().EffectiveSchema(f.ctx, f.subcategory, "p3")

			Convey("Then the target becomes required without being restricted", func() {
				So(err, ShouldBeNil)
				So(out.Required, ShouldBeTrue)
				So(out.Restricted, ShouldBeFalse)
			})
		})
	})
}

func TestDependencyCompletenessEdges(t *testing.T) {
	Convey("Given a cascading dependency and a completeness query", t, func() {
		f := newDepFixture()

		Convey("When the type ID is malformed", func() {
			_, err := f.it.Dependencies().Completeness(f.ctx, "nope", "p1")

			Convey("Then it is rejected as validation", func() {
				So(errors.IsValidation(err), ShouldBeTrue)
			})
		})

		Convey("When the entity ID is empty", func() {
			_, err := f.it.Dependencies().Completeness(f.ctx, f.typeID, "")

			Convey("Then it is rejected as validation", func() {
				So(errors.IsValidation(err), ShouldBeTrue)
			})
		})

		Convey("When the type does not exist", func() {
			_, err := f.it.Dependencies().Completeness(f.ctx,
				valueobjects.MustParseTypeDefinitionID(ulid.New().String()).String(), "p1")

			Convey("Then it is a not-found", func() {
				So(errors.IsNotFound(err), ShouldBeTrue)
			})
		})

		Convey("When no attribute on the type is required", func() {
			out, err := f.it.Dependencies().Completeness(f.ctx, f.typeID, "p1")

			Convey("Then the entity scores complete rather than dividing by zero", func() {
				So(err, ShouldBeNil)
				So(out.Required, ShouldEqual, 0)
				So(out.Score, ShouldEqual, 1)
				So(out.Missing, ShouldBeEmpty)
			})
		})
	})
}
