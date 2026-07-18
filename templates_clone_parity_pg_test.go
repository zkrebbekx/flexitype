package flexitype_test

import (
	"context"
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	appdependency "github.com/zkrebbekx/flexitype/application/dependency"
	appquery "github.com/zkrebbekx/flexitype/application/query"
	apprelationship "github.com/zkrebbekx/flexitype/application/relationship"
	appschema "github.com/zkrebbekx/flexitype/application/schema"
	"github.com/zkrebbekx/flexitype/application/schema/templates"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	appunit "github.com/zkrebbekx/flexitype/application/unit"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

func pgIntPtr(n int) *int { return &n }

// TestSchemaBundleRoundTripParityPostgres re-runs the portable-bundle round-trip
// (infrastructure/memory/schema_bundle_test.go) against Postgres. The memory
// twin uses two separate in-memory instances; over one Postgres database the
// two "instances" are two isolated TENANTS — a schema is exported from tenant
// src and imported into tenant dst, and a re-export must be byte-identical to
// the original (the bundle is internal-name keyed and instance-independent).
func TestSchemaBundleRoundTripParityPostgres(t *testing.T) {
	pool := openTestDB(t)
	defer func() { _ = pool.Close() }()

	svc := flexitype.New(pool)
	if err := svc.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	Convey("Given a source tenant with a schema (inheritance, constraints, a relationship and a dependency) (Postgres)", t, func() {
		truncateAll(t, pool)
		srcCtx := uow.WithTenant(context.Background(), valueobjects.TenantID("bundle-src"))
		dstCtx := uow.WithTenant(context.Background(), valueobjects.TenantID("bundle-dst"))
		it := svc.Interactors(srcCtx)

		product, err := it.TypeDefinitions().Create(srcCtx, apptypedef.CreateInput{InternalName: "product", DisplayName: "Product"})
		So(err, ShouldBeNil)
		book, err := it.TypeDefinitions().Create(srcCtx, apptypedef.CreateInput{
			InternalName: "book", DisplayName: "Book", ExtendsID: product.ID.String(),
		})
		So(err, ShouldBeNil)
		_, err = it.Attributes().Create(srcCtx, appattribute.CreateInput{
			TypeDefinitionID: product.ID.String(), InternalName: "name", DisplayName: "Name",
			DataType: "string", Required: true,
			Constraints: json.RawMessage(`[{"kind":"max_length","n":100}]`),
		})
		So(err, ShouldBeNil)
		fmtAttr, err := it.Attributes().Create(srcCtx, appattribute.CreateInput{
			TypeDefinitionID: product.ID.String(), InternalName: "format", DisplayName: "Format", DataType: "string",
		})
		So(err, ShouldBeNil)
		pagesAttr, err := it.Attributes().Create(srcCtx, appattribute.CreateInput{
			TypeDefinitionID: book.ID.String(), InternalName: "pages", DisplayName: "Pages", DataType: "integer",
		})
		So(err, ShouldBeNil)
		_, err = it.Relationships().CreateDefinition(srcCtx, apprelationship.CreateDefinitionInput{
			InternalName: "sequel_of", DisplayName: "Sequel of",
			ParentTypeID: book.ID.String(), ChildTypeID: book.ID.String(),
		})
		So(err, ShouldBeNil)
		_, err = it.Dependencies().Create(srcCtx, appdependency.CreateInput{
			SourceAttributeID: fmtAttr.ID.String(), TargetAttributeID: pagesAttr.ID.String(),
			Conditions: json.RawMessage(`[{"kind":"equals","value":{"type":"string","value":"ebook"}}]`),
			Effect:     json.RawMessage(`{"required":true}`),
		})
		So(err, ShouldBeNil)

		bundle, err := it.Schema().Export(srcCtx)
		So(err, ShouldBeNil)

		Convey("When the bundle is imported into a fresh tenant", func() {
			res, err := svc.Interactors(dstCtx).Schema().Import(dstCtx, bundle)

			Convey("Then everything is created and a re-export matches the original", func() {
				So(err, ShouldBeNil)
				So(res.Types.Created, ShouldEqual, 2)
				So(res.Attributes.Created, ShouldEqual, 3)
				So(res.RelationshipDefinitions.Created, ShouldEqual, 1)
				So(res.Dependencies.Created, ShouldEqual, 1)

				reExport, err := svc.Interactors(dstCtx).Schema().Export(dstCtx)
				So(err, ShouldBeNil)
				orig, _ := json.Marshal(bundle)
				round, _ := json.Marshal(reExport)
				So(string(round), ShouldEqual, string(orig))
			})

			Convey("And re-importing is idempotent — nothing is created twice", func() {
				So(err, ShouldBeNil)
				res2, err := svc.Interactors(dstCtx).Schema().Import(dstCtx, bundle)
				So(err, ShouldBeNil)
				So(res2.Types.Created, ShouldEqual, 0)
				So(res2.Types.Skipped, ShouldEqual, 2)
				So(res2.Attributes.Created, ShouldEqual, 0)
				So(res2.Attributes.Skipped, ShouldEqual, 3)
				So(res2.RelationshipDefinitions.Created, ShouldEqual, 0)
				So(res2.Dependencies.Created, ShouldEqual, 0)
			})
		})

		Convey("When a bundle with an unsupported version is imported", func() {
			bad := *bundle
			bad.Version = 999
			_, err := svc.Interactors(dstCtx).Schema().Import(dstCtx, &bad)
			Convey("Then it is rejected", func() {
				So(err, ShouldNotBeNil)
			})
		})
	})
}

// TestSchemaBundleCarriesNewFeaturesParityPostgres proves the portable bundle
// round-trips unit families, quantity attributes with a family reference,
// computed attributes, localizable flags and relationship cardinality — over
// Postgres, from a source tenant into a fresh destination tenant.
func TestSchemaBundleCarriesNewFeaturesParityPostgres(t *testing.T) {
	pool := openTestDB(t)
	defer func() { _ = pool.Close() }()

	svc := flexitype.New(pool)
	if err := svc.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	Convey("Given a schema using units, a computed attr, a localizable attr and cardinality (Postgres)", t, func() {
		truncateAll(t, pool)
		srcCtx := uow.WithTenant(context.Background(), valueobjects.TenantID("feat-src"))
		dstCtx := uow.WithTenant(context.Background(), valueobjects.TenantID("feat-dst"))
		it := svc.Interactors(srcCtx)

		fam, err := it.Units().Create(srcCtx, appunit.CreateInput{
			Name: "mass", BaseUnit: "g", Units: map[string]float64{"g": 1, "kg": 1000},
		})
		So(err, ShouldBeNil)

		product, err := it.TypeDefinitions().Create(srcCtx, apptypedef.CreateInput{InternalName: "product", DisplayName: "Product"})
		So(err, ShouldBeNil)
		supplier, err := it.TypeDefinitions().Create(srcCtx, apptypedef.CreateInput{InternalName: "supplier", DisplayName: "Supplier"})
		So(err, ShouldBeNil)

		_, err = it.Attributes().Create(srcCtx, appattribute.CreateInput{
			TypeDefinitionID: product.ID.String(), InternalName: "name", DisplayName: "Name",
			DataType: "string", Localizable: true,
		})
		So(err, ShouldBeNil)
		_, err = it.Attributes().Create(srcCtx, appattribute.CreateInput{
			TypeDefinitionID: product.ID.String(), InternalName: "price", DisplayName: "Price", DataType: "decimal",
		})
		So(err, ShouldBeNil)
		_, err = it.Attributes().Create(srcCtx, appattribute.CreateInput{
			TypeDefinitionID: product.ID.String(), InternalName: "cost", DisplayName: "Cost", DataType: "decimal",
		})
		So(err, ShouldBeNil)
		_, err = it.Attributes().Create(srcCtx, appattribute.CreateInput{
			TypeDefinitionID: product.ID.String(), InternalName: "margin", DisplayName: "Margin", DataType: "decimal",
			Computed: json.RawMessage(`{"kind":"formula","formula":"(price - cost) / price"}`),
		})
		So(err, ShouldBeNil)
		_, err = it.Attributes().Create(srcCtx, appattribute.CreateInput{
			TypeDefinitionID: product.ID.String(), InternalName: "weight", DisplayName: "Weight",
			DataType: "quantity", UnitFamilyID: fam.ID.String(), DisplayUnit: "g",
			Constraints: json.RawMessage(`[{"kind":"min_value","value":{"type":"quantity","value":{"magnitude":"1","unit":"g"}}}]`),
		})
		So(err, ShouldBeNil)
		_, err = it.Relationships().CreateDefinition(srcCtx, apprelationship.CreateDefinitionInput{
			InternalName: "supplied_by", DisplayName: "Supplied by",
			ParentTypeID: product.ID.String(), ChildTypeID: supplier.ID.String(),
			MaxChildren: pgIntPtr(5),
		})
		So(err, ShouldBeNil)

		bundle, err := it.Schema().Export(srcCtx)
		So(err, ShouldBeNil)

		Convey("When exported and imported into a fresh tenant", func() {
			res, err := svc.Interactors(dstCtx).Schema().Import(dstCtx, bundle)
			So(err, ShouldBeNil)

			Convey("Then the unit family, computed, localizable and cardinality all survive a re-export", func() {
				So(res.Attributes.Created, ShouldEqual, 5)
				So(len(bundle.UnitFamilies), ShouldEqual, 1)

				reExport, err := svc.Interactors(dstCtx).Schema().Export(dstCtx)
				So(err, ShouldBeNil)
				orig, _ := json.Marshal(bundle)
				round, _ := json.Marshal(reExport)
				So(string(round), ShouldEqual, string(orig))
			})
		})
	})
}

// TestTemplatesApplyParityPostgres loads every embedded template into an empty
// tenant over Postgres and asserts it yields a working, queryable schema —
// including a quantity value that normalizes and a computed margin that
// materializes and is found by FQL over the real value store.
func TestTemplatesApplyParityPostgres(t *testing.T) {
	pool := openTestDB(t)
	defer func() { _ = pool.Close() }()

	svc := flexitype.New(pool)
	if err := svc.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	Convey("Given the embedded templates (Postgres)", t, func() {
		truncateAll(t, pool)
		list := templates.List()
		So(len(list), ShouldBeGreaterThanOrEqualTo, 2)

		Convey("When each template is applied to an empty tenant", func() {
			for _, summary := range list {
				tmpl, ok := templates.Get(summary.Name)
				So(ok, ShouldBeTrue)
				// Each template gets its own isolated tenant, standing in for the
				// separate in-memory instances the memory twin spins up.
				ctx := uow.WithTenant(context.Background(), valueobjects.TenantID("tmpl-"+summary.Name))
				res, err := svc.Interactors(ctx).Schema().Import(ctx, tmpl.Bundle)
				So(err, ShouldBeNil)
				So(res.Types.Created, ShouldBeGreaterThan, 0)
				So(res.Attributes.Created, ShouldBeGreaterThan, 0)
			}
		})

		Convey("When the product-catalog template is applied and used end to end", func() {
			ctx := uow.WithTenant(context.Background(), valueobjects.TenantID("catalog"))
			tmpl, _ := templates.Get("product-catalog")
			_, err := svc.Interactors(ctx).Schema().Import(ctx, tmpl.Bundle)
			So(err, ShouldBeNil)

			pt, err := svc.Interactors(ctx).TypeDefinitions().GetByInternalName(ctx, "product")
			So(err, ShouldBeNil)
			entity := ulid.New().String()

			set := func(attr string, raw string) error {
				a, aerr := svc.Interactors(ctx).Attributes().List(ctx, appattribute.ListInput{
					TypeDefinitionID: pt.ID.String(), InternalNames: []string{attr}, Page: db.PageArgs{Limit: pgIntPtr(1)},
				})
				So(aerr, ShouldBeNil)
				So(len(a.Items), ShouldEqual, 1)
				_, serr := svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{
					AttributeDefinitionID: a.Items[0].ID.String(),
					EntityID:              entity,
					TypeDefinitionID:      pt.ID.String(),
					Value:                 json.RawMessage(raw),
				})
				return serr
			}

			Convey("Then a quantity value normalizes and FQL finds it, and margin computes", func() {
				So(set("price", `"100"`), ShouldBeNil)
				So(set("cost", `"40"`), ShouldBeNil)
				So(set("weight", `{"magnitude":"2","unit":"kg"}`), ShouldBeNil)

				out, err := svc.Interactors(ctx).Query().Execute(ctx, appquery.ExecuteInput{Type: "product", Query: "weight > 1500 g"})
				So(err, ShouldBeNil)
				So(len(out.Items), ShouldEqual, 1)

				out2, err := svc.Interactors(ctx).Query().Execute(ctx, appquery.ExecuteInput{Type: "product", Query: "margin > 0.5"})
				So(err, ShouldBeNil)
				So(len(out2.Items), ShouldEqual, 1)
			})

			Convey("And the quantity min-value constraint (carried by the template) rejects a sub-gram weight", func() {
				So(set("weight", `{"magnitude":"0","unit":"g"}`), ShouldNotBeNil)
			})
		})
	})
}

