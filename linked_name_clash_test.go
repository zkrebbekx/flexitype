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
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
)

// TestLinkedRefusesAnAmbiguousName is the regression for #500.
//
// linked() unions both endpoint schemas, and on a name clash it kept the
// PARENT's entry. A bound field carries one attribute id, so from the parent
// side — where the far end is the child — the condition tested the parent's
// own attribute and matched nothing, silently. child() on the same data
// matched, which is what made the silence obvious once someone compared them.
func TestLinkedRefusesAnAmbiguousName(t *testing.T) {
	Convey("Given a directed relationship whose endpoints both declare label", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory()
		ia := svc.Interactors(ctx)

		hub, err := ia.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "hub", DisplayName: "Hub",
		})
		So(err, ShouldBeNil)
		spoke, err := svc.Interactors(ctx).TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "spoke", DisplayName: "Spoke",
		})
		So(err, ShouldBeNil)

		mk := func(typeID, name string) string {
			a, aerr := svc.Interactors(ctx).Attributes().Create(ctx, appattribute.CreateInput{
				TypeDefinitionID: typeID, InternalName: name, DisplayName: name, DataType: "string",
			})
			So(aerr, ShouldBeNil)
			return a.ID.String()
		}
		hubLabel := mk(hub.ID.String(), "label")
		spokeLabel := mk(spoke.ID.String(), "label")
		// A name only one endpoint declares stays usable through linked().
		mk(spoke.ID.String(), "code")

		wired, err := svc.Interactors(ctx).Relationships().CreateDefinition(ctx, apprelationship.CreateDefinitionInput{
			InternalName: "wired", DisplayName: "Wired",
			ParentTypeID: hub.ID.String(), ChildTypeID: spoke.ID.String(),
		})
		So(err, ShouldBeNil)

		set := func(attrID, typeID, entity, v string) {
			raw, _ := json.Marshal(v)
			_, serr := svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: attrID, EntityID: entity,
				TypeDefinitionID: typeID, Value: raw,
			})
			So(serr, ShouldBeNil)
		}
		set(hubLabel, hub.ID.String(), "h1", "HUBLABEL")
		set(spokeLabel, spoke.ID.String(), "s1", "SPOKELABEL")
		_, err = svc.Interactors(ctx).Relationships().Link(ctx, apprelationship.LinkInput{
			DefinitionID: wired.ID.String(), ParentEntity: "h1", ChildEntity: "s1",
		})
		So(err, ShouldBeNil)

		run := func(q string) ([]string, error) {
			out, qerr := svc.Interactors(ctx).Query().Execute(ctx, appquery.ExecuteInput{
				Type: "hub", Query: q, Page: db.PageArgs{},
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

		Convey("When linked() conditions on the clashing name", func() {
			_, err := run(`linked(wired){ label = "SPOKELABEL" }`)

			Convey("Then it is refused, naming the traversals that disambiguate", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "declared on both endpoints")
				So(err.Error(), ShouldContainSubstring, "child()")
			})
		})

		Convey("When child() conditions on the same name", func() {
			ids, err := run(`child(wired){ label = "SPOKELABEL" }`)

			Convey("Then it matches, because the side is explicit", func() {
				So(err, ShouldBeNil)
				So(ids, ShouldResemble, []string{"h1"})
			})
		})

		Convey("When linked() conditions on a name only one endpoint declares", func() {
			_, err := run(`linked(wired){ has(code) }`)

			Convey("Then it still binds: there is nothing to disambiguate", func() {
				So(err, ShouldBeNil)
			})
		})
	})
}
