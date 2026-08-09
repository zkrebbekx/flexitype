package flexitype_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	appchangeset "github.com/zkrebbekx/flexitype/application/changeset"
	appdependency "github.com/zkrebbekx/flexitype/application/dependency"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

// enforcementFixture is a dish that must list its allergens once it declares
// it has them — the kitchen example's rule, which is where the question came
// from.
type enforcementFixture struct {
	ctx        context.Context
	svc        *flexitype.Service
	dish       string
	flag       string
	allergens  string
	name       string
	dependency string
}

func newEnforcementFixture(t *testing.T, svc *flexitype.Service, mode string) *enforcementFixture {
	t.Helper()
	ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
	ia := svc.Interactors(ctx)

	dish, err := ia.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
		InternalName: "dish", DisplayName: "Dish",
	})
	So(err, ShouldBeNil)

	attr := func(name, dataType string, multi bool) string {
		a, aerr := ia.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: dish.ID.String(), InternalName: name, DisplayName: name,
			DataType: dataType, MultiValued: multi,
		})
		So(aerr, ShouldBeNil)
		return a.ID.String()
	}
	f := &enforcementFixture{
		ctx:       ctx,
		svc:       svc,
		dish:      dish.ID.String(),
		flag:      attr("contains_allergens", "bool", false),
		allergens: attr("allergens", "string", true),
		name:      attr("name", "string", false),
	}

	effect := `{"required":true}`
	if mode != "" {
		effect = `{"required":true,"enforce":"` + mode + `"}`
	}
	dep, err := ia.Dependencies().Create(ctx, appdependency.CreateInput{
		SourceAttributeID: f.flag,
		TargetAttributeID: f.allergens,
		Conditions:        json.RawMessage(`[{"kind":"equals","value":{"type":"bool","value":true}}]`),
		Effect:            json.RawMessage(effect),
	})
	So(err, ShouldBeNil)
	f.dependency = dep.ID.String()
	return f
}

func (f *enforcementFixture) set(attrID, entity string, v any) error {
	raw, _ := json.Marshal(v)
	_, err := f.svc.Interactors(f.ctx).Values().Set(f.ctx, appvalue.SetInput{
		AttributeDefinitionID: attrID, EntityID: entity,
		TypeDefinitionID: f.dish, Value: raw,
	})
	return err
}

func (f *enforcementFixture) setBatch(items ...appvalue.SetInput) error {
	for i := range items {
		items[i].TypeDefinitionID = f.dish
	}
	_, err := f.svc.Interactors(f.ctx).Values().SetBatch(f.ctx, appvalue.BatchSetInput{Items: items})
	return err
}

func (f *enforcementFixture) item(attrID, entity string, v any) appvalue.SetInput {
	raw, _ := json.Marshal(v)
	return appvalue.SetInput{AttributeDefinitionID: attrID, EntityID: entity, Value: raw}
}