// TestTypeCloneParityPostgres proves a cloned type is an independent copy over
// Postgres: its attributes and dependencies are duplicated, it is a fresh root,
// and mutating the clone leaves the original untouched.
func TestTypeCloneParityPostgres(t *testing.T) {
	pool := openTestDB(t)
	defer func() { _ = pool.Close() }()

	svc := flexitype.New(pool)
	if err := svc.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	Convey("Given a type with attributes and an intra-type dependency (Postgres)", t, func() {
		truncateAll(t, pool)
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		it := svc.Interactors(ctx)

		src, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{InternalName: "widget", DisplayName: "Widget"})
		So(err, ShouldBeNil)
		flag, err := it.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: src.ID.String(), InternalName: "flagged", DisplayName: "Flagged", DataType: "bool",
		})
		So(err, ShouldBeNil)
		code, err := it.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: src.ID.String(), InternalName: "code", DisplayName: "Code", DataType: "string",
		})
		So(err, ShouldBeNil)
		_, err = it.Dependencies().Create(ctx, appdependency.CreateInput{
			SourceAttributeID: flag.ID.String(), TargetAttributeID: code.ID.String(),
			Conditions: json.RawMessage(`[{"kind":"equals","value":{"type":"bool","value":true}}]`),
			Effect:     json.RawMessage(`{"required":true}`),
		})
		So(err, ShouldBeNil)

		Convey("When it is cloned", func() {
			res, err := svc.Interactors(ctx).Schema().Clone(ctx, appschema.CloneInput{
				SourceTypeID: src.ID.String(), InternalName: "widget_copy", DisplayName: "Widget Copy",
			})
			So(err, ShouldBeNil)

			Convey("Then attributes and the dependency are copied, as a fresh root", func() {
				So(res.Attributes, ShouldEqual, 2)
				So(res.Dependencies, ShouldEqual, 1)
				So(res.Type.ExtendsID, ShouldBeNil)
			})

			Convey("And mutating the clone leaves the original untouched", func() {
				clone := res.Type
				_, err := svc.Interactors(ctx).Attributes().Create(ctx, appattribute.CreateInput{
					TypeDefinitionID: clone.ID.String(), InternalName: "extra", DisplayName: "Extra", DataType: "string",
				})
				So(err, ShouldBeNil)

				origAttrs, err := svc.Interactors(ctx).Attributes().ListByTypeDefinition(ctx, src.ID.String(), db.PageArgs{Limit: pgIntPtr(50)})
				So(err, ShouldBeNil)
				So(len(origAttrs.Items), ShouldEqual, 2) // unchanged
			})

			Convey("And an internal-name collision is rejected", func() {
				_, err := svc.Interactors(ctx).Schema().Clone(ctx, appschema.CloneInput{
					SourceTypeID: src.ID.String(), InternalName: "widget", DisplayName: "Dup",
				})
				So(err, ShouldNotBeNil)
			})
		})
	})
}
