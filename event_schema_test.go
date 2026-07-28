package flexitype_test

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	flexispec "github.com/zkrebbekx/flexitype/api"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/events"
)

// TestEventSchemasMatchPayloads holds the published JSON Schemas to what the
// code actually emits.
//
// The envelope was versioned and described in prose; the payloads existed
// only as Go structs. A consumer in another language transcribed them by hand
// and got no signal when one changed — the first sign was a parser failing in
// their deployment.
//
// The check is deliberately narrow and dependency-free: every key the code
// emits must be declared in the schema, and every field the schema marks
// required must be present. That is the direction drift actually goes — a Go
// field added or renamed without updating the published contract.
func TestEventSchemasMatchPayloads(t *testing.T) {
	Convey("Given events emitted by a real service", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		var captured []events.Envelope
		svc := flexitype.NewInMemory(flexitype.WithHandler(
			events.NewHandlerFunc("capture", func(_ context.Context, env events.Envelope) error {
				captured = append(captured, env)
				return nil
			})))

		ia := svc.Interactors(ctx)
		product, err := ia.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "product", DisplayName: "Product",
		})
		So(err, ShouldBeNil)
		sku, err := svc.Interactors(ctx).Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: product.ID.String(), InternalName: "sku",
			DisplayName: "SKU", DataType: "string",
		})
		So(err, ShouldBeNil)
		_, err = svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{
			AttributeDefinitionID: sku.ID.String(), EntityID: "p1",
			TypeDefinitionID: product.ID.String(), Value: json.RawMessage(`"ABC"`),
		})
		So(err, ShouldBeNil)
		So(captured, ShouldNotBeEmpty)

		envelope := schemaObject(t, flexispec.EventEnvelopeSchema)
		payloads := schemaDefs(t, flexispec.EventPayloadSchemas)

		Convey("Then the envelope schema declares every key an envelope carries", func() {
			var problems []string
			for _, env := range captured {
				problems = append(problems, checkAgainst(t, envelope, marshalMap(t, env))...)
			}
			So(strings.Join(unique(problems), "\n"), ShouldEqual, "")
		})

		Convey("Then each payload schema declares every key its payload carries", func() {
			byType := map[string]string{
				"flexitype.type_definition.created":      "type_definition",
				"flexitype.attribute_definition.created": "attribute_definition",
				"flexitype.attribute_value.set":          "value",
				"flexitype.attribute_value.updated":      "value",
				"flexitype.attribute_value.removed":      "value",
			}
			var problems []string
			checked := 0
			for _, env := range captured {
				def, ok := byType[string(env.Type)]
				if !ok {
					continue
				}
				schema, ok := payloads[def]
				So(ok, ShouldBeTrue)
				var body map[string]any
				So(json.Unmarshal(env.Payload, &body), ShouldBeNil)
				problems = append(problems, checkAgainst(t, schema, body)...)
				checked++
			}
			So(strings.Join(unique(problems), "\n"), ShouldEqual, "")

			// Guard the guard: a mapping that matched nothing would let this
			// pass while checking no payload at all.
			So(checked, ShouldBeGreaterThanOrEqualTo, 3)
		})
	})
}

// jsonSchema is the slice of JSON Schema this check reads.
type jsonSchema struct {
	Required   []string               `json:"required"`
	Properties map[string]any         `json:"properties"`
	Defs       map[string]*jsonSchema `json:"$defs"`
}

func schemaObject(t *testing.T, raw []byte) *jsonSchema {
	t.Helper()
	var s jsonSchema
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	if len(s.Properties) == 0 {
		t.Fatal("schema declares no properties")
	}
	return &s
}

func schemaDefs(t *testing.T, raw []byte) map[string]*jsonSchema {
	t.Helper()
	var s jsonSchema
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	if len(s.Defs) == 0 {
		t.Fatal("schema declares no $defs")
	}
	return s.Defs
}

// checkAgainst reports keys the document carries that the schema does not
// declare, and required fields the document omits.
func checkAgainst(t *testing.T, schema *jsonSchema, doc map[string]any) []string {
	t.Helper()
	var problems []string
	for key := range doc {
		if _, ok := schema.Properties[key]; !ok {
			problems = append(problems, "emitted key not in the published schema: "+key)
		}
	}
	for _, req := range schema.Required {
		if _, ok := doc[req]; !ok {
			problems = append(problems, "schema requires a field the event omits: "+req)
		}
	}
	return problems
}

func marshalMap(t *testing.T, v any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}

func unique(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
