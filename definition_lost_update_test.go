package flexitype_test

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
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

// runDefinitionLostUpdate extends the #597 regression to every other
// definition endpoint the console edits as a full replace.
//
// #597 was fixed for attributes and saved views. The type definition, the
// relationship definition and the dependency are the same shape: a PATCH that
// replaces the whole editable record, on an aggregate whose domain already
// carried what its own comment calls "the optimistic version counter", with
// nothing in the application layer reading it. Two operators editing one
// record therefore lost an edit in silence.
//
// The dependency is the sharpest of the three. A dependency decides which
// values are accepted, so a lost update there silently changes validation for
// every later write.
func runDefinitionLostUpdate(t *testing.T, label string, newService func() *flexitype.Service) {
	t.Helper()

	Convey("Given definitions two operators are about to edit ("+label+")", t, func() {
		svc := newService()
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		ia := svc.Interactors(ctx)

		product, err := ia.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "product", DisplayName: "Product",
		})
		So(err, ShouldBeNil)

		Convey("When two operators edit one TYPE definition", func() {
			baseline := product.Version
			update := func(displayName string, version *int) error {
				_, uerr := svc.Interactors(ctx).TypeDefinitions().Update(ctx, apptypedef.UpdateInput{
					ID: product.ID.String(), DisplayName: displayName, Version: version,
				})
				return uerr
			}
			So(update("Article", &baseline), ShouldBeNil)
			serr := update("Item", &baseline)

			Convey("Then the second is refused rather than erasing the first", func() {
				So(serr, ShouldNotBeNil)
				So(domainerrors.CodeOf(serr), ShouldEqual, domainerrors.CodeConflict)
				current, gerr := svc.Interactors(ctx).TypeDefinitions().Get(ctx, product.ID.String())
				So(gerr, ShouldBeNil)
				So(current.DisplayName, ShouldEqual, "Article")
			})

			Convey("And omitting the version is still last-write-wins", func() {
				So(update("Item", nil), ShouldBeNil)
			})
		})

		Convey("When two operators edit one RELATIONSHIP definition", func() {
			def, derr := ia.Relationships().CreateDefinition(ctx, apprelationship.CreateDefinitionInput{
				InternalName: "related_to", DisplayName: "Related to", Kind: "directed",
				ParentTypeID: product.ID.String(), ChildTypeID: product.ID.String(),
			})
			So(derr, ShouldBeNil)
			baseline := def.Version

			update := func(displayName string, version *int) error {
				_, uerr := svc.Interactors(ctx).Relationships().UpdateDefinition(ctx,
					apprelationship.UpdateDefinitionInput{
						ID: def.ID.String(), DisplayName: displayName, Version: version,
					})
				return uerr
			}
			So(update("Goes with", &baseline), ShouldBeNil)
			serr := update("Bundled with", &baseline)

			Convey("Then the second is refused rather than erasing the first", func() {
				So(serr, ShouldNotBeNil)
				So(domainerrors.CodeOf(serr), ShouldEqual, domainerrors.CodeConflict)
			})

			Convey("And omitting the version is still last-write-wins", func() {
				So(update("Bundled with", nil), ShouldBeNil)
			})
		})

		Convey("When two operators edit one DEPENDENCY", func() {
			// A dependency decides which values are accepted, so a lost update
			// here changes validation for every later write.
			kind, kerr := ia.Attributes().Create(ctx, appattribute.CreateInput{
				TypeDefinitionID: product.ID.String(), InternalName: "kind",
				DisplayName: "Kind", DataType: "string",
			})
			So(kerr, ShouldBeNil)
			trim, terr := ia.Attributes().Create(ctx, appattribute.CreateInput{
				TypeDefinitionID: product.ID.String(), InternalName: "trim",
				DisplayName: "Trim", DataType: "string",
			})
			So(terr, ShouldBeNil)

			rule := func(allowed string) json.RawMessage {
				return json.RawMessage(`{"allowed_values":[{"type":"string","value":"` + allowed + `"}]}`)
			}
			dep, cerr := ia.Dependencies().Create(ctx, appdependency.CreateInput{
				SourceAttributeID: kind.ID.String(), TargetAttributeID: trim.ID.String(),
				Conditions: json.RawMessage(
					`[{"kind":"equals","value":{"type":"string","value":"car"}}]`),
				Effect: rule("base"),
			})
			So(cerr, ShouldBeNil)
			baseline := dep.Version

			update := func(allowed string, version *int) error {
				_, uerr := svc.Interactors(ctx).Dependencies().Update(ctx, appdependency.UpdateInput{
					ID: dep.ID.String(),
					Conditions: json.RawMessage(
						`[{"kind":"equals","value":{"type":"string","value":"car"}}]`),
					Effect: rule(allowed), Version: version,
				})
				return uerr
			}
			So(update("sport", &baseline), ShouldBeNil)
			serr := update("luxury", &baseline)

			Convey("Then the second is refused rather than erasing the first", func() {
				So(serr, ShouldNotBeNil)
				So(domainerrors.CodeOf(serr), ShouldEqual, domainerrors.CodeConflict)
			})

			Convey("Then the first operator's rule is the one still enforced", func() {
				// The point of the check: what the API accepts must match the
				// rule that actually committed.
				current, gerr := svc.Interactors(ctx).Dependencies().Get(ctx, dep.ID.String())
				So(gerr, ShouldBeNil)
				So(current.Effect.AllowedValues, ShouldHaveLength, 1)
				So(current.Effect.AllowedValues[0].String(), ShouldEqual, "sport")
			})

			Convey("And omitting the version is still last-write-wins", func() {
				So(update("luxury", nil), ShouldBeNil)
			})
		})
	})
}

// TestDefinitionLostUpdate runs the scenarios against the in-memory backend.
func TestDefinitionLostUpdate(t *testing.T) {
	runDefinitionLostUpdate(t, "memory", func() *flexitype.Service { return flexitype.NewInMemory() })
}

// TestDefinitionLostUpdatePostgres re-runs them against Postgres, where the
// read is a real SELECT ... FOR UPDATE.
func TestDefinitionLostUpdatePostgres(t *testing.T) {
	pool := openTestDB(t)
	defer func() { _ = pool.Close() }()

	runDefinitionLostUpdate(t, "postgres", func() *flexitype.Service {
		svc := flexitype.New(pool)
		if err := svc.Migrate(context.Background()); err != nil {
			t.Fatalf("migrate: %v", err)
		}
		truncateAll(t, pool)
		return svc
	})
}
