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
