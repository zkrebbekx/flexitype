// Package computed materializes read-only computed attributes. A computed
// attribute's value is derived (a formula over the entity's other values)
// and stored like any value by an event subscriber, so it is queryable in
// FQL with no special-casing. Writing one through the values API is
// rejected; only this materializer (an internal write) sets it.
//
// The subscriber recomputes on any value change to the entity and converges:
// re-setting an unchanged computed value emits no event, so the recompute
// loop terminates.
//
// A schema change to a computed attribute also rebuilds the affected type's
// entities, off the request goroutine, so a corrected formula converges
// without waiting for each entity to be written again.
//
// Rollup aggregates over relationships are NOT implemented, and a rollup
// definition is refused at create/update rather than accepted — see
// domain/attribute/computed.go. An accepted rollup would have produced an
// attribute that the schema advertises and that never holds a value.
package computed

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zkrebbekx/flexitype/application"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	domainattribute "github.com/zkrebbekx/flexitype/domain/attribute"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	domaintypedef "github.com/zkrebbekx/flexitype/domain/typedef"
	domainvalue "github.com/zkrebbekx/flexitype/domain/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/events"
	"github.com/zkrebbekx/flexitype/pkg/formula"
)

// Materializer recomputes an entity's computed attributes when its values
// change, writing the results as (internal) values.
type Materializer struct {
	factory application.Factory
	now     func() time.Time

	// hasComputed caches, per type definition id, whether its effective schema
	// holds any computed formula attribute. A value event for a type known to
	// have none early-outs before touching the database, so a bulk import into
	// a type with no computed attributes stops re-walking the inheritance chain
	// and re-listing the entity on every one of its value events. The cache is
	// flushed wholesale on any attribute/type definition event (see invalidate).
	mu          sync.RWMutex
	hasComputed map[string]bool

	// onSchemaChange, when set, is called with a type definition id whose
	// computed attributes may have changed. The facade wires it to a
	// background recompute.
	//
	// Editing a formula used to converge only when each entity happened to be
	// written again, or when an operator remembered to run the tenant-wide
	// recompute. Until then the old materialized values stayed queryable in
	// FQL and visible in the console, so the correction appeared to have been
	// applied while the data still reflected the old formula.
	onSchemaChange func(tenant valueobjects.TenantID, typeID string)
}

// OnSchemaChange registers the callback invoked when a schema change may have
// invalidated materialized values. It is not safe to call once the
// materializer is dispatching; wire it during composition.
func (m *Materializer) OnSchemaChange(fn func(tenant valueobjects.TenantID, typeID string)) {
	m.onSchemaChange = fn
}

// NewMaterializer builds the computed-attribute subscriber.
func NewMaterializer(factory application.Factory) *Materializer {
	return &Materializer{factory: factory, now: uow.UTCNow, hasComputed: map[string]bool{}}
}

// Name implements events.Handler.
func (m *Materializer) Name() string { return "computed-materializer" }

// EventTypes lists the events the materializer subscribes to: value changes
// drive a recompute, and definition changes invalidate the per-type
// "has computed attributes" cache (a formula attribute added to or removed
// from a type — or any of its ancestors — changes whether its entities need
// recomputing).
func EventTypes() []events.Type {
	return []events.Type{
		domainvalue.EventSet, domainvalue.EventUpdated, domainvalue.EventRemoved,
		domainattribute.EventCreated, domainattribute.EventUpdated,
		domainattribute.EventArchived, domainattribute.EventRestored,
		domaintypedef.EventCreated, domaintypedef.EventUpdated,
		domaintypedef.EventArchived, domaintypedef.EventRestored,
	}
}

// isDefinitionEvent reports whether an event type is a schema (attribute or
// type definition) change — the trigger to invalidate the has-computed cache.
func isDefinitionEvent(t events.Type) bool {
	switch t {
	case domainattribute.EventCreated, domainattribute.EventUpdated,
		domainattribute.EventArchived, domainattribute.EventRestored,
		domaintypedef.EventCreated, domaintypedef.EventUpdated,
		domaintypedef.EventArchived, domaintypedef.EventRestored:
		return true
	}
	return false
}

