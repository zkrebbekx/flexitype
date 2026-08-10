package flexitype_test

import (
	"context"
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	appquery "github.com/zkrebbekx/flexitype/application/query"
	apprelationship "github.com/zkrebbekx/flexitype/application/relationship"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	domainrelationship "github.com/zkrebbekx/flexitype/domain/relationship"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
)

// ghostScenario links parent1 -> child1, then removes child1's only value so
// the link outlives its counterpart.
func ghostScenario(t *testing.T, svc *flexitype.Service) (context.Context, string) {
	t.Helper()
	ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
	ia := svc.Interactors(ctx)

	node, err := ia.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
		InternalName: "node", DisplayName: "Node",
	})
	So(err, ShouldBeNil)
	name, err := ia.Attributes().Create(ctx, appattribute.CreateInput{
		TypeDefinitionID: node.ID.String(), InternalName: "name",
		DisplayName: "Name", DataType: "string",
	})
	So(err, ShouldBeNil)
	def, err := ia.Relationships().CreateDefinition(ctx, apprelationship.CreateDefinitionInput{
		InternalName: "has_child", DisplayName: "Has child",
		ParentTypeID: node.ID.String(), ChildTypeID: node.ID.String(),
	})
	So(err, ShouldBeNil)

	for _, e := range []string{"parent1", "child1"} {
		raw, _ := json.Marshal(e)
		_, serr := ia.Values().Set(ctx, appvalue.SetInput{
			AttributeDefinitionID: name.ID.String(), EntityID: e,
			TypeDefinitionID: node.ID.String(), Value: raw,
		})
		So(serr, ShouldBeNil)
	}
	_, err = ia.Relationships().Link(ctx, apprelationship.LinkInput{
		DefinitionID: def.ID.String(), ParentEntity: "parent1", ChildEntity: "child1",
	})
	So(err, ShouldBeNil)

	// Removing child1's only value makes it invisible at the root while the
	// link stays live — the ghost.
	values, err := ia.Values().ListByEntity(ctx, node.ID.String(), "child1")
	So(err, ShouldBeNil)
	So(values, ShouldHaveLength, 1)
	_, err = ia.Values().Remove(ctx, values[0].ID.String())
	So(err, ShouldBeNil)

	return ctx, def.ID.String()
}

// window returns the counterparts and total the nested-connection path reports.
func window(ctx context.Context, svc *flexitype.Service, defID string) ([]string, int) {
	pages, err := svc.Interactors(ctx).Relationships().WindowedLinks(ctx, apprelationship.LinkWindowInput{
		DefinitionID: defID, Side: domainrelationship.ParentSide,
		First: 50, WantTotal: true,
	}, []string{"parent1"})
	So(err, ShouldBeNil)
	p := pages["parent1"]
	others := p.Others
	total := 0
	if p.Total != nil {
		total = *p.Total
	}
	return others, total
}

// TestWindowedLinksExcludeGhostCounterparts covers issue #594.
//
// Removing an entity's last value leaves its relationships live, so a link can
// point at an entity that has nothing. FQL traversal has excluded such ghosts
// since #475. The windowed path GraphQL pages nested connections through did
// not, so a connection listed nodes with an id and null fields and counted
// them in totalCount — two APIs over one link table disagreeing about what a
// counterpart is.
func TestWindowedLinksExcludeGhostCounterparts(t *testing.T) {
	Convey("Given a link whose counterpart lost its last value (memory)", t, func() {
		svc := flexitype.NewInMemory()
		ctx, defID := ghostScenario(t, svc)

		Convey("Then FQL traversal does not see it", func() {
			out, err := svc.Interactors(ctx).Query().Execute(ctx, appquery.ExecuteInput{
				Type: "node", Query: `child(has_child){ has(name) }`, Page: db.PageArgs{},
			})
			So(err, ShouldBeNil)
			So(out.Items, ShouldBeEmpty)
		})

		Convey("Then the window agrees with it", func() {
			others, total := window(ctx, svc, defID)
			So(others, ShouldBeEmpty)
			So(total, ShouldEqual, 0)
		})
	})
}

// TestWindowedLinksExcludeGhostCounterpartsPostgres is the same on the backend
// GraphQL actually runs against.
func TestWindowedLinksExcludeGhostCounterpartsPostgres(t *testing.T) {
	pool := openTestDB(t)
	svc := flexitype.New(pool)
	if err := svc.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	Convey("Given a link whose counterpart lost its last value (Postgres)", t, func() {
		truncateAll(t, pool, svc)
		ctx, defID := ghostScenario(t, svc)

		Convey("Then the window lists no ghost and counts none", func() {
			others, total := window(ctx, svc, defID)
			So(others, ShouldBeEmpty)
			So(total, ShouldEqual, 0)
		})

		Convey("Then a counterpart that regains a value is listed again", func() {
			ia := svc.Interactors(ctx)
			types, terr := ia.TypeDefinitions().List(ctx, apptypedef.ListInput{})
			So(terr, ShouldBeNil)
			So(types.Items, ShouldNotBeEmpty)
			eff, eerr := ia.TypeDefinitions().EffectiveAttributes(ctx, types.Items[0].ID.String())
			So(eerr, ShouldBeNil)
			raw, _ := json.Marshal("back")
			_, serr := ia.Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: eff[0].Attribute.ID.String(), EntityID: "child1",
				TypeDefinitionID: types.Items[0].ID.String(), Value: raw,
			})
			So(serr, ShouldBeNil)

			// The guard is about liveness, not about the link, so restoring
			// the value restores the counterpart.
			others, total := window(ctx, svc, defID)
			So(others, ShouldResemble, []string{"child1"})
			So(total, ShouldEqual, 1)
		})
	})
}
