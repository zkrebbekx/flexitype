package flexitype_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	appchangeset "github.com/zkrebbekx/flexitype/application/changeset"
	apprelationship "github.com/zkrebbekx/flexitype/application/relationship"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"

	"github.com/zkrebbekx/flexitype/internal/testdb"
)

// concurrentWriters is how many goroutines race for each invariant. Each of
// these defects is a read-then-write under READ COMMITTED, so two writers is
// enough to reproduce it and more only shortens the odds.
const concurrentWriters = 8

// TestConcurrencyInvariantsPostgres reproduces the three check-then-write races
// against real Postgres. Each of them committed data that violated a guarantee
// the schema declares, permanently and with no error to either party, and none
// was reachable from a single-threaded test — which is why all three survived
// the suite.
func TestConcurrencyInvariantsPostgres(t *testing.T) {
	pool := openTestDB(t)

	svc := flexitype.New(pool)
	if err := svc.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	admin := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)

	Convey("Given a unique attribute (Postgres)", t, func() {
		truncateAll(t, pool)
		ia := svc.Interactors(admin)
		product, err := ia.TypeDefinitions().Create(admin, apptypedef.CreateInput{
			InternalName: "product", DisplayName: "Product",
		})
		So(err, ShouldBeNil)
		sku, err := ia.Attributes().Create(admin, appattribute.CreateInput{
			TypeDefinitionID: product.ID.String(), InternalName: "sku", DisplayName: "SKU",
			DataType: "string", Unique: true,
		})
		So(err, ShouldBeNil)

		Convey("When many writers set the same value on different entities at once", func() {
			raw, _ := json.Marshal("SHARED-001")
			errs := runConcurrently(concurrentWriters, func(i int) error {
				_, err := svc.Interactors(admin).Values().Set(admin, appvalue.SetInput{
					AttributeDefinitionID: sku.ID.String(),
					EntityID:              entityName(i),
					TypeDefinitionID:      product.ID.String(),
					Value:                 raw,
				})
				return err
			})

			Convey("Then exactly one commits and the rest are rejected as conflicts", func() {
				succeeded, conflicts := 0, 0
				for _, err := range errs {
					switch {
					case err == nil:
						succeeded++
					case domainerrors.CodeOf(err) == domainerrors.CodeConflict:
						conflicts++
					default:
						So(err, ShouldBeNil) // report anything unexpected
					}
				}
				So(succeeded, ShouldEqual, 1)
				So(conflicts, ShouldEqual, concurrentWriters-1)
			})

			Convey("Then the attribute holds exactly one live value", func() {
				out, err := svc.Interactors(admin).Values().List(admin, appvalue.ListInput{
					AttributeDefinitionID: sku.ID.String(),
				})
				So(err, ShouldBeNil)
				So(out.Items, ShouldHaveLength, 1)
			})
		})
	})

	Convey("Given a relationship that permits one parent per child (Postgres)", t, func() {
		truncateAll(t, pool)
		ia := svc.Interactors(admin)
		product, err := ia.TypeDefinitions().Create(admin, apptypedef.CreateInput{
			InternalName: "product", DisplayName: "Product",
		})
		So(err, ShouldBeNil)
		one := 1
		def, err := ia.Relationships().CreateDefinition(admin, apprelationship.CreateDefinitionInput{
			InternalName: "contains", DisplayName: "Contains",
			ParentTypeID: product.ID.String(), ChildTypeID: product.ID.String(),
			MaxParents: &one,
		})
		So(err, ShouldBeNil)

		Convey("When many writers link the same child to different parents at once", func() {
			errs := runConcurrently(concurrentWriters, func(i int) error {
				_, err := svc.Interactors(admin).Relationships().Link(admin, apprelationship.LinkInput{
					DefinitionID: def.ID.String(),
					ParentEntity: entityName(i),
					ChildEntity:  "shared-child",
				})
				return err
			})

			Convey("Then exactly one link commits and the rest are rejected", func() {
				succeeded := 0
				for _, err := range errs {
					if err == nil {
						succeeded++
						continue
					}
					So(domainerrors.CodeOf(err), ShouldEqual, domainerrors.CodeValidation)
				}
				So(succeeded, ShouldEqual, 1)
			})

			Convey("Then the child holds exactly one live parent, as declared", func() {
				links, err := svc.Interactors(admin).Relationships().ListByEntity(admin, "shared-child")
				So(err, ShouldBeNil)
				So(links, ShouldHaveLength, 1)
			})
		})
	})

	Convey("Given an entity that many writers snapshot at once (Postgres)", t, func() {
		truncateAll(t, pool)
		ia := svc.Interactors(admin)
		product, err := ia.TypeDefinitions().Create(admin, apptypedef.CreateInput{
			InternalName: "product", DisplayName: "Product",
		})
		So(err, ShouldBeNil)
		name, err := ia.Attributes().Create(admin, appattribute.CreateInput{
			TypeDefinitionID: product.ID.String(), InternalName: "name", DisplayName: "Name",
			DataType: "string",
		})
		So(err, ShouldBeNil)
		raw, _ := json.Marshal("Alpha")
		_, err = ia.Values().Set(admin, appvalue.SetInput{
			AttributeDefinitionID: name.ID.String(), EntityID: "e1",
			TypeDefinitionID: product.ID.String(), Value: raw,
		})
		So(err, ShouldBeNil)

		Convey("When the snapshots run concurrently", func() {
			errs := runConcurrently(concurrentWriters, func(int) error {
				_, err := svc.Interactors(admin).Revisions().Create(admin,
					product.ID.String(), "e1", "concurrent")
				return err
			})
			for _, err := range errs {
				So(err, ShouldBeNil)
			}

			Convey("Then every revision has a distinct sequence", func() {
				revs, err := svc.Interactors(admin).Revisions().List(admin, product.ID.String(), "e1")
				So(err, ShouldBeNil)
				So(revs, ShouldHaveLength, concurrentWriters)

				seen := map[int]bool{}
				for _, r := range revs {
					So(seen[r.Seq], ShouldBeFalse)
					seen[r.Seq] = true
				}
				So(len(seen), ShouldEqual, concurrentWriters)
			})
		})
	})
}