// definitionPayload carries the ids an attribute or type-definition event
// names. Both event families share these two fields.
type definitionPayload struct {
	TenantID         string `json:"tenant_id"`
	TypeDefinitionID string `json:"type_definition_id"`
	// TypeDefinitionEventID is the id a type-definition event reports for
	// itself, where an attribute event reports its parent type instead.
	TypeDefinitionEventID string `json:"type_definition_id_self"`
	ID                    string `json:"id"`
}

// scheduleRecompute asks the registered callback to rebuild the affected
// type's materialized values. It is best effort: with no callback wired (the
// in-memory default, and any embedder that has not asked for it) the values
// converge on the next write to each entity, as before.
func (m *Materializer) scheduleRecompute(env events.Envelope) {
	if m.onSchemaChange == nil {
		return
	}
	var p definitionPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return
	}
	tenant, err := valueobjects.ParseTenantID(p.TenantID)
	if err != nil {
		return
	}
	typeID := p.TypeDefinitionID
	if typeID == "" {
		// A type-definition event names itself by its aggregate id.
		typeID = env.AggregateID
	}
	if typeID == "" {
		return
	}
	m.onSchemaChange(tenant, typeID)
}

// recomputeTargetOf returns the type definition id a definition event
// concerns, or "" when it names none.
func (m *Materializer) recomputeTargetOf(env events.Envelope) string {
	var p definitionPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return ""
	}
	if p.TypeDefinitionID != "" {
		return p.TypeDefinitionID
	}
	return env.AggregateID
}

// RecomputeType rebuilds every entity of one type, and of its subtypes, since
// an inherited computed attribute belongs to those too. It returns the number
// of entities recomputed.
func (m *Materializer) RecomputeType(ctx context.Context, tenant valueobjects.TenantID, typeID string) (int, error) {
	ctx = uow.WithTenant(ctx, tenant)
	it := m.factory.New(ctx)

	td, err := it.TypeDefinitions().Get(ctx, typeID)
	if err != nil {
		if domainerrors.IsNotFound(err) {
			return 0, nil // archived or removed between the event and here
		}
		return 0, fmt.Errorf("load type %s: %w", typeID, err)
	}
	// Walk the subtype tree: an inherited computed attribute belongs to every
	// descendant, so editing it on the parent invalidates their values too.
	ids := []string{td.ID.String()}
	for i := 0; i < len(ids); i++ {
		children, cerr := it.TypeDefinitions().Children(ctx, ids[i])
		if cerr != nil {
			return 0, fmt.Errorf("load subtypes of %s: %w", ids[i], cerr)
		}
		for _, c := range children {
			ids = append(ids, c.ID.String())
		}
	}

	limit := 200
	count := 0
	// Entities written after the rebuild started are already correct: the
	// schema change invalidated the has-computed cache, so that write's own
	// synchronous recompute used the new definition. Skipping them is not an
	// optimisation — recomputing one would race the newer write and could
	// write a value derived from inputs that have since changed, undoing it.
	startedAt := m.now()
	for _, id := range ids {
		has, err := m.typeHasComputed(ctx, it, id)
		if err != nil {
			return count, err
		}
		if !has {
			continue
		}
		var cursor *string
		for {
			entities, err := it.Values().ListEntitiesStable(ctx, id, false, db.PageArgs{Limit: &limit, Cursor: cursor})
			if err != nil {
				return count, fmt.Errorf("list entities of %s: %w", id, err)
			}
			for _, e := range entities.Items {
				// Skip on >= rather than >. An entity written at the same
				// instant the rebuild started is in the same race, and the
				// comparison boundary is not something to be clever about:
				// skipping an entity that was already correct costs nothing,
				// because the write path recomputed it synchronously against
				// the new definition. Recomputing one that is being written
				// can clear a value from half-written inputs.
				if !e.LastUpdatedAt.Before(startedAt) {
					continue
				}
				if err := m.recomputeConverging(ctx, id, e.EntityID); err != nil {
					return count, fmt.Errorf("recompute %s: %w", e.EntityID, err)
				}
				count++
			}
			if !entities.PageInfo.HasNextPage || entities.PageInfo.NextCursor == nil {
				break
			}
			cursor = entities.PageInfo.NextCursor
		}
	}
	return count, nil
}

