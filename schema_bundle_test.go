package flexitype_test

import (
	"context"
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	appschema "github.com/zkrebbekx/flexitype/application/schema"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	appunit "github.com/zkrebbekx/flexitype/application/unit"
	"github.com/zkrebbekx/flexitype/application/uow"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

// bundleCtx returns a tenant-scoped context and a fresh in-memory schema
// interactor.
func bundleCtx(t *testing.T) (context.Context, *flexitype.Service) {
	t.Helper()
	return uow.WithTenant(context.Background(), valueobjects.DefaultTenant), flexitype.NewInMemory()
}

func TestSchemaBundleImportValidation(t *testing.T) {
	Convey("Given a schema interactor over an empty tenant", t, func() {
		ctx, svc := bundleCtx(t)
		sch := svc.Interactors(ctx).Schema()

		Convey("When a nil bundle is imported", func() {
			_, err := sch.Import(ctx, nil)

			Convey("Then it is rejected as a validation error", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "bundle is required")
			})
		})

		Convey("When the bundle declares an unsupported version", func() {
			_, err := sch.Import(ctx, &appschema.Bundle{Version: appschema.BundleVersion + 99})

			Convey("Then it is rejected, naming the supported version", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "unsupported bundle version")
			})
		})

		Convey("When a type extends a type present in neither bundle nor tenant", func() {
			_, err := sch.Import(ctx, &appschema.Bundle{
				Version: appschema.BundleVersion,
				Types: []appschema.Type{
					{InternalName: "book", DisplayName: "Book", Extends: "ghost"},
				},
			})

			Convey("Then the dangling parent is rejected", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "extends an unknown type")
			})
		})

		Convey("When an attribute names a type the bundle never declares", func() {
			_, err := sch.Import(ctx, &appschema.Bundle{
				Version: appschema.BundleVersion,
				Attributes: []appschema.Attribute{
					{Type: "ghost", InternalName: "title", DisplayName: "Title", DataType: "string"},
				},
			})

			Convey("Then the dangling attribute is rejected", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "unknown type")
			})
		})

		Convey("When an attribute names a unit family the bundle never declares", func() {
			_, err := sch.Import(ctx, &appschema.Bundle{
				Version: appschema.BundleVersion,
				Types:   []appschema.Type{{InternalName: "part", DisplayName: "Part"}},
				Attributes: []appschema.Attribute{{
					Type: "part", InternalName: "mass", DisplayName: "Mass",
					DataType: "quantity", UnitFamily: "ghost_units",
				}},
			})

			Convey("Then the dangling unit family is rejected", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "unknown unit family")
			})
		})

		Convey("When a relationship names an endpoint type the bundle never declares", func() {
			_, err := sch.Import(ctx, &appschema.Bundle{
				Version: appschema.BundleVersion,
				Types:   []appschema.Type{{InternalName: "part", DisplayName: "Part"}},
				RelationshipDefinitions: []appschema.RelationshipDefinition{{
					InternalName: "contains", DisplayName: "Contains", Kind: "directed",
					ParentType: "part", ChildType: "ghost",
				}},
			})

			Convey("Then the dangling endpoint is rejected", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "relationship references an unknown type")
			})
		})

		Convey("When a dependency names an attribute the bundle never declares", func() {
			_, err := sch.Import(ctx, &appschema.Bundle{
				Version: appschema.BundleVersion,
				Types:   []appschema.Type{{InternalName: "part", DisplayName: "Part"}},
				Attributes: []appschema.Attribute{
					{Type: "part", InternalName: "kind", DisplayName: "Kind", DataType: "string"},
				},
				Dependencies: []appschema.Dependency{{
					SourceType: "part", SourceAttribute: "kind",
					TargetType: "part", TargetAttribute: "ghost",
				}},
			})

			Convey("Then the dangling dependency is rejected", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "dependency references an unknown attribute")
			})
		})
	})
}