// TestEntitySummaryDeadlockPostgres reproduces the deadlock class the
// entity-summary row trigger introduced: two transactions writing DISJOINT
// value rows of the same two entities, in opposite order. Their value rows
// never conflict; the summary rows were the only shared resource, and nothing
// ordered them.
func TestEntitySummaryDeadlockPostgres(t *testing.T) {
	pool := openTestDB(t)

	svc := flexitype.New(pool)
	if err := svc.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	admin := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)

	Convey("Given two entities and two attributes (Postgres)", t, func() {
		truncateAll(t, pool)
		ia := svc.Interactors(admin)
		product, err := ia.TypeDefinitions().Create(admin, apptypedef.CreateInput{
			InternalName: "product", DisplayName: "Product",
		})
		So(err, ShouldBeNil)
		attrs := make([]string, 2)
		for i := range attrs {
			a, err := ia.Attributes().Create(admin, appattribute.CreateInput{
				TypeDefinitionID: product.ID.String(),
				InternalName:     entityName(i) + "_attr",
				DisplayName:      "Attr", DataType: "string",
			})
			So(err, ShouldBeNil)
			attrs[i] = a.ID.String()
		}
		entities := []string{"ent-a", "ent-b"}

		Convey("When two batches write their disjoint values in opposite entity order", func() {
			// Each batch is one unit of work touching both entities, so the
			// summary rows are taken in the order the batch lists them.
			batch := func(order []string, attr string) error {
				items := make([]appvalue.SetInput, 0, len(order))
				for _, e := range order {
					raw, _ := json.Marshal("v-" + e)
					items = append(items, appvalue.SetInput{
						AttributeDefinitionID: attr,
						EntityID:              e,
						TypeDefinitionID:      product.ID.String(),
						Value:                 raw,
					})
				}
				_, err := svc.Interactors(admin).Values().SetBatch(admin, appvalue.BatchSetInput{Items: items})
				return err
			}

			var wg sync.WaitGroup
			errs := make([]error, 2)
			wg.Add(2)
			go func() { defer wg.Done(); errs[0] = batch(entities, attrs[0]) }()
			go func() {
				defer wg.Done()
				errs[1] = batch([]string{entities[1], entities[0]}, attrs[1])
			}()
			wg.Wait()

			Convey("Then neither deadlocks", func() {
				for _, err := range errs {
					So(err, ShouldBeNil)
				}
			})

			Convey("Then both entities carry both values", func() {
				for _, err := range errs {
					So(err, ShouldBeNil)
				}
				for _, e := range entities {
					vals, err := svc.Interactors(admin).Values().ListByEntity(admin, product.ID.String(), e)
					So(err, ShouldBeNil)
					So(vals, ShouldHaveLength, 2)
				}
			})
		})
	})
}

