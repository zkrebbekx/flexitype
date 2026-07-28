package memory_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	appchangeset "github.com/zkrebbekx/flexitype/application/changeset"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/infrastructure/memory"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

func TestChangeSets(t *testing.T) {
	Convey("Given a product type with name and price", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory()
		it := svc.Interactors(ctx)

		product, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{InternalName: "product", DisplayName: "Product"})
		So(err, ShouldBeNil)
		typeID := product.ID.String()
		name, err := it.Attributes().Create(ctx, appattribute.CreateInput{TypeDefinitionID: typeID, InternalName: "name", DisplayName: "Name", DataType: "string"})
		So(err, ShouldBeNil)
		price, err := it.Attributes().Create(ctx, appattribute.CreateInput{TypeDefinitionID: typeID, InternalName: "price", DisplayName: "Price", DataType: "integer"})
		So(err, ShouldBeNil)

		setMut := func(attr, entity, v string) appvalue.Mutation {
			return appvalue.Mutation{Kind: appvalue.MutationSet, AttributeDefinitionID: attr, EntityID: entity, TypeDefinitionID: typeID, Value: json.RawMessage(v)}
		}

		Convey("When five edits across two entities are drafted", func() {
			cs, err := it.ChangeSets().Create(ctx, appchangeset.CreateInput{Name: "spring update"})
			So(err, ShouldBeNil)
			id := cs.ID.String()
			for _, m := range []appvalue.Mutation{
				setMut(name.ID.String(), "e1", `"A"`),
				setMut(price.ID.String(), "e1", `100`),
				setMut(name.ID.String(), "e2", `"B"`),
				setMut(price.ID.String(), "e2", `200`),
				setMut(name.ID.String(), "e1", `"A2"`),
			} {
				_, e := it.ChangeSets().AddMutation(ctx, id, m)
				So(e, ShouldBeNil)
			}

			Convey("Then live reads are unchanged but preview shows the overlay", func() {
				live, err := it.Values().ListByEntity(ctx, typeID, "e1")
				So(err, ShouldBeNil)
				So(live, ShouldBeEmpty)

				got, err := it.ChangeSets().Get(ctx, id)
				So(err, ShouldBeNil)
				preview, err := it.Values().Preview(ctx, typeID, "e1", got.Mutations)
				So(err, ShouldBeNil)
				byName := map[string]string{}
				for _, v := range preview {
					if v.AttributeDefinitionID.String() == name.ID.String() {
						byName["name"] = v.Value.String()
					}
					if v.AttributeDefinitionID.String() == price.ID.String() {
						byName["price"] = v.Value.String()
					}
				}
				So(byName["name"], ShouldEqual, "A2") // last set wins
				So(byName["price"], ShouldEqual, "100")
			})

			Convey("Then publishing applies every mutation atomically", func() {
				_, err := it.ChangeSets().Submit(ctx, id)
				So(err, ShouldBeNil)
				_, err = it.ChangeSets().Approve(ctx, id)
				So(err, ShouldBeNil)
				published, err := it.ChangeSets().Publish(ctx, id)
				So(err, ShouldBeNil)
				So(published.State, ShouldEqual, appchangeset.StatePublished)

				e1, _ := it.Values().ListByEntity(ctx, typeID, "e1")
				So(e1, ShouldHaveLength, 2)
				e2, _ := it.Values().ListByEntity(ctx, typeID, "e2")
				So(e2, ShouldHaveLength, 2)
			})
		})

		Convey("When approval is required", func() {
			alice := uow.WithActor(ctx, uow.Actor{ID: "alice"})
			bob := uow.WithActor(ctx, uow.Actor{ID: "bob"})
			cs, err := svc.Interactors(alice).ChangeSets().Create(alice, appchangeset.CreateInput{Name: "gated", RequireApproval: true})
			So(err, ShouldBeNil)
			id := cs.ID.String()
			_, err = svc.Interactors(alice).ChangeSets().AddMutation(alice, id, setMut(name.ID.String(), "e9", `"Z"`))
			So(err, ShouldBeNil)
			_, err = svc.Interactors(alice).ChangeSets().Submit(alice, id)
			So(err, ShouldBeNil)

			Convey("Then the author cannot approve, but a second account can", func() {
				_, err := svc.Interactors(alice).ChangeSets().Approve(alice, id)
				So(err, ShouldNotBeNil)
				So(domainerrors.CodeOf(err), ShouldEqual, domainerrors.CodeForbidden)

				_, err = svc.Interactors(bob).ChangeSets().Approve(bob, id)
				So(err, ShouldBeNil)
			})

			Convey("Then an unidentified (empty) actor cannot approve", func() {
				_, err := svc.Interactors(ctx).ChangeSets().Approve(ctx, id)
				So(err, ShouldNotBeNil)
				So(domainerrors.CodeOf(err), ShouldEqual, domainerrors.CodeForbidden)
			})
		})

		Convey("When a publish hits a constraint failure", func() {
			cs, err := it.ChangeSets().Create(ctx, appchangeset.CreateInput{Name: "bad batch"})
			So(err, ShouldBeNil)
			id := cs.ID.String()
			_, err = it.ChangeSets().AddMutation(ctx, id, setMut(name.ID.String(), "e3", `"ok"`))
			So(err, ShouldBeNil)
			_, err = it.ChangeSets().AddMutation(ctx, id, setMut(price.ID.String(), "e3", `"not-a-number"`))
			So(err, ShouldBeNil)
			_, _ = it.ChangeSets().Submit(ctx, id)
			_, _ = it.ChangeSets().Approve(ctx, id)

			Convey("Then nothing is applied (all-or-nothing)", func() {
				_, err := it.ChangeSets().Publish(ctx, id)
				So(err, ShouldNotBeNil)
				e3, _ := it.Values().ListByEntity(ctx, typeID, "e3")
				So(e3, ShouldBeEmpty) // the valid mutation rolled back too
			})
		})

		Convey("When an approved change-set is scheduled for the past", func() {
			past := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
			cs, err := it.ChangeSets().Create(ctx, appchangeset.CreateInput{Name: "scheduled", PublishAt: &past})
			So(err, ShouldBeNil)
			id := cs.ID.String()
			_, err = it.ChangeSets().AddMutation(ctx, id, setMut(name.ID.String(), "e5", `"scheduled"`))
			So(err, ShouldBeNil)
			_, _ = it.ChangeSets().Submit(ctx, id)
			_, _ = it.ChangeSets().Approve(ctx, id)

			Convey("Then the scheduler publishes it and it applies to live data", func() {
				n, err := it.ChangeSets().PublishDue(ctx)
				So(err, ShouldBeNil)
				So(n, ShouldBeGreaterThanOrEqualTo, 1)
				e5, _ := it.Values().ListByEntity(ctx, typeID, "e5")
				So(e5, ShouldHaveLength, 1)
			})
		})

		Convey("When a change-set is rejected", func() {
			cs, err := it.ChangeSets().Create(ctx, appchangeset.CreateInput{Name: "scrap"})
			So(err, ShouldBeNil)
			id := cs.ID.String()
			_, err = it.ChangeSets().AddMutation(ctx, id, setMut(name.ID.String(), "e4", `"never"`))
			So(err, ShouldBeNil)
			_, err = it.ChangeSets().Reject(ctx, id)
			So(err, ShouldBeNil)

			Convey("Then it leaves zero trace on live data", func() {
				e4, _ := it.Values().ListByEntity(ctx, typeID, "e4")
				So(e4, ShouldBeEmpty)
			})
		})
	})
}

