package flexitype_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

// runApplyDefaults is the regression for #492.
//
// A declared default did nothing at all. Defaults were decoded, validated,
// stored, cloned, exported and rendered in the console, and DefaultFor had no
// caller outside its own tests: an attribute declared with `today` and
// required never received a value, and completeness scored it absent forever
// with nothing reporting the gap.
func runApplyDefaults(t *testing.T, label string, setup func() *flexitype.Service) {
	t.Helper()

	Convey("Given a type whose attributes declare static and dynamic defaults ("+label+")", t, func() {
		svc := setup()
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		ia := svc.Interactors(ctx)

		product, err := ia.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "product", DisplayName: "Product",
		})
		So(err, ShouldBeNil)
		typeID := product.ID.String()

		attr := func(in appattribute.CreateInput) string {
			in.TypeDefinitionID = typeID
			in.DisplayName = in.InternalName
			a, aerr := svc.Interactors(ctx).Attributes().Create(ctx, in)
			So(aerr, ShouldBeNil)
			return a.ID.String()
		}
		status := attr(appattribute.CreateInput{
			InternalName: "status", DataType: "string",
			DefaultValue: json.RawMessage(`{"static":{"type":"string","value":"draft"}}`),
		})
		createdOn := attr(appattribute.CreateInput{
			InternalName: "created_on", DataType: "date", Required: true,
			DefaultValue: json.RawMessage(`{"dynamic":{"kind":"today"}}`),
		})
		name := attr(appattribute.CreateInput{InternalName: "name", DataType: "string"})

		live := func(entity string) map[string]string {
			vals, verr := svc.Interactors(ctx).Values().ListByEntity(ctx, typeID, entity)
			So(verr, ShouldBeNil)
			out := map[string]string{}
			for _, v := range vals {
				out[v.AttributeDefinitionID.String()] = v.Value.String()
			}
			return out
		}

		Convey("When defaults are applied to an entity that holds nothing", func() {
			out, aerr := svc.Interactors(ctx).Values().ApplyDefaults(ctx, typeID, "p1")

			Convey("Then both defaults are written, and the dynamic one resolves to today", func() {
				So(aerr, ShouldBeNil)
				So(out.Applied, ShouldHaveLength, 2)
				So(out.Skipped, ShouldEqual, 0)

				held := live("p1")
				So(held[status], ShouldEqual, "draft")
				So(held[createdOn], ShouldEqual, time.Now().UTC().Format("2006-01-02"))
				// An attribute with no default is not invented.
				_, hasName := held[name]
				So(hasName, ShouldBeFalse)
			})
		})

		Convey("When the entity already holds one of them", func() {
			_, serr := svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: status, EntityID: "p2",
				TypeDefinitionID: typeID, Value: json.RawMessage(`"published"`),
			})
			So(serr, ShouldBeNil)
			out, aerr := svc.Interactors(ctx).Values().ApplyDefaults(ctx, typeID, "p2")

			Convey("Then the written value stands and only the missing default is seeded", func() {
				So(aerr, ShouldBeNil)
				So(out.Applied, ShouldHaveLength, 1)
				So(out.Applied[0].InternalName, ShouldEqual, "created_on")
				So(out.Skipped, ShouldEqual, 1)
				So(live("p2")[status], ShouldEqual, "published")
			})
		})

		Convey("When defaults are applied twice", func() {
			_, aerr := svc.Interactors(ctx).Values().ApplyDefaults(ctx, typeID, "p3")
			So(aerr, ShouldBeNil)
			second, aerr := svc.Interactors(ctx).Values().ApplyDefaults(ctx, typeID, "p3")

			Convey("Then the second run writes nothing: seeding is not repeated", func() {
				So(aerr, ShouldBeNil)
				So(second.Applied, ShouldBeEmpty)
				So(second.Skipped, ShouldEqual, 2)
				So(live("p3"), ShouldHaveLength, 2)
			})
		})

		Convey("When the entity holds the default only in a non-base scope", func() {
			localizable := attr(appattribute.CreateInput{
				InternalName: "blurb", DataType: "string", Localizable: true,
				DefaultValue: json.RawMessage(`{"static":{"type":"string","value":"none"}}`),
			})
			_, serr := svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: localizable, EntityID: "p4",
				TypeDefinitionID: typeID, Locale: "en", Value: json.RawMessage(`"Hello"`),
			})
			So(serr, ShouldBeNil)
			out, aerr := svc.Interactors(ctx).Values().ApplyDefaults(ctx, typeID, "p4")

			Convey("Then the base scope is still seeded, and the scoped value is untouched", func() {
				So(aerr, ShouldBeNil)
				names := []string{}
				for _, a := range out.Applied {
					names = append(names, a.InternalName)
				}
				So(names, ShouldContain, "blurb")

				vals, verr := svc.Interactors(ctx).Values().ListByEntity(ctx, typeID, "p4")
				So(verr, ShouldBeNil)
				var base, scoped string
				for _, v := range vals {
					if v.AttributeDefinitionID.String() != localizable {
						continue
					}
					if v.Locale == "" {
						base = v.Value.String()
					} else {
						scoped = v.Value.String()
					}
				}
				So(base, ShouldEqual, "none")
				So(scoped, ShouldEqual, "Hello")
			})
		})

		Convey("When a computed attribute declares a default", func() {
			attr(appattribute.CreateInput{
				InternalName: "qty", DataType: "integer",
			})
			attr(appattribute.CreateInput{
				InternalName: "doubled", DataType: "integer",
				Computed:     json.RawMessage(`{"kind":"formula","formula":"qty * 2"}`),
				DefaultValue: json.RawMessage(`{"static":{"type":"integer","value":7}}`),
			})
			out, aerr := svc.Interactors(ctx).Values().ApplyDefaults(ctx, typeID, "p5")

			Convey("Then it is skipped: the first recompute would overwrite it", func() {
				So(aerr, ShouldBeNil)
				for _, a := range out.Applied {
					So(a.InternalName, ShouldNotEqual, "doubled")
				}
			})
		})
	})
}

// TestApplyDefaults runs the scenarios against the in-memory backend.
func TestApplyDefaults(t *testing.T) {
	runApplyDefaults(t, "memory", func() *flexitype.Service {
		return flexitype.NewInMemory()
	})
}

// TestApplyDefaultsPostgres re-runs them against Postgres.
func TestApplyDefaultsPostgres(t *testing.T) {
	pool := openTestDB(t)
	defer func() { _ = pool.Close() }()

	svc := flexitype.New(pool)
	if err := svc.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	runApplyDefaults(t, "postgres", func() *flexitype.Service {
		truncateAll(t, pool)
		return svc
	})
}
