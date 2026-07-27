package fieldacl

import (
	"context"
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/application/uow"
	domainattribute "github.com/zkrebbekx/flexitype/domain/attribute"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
)

// stubAttrs is the smallest attribute repository the resolver needs: an
// id -> internal-name table. Only Get and GetMany are reachable from this
// package, so the rest of the port panics rather than pretending to work.
type stubAttrs struct {
	byID map[string]string // attribute id -> internal name
}

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

func TestResolverReadable(t *testing.T) {
	Convey("Given a resolver over a sku attribute and a restricted cost attribute", t, func() {
		skuID, costID := valueobjects.NewAttributeDefinitionID(), valueobjects.NewAttributeDefinitionID()
		unknownID := valueobjects.NewAttributeDefinitionID()
		r := New(&stubAttrs{byID: map[string]string{
			skuID.String():  "sku",
			costID.String(): "cost",
		}})
		ids := []valueobjects.AttributeDefinitionID{skuID, costID, unknownID}

		Convey("When the principal is an admin", func() {
			ctx := uow.WithAccess(context.Background(), uow.Access{Admin: true})
			readable, err := r.Readable(ctx, ids)

			Convey("Then every attribute is readable without a lookup", func() {
				So(err, ShouldBeNil)
				So(readable[skuID.String()], ShouldBeTrue)
				So(readable[costID.String()], ShouldBeTrue)
				So(readable[unknownID.String()], ShouldBeTrue)
			})
		})

		Convey("When the principal has none on cost", func() {
			ctx := uow.WithAccess(context.Background(), uow.Access{
				Attr: map[string]uow.Perm{"cost": uow.PermNone},
			})
			readable, err := r.Readable(ctx, ids)

			Convey("Then cost is unreadable and sku is readable", func() {
				So(err, ShouldBeNil)
				So(readable[skuID.String()], ShouldBeTrue)
				So(readable[costID.String()], ShouldBeFalse)
			})

			Convey("Then an attribute the repository does not know is unreadable", func() {
				So(readable[unknownID.String()], ShouldBeFalse)
			})

			Convey("Then AnyReadable is true for a mixed set and false for cost alone", func() {
				reachable, err := r.AnyReadable(ctx, ids)
				So(err, ShouldBeNil)
				So(reachable, ShouldBeTrue)

				reachable, err = r.AnyReadable(ctx, []valueobjects.AttributeDefinitionID{costID})
				So(err, ShouldBeNil)
				So(reachable, ShouldBeFalse)

				reachable, err = r.AnyReadable(ctx, nil)
				So(err, ShouldBeNil)
				So(reachable, ShouldBeFalse)
			})
		})

		Convey("When the principal holds an allow-list that names only sku", func() {
			ctx := uow.WithAccess(context.Background(), uow.Access{
				Attr:    map[string]uow.Perm{"sku": uow.PermRead},
				Default: uow.PermNone,
			})

			Convey("Then cost is unreadable even though the policy never names it", func() {
				readable, err := r.Readable(ctx, ids)
				So(err, ShouldBeNil)
				So(readable[skuID.String()], ShouldBeTrue)
				So(readable[costID.String()], ShouldBeFalse)
			})

			Convey("Then sku is readable but not writable", func() {
				ok, err := r.CanRead(ctx, skuID)
				So(err, ShouldBeNil)
				So(ok, ShouldBeTrue)

				ok, err = r.CanWrite(ctx, skuID)
				So(err, ShouldBeNil)
				So(ok, ShouldBeFalse)
			})
		})
	})
}

func TestPayloadAttributeAndMask(t *testing.T) {
	Convey("Given a value event payload", t, func() {
		id := valueobjects.NewAttributeDefinitionID()
		payload := json.RawMessage(`{
			"attribute_value_id":"01AAA",
			"attribute_definition_id":"` + id.String() + `",
			"entity_id":"p1",
			"old_value":"250",
			"new_value":"300"
		}`)

		Convey("When the attribute is read from the payload", func() {
			got, ok := PayloadAttribute(payload)

			Convey("Then it is the attribute the payload names", func() {
				So(ok, ShouldBeTrue)
				So(got.String(), ShouldEqual, id.String())
			})
		})

		Convey("When the payload is masked", func() {
			masked, err := MaskPayload(payload)
			So(err, ShouldBeNil)

			var obj map[string]any
			So(json.Unmarshal(masked, &obj), ShouldBeNil)

			Convey("Then every value field is null", func() {
				So(obj["old_value"], ShouldBeNil)
				So(obj["new_value"], ShouldBeNil)
			})

			Convey("Then the identity fields survive so the change is still auditable", func() {
				So(obj["entity_id"], ShouldEqual, "p1")
				So(obj["attribute_definition_id"], ShouldEqual, id.String())
			})

			Convey("Then the redaction marker distinguishes a mask from an absent value", func() {
				So(obj[RedactedMarker], ShouldEqual, true)
			})
		})
	})

	Convey("Given payloads that carry no attribute identity", t, func() {
		Convey("When an empty payload is probed", func() {
			_, ok := PayloadAttribute(nil)
			So(ok, ShouldBeFalse)
		})

		Convey("When a payload names no attribute", func() {
			_, ok := PayloadAttribute(json.RawMessage(`{"entity_id":"p1"}`))
			So(ok, ShouldBeFalse)
		})

		Convey("When a payload names an unparsable attribute", func() {
			_, ok := PayloadAttribute(json.RawMessage(`{"attribute_definition_id":"not-an-id"}`))
			So(ok, ShouldBeFalse)
		})

		Convey("When a payload carries no value field, masking leaves it alone", func() {
			raw := json.RawMessage(`{"entity_id":"p1"}`)
			masked, err := MaskPayload(raw)
			So(err, ShouldBeNil)
			So(string(masked), ShouldEqual, string(raw))
		})

		Convey("When the payload is not a JSON object, masking leaves it alone", func() {
			raw := json.RawMessage(`["a"]`)
			masked, err := MaskPayload(raw)
			So(err, ShouldBeNil)
			So(string(masked), ShouldEqual, string(raw))
		})
	})
}