// TestChangeSetStoreDirect pins the store port's own contract — creation
// instants are supplied explicitly so the documented newest-first List order can
// be asserted without depending on wall-clock resolution.
func TestChangeSetStoreDirect(t *testing.T) {
	Convey("Given change-sets created at distinct instants across two tenants", t, func() {
		ctx := context.Background()
		store := memory.NewChangeSetStore()

		t0 := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
		mk := func(id string, tenant valueobjects.TenantID, name string, state appchangeset.State, created time.Time, publishAt *time.Time) appchangeset.ChangeSet {
			return appchangeset.ChangeSet{
				ID: ulid.MustParse(id), TenantID: tenant, Name: name, State: state,
				PublishAt: publishAt, CreatedAt: created, UpdatedAt: created,
			}
		}
		due := t0.Add(time.Hour)
		later := t0.Add(10 * time.Hour)

		oldest := mk(ulidAt('1'), tenantA, "oldest", appchangeset.StateDraft, t0, nil)
		middle := mk(ulidAt('2'), tenantA, "middle", appchangeset.StateApproved, t0.Add(time.Minute), &due)
		newest := mk(ulidAt('3'), tenantA, "newest", appchangeset.StateApproved, t0.Add(2*time.Minute), &later)
		foreign := mk(ulidAt('4'), tenantB, "foreign", appchangeset.StateApproved, t0.Add(3*time.Minute), &due)

		for _, cs := range []appchangeset.ChangeSet{oldest, middle, newest, foreign} {
			So(store.Create(ctx, cs), ShouldBeNil)
		}

		names := func(sets []appchangeset.ChangeSet) []string {
			out := make([]string, 0, len(sets))
			for _, cs := range sets {
				out = append(out, cs.Name)
			}
			return out
		}

		Convey("When a tenant lists its change-sets", func() {
			got, err := store.List(ctx, tenantA)
			So(err, ShouldBeNil)

			Convey("Then they come back newest-first and the other tenant's set is invisible", func() {
				So(names(got), ShouldResemble, []string{"newest", "middle", "oldest"})
			})
		})

		Convey("When a tenant with no change-sets lists them", func() {
			got, err := store.List(ctx, valueobjects.TenantID("empty"))
			So(err, ShouldBeNil)

			Convey("Then an empty slice is returned rather than nil", func() {
				So(got, ShouldNotBeNil)
				So(got, ShouldBeEmpty)
			})
		})

		Convey("When a change-set is fetched by id under the wrong tenant", func() {
			_, err := store.Get(ctx, tenantA, foreign.ID)

			Convey("Then it is not found — knowing the id is not enough", func() {
				So(domainerrors.IsNotFound(err), ShouldBeTrue)
			})
		})

		Convey("When the scheduler asks which sets are due", func() {
			got, err := store.DueForPublish(ctx, due)
			So(err, ShouldBeNil)

			Convey("Then only approved sets whose publish_at has arrived qualify, across all tenants", func() {
				So(names(got), ShouldHaveLength, 2)
				So(names(got), ShouldContain, "middle")
				So(names(got), ShouldContain, "foreign")
				So(names(got), ShouldNotContain, "newest") // publish_at still in the future
				So(names(got), ShouldNotContain, "oldest") // draft, and no publish_at
			})
		})

		Convey("When a change-set is updated", func() {
			// Update is a compare-and-swap on version, so write from a fresh
			// read rather than from the literal built above.
			fresh, ferr := store.Get(ctx, tenantA, middle.ID)
			So(ferr, ShouldBeNil)
			fresh.State = appchangeset.StatePublished
			So(store.Update(ctx, fresh), ShouldBeNil)

			Convey("Then the new state is read back and it no longer counts as due", func() {
				got, err := store.Get(ctx, tenantA, middle.ID)
				So(err, ShouldBeNil)
				So(got.State, ShouldEqual, appchangeset.StatePublished)

				dueNow, err := store.DueForPublish(ctx, due)
				So(err, ShouldBeNil)
				So(names(dueNow), ShouldResemble, []string{"foreign"})
			})
		})
	})
}