// TestRequiredEnforcementOnWrite covers the mode that refuses a write.
//
// The rule "a dish that declares it contains allergens must list them" was
// only ever reported: setting the flag alone was accepted, and something else
// had to notice. on_write turns the same rule into a refusal.
func TestRequiredEnforcementOnWrite(t *testing.T) {
	Convey("Given a rule that BLOCKS a dish with undeclared allergens", t, func() {
		svc := flexitype.NewInMemory()
		f := newEnforcementFixture(t, svc, "on_write")

		Convey("When the flag is set on its own", func() {
			err := f.set(f.flag, "tart", true)

			Convey("Then the write is refused, naming the attribute", func() {
				So(err, ShouldNotBeNil)
				So(domainerrors.IsDependencyViolation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "allergens")
			})

			Convey("Then nothing was stored", func() {
				// The check runs inside the transaction, so a refusal takes
				// the flag with it. A dish left holding the flag and no
				// allergens is the state the rule exists to prevent.
				values, lerr := f.svc.Interactors(f.ctx).Values().ListByEntity(f.ctx, f.dish, "tart")
				So(lerr, ShouldBeNil)
				So(values, ShouldBeEmpty)
			})
		})

		Convey("When the flag and the allergens are written in one batch", func() {
			err := f.setBatch(
				f.item(f.flag, "tart", true),
				f.item(f.allergens, "tart", "gluten"),
			)

			Convey("Then it is accepted", func() {
				So(err, ShouldBeNil)
			})
		})

		Convey("When the batch writes them in the WRONG order", func() {
			err := f.setBatch(
				f.item(f.allergens, "tart", "gluten"),
				f.item(f.flag, "tart", true),
			)

			Convey("Then it is still accepted", func() {
				// The check runs at the end of the transaction, not on each
				// value. Judging each write as it lands would refuse an import
				// row whose finished state is complete, because the cell that
				// makes the rule apply usually arrives first.
				So(err, ShouldBeNil)
			})
		})

		Convey("When the flag is set on a dish that already lists allergens", func() {
			So(f.set(f.allergens, "tart", "gluten"), ShouldBeNil)
			err := f.set(f.flag, "tart", true)

			Convey("Then it is accepted", func() {
				So(err, ShouldBeNil)
			})
		})

		Convey("When the allergens are removed from a dish that declares them", func() {
			So(f.setBatch(
				f.item(f.flag, "tart", true),
				f.item(f.allergens, "tart", "gluten"),
			), ShouldBeNil)

			values, err := f.svc.Interactors(f.ctx).Values().ListByEntity(f.ctx, f.dish, "tart")
			So(err, ShouldBeNil)
			var allergenValueID string
			for _, v := range values {
				if v.AttributeDefinitionID.String() == f.allergens {
					allergenValueID = v.ID.String()
				}
			}
			So(allergenValueID, ShouldNotBeEmpty)
			_, rerr := f.svc.Interactors(f.ctx).Values().Remove(f.ctx, allergenValueID)

			Convey("Then the removal is refused too", func() {
				// Taking the value away leaves exactly the state the write was
				// refused for. Guarding only writes would make the rule
				// trivial to step around.
				So(rerr, ShouldNotBeNil)
				So(domainerrors.IsDependencyViolation(rerr), ShouldBeTrue)
			})
		})

		Convey("When an unrelated attribute is written to an entity that has no flag", func() {
			err := f.set(f.name, "plain", "Bread")

			Convey("Then nothing is required and the write goes through", func() {
				So(err, ShouldBeNil)
			})
		})

		Convey("When the whole dish is deleted", func() {
			So(f.setBatch(
				f.item(f.flag, "tart", true),
				f.item(f.allergens, "tart", "gluten"),
			), ShouldBeNil)
			_, err := f.svc.Interactors(f.ctx).Values().RemoveEntity(f.ctx, f.dish, "tart")

			Convey("Then it is allowed, because an empty entity is gone not incomplete", func() {
				// Refusing here would make a dish with a matched requirement
				// impossible to delete.
				So(err, ShouldBeNil)
			})
		})

		Convey("When the effective schema is read", func() {
			So(f.set(f.allergens, "tart", "gluten"), ShouldBeNil)
			So(f.set(f.flag, "tart", true), ShouldBeNil)
			eff, err := f.svc.Interactors(f.ctx).Dependencies().EffectiveSchema(f.ctx, f.allergens, "tart")
			So(err, ShouldBeNil)

			Convey("Then it says the requirement blocks writes", func() {
				// A UI has to tell a rule from a wall before it lets someone
				// save half a record.
				So(eff.Required, ShouldBeTrue)
				So(eff.RequiredEnforcement, ShouldEqual, "on_write")
			})
		})
	})
}

