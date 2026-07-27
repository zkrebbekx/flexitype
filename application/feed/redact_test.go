package feed

import (
	"context"
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/application/fieldacl"
	"github.com/zkrebbekx/flexitype/application/uow"
	domainattribute "github.com/zkrebbekx/flexitype/domain/attribute"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/events"
)

// stubAttrs resolves attribute ids to internal names for the field-ACL
// resolver; the rest of the repository port is unreachable from here.
type stubAttrs struct{ byID map[string]string }

func (s *stubAttrs) def(id valueobjects.AttributeDefinitionID) *domainattribute.Definition {
	return domainattribute.Rehydrate(domainattribute.Snapshot{
		ID:           id,
		TenantID:     valueobjects.DefaultTenant,
		InternalName: s.byID[id.String()],
		DataType:     valueobjects.DataTypeString,
	})
}

func (s *stubAttrs) Get(_ context.Context, id valueobjects.AttributeDefinitionID) (*domainattribute.Definition, error) {
	if _, ok := s.byID[id.String()]; !ok {
		return nil, domainerrors.NewNotFound("attribute_definition", id.String())
	}
	return s.def(id), nil
}

func (s *stubAttrs) GetMany(_ context.Context, ids []valueobjects.AttributeDefinitionID) ([]*domainattribute.Definition, error) {
	out := make([]*domainattribute.Definition, 0, len(ids))
	for _, id := range ids {
		if _, ok := s.byID[id.String()]; ok {
			out = append(out, s.def(id))
		}
	}
	return out, nil
}

func (s *stubAttrs) WithTx(db.Tx) domainattribute.Repository { panic("unused") }
func (s *stubAttrs) GetForUpdate(context.Context, valueobjects.AttributeDefinitionID) (*domainattribute.Definition, error) {
	panic("unused")
}
func (s *stubAttrs) GetByInternalName(context.Context, valueobjects.TypeDefinitionID, string) (*domainattribute.Definition, error) {
	panic("unused")
}
func (s *stubAttrs) ListByTypeDefinition(context.Context, valueobjects.TypeDefinitionID, db.Page) ([]*domainattribute.Definition, int, error) {
	panic("unused")
}
func (s *stubAttrs) List(context.Context, domainattribute.Filter, db.Page) ([]*domainattribute.Definition, int, error) {
	panic("unused")
}
func (s *stubAttrs) Save(context.Context, *domainattribute.Definition) error { panic("unused") }

// valueEvent builds a feed entry shaped like a value.updated envelope.
func valueEvent(seq int64, attrID valueobjects.AttributeDefinitionID, oldV, newV string) Event {
	payload, _ := json.Marshal(map[string]any{
		"attribute_definition_id": attrID.String(),
		"entity_id":               "p1",
		"old_value":               oldV,
		"new_value":               newV,
	})
	return Event{
		Seq: seq,
		Envelope: events.Envelope{
			ID:      "env",
			Type:    events.Type("flexitype.attribute_value.updated"),
			Payload: payload,
		},
	}
}

func TestFeedRedactsUnreadableValues(t *testing.T) {
	Convey("Given a feed carrying a sku change and a restricted cost change", t, func() {
		skuID, costID := valueobjects.NewAttributeDefinitionID(), valueobjects.NewAttributeDefinitionID()
		store := &fakeFeedStore{events: []Event{
			valueEvent(1, skuID, "ABC", "XYZ"),
			valueEvent(2, costID, "250", "300"),
		}}
		acl := fieldacl.New(&stubAttrs{byID: map[string]string{
			skuID.String():  "sku",
			costID.String(): "cost",
		}})
		i := NewInteractor(store, &fakeCursorStore{positions: map[string]int64{}}, acl)

		payloadOf := func(items []Event, seq int64) map[string]any {
			for _, ev := range items {
				if ev.Seq == seq {
					var obj map[string]any
					So(json.Unmarshal(ev.Envelope.Payload, &obj), ShouldBeNil)
					return obj
				}
			}
			return nil
		}

		Convey("When a principal barred from reading cost lists the feed", func() {
			ctx := uow.WithAccess(context.Background(), uow.Access{
				Attr: map[string]uow.Perm{"cost": uow.PermNone},
			})
			out, err := i.List(ctx, ListInput{})
			So(err, ShouldBeNil)

			Convey("Then both events are still delivered so the cursor stays gap-free", func() {
				So(out.Items, ShouldHaveLength, 2)
				So(out.NextCursor, ShouldEqual, 2)
			})

			Convey("Then the cost values are masked and marked redacted", func() {
				cost := payloadOf(out.Items, 2)
				So(cost["old_value"], ShouldBeNil)
				So(cost["new_value"], ShouldBeNil)
				So(cost[fieldacl.RedactedMarker], ShouldEqual, true)
				So(cost["entity_id"], ShouldEqual, "p1")
			})

			Convey("Then the readable sku values are untouched", func() {
				sku := payloadOf(out.Items, 1)
				So(sku["old_value"], ShouldEqual, "ABC")
				So(sku["new_value"], ShouldEqual, "XYZ")
				_, marked := sku[fieldacl.RedactedMarker]
				So(marked, ShouldBeFalse)
			})
		})

		Convey("When an admin lists the feed", func() {
			ctx := uow.WithAccess(context.Background(), uow.Access{Admin: true})
			out, err := i.List(ctx, ListInput{})
			So(err, ShouldBeNil)

			Convey("Then nothing is masked", func() {
				cost := payloadOf(out.Items, 2)
				So(cost["old_value"], ShouldEqual, "250")
				So(cost["new_value"], ShouldEqual, "300")
			})
		})
	})
}