// TestChangeSetFieldACL covers the surface the ACL-completeness sweep never
// enumerated.
//
// A mutation embeds the value verbatim, so a principal with `salary: none`
// read the salary from another user's staged pay review — while the same
// value was filtered from every other surface. Staging a write was unchecked
// too, so a principal barred from an attribute could stage a change to it and
// have an approver publish it.
func TestChangeSetFieldACL(t *testing.T) {
	Convey("Given a staged change to a restricted attribute", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory()
		it := svc.Interactors(ctx)

		emp, err := it.TypeDefinitions().Create(ctx,
			apptypedef.CreateInput{InternalName: "employee", DisplayName: "Employee"})
		So(err, ShouldBeNil)
		mk := func(name string) string {
			a, e := it.Attributes().Create(ctx, appattribute.CreateInput{
				TypeDefinitionID: emp.ID.String(), InternalName: name,
				DisplayName: name, DataType: "integer",
			})
			So(e, ShouldBeNil)
			return a.ID.String()
		}
		salary := mk("salary")
		grade := mk("grade")

		cs, err := it.ChangeSets().Create(ctx, appchangeset.CreateInput{Name: "pay review"})
		So(err, ShouldBeNil)
		_, err = it.ChangeSets().AddMutation(ctx, cs.ID.String(), appvalue.Mutation{
			Kind: appvalue.MutationSet, AttributeDefinitionID: salary,
			EntityID: "emp-1", TypeDefinitionID: emp.ID.String(),
			Value: json.RawMessage(`244000`),
		})
		So(err, ShouldBeNil)
		_, err = it.ChangeSets().AddMutation(ctx, cs.ID.String(), appvalue.Mutation{
			Kind: appvalue.MutationSet, AttributeDefinitionID: grade,
			EntityID: "emp-1", TypeDefinitionID: emp.ID.String(),
			Value: json.RawMessage(`7`),
		})
		So(err, ShouldBeNil)

		restricted := uow.WithAccess(ctx, uow.Access{
			Attr: map[string]uow.Perm{"salary": uow.PermNone},
		})
		rit := svc.Interactors(restricted)

		Convey("When a restricted principal reads the set", func() {
			got, err := rit.ChangeSets().Get(restricted, cs.ID.String())

			Convey("Then the restricted value is masked and the readable one is not", func() {
				So(err, ShouldBeNil)
				So(got.Mutations, ShouldHaveLength, 2)
				So(got.Mutations[0].Value, ShouldBeNil)
				So(got.Mutations[0].Redacted, ShouldBeTrue)
				So(string(got.Mutations[1].Value), ShouldEqual, "7")
				So(got.Mutations[1].Redacted, ShouldBeFalse)
			})

			Convey("Then the skeleton survives, so the change is still visible as a change", func() {
				So(got.Mutations[0].AttributeDefinitionID, ShouldEqual, salary)
				So(got.Mutations[0].EntityID, ShouldEqual, "emp-1")
			})
		})

		Convey("When a restricted principal lists sets", func() {
			sets, err := rit.ChangeSets().List(restricted)

			Convey("Then the same masking applies", func() {
				So(err, ShouldBeNil)
				So(sets, ShouldHaveLength, 1)
				So(sets[0].Mutations[0].Value, ShouldBeNil)
			})
		})

		Convey("When an unrestricted principal reads the set", func() {
			got, err := it.ChangeSets().Get(ctx, cs.ID.String())

			Convey("Then nothing is masked", func() {
				So(err, ShouldBeNil)
				So(string(got.Mutations[0].Value), ShouldEqual, "244000")
			})
		})

		Convey("When a restricted principal stages a change to that attribute", func() {
			draft, err := rit.ChangeSets().Create(restricted, appchangeset.CreateInput{Name: "sneaky"})
			So(err, ShouldBeNil)
			_, err = rit.ChangeSets().AddMutation(restricted, draft.ID.String(), appvalue.Mutation{
				Kind: appvalue.MutationSet, AttributeDefinitionID: salary,
				EntityID: "emp-1", TypeDefinitionID: emp.ID.String(),
				Value: json.RawMessage(`999999`),
			})

			Convey("Then it is refused: staging a write is writing, only later", func() {
				So(err, ShouldNotBeNil)
				So(domainerrors.CodeOf(err), ShouldEqual, domainerrors.CodeForbidden)
			})
		})

		Convey("When a restricted principal stages a change it may write", func() {
			draft, err := rit.ChangeSets().Create(restricted, appchangeset.CreateInput{Name: "grade bump"})
			So(err, ShouldBeNil)
			_, err = rit.ChangeSets().AddMutation(restricted, draft.ID.String(), appvalue.Mutation{
				Kind: appvalue.MutationSet, AttributeDefinitionID: grade,
				EntityID: "emp-1", TypeDefinitionID: emp.ID.String(),
				Value: json.RawMessage(`8`),
			})

			Convey("Then it is accepted", func() {
				So(err, ShouldBeNil)
			})
		})
	})
}