// TestRequiredEnforcementOnRead covers the default, which is what every
// dependency did before the mode existed.
func TestRequiredEnforcementOnRead(t *testing.T) {
	Convey("Given a rule that only REPORTS the same requirement", t, func() {
		svc := flexitype.NewInMemory()
		f := newEnforcementFixture(t, svc, "on_read")

		Convey("When the flag is set on its own", func() {
			err := f.set(f.flag, "tart", true)

			Convey("Then the write is accepted", func() {
				So(err, ShouldBeNil)
			})

			Convey("Then completeness reports the gap", func() {
				// The requirement did not disappear; it moved to where the
				// host decides — at publish, at checkout, at submit.
				comp, cerr := f.svc.Interactors(f.ctx).Dependencies().Completeness(f.ctx, f.dish, "tart")
				So(cerr, ShouldBeNil)
				So(comp.Missing, ShouldHaveLength, 1)
				So(comp.Missing[0].InternalName, ShouldEqual, "allergens")
			})

			Convey("Then the effective schema says so", func() {
				eff, eerr := f.svc.Interactors(f.ctx).Dependencies().EffectiveSchema(f.ctx, f.allergens, "tart")
				So(eerr, ShouldBeNil)
				So(eff.Required, ShouldBeTrue)
				So(eff.RequiredEnforcement, ShouldEqual, "on_read")
			})
		})
	})

	Convey("Given a rule that does not declare a mode at all", t, func() {
		svc := flexitype.NewInMemory()
		f := newEnforcementFixture(t, svc, "")

		Convey("When the flag is set on its own", func() {
			err := f.set(f.flag, "tart", true)

			Convey("Then it behaves exactly as it did before the mode existed", func() {
				// Every rule stored before this feature has no mode. Reading an
				// absent mode as on_write would have made all of them start
				// refusing writes on upgrade.
				So(err, ShouldBeNil)
			})
		})
	})
}

// TestRequiredEnforcementDoesNotBlockCreation pins the line between an
// attribute's own required flag and a dependency's demand.
func TestRequiredEnforcementDoesNotBlockCreation(t *testing.T) {
	Convey("Given a type with a declared-required attribute", t, func() {
		svc := flexitype.NewInMemory()
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		ia := svc.Interactors(ctx)

		dish, err := ia.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "dish", DisplayName: "Dish",
		})
		So(err, ShouldBeNil)
		sku, err := ia.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: dish.ID.String(), InternalName: "sku", DisplayName: "SKU",
			DataType: "string", Required: true,
		})
		So(err, ShouldBeNil)
		name, err := ia.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: dish.ID.String(), InternalName: "name", DisplayName: "Name",
			DataType: "string",
		})
		So(err, ShouldBeNil)
		// A rule that blocks writes, targeting something else entirely.
		_, err = ia.Dependencies().Create(ctx, appdependency.CreateInput{
			SourceAttributeID: name.ID.String(),
			TargetAttributeID: sku.ID.String(),
			Conditions:        json.RawMessage(`[{"kind":"equals","value":{"type":"string","value":"special"}}]`),
			Effect:            json.RawMessage(`{"required":true,"enforce":"on_write"}`),
		})
		So(err, ShouldBeNil)

		Convey("When the first value of a new entity is written", func() {
			raw, _ := json.Marshal("Bread")
			_, werr := ia.Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: name.ID.String(), EntityID: "loaf",
				TypeDefinitionID: dish.ID.String(), Value: raw,
			})

			Convey("Then the declared-required attribute does not block it", func() {
				// Gating on a declared required flag would make every entity
				// impossible to create: the first value written always leaves
				// the others empty. Only a MATCHED dependency is gated.
				So(werr, ShouldBeNil)
			})
		})
	})
}

// TestRequiredEnforcementPostgres runs the blocking mode against the real
// backend.
//
// The in-memory service and Postgres have disagreed before — a computed
// rollup converged in memory because a nested recompute dispatched
// synchronously, and only Postgres showed the stale read. A rule that refuses
// a write has to be proven where the transaction is real.
func TestRequiredEnforcementPostgres(t *testing.T) {
	pool := openTestDB(t)
	svc := flexitype.New(pool)
	if err := svc.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	Convey("Given a blocking rule (Postgres)", t, func() {
		truncateAll(t, pool)
		f := newEnforcementFixture(t, svc, "on_write")

		Convey("When the flag is set on its own", func() {
			err := f.set(f.flag, "tart", true)

			Convey("Then the write is refused and the transaction rolls back", func() {
				So(err, ShouldNotBeNil)
				So(domainerrors.IsDependencyViolation(err), ShouldBeTrue)
				values, lerr := f.svc.Interactors(f.ctx).Values().ListByEntity(f.ctx, f.dish, "tart")
				So(lerr, ShouldBeNil)
				So(values, ShouldBeEmpty)
			})
		})

		Convey("When one batch supplies both values", func() {
			err := f.setBatch(
				f.item(f.allergens, "tart", "gluten"),
				f.item(f.flag, "tart", true),
			)

			Convey("Then it commits", func() {
				So(err, ShouldBeNil)
				values, lerr := f.svc.Interactors(f.ctx).Values().ListByEntity(f.ctx, f.dish, "tart")
				So(lerr, ShouldBeNil)
				So(values, ShouldHaveLength, 2)
			})
		})
	})
}

