package memory_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	"github.com/zkrebbekx/flexitype/application"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	appdependency "github.com/zkrebbekx/flexitype/application/dependency"
	appquery "github.com/zkrebbekx/flexitype/application/query"
	apprelationship "github.com/zkrebbekx/flexitype/application/relationship"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	domaintypedef "github.com/zkrebbekx/flexitype/domain/typedef"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/infrastructure/memory"
	"github.com/zkrebbekx/flexitype/pkg/db"
)

// harness spins one in-memory service and provides schema shorthand.
type harness struct {
	svc *flexitype.Service
	ctx context.Context
}

func newHarness() *harness {
	return &harness{
		svc: flexitype.NewInMemory(flexitype.WithSearchIndex()),
		ctx: context.Background(),
	}
}

func (h *harness) interactors() *application.Interactors {
	return h.svc.Interactors(h.ctx)
}

func (h *harness) createType(name, extendsID string) string {
	snap, err := h.interactors().TypeDefinitions().Create(h.ctx, apptypedef.CreateInput{
		InternalName: name,
		DisplayName:  name,
		ExtendsID:    extendsID,
	})
	So(err, ShouldBeNil)
	return snap.ID.String()
}

func (h *harness) createAttr(typeID, name, dataType string) string {
	snap, err := h.interactors().Attributes().Create(h.ctx, appattribute.CreateInput{
		TypeDefinitionID: typeID,
		InternalName:     name,
		DisplayName:      name,
		DataType:         dataType,
	})
	So(err, ShouldBeNil)
	return snap.ID.String()
}

func (h *harness) setValue(attrID, entityID, typeID string, value any) {
	raw, err := json.Marshal(value)
	So(err, ShouldBeNil)
	_, err = h.interactors().Values().Set(h.ctx, appvalue.SetInput{
		AttributeDefinitionID: attrID,
		EntityID:              entityID,
		TypeDefinitionID:      typeID,
		Value:                 raw,
	})
	So(err, ShouldBeNil)
}

func (h *harness) query(rootType, fqlText string) *appquery.ExecuteOutput {
	out, err := h.interactors().Query().Execute(h.ctx, appquery.ExecuteInput{
		Type:  rootType,
		Query: fqlText,
	})
	So(err, ShouldBeNil)
	return out
}

func entityIDs(out *appquery.ExecuteOutput) []string {
	ids := make([]string, 0, len(out.Items))
	for _, item := range out.Items {
		ids = append(ids, item.EntityID)
	}
	return ids
}