// TestChangeSetErasureResidue covers the copy an erasure left behind.
//
// flexitype_changeset.mutations embeds the value verbatim, and a draft or
// rejected set is never pruned — so a purged value stayed readable there
// indefinitely while the report said the erasure had succeeded.
func TestChangeSetErasureResidue(t *testing.T) {
	Convey("Given a purged entity whose value is staged in a draft change-set", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory()
		it := svc.Interactors(ctx)

		person, err := it.TypeDefinitions().Create(ctx,
			apptypedef.CreateInput{InternalName: "person", DisplayName: "Person"})
		So(err, ShouldBeNil)
		email, err := it.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: person.ID.String(), InternalName: "email",
			DisplayName: "Email", DataType: "string",
		})
		So(err, ShouldBeNil)
		_, err = it.Values().Set(ctx, appvalue.SetInput{
			AttributeDefinitionID: email.ID.String(), EntityID: "p1",
			TypeDefinitionID: person.ID.String(), Value: json.RawMessage(`"alice@example.com"`),
		})
		So(err, ShouldBeNil)

		cs, err := it.ChangeSets().Create(ctx, appchangeset.CreateInput{Name: "draft"})
		So(err, ShouldBeNil)
		_, err = it.ChangeSets().AddMutation(ctx, cs.ID.String(), appvalue.Mutation{
			Kind: appvalue.MutationSet, AttributeDefinitionID: email.ID.String(),
			EntityID: "p1", TypeDefinitionID: person.ID.String(),
			Value: json.RawMessage(`"alice@example.com"`),
		})
		So(err, ShouldBeNil)

		Convey("When the entity is erased", func() {
			report, err := svc.Interactors(ctx).Erasure().PurgeEntity(ctx, person.ID.String(), "p1")
			So(err, ShouldBeNil)

			Convey("Then the staged copy is redacted, not left readable", func() {
				So(report.RecordsRedacted, ShouldBeGreaterThan, 0)
				got, gerr := svc.Interactors(ctx).ChangeSets().Get(ctx, cs.ID.String())
				So(gerr, ShouldBeNil)
				So(got.Mutations, ShouldHaveLength, 1)
				So(got.Mutations[0].Value, ShouldBeNil)
				So(got.Mutations[0].Erased, ShouldBeTrue)
			})

			Convey("Then the mutation skeleton survives, so the set still says what it does", func() {
				got, gerr := svc.Interactors(ctx).ChangeSets().Get(ctx, cs.ID.String())
				So(gerr, ShouldBeNil)
				So(got.Mutations[0].EntityID, ShouldEqual, "p1")
				So(got.Mutations[0].Kind, ShouldEqual, appvalue.MutationSet)
			})
		})

		Convey("When a different entity is erased", func() {
			_, err := svc.Interactors(ctx).Erasure().PurgeEntity(ctx, person.ID.String(), "p2")

			Convey("Then the staged value for p1 is untouched", func() {
				So(err, ShouldNotBeNil) // nothing to purge
				got, gerr := svc.Interactors(ctx).ChangeSets().Get(ctx, cs.ID.String())
				So(gerr, ShouldBeNil)
				So(string(got.Mutations[0].Value), ShouldEqual, `"alice@example.com"`)
			})
		})
	})
}