// TestRequiredEnforcementDuringImport is the case the deferred check exists
// for.
//
// A CSV row is written cell by cell. The cell that makes a rule apply —
// contains_allergens — normally lands before the cell that satisfies it, so a
// check that judged each write as it happened would refuse a row whose
// finished state is complete, and the file's column order would decide
// whether an import worked.
func TestRequiredEnforcementDuringImport(t *testing.T) {
	Convey("Given a blocking rule and a supplier CSV", t, func() {
		svc := flexitype.NewInMemory()
		f := newEnforcementFixture(t, svc, "on_write")

		imp := func(rows [][]string) *appvalue.ImportReport {
			report, err := f.svc.Interactors(f.ctx).Values().Import(f.ctx, appvalue.ImportInput{
				TypeDefinitionID: f.dish,
				KeyColumn:        "id",
				Mapping: map[string]string{
					"contains_allergens": "contains_allergens",
					"allergens":          "allergens",
					"name":               "name",
				},
				// The flag comes FIRST, which is the order that would break a
				// per-cell check.
				Columns: []string{"id", "contains_allergens", "allergens", "name"},
				Rows:    rows,
			})
			So(err, ShouldBeNil)
			return report
		}

		Convey("When a row declares allergens and lists them", func() {
			report := imp([][]string{{"tart", "true", "gluten", "Tart"}})

			Convey("Then the row is written", func() {
				So(report.Errors, ShouldBeEmpty)
				So(report.RowsWritten, ShouldEqual, 1)
			})
		})

		Convey("When a row declares allergens and lists none", func() {
			report := imp([][]string{{"tart", "true", "", "Tart"}})

			Convey("Then that row is refused", func() {
				So(report.Errors, ShouldNotBeEmpty)
				So(report.RowsWritten, ShouldEqual, 0)
			})

			Convey("Then nothing from it was stored", func() {
				values, err := f.svc.Interactors(f.ctx).Values().ListByEntity(f.ctx, f.dish, "tart")
				So(err, ShouldBeNil)
				So(values, ShouldBeEmpty)
			})
		})

		Convey("When one row of several is incomplete", func() {
			report := imp([][]string{
				{"good", "true", "gluten", "Good"},
				{"bad", "true", "", "Bad"},
				{"plain", "false", "", "Plain"},
			})

			Convey("Then the good rows are written and only the bad one is reported", func() {
				// Best effort is per row: one row's refusal must not cost the
				// others, and the rule must not be satisfied by a neighbour.
				So(report.RowsWritten, ShouldEqual, 2)
				So(report.Errors, ShouldHaveLength, 1)

				good, err := f.svc.Interactors(f.ctx).Values().ListByEntity(f.ctx, f.dish, "good")
				So(err, ShouldBeNil)
				So(good, ShouldNotBeEmpty)
				bad, err := f.svc.Interactors(f.ctx).Values().ListByEntity(f.ctx, f.dish, "bad")
				So(err, ShouldBeNil)
				So(bad, ShouldBeEmpty)
			})
		})
	})
}

