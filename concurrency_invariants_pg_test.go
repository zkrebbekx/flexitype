package flexitype_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	apprelationship "github.com/zkrebbekx/flexitype/application/relationship"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
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