// runConcurrently starts n goroutines, releases them together, and returns
// their errors in order. Releasing together matters: staggered starts would
// let each writer see the previous one's committed row and pass the check
// legitimately, which is how the single-threaded suite missed these.
func runConcurrently(n int, fn func(i int) error) []error {
	start := make(chan struct{})
	errs := make([]error, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			<-start
			errs[i] = fn(i)
		}(i)
	}
	close(start)
	wg.Wait()
	return errs
}

// entityName gives each writer its own entity, so the only contention is the
// invariant under test rather than the row identity.
func entityName(i int) string {
	return string(rune('a' + i))
}

// TestChangeSetPublishDeadlockPostgres extends the entity-summary ordering
// invariant to the paths the first fix did not cover.
//
// The statement trigger sorts keys WITHIN one statement, and SetBatch sorted
// its own items — but change-set publish and CSV import each issue one INSERT
// per value in caller order, so two of them over the same entities in
// opposite order still deadlocked: 40 rounds out of 40 in the reported
// reproduction, on transactions whose value rows are disjoint.
func TestChangeSetPublishDeadlockPostgres(t *testing.T) {
	pool := openTestDB(t)
	svc := flexitype.New(pool)
	if err := svc.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	admin := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)

	Convey("Given two entities and two attributes (Postgres)", t, func() {
		truncateAll(t, pool)
		ia := svc.Interactors(admin)
		product, err := ia.TypeDefinitions().Create(admin, apptypedef.CreateInput{
			InternalName: "product", DisplayName: "Product",
		})
		So(err, ShouldBeNil)
		attrs := make([]string, 2)
		for i := range attrs {
			a, aerr := ia.Attributes().Create(admin, appattribute.CreateInput{
				TypeDefinitionID: product.ID.String(),
				InternalName:     entityName(i) + "_cs_attr",
				DisplayName:      "Attr", DataType: "string",
			})
			So(aerr, ShouldBeNil)
			attrs[i] = a.ID.String()
		}
		entities := []string{"cs-ent-a", "cs-ent-b"}

		// stage builds a change-set whose mutations touch the entities in the
		// given order, and returns a func that publishes it.
		stage := func(order []string, attr string) func() error {
			cs, cerr := svc.Interactors(admin).ChangeSets().Create(admin,
				appchangeset.CreateInput{Name: "cs-" + attr})
			So(cerr, ShouldBeNil)
			for _, e := range order {
				raw, _ := json.Marshal("v-" + e)
				_, aerr := svc.Interactors(admin).ChangeSets().AddMutation(admin, cs.ID.String(),
					appvalue.Mutation{
						Kind: appvalue.MutationSet, AttributeDefinitionID: attr,
						EntityID: e, TypeDefinitionID: product.ID.String(), Value: raw,
					})
				So(aerr, ShouldBeNil)
			}
			return func() error {
				_, perr := svc.Interactors(admin).ChangeSets().Publish(admin, cs.ID.String())
				return perr
			}
		}

		Convey("When two change-sets publish over the same entities in opposite order", func() {
			// Ten rounds: a deadlock is a race, and the unfixed code lost it
			// every time. One round would be a weak assertion.
			const rounds = 10
			var deadlocks int
			for round := 0; round < rounds; round++ {
				testdb.TruncateTablesCascade(t, pool, "flexitype_attribute_value", "flexitype_entity_summary", "flexitype_changeset")
				forward := stage(entities, attrs[0])
				backward := stage([]string{entities[1], entities[0]}, attrs[1])

				var wg sync.WaitGroup
				errs := make([]error, 2)
				wg.Add(2)
				go func() { defer wg.Done(); errs[0] = forward() }()
				go func() { defer wg.Done(); errs[1] = backward() }()
				wg.Wait()

				for _, err := range errs {
					if err != nil && strings.Contains(err.Error(), "deadlock") {
						deadlocks++
					}
				}
			}

			Convey("Then none of them deadlocks", func() {
				So(deadlocks, ShouldEqual, 0)
			})
		})
	})
}