// TestRequiredEnforcementAcrossWritePaths pins the gate on every path that
// changes an entity, and OFF the one that must never be blocked.
//
// The check hangs off the value write path, and several features write values
// without going through Set or Remove — a change set publishing mutations, a
// snapshot restore, an erasure purge. A rule that a change set can step around
// is not a rule.
func TestRequiredEnforcementAcrossWritePaths(t *testing.T) {
	Convey("Given a dish that declares allergens, under a blocking rule", t, func() {
		svc := flexitype.NewInMemory()
		f := newEnforcementFixture(t, svc, "on_write")
		So(f.setBatch(
			f.item(f.flag, "tart", true),
			f.item(f.allergens, "tart", "gluten"),
		), ShouldBeNil)

		ia := f.svc.Interactors(f.ctx)

		Convey("When a change set publishes a removal of the allergens", func() {
			cs, err := ia.ChangeSets().Create(f.ctx, appchangeset.CreateInput{Name: "drop allergens"})
			So(err, ShouldBeNil)
			_, err = ia.ChangeSets().AddMutation(f.ctx, cs.ID.String(), appvalue.Mutation{
				Kind:                  appvalue.MutationRemove,
				AttributeDefinitionID: f.allergens,
				EntityID:              "tart",
				TypeDefinitionID:      f.dish,
			})
			So(err, ShouldBeNil)
			_, err = ia.ChangeSets().Submit(f.ctx, cs.ID.String())
			So(err, ShouldBeNil)
			_, err = ia.ChangeSets().Approve(f.ctx, cs.ID.String())
			So(err, ShouldBeNil)
			_, perr := ia.ChangeSets().Publish(f.ctx, cs.ID.String())

			Convey("Then publishing is refused, and the value survives", func() {
				// A change set is the obvious way around a write rule: stage
				// the removal, approve it, let the scheduler apply it. It goes
				// through the same gate.
				So(perr, ShouldNotBeNil)
				So(domainerrors.IsDependencyViolation(perr), ShouldBeTrue)

				values, lerr := ia.Values().ListByEntity(f.ctx, f.dish, "tart")
				So(lerr, ShouldBeNil)
				So(values, ShouldHaveLength, 2)
			})
		})

		Convey("When a change set publishes a complete pair of mutations", func() {
			cs, err := ia.ChangeSets().Create(f.ctx, appchangeset.CreateInput{Name: "new dish"})
			So(err, ShouldBeNil)
			for _, m := range []appvalue.Mutation{
				{
					Kind: appvalue.MutationSet, AttributeDefinitionID: f.flag,
					EntityID: "cake", TypeDefinitionID: f.dish, Value: json.RawMessage(`true`),
				},
				{
					Kind: appvalue.MutationSet, AttributeDefinitionID: f.allergens,
					EntityID: "cake", TypeDefinitionID: f.dish, Value: json.RawMessage(`"egg"`),
				},
			} {
				_, aerr := ia.ChangeSets().AddMutation(f.ctx, cs.ID.String(), m)
				So(aerr, ShouldBeNil)
			}
			_, err = ia.ChangeSets().Submit(f.ctx, cs.ID.String())
			So(err, ShouldBeNil)
			_, err = ia.ChangeSets().Approve(f.ctx, cs.ID.String())
			So(err, ShouldBeNil)
			_, perr := ia.ChangeSets().Publish(f.ctx, cs.ID.String())

			Convey("Then it publishes, because the set is judged as a whole", func() {
				So(perr, ShouldBeNil)
				values, lerr := ia.Values().ListByEntity(f.ctx, f.dish, "cake")
				So(lerr, ShouldBeNil)
				So(values, ShouldHaveLength, 2)
			})
		})

		Convey("When the dish is erased", func() {
			_, err := ia.Erasure().PurgeEntity(f.ctx, f.dish, "tart")

			Convey("Then the purge succeeds", func() {
				// Erasure answers an obligation that outranks a schema rule.
				// A tenant that cannot delete a record because a dependency
				// says it is incomplete has a compliance problem the schema
				// created, so this path is deliberately NOT gated.
				So(err, ShouldBeNil)
				values, lerr := ia.Values().ListByEntity(f.ctx, f.dish, "tart")
				So(lerr, ShouldBeNil)
				So(values, ShouldBeEmpty)
			})
		})
	})
}