func TestSchemaBundleTypeOrdering(t *testing.T) {
	Convey("Given a bundle whose types are not in dependency order", t, func() {
		ctx, svc := bundleCtx(t)
		sch := svc.Interactors(ctx).Schema()

		Convey("When a subtype is listed before the parent it extends", func() {
			res, err := sch.Import(ctx, &appschema.Bundle{
				Version: appschema.BundleVersion,
				Types: []appschema.Type{
					{InternalName: "ebook", DisplayName: "eBook", Extends: "book"},
					{InternalName: "book", DisplayName: "Book"},
				},
			})

			Convey("Then both import, the parent having been sorted ahead of the child", func() {
				So(err, ShouldBeNil)
				So(res.Types.Created, ShouldEqual, 2)
			})
		})

		Convey("When the bundle contains an inheritance cycle", func() {
			_, err := sch.Import(ctx, &appschema.Bundle{
				Version: appschema.BundleVersion,
				Types: []appschema.Type{
					{InternalName: "a", DisplayName: "A", Extends: "b"},
					{InternalName: "b", DisplayName: "B", Extends: "a"},
				},
			})

			Convey("Then the cycle is detected and rejected", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "cycle")
			})
		})

		Convey("When a bundle type extends a type that already exists in the tenant", func() {
			first, err := sch.Import(ctx, &appschema.Bundle{
				Version: appschema.BundleVersion,
				Types:   []appschema.Type{{InternalName: "book", DisplayName: "Book"}},
			})
			So(err, ShouldBeNil)
			So(first.Types.Created, ShouldEqual, 1)

			res, err := sch.Import(ctx, &appschema.Bundle{
				Version: appschema.BundleVersion,
				Types: []appschema.Type{
					{InternalName: "ebook", DisplayName: "eBook", Extends: "book"},
				},
			})

			Convey("Then the already-imported parent resolves and the subtype is created", func() {
				So(err, ShouldBeNil)
				So(res.Types.Created, ShouldEqual, 1)
			})
		})
	})
}

func TestSchemaBundleRoundTrip(t *testing.T) {
	Convey("Given a tenant with a populated schema", t, func() {
		ctx, svc := bundleCtx(t)
		it := svc.Interactors(ctx)
		sch := it.Schema()

		// Build a schema through the bundle importer itself, exercising unit
		// families, computed attributes, constraints, relationships and
		// dependencies in one pass.
		source := &appschema.Bundle{
			Version: appschema.BundleVersion,
			UnitFamilies: []appschema.UnitFamily{{
				Name: "mass", BaseUnit: "kg", Units: map[string]float64{"kg": 1, "g": 0.001},
			}},
			Types: []appschema.Type{
				{InternalName: "part", DisplayName: "Part", Description: "a component"},
				{InternalName: "assembly", DisplayName: "Assembly"},
			},
			Attributes: []appschema.Attribute{
				{
					Type: "part", InternalName: "kind", DisplayName: "Kind", DataType: "enum",
					Constraints: json.RawMessage(
						`[{"kind":"one_of","values":[{"type":"enum","value":"bolt"},{"type":"enum","value":"nut"}]}]`),
					Group: "classification", SortOrder: 1, HelpText: "what it is",
				},
				{
					Type: "part", InternalName: "mass", DisplayName: "Mass",
					DataType: "quantity", UnitFamily: "mass", DisplayUnit: "g",
				},
				{
					Type: "part", InternalName: "net_mass", DisplayName: "Net Mass",
					DataType: "float",
					Computed: json.RawMessage(`{"kind":"formula","formula":"mass"}`),
				},
				{
					Type: "part", InternalName: "note", DisplayName: "Note", DataType: "string",
					DefaultValue: json.RawMessage(`{"static":{"type":"string","value":"tbd"}}`),
					Localizable:  true, Scopable: true,
				},
			},
			RelationshipDefinitions: []appschema.RelationshipDefinition{{
				InternalName: "contains", DisplayName: "Contains", Kind: "directed",
				ParentType: "assembly", ChildType: "part",
			}},
			Dependencies: []appschema.Dependency{{
				SourceType: "part", SourceAttribute: "kind",
				TargetType: "part", TargetAttribute: "note",
				Conditions:  json.RawMessage(`[{"kind":"equals","value":{"type":"enum","value":"bolt"}}]`),
				Effect:      json.RawMessage(`{"required":true}`),
				Description: "bolts must be annotated",
			}},
		}

		res, err := sch.Import(ctx, source)
		So(err, ShouldBeNil)

		Convey("When the bundle is first imported", func() {
			Convey("Then every object is created", func() {
				So(res.Types.Created, ShouldEqual, 2)
				So(res.Attributes.Created, ShouldEqual, 4)
				So(res.RelationshipDefinitions.Created, ShouldEqual, 1)
				So(res.Dependencies.Created, ShouldEqual, 1)
				So(res.Types.Skipped, ShouldEqual, 0)
			})
		})

		Convey("When the same bundle is imported again", func() {
			again, err := sch.Import(ctx, source)

			Convey("Then the import is idempotent: everything is skipped, nothing created", func() {
				So(err, ShouldBeNil)
				So(again.Types.Created, ShouldEqual, 0)
				So(again.Types.Skipped, ShouldEqual, 2)
				So(again.Attributes.Created, ShouldEqual, 0)
				So(again.Attributes.Skipped, ShouldEqual, 4)
				So(again.RelationshipDefinitions.Created, ShouldEqual, 0)
				So(again.RelationshipDefinitions.Skipped, ShouldEqual, 1)
				So(again.Dependencies.Created, ShouldEqual, 0)
				So(again.Dependencies.Skipped, ShouldEqual, 1)
			})
		})

		Convey("When the tenant schema is exported", func() {
			out, err := sch.Export(ctx)
			So(err, ShouldBeNil)

			Convey("Then the bundle carries the current version and every object kind", func() {
				So(out.Version, ShouldEqual, appschema.BundleVersion)
				So(out.Types, ShouldHaveLength, 2)
				So(out.Attributes, ShouldHaveLength, 4)
				So(out.RelationshipDefinitions, ShouldHaveLength, 1)
				So(out.Dependencies, ShouldHaveLength, 1)
				So(out.UnitFamilies, ShouldHaveLength, 1)
			})

			Convey("Then it is keyed by internal name, never by ID", func() {
				names := map[string]appschema.Attribute{}
				for _, a := range out.Attributes {
					names[a.InternalName] = a
					So(a.Type, ShouldNotBeEmpty)
				}

				So(names["mass"].UnitFamily, ShouldEqual, "mass")
				So(names["mass"].DisplayUnit, ShouldEqual, "g")
				So(names["net_mass"].Computed, ShouldNotBeEmpty)
				So(names["kind"].Constraints, ShouldNotBeEmpty)
				So(names["kind"].Group, ShouldEqual, "classification")
				So(names["note"].DefaultValue, ShouldNotBeEmpty)
				So(names["note"].Localizable, ShouldBeTrue)

				So(out.Dependencies[0].SourceAttribute, ShouldEqual, "kind")
				So(out.Dependencies[0].TargetAttribute, ShouldEqual, "note")
				So(out.RelationshipDefinitions[0].ParentType, ShouldEqual, "assembly")
				So(out.RelationshipDefinitions[0].ChildType, ShouldEqual, "part")
			})
		})

		Convey("When the export is replayed into a second, empty tenant service", func() {
			out, err := sch.Export(ctx)
			So(err, ShouldBeNil)

			ctx2, svc2 := bundleCtx(t)
			replay, err := svc2.Interactors(ctx2).Schema().Import(ctx2, out)

			Convey("Then the schema reconstructs object-for-object", func() {
				So(err, ShouldBeNil)
				So(replay.Types.Created, ShouldEqual, 2)
				So(replay.Attributes.Created, ShouldEqual, 4)
				So(replay.RelationshipDefinitions.Created, ShouldEqual, 1)
				So(replay.Dependencies.Created, ShouldEqual, 1)
			})
		})
	})
}