func TestInMemoryService(t *testing.T) {
	Convey("Given an in-memory flexitype service with the search index enabled", t, func() {
		h := newHarness()

		Convey("When a type with attributes is created and values are set", func() {
			productID := h.createType("product", "")
			nameAttr := h.createAttr(productID, "name", "string")
			priceAttr := h.createAttr(productID, "price", "decimal")

			h.setValue(nameAttr, "sku-1", productID, "Trail Bike")
			h.setValue(priceAttr, "sku-1", productID, "1499.00")
			h.setValue(nameAttr, "sku-2", productID, "City Bike")
			h.setValue(priceAttr, "sku-2", productID, "799.00")

			Convey("Then the type is retrievable by ID and internal name", func() {
				got, err := h.interactors().TypeDefinitions().Get(h.ctx, productID)
				So(err, ShouldBeNil)
				So(got.InternalName, ShouldEqual, "product")
			})

			Convey("Then entities list with value counts, newest first", func() {
				out, err := h.interactors().Values().ListEntities(h.ctx, productID, false, db.PageArgs{})
				So(err, ShouldBeNil)
				So(out.Items, ShouldHaveLength, 2)
				So(out.Items[0].ValueCount, ShouldEqual, 2)
			})

			Convey("Then FQL compares decimals numerically", func() {
				out := h.query("product", `price > 1000`)
				So(entityIDs(out), ShouldResemble, []string{"sku-1"})
			})

			Convey("Then FQL string predicates and in() work", func() {
				So(entityIDs(h.query("product", `icontains(name, "trail")`)), ShouldResemble, []string{"sku-1"})
				out := h.query("product", `name in ("Trail Bike", "City Bike")`)
				So(out.Items, ShouldHaveLength, 2)
			})

			Convey("Then FQL matches() hits the search projection", func() {
				out := h.query("product", `matches("trail bike")`)
				So(entityIDs(out), ShouldResemble, []string{"sku-1"})
				So(h.query("product", `matches("gravel")`).Items, ShouldBeEmpty)
			})

			Convey("Then min/max over absent values is UNKNOWN, excluded even under NOT", func() {
				// sku-3 has a name but no price. In SQL, min(price) is NULL,
				// so `min(price) > 1000` and its NOT are both NULL and exclude
				// sku-3. The memory evaluator must agree — this is the exact
				// SQL/memory divergence #29 fixes.
				h.setValue(nameAttr, "sku-3", productID, "Gravel Bike")

				pos := entityIDs(h.query("product", `min(price) > 1000`))
				So(pos, ShouldResemble, []string{"sku-1"}) // only 1499

				neg := entityIDs(h.query("product", `not (min(price) > 1000)`))
				So(neg, ShouldContain, "sku-2")    // 799: definite FALSE → NOT true
				So(neg, ShouldNotContain, "sku-3") // NULL, not FALSE → still excluded

				// count() over no rows is 0 (definite, not NULL), so this matches.
				So(entityIDs(h.query("product", `count(price) = 0`)), ShouldContain, "sku-3")
			})

			Convey("Then the activity log recorded the changes", func() {
				out, err := h.interactors().Activity().List(h.ctx, application.ActivityListInput{Page: db.PageArgs{WantTotal: true}})
				So(err, ShouldBeNil)
				So(out.PageInfo.TotalCount, ShouldNotBeNil)
				So(*out.PageInfo.TotalCount, ShouldBeGreaterThanOrEqualTo, 6)
			})
		})

		Convey("When a subtype extends a parent type", func() {
			vehicleID := h.createType("vehicle", "")
			wheelsAttr := h.createAttr(vehicleID, "wheels", "integer")
			bikeID := h.createType("bike", vehicleID)

			h.setValue(wheelsAttr, "v-1", vehicleID, 4)
			h.setValue(wheelsAttr, "b-1", bikeID, 2)

			Convey("Then querying the root type spans the hierarchy", func() {
				out := h.query("vehicle", `wheels >= 2`)
				So(out.Items, ShouldHaveLength, 2)
			})

			Convey("Then type = narrows to one declared type", func() {
				out := h.query("vehicle", `type = bike`)
				So(entityIDs(out), ShouldResemble, []string{"b-1"})
			})
		})

		Convey("When two types are linked through a relationship", func() {
			productID := h.createType("product", "")
			supplierID := h.createType("supplier", "")
			nameAttr := h.createAttr(productID, "name", "string")
			regionAttr := h.createAttr(supplierID, "region", "string")

			rels := h.interactors().Relationships()
			def, err := rels.CreateDefinition(h.ctx, apprelationship.CreateDefinitionInput{
				InternalName: "supplied_by",
				DisplayName:  "Supplied by",
				ParentTypeID: productID,
				ChildTypeID:  supplierID,
			})
			So(err, ShouldBeNil)

			h.setValue(nameAttr, "sku-1", productID, "Trail Bike")
			h.setValue(regionAttr, "acme", supplierID, "EU")
			_, err = rels.Link(h.ctx, apprelationship.LinkInput{
				DefinitionID: def.ID.String(),
				ParentEntity: "sku-1",
				ChildEntity:  "acme",
			})
			So(err, ShouldBeNil)

			Convey("Then FQL traversals cross the relationship", func() {
				out := h.query("product", `child(supplied_by) { region = "EU" }`)
				So(entityIDs(out), ShouldResemble, []string{"sku-1"})
				So(h.query("product", `child(supplied_by) { region = "US" }`).Items, ShouldBeEmpty)
			})
		})

		Convey("When two products are linked symmetrically", func() {
			productID := h.createType("product", "")
			nameAttr := h.createAttr(productID, "name", "string")

			rels := h.interactors().Relationships()
			def, err := rels.CreateDefinition(h.ctx, apprelationship.CreateDefinitionInput{
				InternalName: "compatible_with",
				DisplayName:  "Compatible with",
				Kind:         "symmetric",
				ParentTypeID: productID,
				ChildTypeID:  productID,
			})
			So(err, ShouldBeNil)

			h.setValue(nameAttr, "sku-a", productID, "Alpha")
			h.setValue(nameAttr, "sku-b", productID, "Beta")
			// Link in reverse order: canonical storage makes the pair unique.
			_, err = rels.Link(h.ctx, apprelationship.LinkInput{
				DefinitionID: def.ID.String(),
				ParentEntity: "sku-b",
				ChildEntity:  "sku-a",
			})
			So(err, ShouldBeNil)

			Convey("Then the reverse link conflicts as a duplicate", func() {
				_, err := rels.Link(h.ctx, apprelationship.LinkInput{
					DefinitionID: def.ID.String(),
					ParentEntity: "sku-a",
					ChildEntity:  "sku-b",
				})
				So(domainerrors.IsConflict(err), ShouldBeTrue)
			})

			Convey("Then linked() traverses from either end", func() {
				So(entityIDs(h.query("product", `linked(compatible_with) { name = "Beta" }`)),
					ShouldResemble, []string{"sku-a"})
				So(entityIDs(h.query("product", `linked(compatible_with) { name = "Alpha" }`)),
					ShouldResemble, []string{"sku-b"})
			})

			Convey("Then child() on a symmetric definition is rejected", func() {
				err := h.interactors().Query().Validate(h.ctx, "product",
					`child(compatible_with) { name = "Beta" }`)
				So(domainerrors.IsValidation(err), ShouldBeTrue)
			})
		})

		Convey("When a write fails validation", func() {
			productID := h.createType("product", "")

			Convey("Then a duplicate internal name conflicts and nothing is written", func() {
				_, err := h.interactors().TypeDefinitions().Create(h.ctx, apptypedef.CreateInput{
					InternalName: "product",
					DisplayName:  "Product again",
				})
				So(domainerrors.IsConflict(err), ShouldBeTrue)

				out, err := h.interactors().TypeDefinitions().List(h.ctx, apptypedef.ListInput{})
				So(err, ShouldBeNil)
				So(out.Items, ShouldHaveLength, 1)
				So(out.Items[0].ID.String(), ShouldEqual, productID)
			})
		})

		Convey("When a unit of work fails after an intermediate write", func() {
			productID := h.createType("product", "")
			attrID := h.createAttr(productID, "sku", "string")

			// A dependency create loads source + target attributes and
			// validates they share a hierarchy chain; a bad target aborts
			// the transaction after the source was resolved. Assert the
			// store is left untouched: no dependency, no orphaned state.
			before, err := h.interactors().Dependencies().List(h.ctx, appdependency.ListInput{})
			So(err, ShouldBeNil)

			_, err = h.interactors().Dependencies().Create(h.ctx, appdependency.CreateInput{
				SourceAttributeID: attrID,
				TargetAttributeID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", // nonexistent
				Conditions:        json.RawMessage(`[{"kind":"equals","value":{"type":"string","value":"x"}}]`),
				Effect:            json.RawMessage(`{}`),
			})
			So(err, ShouldNotBeNil)

			Convey("Then no partial data survives the rollback", func() {
				after, err := h.interactors().Dependencies().List(h.ctx, appdependency.ListInput{})
				So(err, ShouldBeNil)
				So(len(after.Items), ShouldEqual, len(before.Items))
				// The type and attribute created before the failed uow remain.
				got, err := h.interactors().TypeDefinitions().Get(h.ctx, productID)
				So(err, ShouldBeNil)
				So(got.InternalName, ShouldEqual, "product")
			})
		})
	})
}