// TestRequiredEnforcementImportScope covers the two ways the end-of-write
// check and the import machinery disagreed about what "the write" is.
func TestRequiredEnforcementImportScope(t *testing.T) {
	Convey("Given a blocking rule and an import", t, func() {
		svc := flexitype.NewInMemory()
		f := newEnforcementFixture(t, svc, "on_write")

		run := func(rows [][]string, dry bool) *appvalue.ImportReport {
			report, err := f.svc.Interactors(f.ctx).Values().Import(f.ctx, appvalue.ImportInput{
				TypeDefinitionID: f.dish,
				KeyColumn:        "id",
				Mapping: map[string]string{
					"contains_allergens": "contains_allergens",
					"allergens":          "allergens",
					"name":               "name",
				},
				Columns: []string{"id", "contains_allergens", "allergens", "name"},
				Rows:    rows,
				DryRun:  dry,
			})
			So(err, ShouldBeNil)
			return report
		}

		Convey("When a row that the import would refuse is PREVIEWED", func() {
			report := run([][]string{{"tart", "true", "", "Tart"}}, true)

			Convey("Then the preview refuses it too", func() {
				// A dry run rolls back deliberately, and the checks used to run
				// only when the usecase succeeded — so the preview called a row
				// valid that the real import rejects. That is the one question
				// a preview exists to answer.
				So(report.Errors, ShouldNotBeEmpty)
				So(report.Errors[0].Reason, ShouldContainSubstring, "allergens")
			})
		})

		Convey("When a complete row is PREVIEWED", func() {
			report := run([][]string{{"tart", "true", "gluten", "Tart"}}, true)

			Convey("Then it is reported valid and nothing is written", func() {
				So(report.Errors, ShouldBeEmpty)
				values, err := f.svc.Interactors(f.ctx).Values().ListByEntity(f.ctx, f.dish, "tart")
				So(err, ShouldBeNil)
				So(values, ShouldBeEmpty)
			})
		})

		Convey("When one entity's two rows straddle a chunk boundary", func() {
			// importChunkSize is 100. Build 102 rows where the pair that only
			// succeeds together sits at 100/101 — the split the old fixed-size
			// chunking made fatal.
			rows := make([][]string, 0, 101)
			for k := 0; k < 99; k++ {
				rows = append(rows, []string{fmt.Sprintf("filler-%03d", k), "false", "", "Filler"})
			}
			rows = append(rows,
				[]string{"tart", "true", "", "Tart"},
				[]string{"tart", "", "gluten", ""},
			)
			report := run(rows, false)

			Convey("Then the entity is judged whole and the import succeeds", func() {
				// The same two rows at 50/51 always worked. Row position is not
				// a property of the data and must not decide the outcome.
				So(report.Errors, ShouldBeEmpty)
				So(report.RowsWritten, ShouldEqual, 101)
			})
		})

		Convey("When a chunk fails and one entity in it spans two rows", func() {
			// The bad dish makes the whole chunk roll back. The retry must keep
			// each entity's rows together: retrying row by row would split the
			// pair that only succeeds as a pair, so a row's fate would depend
			// on an unrelated row elsewhere in the file.
			report := run([][]string{
				{"bad", "true", "", "Bad"},
				{"tart", "true", "", "Tart"},
				{"tart", "", "gluten", ""},
			}, false)

			Convey("Then only the bad entity is reported", func() {
				So(report.RowsWritten, ShouldEqual, 2)
				So(report.Errors, ShouldHaveLength, 1)
				So(report.Errors[0].Row, ShouldEqual, 1)

				good, err := f.svc.Interactors(f.ctx).Values().ListByEntity(f.ctx, f.dish, "tart")
				So(err, ShouldBeNil)
				So(good, ShouldNotBeEmpty)
				bad, err := f.svc.Interactors(f.ctx).Values().ListByEntity(f.ctx, f.dish, "bad")
				So(err, ShouldBeNil)
				So(bad, ShouldBeEmpty)
			})
		})

		Convey("When a transactional import is refused by the check", func() {
			report, err := f.svc.Interactors(f.ctx).Values().Import(f.ctx, appvalue.ImportInput{
				TypeDefinitionID: f.dish,
				KeyColumn:        "id",
				Mapping: map[string]string{
					"contains_allergens": "contains_allergens",
					"allergens":          "allergens",
				},
				Columns: []string{"id", "contains_allergens", "allergens"},
				Rows: [][]string{
					{"plain", "false", ""},
					{"tart", "true", ""},
				},
				Mode: appvalue.ImportTransactional,
			})
			So(err, ShouldBeNil)

			Convey("Then the error points at the offending row, not the header", func() {
				// The check fails after every row has been written, so the
				// usual "which setWithin failed" answer is empty and the report
				// blamed row 0 — the header, which is no data row at all.
				So(report.Errors, ShouldHaveLength, 1)
				So(report.Errors[0].Row, ShouldEqual, 2)
			})
		})
	})
}