func TestSchemaBundleUnitFamilies(t *testing.T) {
	Convey("Given a tenant that already declares a unit family", t, func() {
		ctx, svc := bundleCtx(t)
		it := svc.Interactors(ctx)

		_, err := it.Units().Create(ctx, appunit.CreateInput{
			Name: "mass", BaseUnit: "kg", Units: map[string]float64{"kg": 1, "g": 0.001},
		})
		So(err, ShouldBeNil)

		Convey("When a bundle declaring the same family by name is imported", func() {
			res, err := it.Schema().Import(ctx, &appschema.Bundle{
				Version: appschema.BundleVersion,
				UnitFamilies: []appschema.UnitFamily{{
					Name: "mass", BaseUnit: "kg", Units: map[string]float64{"kg": 1},
				}},
				Types: []appschema.Type{{InternalName: "part", DisplayName: "Part"}},
				Attributes: []appschema.Attribute{{
					Type: "part", InternalName: "mass", DisplayName: "Mass",
					DataType: "quantity", UnitFamily: "mass",
				}},
			})

			Convey("Then the existing family is reused rather than duplicated", func() {
				So(err, ShouldBeNil)
				So(res.Attributes.Created, ShouldEqual, 1)

				families, err := it.Units().List(ctx)
				So(err, ShouldBeNil)
				So(families, ShouldHaveLength, 1)
			})
		})
	})
}

