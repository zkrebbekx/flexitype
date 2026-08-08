package flexitype_test

import (
	"context"
	"encoding/json"
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

// runMatchesPerAttribute covers #512, and re-covers the disclosure #473
// closed by refusing matches() outright.
//
// The search document was one flattening of every textual value with no
// attribute identity, so a search over it could not be filtered the way a
// named condition is: contains(internal_notes, ...) was refused as unknown
// while matches("recall") returned the entity, recovering restricted content
// word by word. Failing closed stopped the leak and took the feature from
// exactly the deployments that use field permissions.
//
// The index now carries one vector per attribute, so a restricted principal
// searches what it may read — and only that.
func runMatchesPerAttribute(t *testing.T, label string, setup func() *flexitype.Service) {
	t.Helper()

	Convey("Given an entity whose restricted notes hold confidential text ("+label+")", t, func() {
		svc := setup()
		admin := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		restricted := uow.WithAccess(admin, uow.Access{Attr: map[string]uow.Perm{
			"internal_notes": uow.PermNone,
		}})
		ia := svc.Interactors(admin)

		product, err := ia.TypeDefinitions().Create(admin, apptypedef.CreateInput{
			InternalName: "product", DisplayName: "Product",
		})
		So(err, ShouldBeNil)
		attrID := map[string]string{}
		for _, name := range []string{"name", "internal_notes"} {
			a, aerr := svc.Interactors(admin).Attributes().Create(admin, appattribute.CreateInput{
				TypeDefinitionID: product.ID.String(), InternalName: name,
				DisplayName: name, DataType: "string",
			})
			So(aerr, ShouldBeNil)
			attrID[name] = a.ID.String()
		}
		set := func(name, v string) {
			raw, _ := json.Marshal(v)
			_, serr := svc.Interactors(admin).Values().Set(admin, appvalue.SetInput{
				AttributeDefinitionID: attrID[name], EntityID: "e1",
				TypeDefinitionID: product.ID.String(), Value: raw,
			})
			So(serr, ShouldBeNil)
		}
		set("name", "widget")
		set("internal_notes", "confidential recall pending")

		run := func(ctx context.Context, q string) ([]string, error) {
			out, qerr := svc.Interactors(ctx).Query().Execute(ctx, appquery.ExecuteInput{
				Type: "product", Query: q, Page: db.PageArgs{},
			})
			if qerr != nil {
				return nil, qerr
			}
			ids := make([]string, 0, len(out.Items))
			for _, r := range out.Items {
				ids = append(ids, r.EntityID)
			}
			return ids, nil
		}

		Convey("When a denied principal searches for a word only the restricted attribute holds", func() {
			ids, err := run(restricted, `matches("recall")`)

			Convey("Then it finds nothing, and no longer errors", func() {
				So(err, ShouldBeNil)
				So(ids, ShouldBeEmpty)
			})
		})

		Convey("When that principal searches a word its READABLE attribute holds", func() {
			ids, err := run(restricted, `matches("widget")`)

			Convey("Then the search works again for a restricted principal", func() {
				So(err, ShouldBeNil)
				So(ids, ShouldResemble, []string{"e1"})
			})
		})

		Convey("When that principal searches by entity id", func() {
			ids, err := run(restricted, `matches("e1")`)

			Convey("Then it still matches: an id is not an attribute", func() {
				So(err, ShouldBeNil)
				So(ids, ShouldResemble, []string{"e1"})
			})
		})

		Convey("When an allow-list principal with nothing granted searches", func() {
			denyAll := uow.WithAccess(admin, uow.DenyAll())
			ids, err := run(denyAll, `matches("widget")`)

			Convey("Then it finds nothing: it may read no attribute at all", func() {
				So(err, ShouldBeNil)
				So(ids, ShouldBeEmpty)
			})
		})

		Convey("When a read-only principal that hides nothing searches", func() {
			readOnly := uow.WithAccess(admin, uow.Access{Attr: map[string]uow.Perm{
				"internal_notes": uow.PermRead,
			}})
			ids, err := run(readOnly, `matches("recall")`)

			Convey("Then it matches: read-only hides no value", func() {
				So(err, ShouldBeNil)
				So(ids, ShouldResemble, []string{"e1"})
			})
		})

		Convey("When an admin searches the restricted word", func() {
			ids, err := run(admin, `matches("recall")`)

			Convey("Then it matches, as it always did", func() {
				So(err, ShouldBeNil)
				So(ids, ShouldResemble, []string{"e1"})
			})
		})

		Convey("When the restricted value is removed and re-searched by an admin", func() {
			vals, verr := svc.Interactors(admin).Values().ListByEntity(admin, product.ID.String(), "e1")
			So(verr, ShouldBeNil)
			for _, v := range vals {
				if v.AttributeDefinitionID.String() == attrID["internal_notes"] {
					_, rerr := svc.Interactors(admin).Values().Remove(admin, v.ID.String())
					So(rerr, ShouldBeNil)
				}
			}

			Convey("Then the word is gone from the index, not left behind by a stale row", func() {
				ids, err := run(admin, `matches("recall")`)
				So(err, ShouldBeNil)
				So(ids, ShouldBeEmpty)

				ids, err = run(admin, `matches("widget")`)
				So(err, ShouldBeNil)
				So(ids, ShouldResemble, []string{"e1"})
			})
		})
	})
}

// TestMatchesPerAttribute runs the scenarios against the in-memory backend.
func TestMatchesPerAttribute(t *testing.T) {
	runMatchesPerAttribute(t, "memory", func() *flexitype.Service {
		return flexitype.NewInMemory(flexitype.WithSearchIndex())
	})
}

// TestMatchesPerAttributePostgres re-runs them against Postgres, where the
// per-attribute vectors are their own table.
func TestMatchesPerAttributePostgres(t *testing.T) {
	pool := openTestDB(t)
	defer func() { _ = pool.Close() }()

	svc := flexitype.New(pool, flexitype.WithSearchIndex())
	if err := svc.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	runMatchesPerAttribute(t, "postgres", func() *flexitype.Service {
		truncateAll(t, pool)
		return svc
	})
}

// TestSearchAttrBackfillPostgres covers the step that carries entities
// indexed before migration 000037 into the per-attribute table.
//
// It derives from flexitype_entity_search.document, which already holds the
// per-attribute values, so no application pass is needed. Until it runs, a
// restricted principal finds nothing for a not-yet-carried entity — the safe
// direction — and an unrestricted one is unaffected, since it searches the
// entity-level vector.
func TestSearchAttrBackfillPostgres(t *testing.T) {
	pool := openTestDB(t)
	defer func() { _ = pool.Close() }()

	svc := flexitype.New(pool, flexitype.WithSearchIndex())
	if err := svc.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	Convey("Given an entity indexed before the per-attribute split", t, func() {
		truncateAll(t, pool)
		admin := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		restricted := uow.WithAccess(admin, uow.Access{Attr: map[string]uow.Perm{
			"internal_notes": uow.PermNone,
		}})
		ia := svc.Interactors(admin)

		product, err := ia.TypeDefinitions().Create(admin, apptypedef.CreateInput{
			InternalName: "product", DisplayName: "Product",
		})
		So(err, ShouldBeNil)
		attrID := map[string]string{}
		for _, name := range []string{"name", "internal_notes"} {
			a, aerr := svc.Interactors(admin).Attributes().Create(admin, appattribute.CreateInput{
				TypeDefinitionID: product.ID.String(), InternalName: name,
				DisplayName: name, DataType: "string",
			})
			So(aerr, ShouldBeNil)
			attrID[name] = a.ID.String()
		}
		for name, v := range map[string]string{
			"name": "widget", "internal_notes": "confidential recall pending",
		} {
			raw, _ := json.Marshal(v)
			_, serr := svc.Interactors(admin).Values().Set(admin, appvalue.SetInput{
				AttributeDefinitionID: attrID[name], EntityID: "old1",
				TypeDefinitionID: product.ID.String(), Value: raw,
			})
			So(serr, ShouldBeNil)
		}

		// The pre-000037 state: the entity-level document is there, with its
		// per-attribute values, and the split table holds nothing.
		_, err = pool.Exec(`DELETE FROM flexitype_entity_search_attr WHERE entity_id = 'old1'`)
		So(err, ShouldBeNil)
		var before int
		So(pool.Get(&before,
			`SELECT count(*) FROM flexitype_entity_search_attr WHERE entity_id = 'old1'`), ShouldBeNil)
		So(before, ShouldEqual, 0)

		Convey("When the backfill runs", func() {
			// Migrate runs the registered backfills. Clearing this step's
			// completion marker is what an upgrade looks like from the
			// backfill's side: the rows exist, the step has not run yet.
			_, derr := pool.Exec(
				`DELETE FROM flexitype_schema_backfill WHERE name = '000037_entity_search_attr'`)
			So(derr, ShouldBeNil)
			So(svc.Migrate(context.Background()), ShouldBeNil)

			run := func(ctx context.Context, q string) []string {
				out, qerr := svc.Interactors(ctx).Query().Execute(ctx, appquery.ExecuteInput{
					Type: "product", Query: q, Page: db.PageArgs{},
				})
				So(qerr, ShouldBeNil)
				ids := []string{}
				for _, r := range out.Items {
					ids = append(ids, r.EntityID)
				}
				return ids
			}

			Convey("Then a restricted principal searches its readable attribute", func() {
				So(run(restricted, `matches("widget")`), ShouldResemble, []string{"old1"})
			})

			Convey("And still cannot reach the restricted one", func() {
				So(run(restricted, `matches("recall")`), ShouldBeEmpty)
			})

			Convey("And the entity id remains searchable", func() {
				So(run(restricted, `matches("old1")`), ShouldResemble, []string{"old1"})
			})
		})
	})
}