// TestRequiredEnforcementAndFieldACL covers the branch that decides what a
// principal with restricted read access sees, which had no test at all.
func TestRequiredEnforcementAndFieldACL(t *testing.T) {
	Convey("Given a blocking rule over an attribute the caller cannot read", t, func() {
		svc := flexitype.NewInMemory()
		f := newEnforcementFixture(t, svc, "on_write")

		restricted := uow.WithAccess(f.ctx, uow.Access{
			Attr:    map[string]uow.Perm{"allergens": uow.PermNone},
			Default: uow.PermWrite,
		})

		Convey("When that caller writes the source on its own", func() {
			raw, _ := json.Marshal(true)
			_, err := f.svc.Interactors(restricted).Values().Set(restricted, appvalue.SetInput{
				AttributeDefinitionID: f.flag, EntityID: "tart",
				TypeDefinitionID: f.dish, Value: raw,
			})

			Convey("Then the rule does not fire for it", func() {
				// Resolving over values the caller cannot see makes the answer
				// an oracle over those values, and refusing on an attribute it
				// can neither read nor write would lock it out of the entity
				// with an error naming nothing. Completeness already leaves
				// such an attribute out; the gate now agrees.
				So(err, ShouldBeNil)
			})
		})

		Convey("When the caller cannot read the rule's SOURCE", func() {
			// The target is readable, so the attribute-skip does not apply and
			// only the value filter decides. Resolving over raw values here
			// leaked one bit of the source per entity, through a refusal on a
			// write to an unrelated attribute.
			blind := uow.WithAccess(f.ctx, uow.Access{
				Attr:    map[string]uow.Perm{"contains_allergens": uow.PermNone},
				Default: uow.PermWrite,
			})
			So(f.set(f.flag, "tart", true), ShouldNotBeNil) // admin cannot either, yet
			So(f.setBatch(
				f.item(f.flag, "tart", true),
				f.item(f.allergens, "tart", "gluten"),
			), ShouldBeNil)

			values, err := f.svc.Interactors(f.ctx).Values().ListByEntity(f.ctx, f.dish, "tart")
			So(err, ShouldBeNil)
			var allergenValueID string
			for _, v := range values {
				if v.AttributeDefinitionID.String() == f.allergens {
					allergenValueID = v.ID.String()
				}
			}
			So(allergenValueID, ShouldNotBeEmpty)
			_, rerr := f.svc.Interactors(blind).Values().Remove(blind, allergenValueID)

			Convey("Then the rule does not fire for that caller", func() {
				// It cannot see the flag, so it must not be refused because of
				// the flag's value — that refusal IS the flag's value.
				So(rerr, ShouldBeNil)
			})
		})

		Convey("When a caller that CAN read it writes the same thing", func() {
			err := f.set(f.flag, "tart", true)

			Convey("Then the rule fires", func() {
				So(err, ShouldNotBeNil)
				So(domainerrors.IsDependencyViolation(err), ShouldBeTrue)
			})
		})
	})
}

