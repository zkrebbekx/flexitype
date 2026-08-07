package flexitype_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	appdependency "github.com/zkrebbekx/flexitype/application/dependency"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

// TestDependencyUpdateLocksTargetPostgres is the regression for #481.
//
// The value write path serializes on the target attribute definition row
// (lockDefinition -> GetForUpdate), and dependency Create locks the same
// row — but Update and Archive read it with a plain Get. Under READ
// COMMITTED a value write could load the old rule while an Update committed
// a tighter effect, then commit a now-forbidden value, permanently. The test
// holds the row lock from a raw transaction and asserts Update and Archive
// WAIT on it, which is exactly the serialization the write path relies on.
func TestDependencyUpdateLocksTargetPostgres(t *testing.T) {
	pool := openTestDB(t)
	defer func() { _ = pool.Close() }()

	svc := flexitype.New(pool)
	if err := svc.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	admin := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)

	Convey("Given a dependency whose target row is locked by another transaction", t, func() {
		truncateAll(t, pool)
		ia := svc.Interactors(admin)
		order, err := ia.TypeDefinitions().Create(admin, apptypedef.CreateInput{
			InternalName: "order", DisplayName: "Order",
		})
		So(err, ShouldBeNil)
		mk := func(name string) string {
			a, aerr := ia.Attributes().Create(admin, appattribute.CreateInput{
				TypeDefinitionID: order.ID.String(), InternalName: name,
				DisplayName: name, DataType: "string",
			})
			So(aerr, ShouldBeNil)
			return a.ID.String()
		}
		fragile := mk("fragile")
		shipping := mk("shipping_method")
		dep, err := ia.Dependencies().Create(admin, appdependency.CreateInput{
			SourceAttributeID: fragile,
			TargetAttributeID: shipping,
			Conditions:        json.RawMessage(`[{"kind":"equals","value":{"type":"string","value":"yes"}}]`),
			Effect: json.RawMessage(
				`{"allowed_values":[{"type":"string","value":"air"},{"type":"string","value":"ground"}]}`),
		})
		So(err, ShouldBeNil)

		lockRow := func() (release func()) {
			tx, terr := pool.Beginx()
			So(terr, ShouldBeNil)
			_, terr = tx.Exec(`SELECT id FROM flexitype_attribute_definition WHERE id = $1 FOR UPDATE`, shipping)
			So(terr, ShouldBeNil)
			return func() { _ = tx.Rollback() }
		}

		waitsForLock := func(op func() error) {
			release := lockRow()
			done := make(chan error, 1)
			go func() { done <- op() }()
			select {
			case err := <-done:
				release()
				So("returned before the target lock was released: "+errText(err), ShouldBeBlank)
			case <-time.After(400 * time.Millisecond):
				// Blocked on the row lock: the serialization exists.
				release()
			}
			So(<-done, ShouldBeNil)
		}

		Convey("When the rule is tightened concurrently", func() {
			waitsForLock(func() error {
				_, uerr := svc.Interactors(admin).Dependencies().Update(admin, appdependency.UpdateInput{
					ID:         dep.ID.String(),
					Conditions: json.RawMessage(`[{"kind":"equals","value":{"type":"string","value":"yes"}}]`),
					Effect:     json.RawMessage(`{"allowed_values":[{"type":"string","value":"ground"}]}`),
				})
				return uerr
			})

			Convey("Then Update waited for the target definition lock", func() {
				// Asserted inside waitsForLock; re-read to confirm the commit.
				got, gerr := svc.Interactors(admin).Dependencies().Get(admin, dep.ID.String())
				So(gerr, ShouldBeNil)
				So(len(got.Effect.AllowedValues), ShouldEqual, 1)
			})
		})

		Convey("When the rule is archived concurrently", func() {
			waitsForLock(func() error {
				_, aerr := svc.Interactors(admin).Dependencies().Archive(admin, dep.ID.String())
				return aerr
			})

			Convey("Then Archive waited for the target definition lock", func() {
				got, gerr := svc.Interactors(admin).Dependencies().Get(admin, dep.ID.String())
				So(gerr, ShouldBeNil)
				So(got.ArchivedAt, ShouldNotBeNil)
			})
		})
	})
}

func errText(err error) string {
	if err == nil {
		return "no error"
	}
	return err.Error()
}
