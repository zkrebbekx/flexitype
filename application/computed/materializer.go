// Package computed materializes read-only computed attributes. A computed
// attribute's value is derived (a formula over the entity's other values)
// and stored like any value by an event subscriber, so it is queryable in
// FQL with no special-casing. Writing one through the values API is
// rejected; only this materializer (an internal write) sets it.
//
// The subscriber recomputes on any value change to the entity and converges:
// re-setting an unchanged computed value emits no event, so the recompute
// loop terminates. Rollup aggregates over relationships are a planned
// follow-up.
package computed

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/zkrebbekx/flexitype/application"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	domainattribute "github.com/zkrebbekx/flexitype/domain/attribute"
	domainvalue "github.com/zkrebbekx/flexitype/domain/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/events"
	"github.com/zkrebbekx/flexitype/pkg/formula"
)

// Materializer recomputes an entity's computed attributes when its values
// change, writing the results as (internal) values.
type Materializer struct {
	factory application.Factory
	now     func() time.Time
}

// NewMaterializer builds the computed-attribute subscriber.
func NewMaterializer(factory application.Factory) *Materializer {
	return &Materializer{factory: factory, now: time.Now}
}

// Name implements events.Handler.
func (m *Materializer) Name() string { return "computed-materializer" }

// EventTypes lists the events that trigger a recompute.
func EventTypes() []events.Type {
	return []events.Type{domainvalue.EventSet, domainvalue.EventUpdated, domainvalue.EventRemoved}
}

type valuePayload struct {
	TenantID         string `json:"tenant_id"`
	TypeDefinitionID string `json:"type_definition_id"`
	EntityID         string `json:"entity_id"`
	AttributeDefID   string `json:"attribute_definition_id"`
}

// Handle implements events.Handler.
func (m *Materializer) Handle(ctx context.Context, env events.Envelope) error {
	var p valuePayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return fmt.Errorf("decode value event payload: %w", err)
	}
	tenant, err := valueobjects.ParseTenantID(p.TenantID)
	if err != nil {
		return err
	}
	ctx = uow.WithTenant(ctx, tenant)
	return m.Recompute(ctx, p.TypeDefinitionID, p.EntityID)
}

// Recompute evaluates every computed formula attribute of the entity's type
// and materializes the results. Missing inputs (or division by zero) remove
// a stale computed value rather than writing a wrong one.
func (m *Materializer) Recompute(ctx context.Context, typeID, entityID string) error {
	it := m.factory.New(ctx)

	attrs, err := it.TypeDefinitions().EffectiveAttributes(ctx, typeID)
	if err != nil {
		return fmt.Errorf("load effective attributes: %w", err)
	}
	computed := make([]domainattribute.Snapshot, 0)
	for _, a := range attrs {
		if a.Attribute.Computed != nil && a.Attribute.Computed.Kind == domainattribute.ComputedFormula {
			computed = append(computed, a.Attribute)
		}
	}
	if len(computed) == 0 {
		return nil
	}

	values, err := it.Values().ListByEntity(ctx, typeID, entityID)
	if err != nil {
		return fmt.Errorf("load entity values: %w", err)
	}
	// Numeric inputs by internal name (base scope only). Also index computed
	// value ids so a now-undefined formula can be cleared.
	inputs := map[string]float64{}
	computedValueID := map[string]string{} // attr id -> value id
	nameByID := map[string]string{}
	for _, a := range attrs {
		nameByID[a.Attribute.ID.String()] = a.Attribute.InternalName
	}
	for _, v := range values {
		if v.Locale != "" || v.Channel != "" {
			continue
		}
		if f, ok := toFloat(v.Value); ok {
			if name := nameByID[v.AttributeDefinitionID.String()]; name != "" {
				inputs[name] = f
			}
		}
		computedValueID[v.AttributeDefinitionID.String()] = v.ID.String()
	}

	for _, c := range computed {
		expr, err := formula.Parse(c.Computed.Formula)
		if err != nil {
			continue // a malformed formula shouldn't have persisted; skip defensively
		}
		result, ok := expr.Eval(inputs)
		if !ok {
			// Undefined now: clear any stale materialized value.
			if id := computedValueID[c.ID.String()]; id != "" {
				if _, rerr := it.Values().Remove(ctx, id); rerr != nil {
					return fmt.Errorf("clear computed value: %w", rerr)
				}
			}
			continue
		}
		raw, err := numberForType(c.DataType, result)
		if err != nil {
			continue
		}
		if _, err := it.Values().Set(ctx, appvalue.SetInput{
			AttributeDefinitionID: c.ID.String(),
			EntityID:              entityID,
			TypeDefinitionID:      typeID,
			Internal:              true,
			Value:                 raw,
		}); err != nil {
			return fmt.Errorf("materialize computed value: %w", err)
		}
	}
	return nil
}

// toFloat extracts a numeric value from bool/int/float/decimal values.
func toFloat(v valueobjects.Value) (float64, bool) {
	switch v.DataType() {
	case valueobjects.DataTypeInteger:
		return float64(v.Int()), true
	case valueobjects.DataTypeFloat:
		return v.Float(), true
	case valueobjects.DataTypeDecimal:
		f, err := strconv.ParseFloat(v.Text(), 64)
		return f, err == nil
	default:
		return 0, false
	}
}

// numberForType encodes a computed float as the raw JSON the target data
// type parses: a number for integer/float, a string for decimal.
func numberForType(dt valueobjects.DataType, f float64) (json.RawMessage, error) {
	switch dt {
	case valueobjects.DataTypeInteger:
		return json.Marshal(int64(f))
	case valueobjects.DataTypeFloat:
		return json.Marshal(f)
	case valueobjects.DataTypeDecimal:
		return json.Marshal(strconv.FormatFloat(f, 'f', -1, 64))
	default:
		return nil, fmt.Errorf("computed result cannot target data type %q", dt)
	}
}
