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
// A ROLLUP aggregates one attribute across the entities a relationship
// reaches (see rollup.go). Its inputs are on other entities, so it is driven
// by two further triggers: a link change recomputes both endpoints, and a
// value change recomputes whatever aggregates the entity that was written.
package computed

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"sort"
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
	domainrelationship "github.com/zkrebbekx/flexitype/domain/relationship"
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

	// onFormulaError, when set, reports a stored formula the evaluator cannot
	// parse. Such a formula is skipped, and skipping it silently is how a
	// computed value came to freeze at its last result while tracking
	// nothing: no error, no clear, and a number that still looks current in
	// FQL, the console and exports. Nil-safe.
	onFormulaError func(err error)
}

// OnFormulaError registers the callback invoked when a stored formula cannot
// be parsed. The facade wires it to the background-error observer.
func (m *Materializer) OnFormulaError(fn func(err error)) { m.onFormulaError = fn }

// reportFormulaError surfaces an unparseable stored formula. It names the
// attribute and the formula, because the operator has to find it to fix it.
func (m *Materializer) reportFormulaError(attr, src string, err error) {
	if m.onFormulaError == nil {
		return
	}
	m.onFormulaError(fmt.Errorf("computed attribute %s has an unparseable formula %q: %w", attr, src, err))
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
		// A rollup's inputs live on OTHER entities, so linking or unlinking
		// changes an aggregate without any value event for the entity that
		// holds it.
		domainrelationship.EventLinked, domainrelationship.EventUnlinked,
		domainrelationship.EventRePinned,
	}
}