func TestSchemaClone(t *testing.T) {
	Convey("Given a populated source type", t, func() {
		ctx, svc := bundleCtx(t)
		it := svc.Interactors(ctx)
		sch := it.Schema()

		_, err := sch.Import(ctx, &appschema.Bundle{
			Version: appschema.BundleVersion,
			Types: []appschema.Type{
				{InternalName: "part", DisplayName: "Part", Description: "a component"},
				{InternalName: "other", DisplayName: "Other"},
			},
			Attributes: []appschema.Attribute{
				{
					Type: "part", InternalName: "kind", DisplayName: "Kind", DataType: "enum",
					Constraints: json.RawMessage(
						`[{"kind":"one_of","values":[{"type":"enum","value":"bolt"},{"type":"enum","value":"nut"}]}]`),
				},
				{Type: "part", InternalName: "note", DisplayName: "Note", DataType: "string"},
				{
					Type: "part", InternalName: "derived", DisplayName: "Derived", DataType: "float",
					Computed: json.RawMessage(`{"kind":"formula","formula":"1 + 2"}`),
				},
				{Type: "other", InternalName: "flag", DisplayName: "Flag", DataType: "bool"},
				{Type: "other", InternalName: "label", DisplayName: "Label", DataType: "string"},
			},
			Dependencies: []appschema.Dependency{
				{
					// Intra-type: must be rewired onto the clone.
					SourceType: "part", SourceAttribute: "kind",
					TargetType: "part", TargetAttribute: "note",
					Conditions: json.RawMessage(`[{"kind":"equals","value":{"type":"enum","value":"bolt"}}]`),
					Effect:     json.RawMessage(`{"required":true}`),
				},
				{
					// Belongs to a different type entirely: must NOT be copied
					// into the clone of "part".
					SourceType: "other", SourceAttribute: "flag",
					TargetType: "other", TargetAttribute: "label",
					Conditions: json.RawMessage(`[{"kind":"equals","value":{"type":"bool","value":true}}]`),
					Effect:     json.RawMessage(`{"required":true}`),
				},
			},
		})
		So(err, ShouldBeNil)

		types, err := it.TypeDefinitions().List(ctx, apptypedef.ListInput{
			InternalNames: []string{"part"},
		})
		So(err, ShouldBeNil)
		So(types.Items, ShouldHaveLength, 1)
		sourceID := types.Items[0].ID.String()

		Convey("When the clone is requested without a new internal name", func() {
			_, err := sch.Clone(ctx, appschema.CloneInput{SourceTypeID: sourceID})

			Convey("Then it is rejected", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "internal name")
			})
		})

		Convey("When the source type does not exist", func() {
			_, err := sch.Clone(ctx, appschema.CloneInput{
				SourceTypeID: valueobjects.NewTypeDefinitionID().String(),
				InternalName: "copy",
			})

			Convey("Then the missing source surfaces as an error", func() {
				So(err, ShouldNotBeNil)
			})
		})

		Convey("When the type is cloned without an explicit display name", func() {
			res, err := sch.Clone(ctx, appschema.CloneInput{
				SourceTypeID: sourceID, InternalName: "part_copy",
			})

			Convey("Then the source's display name and description carry over", func() {
				So(err, ShouldBeNil)
				So(res.Type.InternalName, ShouldEqual, "part_copy")
				So(res.Type.DisplayName, ShouldEqual, "Part")
				So(res.Type.Description, ShouldEqual, "a component")
			})

			Convey("Then only the source type's own attributes and dependencies are copied", func() {
				So(res.Attributes, ShouldEqual, 3)   // kind, note, derived
				So(res.Dependencies, ShouldEqual, 1) // the intra-type one only
			})

			Convey("Then the clone is a fresh root, not a subtype of the source", func() {
				So(res.Type.ExtendsID, ShouldBeNil)
			})
		})

		Convey("When the type is cloned with an explicit display name", func() {
			res, err := sch.Clone(ctx, appschema.CloneInput{
				SourceTypeID: sourceID, InternalName: "part_v2", DisplayName: "Part (v2)",
			})

			Convey("Then the supplied display name wins", func() {
				So(err, ShouldBeNil)
				So(res.Type.DisplayName, ShouldEqual, "Part (v2)")
			})
		})

		Convey("When a clone reuses an existing internal name", func() {
			_, err := sch.Clone(ctx, appschema.CloneInput{
				SourceTypeID: sourceID, InternalName: "other",
			})

			Convey("Then the collision is rejected by the underlying create path", func() {
				So(err, ShouldNotBeNil)
			})
		})
	})
}