// TestPurgeIsChunkedPostgres covers the transition-table cost the statement
// trigger introduced.
//
// The trigger is FOR EACH STATEMENT with REFERENCING OLD TABLE, so an
// unbounded purge materialises every deleted row into a tuplestore and spills
// it to temp disk: 17.2x slower and ~42MB of temp blocks at 300k rows, and on
// the order of 14GB at the 10^8 rows migration 000022 cites as the target.
// Chunking keeps each transition table proportional to the chunk.
func TestPurgeIsChunkedPostgres(t *testing.T) {
	pool := openTestDB(t)
	svc := flexitype.New(pool)
	if err := svc.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	admin := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)

	Convey("Given more values than one purge chunk holds", t, func() {
		truncateAll(t, pool)
		ia := svc.Interactors(admin)
		product, err := ia.TypeDefinitions().Create(admin, apptypedef.CreateInput{
			InternalName: "product", DisplayName: "Product",
		})
		So(err, ShouldBeNil)
		attr, err := ia.Attributes().Create(admin, appattribute.CreateInput{
			TypeDefinitionID: product.ID.String(), InternalName: "code",
			DisplayName: "Code", DataType: "string",
		})
		So(err, ShouldBeNil)

		// Insert past the 5000-row chunk directly: the point is the delete
		// path, and going through the write path would make the test minutes
		// long for nothing.
		const rows = 5200
		pool.MustExec(`
			INSERT INTO flexitype_attribute_value
			  (id, tenant_id, type_definition_id, attribute_definition_id, entity_id,
			   locale, channel, data_type, value_json, definition_version, created_at, updated_at)
			SELECT lpad(g::text, 26, '0'), $1, $2, $3, 'e-' || g,
			       '', '', 'string', jsonb_build_object('text', 'v'), 1, now(), now()
			  FROM generate_series(1, $4) g`,
			valueobjects.DefaultTenant.String(), product.ID.String(), attr.ID.String(), rows)

		Convey("When the tenant is purged", func() {
			report, err := svc.Interactors(admin).Erasure().PurgeTenant(admin)

			Convey("Then every row is removed across chunks, and the count is the total", func() {
				So(err, ShouldBeNil)
				So(report.ValuesPurged, ShouldEqual, rows)

				var left int
				So(pool.Get(&left, `SELECT count(*) FROM flexitype_attribute_value`), ShouldBeNil)
				So(left, ShouldEqual, 0)
			})

			Convey("Then the summary projection is emptied with them", func() {
				var summaries int
				So(pool.Get(&summaries, `SELECT count(*) FROM flexitype_entity_summary`), ShouldBeNil)
				So(summaries, ShouldEqual, 0)
			})
		})
	})
}