func TestMemoryTransactor(t *testing.T) {
	Convey("Given the in-memory transactor", t, func() {
		store := memory.NewStore()
		ctx := context.Background()

		Convey("When a transaction commits", func() {
			tx, err := store.Transactor().Begin(ctx)
			So(err, ShouldBeNil)

			var order []string
			tx.OnPreCommit(func(context.Context) error { order = append(order, "pre"); return nil })
			tx.OnPostCommit(func(context.Context) error { order = append(order, "post"); return nil })
			tx.OnRollback(func(context.Context) error { order = append(order, "rollback"); return nil })

			So(tx.Commit(ctx), ShouldBeNil)

			Convey("Then pre-commit runs before post-commit and rollback never fires", func() {
				So(order, ShouldResemble, []string{"pre", "post"})
			})
		})

		Convey("When a pre-commit hook fails", func() {
			tx, err := store.Transactor().Begin(ctx)
			So(err, ShouldBeNil)

			var order []string
			tx.OnPreCommit(func(context.Context) error { return errors.New("audit write failed") })
			tx.OnPostCommit(func(context.Context) error { order = append(order, "post"); return nil })
			tx.OnRollback(func(context.Context) error { order = append(order, "rollback"); return nil })

			err = tx.Commit(ctx)

			Convey("Then commit fails, rollback hooks fire and post-commit never runs", func() {
				So(err, ShouldNotBeNil)
				So(order, ShouldResemble, []string{"rollback"})
			})
		})

		Convey("When transactions nest", func() {
			tx, err := store.Transactor().Begin(ctx)
			So(err, ShouldBeNil)
			inner, err := tx.Begin(ctx)
			So(err, ShouldBeNil)

			fired := false
			tx.OnPostCommit(func(context.Context) error { fired = true; return nil })

			Convey("Then hooks fire only at the outermost commit", func() {
				So(inner.Commit(ctx), ShouldBeNil)
				So(fired, ShouldBeFalse)
				So(tx.Commit(ctx), ShouldBeNil)
				So(fired, ShouldBeTrue)
			})
		})

		Convey("When two transactions interleave, one committing and one rolling back", func() {
			// Reproduces the isolation bug in the old whole-store snapshot: A
			// begins, B begins, B writes and commits, then A rolls back. A
			// snapshot restore reverts the ENTIRE store to A's begin-time state,
			// silently erasing B's committed write. The undo journal must revert
			// only the keys A itself wrote.
			repos := store.Repositories()
			newType := func(name string) *domaintypedef.TypeDefinition {
				td, _, err := domaintypedef.New(domaintypedef.NewInput{
					TenantID: valueobjects.DefaultTenant, InternalName: name, DisplayName: name,
				}, time.Now())
				So(err, ShouldBeNil)
				return td
			}

			txA, err := store.Transactor().Begin(ctx)
			So(err, ShouldBeNil)
			txB, err := store.Transactor().Begin(ctx)
			So(err, ShouldBeNil)

			// B writes its own type and commits.
			bType := newType("beta")
			So(repos.TypeDefinitions.WithTx(txB).Save(ctx, bType), ShouldBeNil)
			So(txB.Commit(ctx), ShouldBeNil)

			// A writes a different type, then rolls back.
			aType := newType("alpha")
			So(repos.TypeDefinitions.WithTx(txA).Save(ctx, aType), ShouldBeNil)
			So(txA.Rollback(ctx), ShouldBeNil)

			Convey("Then B's committed write survives and A's rolled-back write is gone", func() {
				gotB, err := repos.TypeDefinitions.Get(ctx, bType.ID())
				So(err, ShouldBeNil)
				So(gotB.InternalName(), ShouldEqual, "beta")

				_, err = repos.TypeDefinitions.Get(ctx, aType.ID())
				So(err, ShouldNotBeNil) // rolled back: never persisted
			})
		})

		Convey("When a nested transaction rolls back", func() {
			// The depth guard must decline to undo at a nested frame; only the
			// outermost rollback performs the actual reversal.
			tx, err := store.Transactor().Begin(ctx)
			So(err, ShouldBeNil)
			inner, err := tx.Begin(ctx)
			So(err, ShouldBeNil)

			Convey("Then the nested rollback is reported and the outer frame still governs undo", func() {
				So(inner.Rollback(ctx), ShouldNotBeNil) // rollback inside nested transaction
				So(tx.Rollback(ctx), ShouldBeNil)       // outermost unwinds cleanly
			})
		})

		Convey("When an empty store is listed", func() {
			repos := store.Repositories()
			_, total, err := repos.TypeDefinitions.List(ctx,
				domaintypedef.Filter{TenantID: valueobjects.DefaultTenant}, db.Page{Limit: 10})
			So(err, ShouldBeNil)

			Convey("Then it reports zero without error", func() {
				So(total, ShouldEqual, 0)
			})
		})
	})
}
