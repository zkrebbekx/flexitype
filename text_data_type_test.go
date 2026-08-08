package flexitype_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	appquery "github.com/zkrebbekx/flexitype/application/query"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
)

// runTextDataType covers #551.
//
// There was no data type for long text, so a description was a `string` with a
// large `max_length`. From a form's side those two are identical: a renderer
// had to guess from the constraint, or from the attribute's name, whether to
// draw one line or a text area. The API's promise is that a client can render
// a correct form from the schema ALONE, and one missing distinction broke that
// for the most common content in a catalog.
//
// `text` stores and compares exactly as `string`. It differs in one declared
// thing, and everything else must keep working — which is what these cases
// pin.
func runTextDataType(t *testing.T, label string, setup func() *flexitype.Service) {
	t.Helper()

	Convey("Given a type with a long-form text attribute ("+label+")", t, func() {
		svc := setup()
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		ia := svc.Interactors(ctx)

		product, err := ia.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "product", DisplayName: "Product",
		})
		So(err, ShouldBeNil)

		description, err := svc.Interactors(ctx).Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: product.ID.String(), InternalName: "description",
			DisplayName: "Description", DataType: "text",
			// The same constraints a string takes.
			Constraints: json.RawMessage(`[{"kind":"max_length","n":4000}]`),
			HelpText:    "Long-form copy.",
		})
		So(err, ShouldBeNil)

		set := func(entity, value string) error {
			raw, merr := json.Marshal(value)
			So(merr, ShouldBeNil)
			_, serr := svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: description.ID.String(), EntityID: entity,
				TypeDefinitionID: product.ID.String(), Value: raw,
			})
			return serr
		}

		Convey("When the attribute is read back", func() {
			read, rerr := svc.Interactors(ctx).Attributes().Get(ctx, description.ID.String())

			Convey("Then it reports the text type, which is the whole point", func() {
				So(rerr, ShouldBeNil)
				So(read.DataType.String(), ShouldEqual, "text")
			})
		})

		Convey("When a multi-line value is written", func() {
			body := "A gooseneck kettle.\n\nIt has a temperature dial, and a long spout."
			So(set("p-1", body), ShouldBeNil)

			Convey("Then it round-trips unchanged, newlines and all", func() {
				values, verr := svc.Interactors(ctx).Values().ListByEntity(ctx, product.ID.String(), "p-1")
				So(verr, ShouldBeNil)
				So(values, ShouldHaveLength, 1)
				raw, merr := json.Marshal(values[0].Value)
				So(merr, ShouldBeNil)
				var got string
				So(json.Unmarshal(raw, &got), ShouldBeNil)
				So(got, ShouldEqual, body)
			})
		})

		Convey("When a value longer than the constraint is written", func() {
			err := set("p-2", strings.Repeat("x", 4001))

			Convey("Then max_length applies, exactly as it does to a string", func() {
				So(err, ShouldNotBeNil)
			})
		})

		Convey("When a non-string is written", func() {
			raw := json.RawMessage(`42`)
			_, serr := svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: description.ID.String(), EntityID: "p-3",
				TypeDefinitionID: product.ID.String(), Value: raw,
			})

			Convey("Then it is refused: text is text", func() {
				So(serr, ShouldNotBeNil)
			})
		})

		Convey("When the catalog is queried with a textual predicate", func() {
			So(set("p-4", "A merino base layer, made in Italy."), ShouldBeNil)
			So(set("p-5", "A cotton tee."), ShouldBeNil)

			run := func(q string) []string {
				out, qerr := svc.Interactors(ctx).Query().Execute(ctx, appquery.ExecuteInput{
					Type: "product", Query: q, Page: db.PageArgs{},
				})
				So(qerr, ShouldBeNil)
				ids := []string{}
				for _, row := range out.Items {
					ids = append(ids, row.EntityID)
				}
				return ids
			}

			Convey("Then contains() works: text is a textual type", func() {
				So(run(`contains(description, "merino")`), ShouldResemble, []string{"p-4"})
			})

			Convey("And icontains() ignores case, as it does for a string", func() {
				So(run(`icontains(description, "COTTON")`), ShouldResemble, []string{"p-5"})
			})
		})
	})
}

// TestTextDataType runs the scenarios against the in-memory backend.
func TestTextDataType(t *testing.T) {
	runTextDataType(t, "memory", func() *flexitype.Service {
		return flexitype.NewInMemory()
	})
}

// TestTextDataTypePostgres re-runs them against Postgres, where the value is a
// column and the predicates are SQL.
func TestTextDataTypePostgres(t *testing.T) {
	pool := openTestDB(t)
	defer func() { _ = pool.Close() }()

	svc := flexitype.New(pool)
	if err := svc.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	runTextDataType(t, "postgres", func() *flexitype.Service {
		truncateAll(t, pool)
		return svc
	})
}

// TestTextIsSearchable pins that the search index covers long text.
//
// A description is the most search-worthy content an entity has. A `text`
// attribute that the index skipped would make the feature worse than the
// `string` it replaces.
func TestTextIsSearchable(t *testing.T) {
	Convey("Given an indexed service with a text attribute", t, func() {
		svc := flexitype.NewInMemory(flexitype.WithSearchIndex())
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		ia := svc.Interactors(ctx)

		product, err := ia.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "product", DisplayName: "Product",
		})
		So(err, ShouldBeNil)
		description, err := svc.Interactors(ctx).Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: product.ID.String(), InternalName: "description",
			DisplayName: "Description", DataType: "text",
		})
		So(err, ShouldBeNil)
		_, err = svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{
			AttributeDefinitionID: description.ID.String(), EntityID: "p-1",
			TypeDefinitionID: product.ID.String(),
			Value:            json.RawMessage(`"A merino base layer, made in Italy."`),
		})
		So(err, ShouldBeNil)

		Convey("When a word from the body is searched", func() {
			out, qerr := svc.Interactors(ctx).Query().Execute(ctx, appquery.ExecuteInput{
				Type: "product", Query: `matches("merino")`, Page: db.PageArgs{},
			})

			Convey("Then the entity is found: long text is indexed like a string", func() {
				So(qerr, ShouldBeNil)
				So(out.Items, ShouldHaveLength, 1)
				So(out.Items[0].EntityID, ShouldEqual, "p-1")
			})
		})
	})
}