// isLinkEvent reports whether an event type changes which entities a
// relationship reaches.
func isLinkEvent(t events.Type) bool {
	switch t {
	case domainrelationship.EventLinked, domainrelationship.EventUnlinked,
		domainrelationship.EventRePinned:
		return true
	default:
		return false
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
	if isLinkEvent(env.Type) {
		return m.handleLink(ctx, env)
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
	if err := m.Recompute(ctx, p.TypeDefinitionID, p.EntityID); err != nil {
		return err
	}
	// Anything aggregating this entity received no event of its own.
	return m.cascadeToNeighbours(ctx, p.EntityID)
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
		if isLinkEvent(env.Type) {
			// The batch path is the one that runs, so a link handled only in
			// Handle would never reach a rollup in production.
			if err := m.handleLink(ctx, env); err != nil {
				errs = append(errs, err)
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
		// Anything aggregating this entity received no event of its own.
		if err := m.cascadeToNeighbours(rctx, k.entityID); err != nil {
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
	// A ROLLUP counts too. Checking only formulas made the fast path skip
	// every value event for a type whose derived fields are all rollups — a
	// dish whose cost is the total of its lines — so the aggregate was
	// computed once, before any link existed, and never again.
	has := false
	for _, a := range attrs {
		if a.Attribute.Computed == nil {
			continue
		}
		switch a.Attribute.Computed.Kind {
		case domainattribute.ComputedFormula, domainattribute.ComputedRollup:
			has = true
		}
		if has {
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

// recomputeConverging is the background-rebuild variant.
//
// It DOES clear a value whose formula has become undefined. It used not to,
// because a rebuild reading an entity mid-write sees half its inputs,
// computes an undefined result, and cannot tell that apart from a formula
// that is genuinely undefined — so clearing was destructive. The cost was
// that after an edit introduced, say, a division by zero, the pre-edit value
// survived indefinitely: queryable in FQL, present in exports, counted toward
// completeness, with no formula that produces it.
//
// The fingerprint check in recomputeStable is what makes clearing safe now.
// A clear based on half-written inputs is followed by a source change, which
// the fingerprint sees, so the entity is recomputed and the value restored.
// A formula that is genuinely undefined leaves the sources unchanged, and the
// clear stands.
func (m *Materializer) recomputeConverging(ctx context.Context, typeID, entityID string) error {
	return m.recomputeStable(ctx, typeID, entityID, true)
}

// maxRecomputeAttempts bounds the re-read loop below.
const maxRecomputeAttempts = 3

// recomputeStable recomputes and then CONFIRMS that the inputs it read did not
// change while it was writing; if they did, it recomputes again.
//
// The rebuild used to decide whether to touch an entity by comparing two wall
// clocks taken on different machines: the entity's last_updated_at (stamped
// from the writing request's clock when the write BEGAN) against the
// rebuilding process's own. Nothing serialised them, so a write that began
// before the rebuild started and committed after it listed the entity was
// invisible to the check — the rebuild then read the pre-write inputs under
// READ COMMITTED and wrote a computed value derived from them, leaving the
// source new and the computed value stale. Replica clock skew widened the
// window, and the rebuild runs on a different replica from the writes by
// design.
//
// A fingerprint of the source values is compared instead of a clock. It needs
// no serialisation and no synchronised time: if the inputs moved, the answer
// is recomputed from the ones that are there now. Bounded, because a
// continuously-written entity would otherwise spin — and that entity is being
// recomputed synchronously by its own writes anyway.
func (m *Materializer) recomputeStable(ctx context.Context, typeID, entityID string, allowClear bool) error {
	for attempt := 0; attempt < maxRecomputeAttempts; attempt++ {
		before, err := m.sourceFingerprint(ctx, typeID, entityID)
		if err != nil {
			return err
		}
		if err := m.recompute(ctx, typeID, entityID, allowClear); err != nil {
			return err
		}
		after, err := m.sourceFingerprint(ctx, typeID, entityID)
		if err != nil {
			return err
		}
		if after == before {
			return nil
		}
	}
	return nil
}

// sourceFingerprint summarises an entity's NON-computed base-scope values, so
// a change to any formula input is visible without a clock.
//
// Computed values are excluded deliberately: the recompute writes those, and
// including them would make every pass look like a concurrent change.
func (m *Materializer) sourceFingerprint(ctx context.Context, typeID, entityID string) (string, error) {
	// System access, independent of the caller: a fingerprint taken over a
	// principal's redacted subset would differ from one taken by the sweeps,
	// and a restricted input would look permanently "changed" or invisible.
	ctx = uow.WithSystemAccess(ctx)
	it := m.factory.New(ctx)
	attrs, err := it.TypeDefinitions().EffectiveAttributes(ctx, typeID)
	if err != nil {
		return "", fmt.Errorf("load effective attributes: %w", err)
	}
	isComputed := make(map[string]bool, len(attrs))
	for _, a := range attrs {
		if a.Attribute.Computed != nil {
			isComputed[a.Attribute.ID.String()] = true
		}
	}
	values, err := it.Values().ListByEntity(ctx, typeID, entityID)
	if err != nil {
		return "", fmt.Errorf("load entity values: %w", err)
	}
	parts := make([]string, 0, len(values))
	for _, v := range values {
		if isComputed[v.AttributeDefinitionID.String()] {
			continue
		}
		parts = append(parts, v.AttributeDefinitionID.String()+"\x1f"+v.Locale+"\x1f"+
			v.Channel+"\x1f"+v.Value.String()+"\x1f"+v.UpdatedAt.UTC().Format(time.RFC3339Nano))
	}
	sort.Strings(parts)
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x1e")))
	return hex.EncodeToString(sum[:]), nil
}

func (m *Materializer) recompute(ctx context.Context, typeID, entityID string, allowClear bool) error {
	// Fast path: a type known to hold no computed attribute needs no work and
	// no query at all.
	if known, has := m.cachedHasComputed(typeID); known && !has {
		return nil
	}

	// A recompute derives state every reader shares, so its input reads run
	// under system access regardless of who triggered it. Reading through the
	// triggering principal's field ACL redacted the values that principal may
	// not see, and the formula then materialized a wrong result — or cleared a
	// right one — durably, for everyone. The dispatch path stamps the same
	// access; this keeps the materializer safe if it is ever invoked directly.
	ctx = uow.WithSystemAccess(ctx)
	it := m.factory.New(ctx)

	attrs, err := it.TypeDefinitions().EffectiveAttributes(ctx, typeID)
	if err != nil {
		return fmt.Errorf("load effective attributes: %w", err)
	}
	computed := make([]domainattribute.Snapshot, 0)
	for _, a := range attrs {
		if a.Attribute.Computed == nil {
			continue
		}
		switch a.Attribute.Computed.Kind {
		case domainattribute.ComputedFormula, domainattribute.ComputedRollup:
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
	// Every value a name holds, in repository order — not one. Assigning
	// collapsed a multi-valued source to whichever member came back last, so
	// adding a member changed the answer with nothing to explain it.
	inputs := formula.Inputs{}
	// exact carries the same inputs as rationals, for decimal targets. A
	// decimal evaluated in binary float64 materialized artifacts —
	// 0.1 + 0.2 stored as 0.30000000000000004 — which then failed exact
	// equality in FQL and appeared verbatim in exports. Choosing `decimal` is
	// how a schema author asks for exactness.
	exact := formula.RatInputs{}
	// members counts every value a name holds, whatever its data type. Inputs
	// holds only the values that coerce to a number, so count() over a name
	// whose members are strings, dates or media saw an empty list and
	// answered 0 for every entity.
	members := formula.Members{}
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
			members[name]++
			if f, ok := toFloat(v.Value); ok {
				inputs[name] = append(inputs[name], f)
			}
			if r, ok := toRat(v.Value); ok {
				exact[name] = append(exact[name], r)
			}
		}
		computedValueID[v.AttributeDefinitionID.String()] = v.ID.String()
	}

	// Defensive net: even though cycles are rejected at create/update, skip any
	// computed attribute caught in a dependency cycle so a cycle from any source
	// can never drive the recompute loop without end.
	cyclic := cyclicNames(computed)

	// ROLLUPS FIRST, and their results fed into the formula inputs.
	//
	// A formula reading a rollup on the same entity — line_cost = quantity *
	// ingredient_cost, where ingredient_cost is rolled up from the linked
	// ingredient — used to evaluate against the values loaded at the top of
	// this function, so it saw the rollup's PREVIOUS result. It converged only
	// because the rollup's write emitted an event that recomputed the entity
	// again, which makes the answer depend on a follow-up dispatch: under load
	// the stale value was still there when the next read happened.
	//
	// Ordering them here makes one pass self-consistent, and the follow-up
	// event becomes a no-op rather than the thing that saves it.
	sort.SliceStable(computed, func(i, j int) bool {
		return computed[i].Computed.Kind == domainattribute.ComputedRollup &&
			computed[j].Computed.Kind != domainattribute.ComputedRollup
	})

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

		if c.Computed.Kind == domainattribute.ComputedRollup {
			result, rerr := m.evalRollup(ctx, it, c, entityID)
			if rerr != nil {
				return rerr
			}
			if !result.ok {
				if cerr := clearStale(); cerr != nil {
					return cerr
				}
				// A cleared rollup is absent, not its last value. Leaving it in
				// the inputs let a formula in this same pass re-derive from the
				// number that just went away, and write it straight back.
				delete(inputs, c.InternalName)
				delete(exact, c.InternalName)
				delete(members, c.InternalName)
				continue
			}
			if _, werr := it.Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: c.ID.String(),
				EntityID:              entityID,
				TypeDefinitionID:      typeID,
				Internal:              true,
				Value:                 result.raw,
			}); werr != nil {
				return fmt.Errorf("materialize rollup value: %w", werr)
			}
			// A formula later in this pass reads what the rollup just
			// produced, not what it held when this function started.
			if value, perr := valueobjects.ParseValue(c.DataType, result.raw); perr == nil {
				inputs[c.InternalName] = nil
				exact[c.InternalName] = nil
				members[c.InternalName] = 1
				if f, ok := toFloat(value); ok {
					inputs[c.InternalName] = []float64{f}
				}
				if r, ok := toRat(value); ok {
					exact[c.InternalName] = []*big.Rat{r}
				}
			}
			continue
		}

		expr, err := formula.Parse(c.Computed.Formula)
		if err != nil {
			// Skip it — one bad formula must not stop the others — but say
			// so. A silent skip left the last materialized value in place,
			// tracking nothing.
			m.reportFormulaError(c.InternalName, c.Computed.Formula, err)
			continue
		}
		var raw json.RawMessage
		var representable bool
		switch c.DataType {
		case valueobjects.DataTypeDecimal:
			r, ok := expr.EvalRatWithMembers(exact, members)
			raw, representable = decimalForRat(r, ok)
		case valueobjects.DataTypeInteger:
			// Integers take the exact path too. Evaluating them in float64
			// narrowed every operand — sum{9007199254740993, 1} materialized
			// 9007199254740992, wrong by two, with no error and no clear —
			// while toRat already had an exact integer arm. A genuine
			// overflow still clears the value.
			r, ok := expr.EvalRatWithMembers(exact, members)
			raw, representable = integerForRat(r, ok)
		default:
			result, ok := expr.EvalWithMembers(inputs, members)
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
	case valueobjects.DataTypeFloat, valueobjects.DataTypeQuantity:
		// A float input is already inexact; convert it as-is rather than
		// pretending otherwise. A QUANTITY converts through its magnitude in
		// the family's BASE unit, which is what makes `pack_price / pack_size`
		// a price per base unit whatever unit the pack was entered in.
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

// integerForRat rounds an exact result to int64, half away from zero, and
// reports representable=false outside int64 range so the caller clears the
// value rather than writing a wrong one. It mirrors numberForType's integer
// arm, without the float64 narrowing that arm cannot avoid.
func integerForRat(r *big.Rat, ok bool) (json.RawMessage, bool) {
	if !ok || r == nil {
		return nil, false
	}
	num, denom := r.Num(), r.Denom()
	q, rem := new(big.Int).QuoRem(num, denom, new(big.Int))
	// Round half away from zero: 2*|rem| >= denom rounds the magnitude up.
	twice := new(big.Int).Abs(rem)
	twice.Lsh(twice, 1)
	if twice.Cmp(denom) >= 0 {
		if r.Sign() < 0 {
			q.Sub(q, big.NewInt(1))
		} else {
			q.Add(q, big.NewInt(1))
		}
	}
	if !q.IsInt64() {
		return nil, false
	}
	b, err := json.Marshal(q.Int64())
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
	case valueobjects.DataTypeFloat, valueobjects.DataTypeQuantity:
		// A quantity reads as its magnitude in the family's base unit.
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