// TestRequiredEnforcementRemainingPaths covers the behaviours that had been
// claimed but never guarded: the materializer exemption, applied defaults,
// snapshot restore, and a rule that only relaxes.
func TestRequiredEnforcementRemainingPaths(t *testing.T) {
	Convey("Given a blocking rule", t, func() {
		svc := flexitype.NewInMemory()
		f := newEnforcementFixture(t, svc, "on_write")
		ia := f.svc.Interactors(f.ctx)

		Convey("When a restore lands in a state the rule allows", func() {
			So(f.set(f.name, "tart", "Tart"), ShouldBeNil)
			before, err := ia.Revisions().Create(f.ctx, f.dish, "tart", "before")
			So(err, ShouldBeNil)
			So(f.setBatch(
				f.item(f.flag, "tart", true),
				f.item(f.allergens, "tart", "gluten"),
			), ShouldBeNil)

			_, rerr := ia.Revisions().Restore(f.ctx, before.ID.String())

			Convey("Then it is allowed", func() {
				// Rolling back to before the flag was set removes the demand
				// along with it. The rule asks for allergens only while the
				// dish says it has them.
				So(rerr, ShouldBeNil)
			})
		})

		Convey("When applying defaults to an entity that already violates the rule", func() {
			So(f.setBatch(
				f.item(f.flag, "tart", true),
				f.item(f.allergens, "tart", "gluten"),
			), ShouldBeNil)
			_, err := ia.Values().ApplyDefaults(f.ctx, f.dish, "tart")

			Convey("Then a complete entity is untouched by the rule", func() {
				So(err, ShouldBeNil)
			})
		})

		Convey("When a computed attribute is made required on write", func() {
			computed, err := ia.Attributes().Create(f.ctx, appattribute.CreateInput{
				TypeDefinitionID: f.dish, InternalName: "portion_cost", DisplayName: "Portion cost",
				DataType: "decimal",
				Computed: json.RawMessage(`{"kind":"formula","formula":"1 + 1"}`),
			})
			So(err, ShouldBeNil)
			_, derr := ia.Dependencies().Create(f.ctx, appdependency.CreateInput{
				SourceAttributeID: f.flag,
				TargetAttributeID: computed.ID.String(),
				Conditions:        json.RawMessage(`[{"kind":"equals","value":{"type":"bool","value":true}}]`),
				Effect:            json.RawMessage(`{"required":true,"enforce":"on_write"}`),
			})

			Convey("Then the rule is refused where it is authored", func() {
				// A computed value is materialized after the transaction
				// commits, so the demand can never be met while the write is
				// judged. Such a rule would refuse an entity that is complete
				// the instant it commits, and no caller ordering could help.
				So(derr, ShouldNotBeNil)
				So(derr.Error(), ShouldContainSubstring, "materialized after")
			})
		})
	})

	Convey("Given data that predates a blocking rule", t, func() {
		svc := flexitype.NewInMemory()
		f := newEnforcementFixture(t, svc, "on_read")
		ia := f.svc.Interactors(f.ctx)

		// Legal while the rule only reported.
		So(f.set(f.flag, "tart", true), ShouldBeNil)
		before, err := ia.Revisions().Create(f.ctx, f.dish, "tart", "before")
		So(err, ShouldBeNil)
		So(f.set(f.allergens, "tart", "gluten"), ShouldBeNil)

		// The schema author now switches the rule to blocking.
		_, uerr := ia.Dependencies().Update(f.ctx, appdependency.UpdateInput{
			ID:         f.dependency,
			Conditions: json.RawMessage(`[{"kind":"equals","value":{"type":"bool","value":true}}]`),
			Effect:     json.RawMessage(`{"required":true,"enforce":"on_write"}`),
		})
		So(uerr, ShouldBeNil)

		Convey("When a restore would roll the entity back into the forbidden state", func() {
			_, rerr := ia.Revisions().Restore(f.ctx, before.ID.String())

			Convey("Then the restore is refused", func() {
				// A restore is a write like any other. Rolling an entity into
				// a state a direct write is refused for would be the way
				// around the rule.
				So(rerr, ShouldNotBeNil)
				So(domainerrors.IsDependencyViolation(rerr), ShouldBeTrue)
			})
		})

		Convey("When an unrelated attribute of that entity is written", func() {
			werr := f.set(f.name, "tart", "Tarte")

			Convey("Then it is allowed, because the entity is complete", func() {
				So(werr, ShouldBeNil)
			})
		})
	})

	Convey("Given a rule that RELAXES a requirement", t, func() {
		svc := flexitype.NewInMemory()
		f := newEnforcementFixture(t, svc, "on_write")

		Convey("When it is authored with an enforcement mode", func() {
			_, err := f.svc.Interactors(f.ctx).Dependencies().Create(f.ctx, appdependency.CreateInput{
				SourceAttributeID: f.name,
				TargetAttributeID: f.allergens,
				Conditions:        json.RawMessage(`[{"kind":"equals","value":{"type":"string","value":"Plain"}}]`),
				Effect:            json.RawMessage(`{"required":false,"enforce":"on_write"}`),
			})

			Convey("Then it is refused", func() {
				// Accepting it let a relaxing rule contribute on_write to a
				// target another rule demanded, so a pair of rules blocked
				// writes although neither asked to.
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "requires a value")
			})
		})
	})
}