type valuePayload struct {
	TenantID         string `json:"tenant_id"`
	TypeDefinitionID string `json:"type_definition_id"`
	EntityID         string `json:"entity_id"`
	AttributeDefID   string `json:"attribute_definition_id"`
}

// Handle implements events.Handler. A definition event invalidates the cache;
// a value event recomputes the one entity it names.
func (m *Materializer) Handle(ctx context.Context, env events.Envelope) error {
	if isDefinitionEvent(env.Type) {
		m.invalidate()
		m.scheduleRecompute(env)
		return nil
	}
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

// HandleBatch implements events.BatchHandler. It coalesces one commit's value
// events per (tenant, type, entity) so each touched entity is recomputed once,
// not once per value event: a row that sets ten attributes emits ten events
// but needs a single recompute. A definition event anywhere in the batch
// flushes the cache first, so a recompute later in the same commit sees the
// fresh schema.
func (m *Materializer) HandleBatch(ctx context.Context, envs []events.Envelope) error {
	type key struct{ tenant, typeID, entityID string }
	seen := make(map[key]struct{}, len(envs))
	order := make([]key, 0, len(envs))
	invalidated := false
	rebuilt := map[string]bool{} // one rebuild per type per batch
	var errs []error

	for _, env := range envs {
		if isDefinitionEvent(env.Type) {
			if !invalidated {
				m.invalidate()
				invalidated = true
			}
			// The batch path is the one that runs: a BatchHandler takes
			// precedence over Handle, so a rebuild scheduled only in Handle
			// would never fire. One rebuild per type per batch — a schema
			// import emits many definition events for one type.
			if id := m.recomputeTargetOf(env); id != "" && !rebuilt[id] {
				rebuilt[id] = true
				m.scheduleRecompute(env)
			}
			continue
		}
		var p valuePayload
		if err := json.Unmarshal(env.Payload, &p); err != nil {
			errs = append(errs, fmt.Errorf("decode value event payload: %w", err))
			continue
		}
		k := key{tenant: p.TenantID, typeID: p.TypeDefinitionID, entityID: p.EntityID}
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		order = append(order, k)
	}

	for _, k := range order {
		tenant, err := valueobjects.ParseTenantID(k.tenant)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		rctx := uow.WithTenant(ctx, tenant)
		if err := m.Recompute(rctx, k.typeID, k.entityID); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// cachedHasComputed reports the cached has-computed flag for a type; known is
// false when the type has not been seen since the last invalidation.
func (m *Materializer) cachedHasComputed(typeID string) (known, has bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	has, known = m.hasComputed[typeID]
	return known, has
}

// setCachedHasComputed records whether a type's effective schema has any
// computed formula attribute.
func (m *Materializer) setCachedHasComputed(typeID string, has bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hasComputed[typeID] = has
}

// invalidate flushes the whole has-computed cache. A definition change on any
// type can alter a descendant's effective schema through inheritance, so
// per-type invalidation would be unsound — clearing everything is cheap
// relative to the value-write traffic the cache accelerates.
func (m *Materializer) invalidate() {
	m.mu.Lock()
	defer m.mu.Unlock()
	clear(m.hasComputed)
}

// RecomputeTenant re-materializes every entity's computed attributes for a
// tenant — the recovery counterpart to the search index's Reindex. Internal
// projections are maintained in the originating request's post-commit
// (issue #211), so read-your-writes holds regardless of the outbox setting;
// the trade-off is that a process crash between commit and that post-commit can
// leave a computed value stale. Running this rebuilds them all. It pages types
// and their entities, skipping types with no computed attributes, and reuses
// the per-entity Recompute (each in its own unit of work). Pagination stays
// correct even though Recompute bumps an entity's last_updated_at: each page's
// cursor is taken from the values read before that page was recomputed, so a
// recomputed entity moves ahead of the cursor and is never revisited.
func (m *Materializer) RecomputeTenant(ctx context.Context, tenant valueobjects.TenantID) (int, error) {
	ctx = uow.WithTenant(ctx, tenant)
	it := m.factory.New(ctx)
	limit := 200
	count := 0

	var typeCursor *string
	for {
		types, err := it.TypeDefinitions().List(ctx, apptypedef.ListInput{Page: db.PageArgs{Limit: &limit, Cursor: typeCursor}})
		if err != nil {
			return count, fmt.Errorf("list types: %w", err)
		}
		for _, t := range types.Items {
			typeID := t.ID.String()
			has, err := m.typeHasComputed(ctx, it, typeID)
			if err != nil {
				return count, err
			}
			if !has {
				continue // no computed attributes → nothing to rebuild
			}
			var entCursor *string
			for {
				entities, err := it.Values().ListEntitiesStable(ctx, typeID, false, db.PageArgs{Limit: &limit, Cursor: entCursor})
				if err != nil {
					return count, fmt.Errorf("list entities of %s: %w", typeID, err)
				}
				for _, e := range entities.Items {
					if err := m.Recompute(ctx, typeID, e.EntityID); err != nil {
						return count, fmt.Errorf("recompute %s: %w", e.EntityID, err)
					}
					count++
				}
				if entities.PageInfo.NextCursor == nil {
					break
				}
				entCursor = entities.PageInfo.NextCursor
			}
		}
		if types.PageInfo.NextCursor == nil {
			break
		}
		typeCursor = types.PageInfo.NextCursor
	}
	return count, nil
}

// typeHasComputed reports whether a type's effective schema holds any computed
// formula attribute, consulting and populating the per-type cache so a bulk
// recompute skips computed-free types without re-walking their inheritance
// chain on every entity.
func (m *Materializer) typeHasComputed(ctx context.Context, it *application.Interactors, typeID string) (bool, error) {
	if known, has := m.cachedHasComputed(typeID); known {
		return has, nil
	}
	attrs, err := it.TypeDefinitions().EffectiveAttributes(ctx, typeID)
	if err != nil {
		return false, fmt.Errorf("load effective attributes: %w", err)
	}
	has := false
	for _, a := range attrs {
		if a.Attribute.Computed != nil && a.Attribute.Computed.Kind == domainattribute.ComputedFormula {
			has = true
			break
		}
	}
	m.setCachedHasComputed(typeID, has)
	return has, nil
}

// Recompute evaluates every computed formula attribute of the entity's type
// and materializes the results. Missing inputs (or division by zero) remove
// a stale computed value rather than writing a wrong one.
func (m *Materializer) Recompute(ctx context.Context, typeID, entityID string) error {
	return m.recompute(ctx, typeID, entityID, true)
}

// recomputeConverging is the background-rebuild variant: it writes computed
// values and never clears one.
//
// Clearing is what makes a concurrent write destructive. A rebuild that reads
// an entity mid-write sees half its inputs, computes an undefined result, and
// would clear a value that is about to be correct — and it cannot tell that
// apart from a formula that has genuinely become undefined. The write path
// can: it runs inside the writing request with the entity's whole value set.
//
// So a rebuild converges values FORWARD, and a computed value that becomes
// undefined for an entity is cleared by that entity's next write or by the
// tenant-wide RecomputeComputed.
func (m *Materializer) recomputeConverging(ctx context.Context, typeID, entityID string) error {
	return m.recompute(ctx, typeID, entityID, false)
}

func (m *Materializer) recompute(ctx context.Context, typeID, entityID string, allowClear bool) error {
	// Fast path: a type known to hold no computed attribute needs no work and
	// no query at all.
	if known, has := m.cachedHasComputed(typeID); known && !has {
		return nil
	}

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
	// Cache the outcome so subsequent value events for this type take (or skip)
	// the fast path above until the next definition change.
	m.setCachedHasComputed(typeID, len(computed) > 0)
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
	// exact carries the same inputs as rationals, for decimal targets. A
	// decimal evaluated in binary float64 materialized artifacts —
	// 0.1 + 0.2 stored as 0.30000000000000004 — which then failed exact
	// equality in FQL and appeared verbatim in exports. Choosing `decimal` is
	// how a schema author asks for exactness.
	exact := map[string]*big.Rat{}
	computedValueID := map[string]string{} // attr id -> value id
	nameByID := map[string]string{}
	for _, a := range attrs {
		nameByID[a.Attribute.ID.String()] = a.Attribute.InternalName
	}
	for _, v := range values {
		if v.Locale != "" || v.Channel != "" {
			continue
		}
		if name := nameByID[v.AttributeDefinitionID.String()]; name != "" {
			if f, ok := toFloat(v.Value); ok {
				inputs[name] = f
			}
			if r, ok := toRat(v.Value); ok {
				exact[name] = r
			}
		}
		computedValueID[v.AttributeDefinitionID.String()] = v.ID.String()
	}

	// Defensive net: even though cycles are rejected at create/update, skip any
	// computed attribute caught in a dependency cycle so a cycle from any source
	// can never drive the recompute loop without end.
	cyclic := cyclicNames(computed)

	for _, c := range computed {
		if cyclic[c.InternalName] {
			continue
		}
		clearStale := func() error {
			if !allowClear {
				return nil
			}
			if id := computedValueID[c.ID.String()]; id != "" {
				// A nested recompute (synchronous dispatch) may already have
				// cleared it — tolerate an already-removed (archived) value.
				if _, rerr := it.Values().Remove(ctx, id); rerr != nil &&
					!domainerrors.IsNotFound(rerr) && !domainerrors.IsArchived(rerr) {
					return fmt.Errorf("clear computed value: %w", rerr)
				}
			}
			return nil
		}

		expr, err := formula.Parse(c.Computed.Formula)
		if err != nil {
			continue // a malformed formula shouldn't have persisted; skip defensively
		}
		var raw json.RawMessage
		var representable bool
		if c.DataType == valueobjects.DataTypeDecimal {
			r, ok := expr.EvalRat(exact)
			raw, representable = decimalForRat(r, ok)
		} else {
			result, ok := expr.Eval(inputs)
			raw, representable = numberForType(c.DataType, result, ok)
		}
		if !representable {
			// Undefined or non-representable (missing input, division by zero,
			// NaN, infinity, or integer overflow): clear any stale value rather
			// than leave a wrong or outdated one.
			if cerr := clearStale(); cerr != nil {
				return cerr
			}
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

// cyclicNames returns the computed attributes that participate in a dependency
// cycle among the computed set — a defensive guard for the recompute loop.
func cyclicNames(computed []domainattribute.Snapshot) map[string]bool {
	names := make(map[string]bool, len(computed))
	for _, c := range computed {
		names[c.InternalName] = true
	}
	deps := make(map[string][]string, len(computed))
	for _, c := range computed {
		if c.Computed == nil {
			continue
		}
		refs, err := c.Computed.Validate()
		if err != nil {
			continue
		}
		var within []string
		for _, r := range refs {
			if names[r] { // only edges to other computed attributes matter
				within = append(within, r)
			}
		}
		deps[c.InternalName] = within
	}
	bad := map[string]bool{}
	for name := range deps {
		var visit func(cur string, seen map[string]bool) bool
		visit = func(cur string, seen map[string]bool) bool {
			for _, ref := range deps[cur] {
				if ref == name {
					return true
				}
				if seen[ref] {
					continue
				}
				seen[ref] = true
				if visit(ref, seen) {
					return true
				}
			}
			return false
		}
		if visit(name, map[string]bool{name: true}) {
			bad[name] = true
		}
	}
	return bad
}

// toRat extracts an exact rational from a numeric value, for decimal
// evaluation. It reads a decimal's stored text rather than a float form of
// it, so no precision is lost on the way in.
func toRat(v valueobjects.Value) (*big.Rat, bool) {
	switch v.DataType() {
	case valueobjects.DataTypeBool:
		if v.Bool() {
			return big.NewRat(1, 1), true
		}
		return new(big.Rat), true
	case valueobjects.DataTypeInteger:
		return new(big.Rat).SetInt64(v.Int()), true
	case valueobjects.DataTypeDecimal:
		return new(big.Rat).SetString(v.Text())
	case valueobjects.DataTypeFloat:
		// A float input is already inexact; convert it as-is rather than
		// pretending otherwise.
		r := new(big.Rat)
		if math.IsNaN(v.Float()) || math.IsInf(v.Float(), 0) {
			return nil, false
		}
		return r.SetFloat64(v.Float()), true
	default:
		return nil, false
	}
}

// maxDecimalScale bounds the rendering of a rational with no finite decimal
// expansion (1/3). Everything that terminates is rendered exactly; only a
// repeating expansion is rounded, and it is rounded here rather than silently
// at some earlier float step.
const maxDecimalScale = 20

// decimalForRat renders an exact result as the decimal string the value type
// parses.
func decimalForRat(r *big.Rat, ok bool) (json.RawMessage, bool) {
	if !ok || r == nil {
		return nil, false
	}
	text := r.FloatString(maxDecimalScale)
	if r.IsInt() {
		text = r.Num().String()
	} else if strings.Contains(text, ".") {
		// FloatString pads to the full scale; drop the padding so an exact
		// result reads as the author would write it (0.3, not 0.30000…).
		text = strings.TrimRight(text, "0")
		text = strings.TrimSuffix(text, ".")
	}
	b, err := json.Marshal(text)
	if err != nil {
		return nil, false
	}
	return b, true
}

// toFloat extracts a numeric value from bool/int/float/decimal values. A
// bool is 0 or 1 so it can participate in arithmetic.
func toFloat(v valueobjects.Value) (float64, bool) {
	switch v.DataType() {
	case valueobjects.DataTypeBool:
		if v.Bool() {
			return 1, true
		}
		return 0, true
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

// numberForType encodes a computed float as the raw JSON the target data type
// parses: a number for integer/float, a string for decimal. It reports
// representable=false when the result is not a finite value the type can hold
// (NaN, ±Inf, or an integer out of int64 range), so the caller clears the
// value instead of writing a wrong one. Integers round to nearest.
func numberForType(dt valueobjects.DataType, f float64, ok bool) (json.RawMessage, bool) {
	if !ok || math.IsNaN(f) || math.IsInf(f, 0) {
		return nil, false
	}
	switch dt {
	case valueobjects.DataTypeInteger:
		r := math.Round(f)
		if r >= float64(math.MaxInt64) || r < float64(math.MinInt64) {
			return nil, false // out of int64 range
		}
		b, _ := json.Marshal(int64(r))
		return b, true
	case valueobjects.DataTypeFloat:
		b, _ := json.Marshal(f)
		return b, true
	case valueobjects.DataTypeDecimal:
		b, _ := json.Marshal(strconv.FormatFloat(f, 'f', -1, 64))
		return b, true
	default:
		return nil, false // no numeric target type
	}
}

// clearsStaleValues reports whether a recompute in the given mode may remove
// a computed value that no longer has a defined result.
//
// It exists so the rule is testable and named rather than implicit in a
// boolean argument: only the write path clears, because only the write path
// can tell "undefined" from "mid-write".
func (m *Materializer) clearsStaleValues(allowClear bool) bool { return allowClear }
