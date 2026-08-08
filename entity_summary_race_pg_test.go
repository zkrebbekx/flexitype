package flexitype_test

import (
	"context"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// TestEntitySummaryCountsAfterLockPostgres is the regression for #501.
//
// The refresh trigger counted the entity's live rows BEFORE its upsert
// blocked on the summary-row lock a concurrent same-entity writer held, so
// the last committer persisted a count taken before the other writer's row
// was visible: two concurrent inserts left value_count = 1 with 2 live rows.
//
// The interleave is forced deterministically with two raw transactions: T1
// inserts a value row and holds the summary lock until commit; T2's insert
// blocks inside the trigger; T1 commits; T2 resumes and commits. The stored
// count must include both rows.
func TestEntitySummaryCountsAfterLockPostgres(t *testing.T) {
	pool := openTestDB(t)
	defer func() { _ = pool.Close() }()

	svc := flexitype.New(pool)
	if err := svc.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	admin := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)

	Convey("Given two attributes of one type and one entity", t, func() {
		truncateAll(t, pool)
		ia := svc.Interactors(admin)
		product, err := ia.TypeDefinitions().Create(admin, apptypedef.CreateInput{
			InternalName: "product", DisplayName: "Product",
		})
		So(err, ShouldBeNil)
		mk := func(name string) string {
			a, aerr := ia.Attributes().Create(admin, appattribute.CreateInput{
				TypeDefinitionID: product.ID.String(), InternalName: name,
				DisplayName: name, DataType: "string",
			})
			So(aerr, ShouldBeNil)
			return a.ID.String()
		}
		attr1, attr2 := mk("first"), mk("second")

		insertValue := `INSERT INTO flexitype_attribute_value
			(id, tenant_id, type_definition_id, attribute_definition_id, entity_id,
			 data_type, value_text, definition_version, created_at, updated_at, locale, channel)
			VALUES ($1, $2, $3, $4, $5, 'string', 'v', 1, now(), now(), '', '')`
		tenant := valueobjects.DefaultTenant.String()
		typeID := product.ID.String()

		Convey("When two transactions insert a value each, overlapping on the summary lock", func() {
			tx1, err := pool.Beginx()
			So(err, ShouldBeNil)
			_, err = tx1.Exec(insertValue, ulid.New().String(), tenant, typeID, attr1, "e1")
			So(err, ShouldBeNil) // T1's trigger ran; T1 holds the summary row lock.

			done := make(chan error, 1)
			go func() {
				tx2, terr := pool.Beginx()
				if terr != nil {
					done <- terr
					return
				}
				// Blocks inside the trigger until T1 commits.
				if _, terr = tx2.Exec(insertValue, ulid.New().String(), tenant, typeID, attr2, "e1"); terr != nil {
					_ = tx2.Rollback()
					done <- terr
					return
				}
				done <- tx2.Commit()
			}()

			// Give T2 time to reach the lock wait, then release it.
			time.Sleep(300 * time.Millisecond)
			So(tx1.Commit(), ShouldBeNil)
			So(<-done, ShouldBeNil)

			Convey("Then the summary counts both live rows", func() {
				var count int
				err := pool.Get(&count,
					`SELECT value_count FROM flexitype_entity_summary
					  WHERE tenant_id = $1 AND type_definition_id = $2 AND entity_id = $3`,
					tenant, typeID, "e1")
				So(err, ShouldBeNil)
				So(count, ShouldEqual, 2)
			})
		})
	})
}
