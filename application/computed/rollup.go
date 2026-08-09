package computed

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"

	"github.com/zkrebbekx/flexitype/application/uow"
	"github.com/zkrebbekx/flexitype/pkg/events"

	"github.com/zkrebbekx/flexitype/application"
	apprelationship "github.com/zkrebbekx/flexitype/application/relationship"
	domainattribute "github.com/zkrebbekx/flexitype/domain/attribute"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

// rollupResult is one evaluated aggregate: the wire value, and whether the
// rollup produced anything at all.
type rollupResult struct {
	raw json.RawMessage
	ok  bool
}

// evalRollup aggregates one attribute across the entities a relationship
// reaches.
//
// The counterparts' values are read in ONE query rather than one per
// counterpart: a dish with forty ingredient lines would otherwise cost forty
// round trips on every recompute, and a recompute runs on every write.
//
// It aggregates the BASE scope only, like every other computed input. A
// localized or scoped member would otherwise be counted once per locale, and
// "the total cost" would rise with the number of translations.
func (m *Materializer) evalRollup(
	ctx context.Context,
	it *application.Interactors,
	attr domainattribute.Snapshot,
	entityID string,
) (rollupResult, error) {
	spec := attr.Computed.Rollup
	counterparts, err := m.counterparts(ctx, it, entityID, spec.Relationship, spec.Direction)
	if err != nil {
		return rollupResult{}, err
	}

	if spec.Aggregate == domainattribute.RollupCount {
		// count answers 0 rather than nothing: "this dish has no lines" is a
		// fact, and an absent value would be indistinguishable from a rollup
		// that never ran.
		raw, ok := integerForRat(new(big.Rat).SetInt64(int64(len(counterparts))), true)
		return rollupResult{raw: raw, ok: ok}, nil
	}
	if len(counterparts) == 0 {
		// sum, min and max over nothing are undefined, exactly as the formula
		// aggregates are. The caller clears any stale value.
		return rollupResult{}, nil
	}

	values, err := it.Values().ListByEntities(ctx, counterparts)
	if err != nil {
		return rollupResult{}, fmt.Errorf("load counterpart values: %w", err)
	}

	// The target is named on the COUNTERPART's type, which may differ per
	// counterpart (a relationship can reach a subtype). Resolving by internal
	// name per attribute definition keeps that working without loading each
	// type's schema.
	targetIDs, err := m.attributeIDsNamed(ctx, it, counterparts, spec.Target)
	if err != nil {
		return rollupResult{}, err
	}

	var acc *big.Rat
	seen := 0
	for _, v := range values {
		if v.Locale != "" || v.Channel != "" {
			continue
		}
		if !targetIDs[v.AttributeDefinitionID.String()] {
			continue
		}
		r, ok := toRat(v.Value)
		if !ok {
			// A non-numeric member contributes nothing rather than failing the
			// whole aggregate: one text value among the numbers must not make
			// the total disappear.
			continue
		}
		seen++
		switch {
		case acc == nil:
			acc = new(big.Rat).Set(r)
		case spec.Aggregate == domainattribute.RollupSum:
			acc.Add(acc, r)
		case spec.Aggregate == domainattribute.RollupMin:
			if r.Cmp(acc) < 0 {
				acc.Set(r)
			}
		case spec.Aggregate == domainattribute.RollupMax:
			if r.Cmp(acc) > 0 {
				acc.Set(r)
			}
		}
	}
	if seen == 0 {
		return rollupResult{}, nil
	}

	switch attr.DataType {
	case valueobjects.DataTypeDecimal:
		raw, ok := decimalForRat(acc, true)
		return rollupResult{raw: raw, ok: ok}, nil
	case valueobjects.DataTypeInteger:
		raw, ok := integerForRat(acc, true)
		return rollupResult{raw: raw, ok: ok}, nil
	default:
		exact, _ := acc.Float64()
		raw, ok := numberForType(attr.DataType, exact, true)
		return rollupResult{raw: raw, ok: ok}, nil
	}
}

// counterparts lists the entities one relationship reaches from an entity, in
// the named direction.
//
//	child   the entities BELOW this one   (this entity is the parent)
//	parent  the entities ABOVE this one   (this entity is the child)
//	linked  either side, for a symmetric relationship
func (m *Materializer) counterparts(
	ctx context.Context,
	it *application.Interactors,
	entityID, relationship, direction string,
) ([]string, error) {
	links, err := it.Relationships().ListByEntity(ctx, entityID)
	if err != nil {
		return nil, fmt.Errorf("load relationships: %w", err)
	}
	out := make([]string, 0, len(links))
	seen := map[string]bool{}
	for _, link := range links {
		if link.Definition.InternalName != relationship {
			continue
		}
		other := counterpartOf(link, entityID)
		if other == "" || !directionMatches(direction, link.Role) {
			continue
		}
		// A symmetric relationship can hold both arms of one pair, and a
		// counterpart counted twice would double a sum.
		if seen[other] {
			continue
		}
		seen[other] = true
		out = append(out, other)
	}
	return out, nil
}

// declaredTypeOf returns the type a relationship declares for one of its
// endpoints, which is the only clue to the type of an entity that holds no
// value yet.
func declaredTypeOf(link apprelationship.EntityLink, entityID string) string {
	if link.Relationship.ParentEntityID.String() == entityID {
		return link.Definition.ParentTypeID.String()
	}
	if link.Relationship.ChildEntityID.String() == entityID {
		return link.Definition.ChildTypeID.String()
	}
	return ""
}

// counterpartOf returns the entity at the other end of a link.
func counterpartOf(link apprelationship.EntityLink, entityID string) string {
	parent := link.Relationship.ParentEntityID.String()
	child := link.Relationship.ChildEntityID.String()
	switch entityID {
	case parent:
		return child
	case child:
		return parent
	default:
		return ""
	}
}

// directionMatches reports whether a link seen from a given role belongs to
// the rollup's direction. `role` is the queried entity's own side.
func directionMatches(direction, role string) bool {
	switch direction {
	case "child":
		return role == "parent"
	case "parent":
		return role == "child"
	default: // linked
		return true
	}
}

// attributeIDsNamed resolves an attribute internal name to every definition id
// it has across the counterparts' types.
//
// The name is resolved per TYPE, and a relationship can reach several types
// (a subtype, or a `linked` traversal that reaches both ends), so one name can
// be several ids.
func (m *Materializer) attributeIDsNamed(
	ctx context.Context,
	it *application.Interactors,
	entityIDs []string,
	name string,
) (map[string]bool, error) {
	types, err := m.typesOf(ctx, it, entityIDs)
	if err != nil {
		return nil, err
	}
	ids := map[string]bool{}
	for typeID := range types {
		attrs, aerr := it.TypeDefinitions().EffectiveAttributes(ctx, typeID)
		if aerr != nil {
			return nil, fmt.Errorf("load counterpart attributes: %w", aerr)
		}
		for _, a := range attrs {
			if a.Attribute.InternalName == name {
				ids[a.Attribute.ID.String()] = true
			}
		}
	}
	return ids, nil
}

// typesOf returns the distinct type definition ids the given entities belong
// to, read from the values they hold.
func (m *Materializer) typesOf(
	ctx context.Context,
	it *application.Interactors,
	entityIDs []string,
) (map[string]bool, error) {
	values, err := it.Values().ListByEntities(ctx, entityIDs)
	if err != nil {
		return nil, fmt.Errorf("load counterpart values: %w", err)
	}
	out := map[string]bool{}
	for _, v := range values {
		out[v.TypeDefinitionID.String()] = true
	}
	return out, nil
}

// linkPayload is the part of a relationship event the materializer needs: the
// two endpoints, whose rollups may now be stale.
type linkPayload struct {
	TenantID                 string `json:"tenant_id"`
	RelationshipDefinitionID string `json:"relationship_definition_id"`
	ParentEntityID           string `json:"parent_entity_id"`
	ChildEntityID            string `json:"child_entity_id"`
}

// handleLink recomputes both endpoints of a link that was created, removed or
// re-pinned.
//
// A rollup's inputs are on OTHER entities, so nothing about the rolling-up
// entity changes when a link does — no value event fires for it, and without
// this the aggregate would keep a total that no longer matches the links.
func (m *Materializer) handleLink(ctx context.Context, env events.Envelope) error {
	var p linkPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return fmt.Errorf("decode relationship event payload: %w", err)
	}
	tenant, err := valueobjects.ParseTenantID(p.TenantID)
	if err != nil {
		return err
	}
	ctx = uow.WithTenant(ctx, tenant)

	// The endpoints' declared types come from the relationship definition. An
	// entity whose every attribute is a rollup holds no value yet, so its type
	// is not discoverable from its values.
	parentType, childType := "", ""
	if def, derr := m.factory.New(uow.WithSystemAccess(ctx)).
		Relationships().GetDefinition(uow.WithSystemAccess(ctx), p.RelationshipDefinitionID); derr == nil {
		parentType = def.ParentTypeID.String()
		childType = def.ChildTypeID.String()
	}

	var errs []error
	for _, endpoint := range []struct{ entityID, typeID string }{
		{p.ParentEntityID, parentType},
		{p.ChildEntityID, childType},
	} {
		if endpoint.entityID == "" {
			continue
		}
		if err := m.recomputeEntityByID(ctx, endpoint.entityID, endpoint.typeID); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// cascadeToNeighbours recomputes the entities whose rollups read the entity
// that just changed.
//
// A value event names the entity that was written. Anything aggregating it is
// a different entity, which received no event of its own — so a dish's cost
// would sit unchanged after its ingredient's price moved, which is the whole
// point of the feature.
//
// The neighbours' own writes emit value events, which cascade again: a chain
// of rollup -> formula -> rollup converges without special handling, because
// re-setting an unchanged value emits nothing. The depth bound below is for
// the case that convergence does not hold — a cycle of aggregates — where the
// cascade must stop rather than run forever.
func (m *Materializer) cascadeToNeighbours(ctx context.Context, entityID string) error {
	depth := cascadeDepth(ctx)
	if depth >= maxCascadeDepth {
		return nil
	}
	ctx = withCascadeDepth(ctx, depth+1)

	it := m.factory.New(uow.WithSystemAccess(ctx))
	links, err := it.Relationships().ListByEntity(uow.WithSystemAccess(ctx), entityID)
	if err != nil {
		return fmt.Errorf("load relationships: %w", err)
	}

	var errs []error
	seen := map[string]bool{}
	for _, link := range links {
		other := counterpartOf(link, entityID)
		if other == "" || seen[other] {
			continue
		}
		seen[other] = true
		if err := m.recomputeEntityByID(ctx, other, declaredTypeOf(link, other)); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// maxCascadeDepth bounds how far one write's rollup cascade travels. A chain
// deeper than this is a modelling mistake — or a cycle — and the alternative
// to stopping is a recompute that never ends.
const maxCascadeDepth = 8

type cascadeDepthKey struct{}

func withCascadeDepth(ctx context.Context, depth int) context.Context {
	return context.WithValue(ctx, cascadeDepthKey{}, depth)
}

func cascadeDepth(ctx context.Context) int {
	if depth, ok := ctx.Value(cascadeDepthKey{}).(int); ok {
		return depth
	}
	return 0
}

// recomputeEntityByID recomputes an entity whose type is not known to the
// caller — a link endpoint, or a neighbour reached by the cascade.
//
// The type is read from the entity's own values, and from the DECLARED type of
// any relationship reaching it. Values alone are not enough: an entity that
// holds a rollup and nothing else — a dish whose every field is derived from
// its lines — has no values until the rollup produces one, so a values-only
// lookup would find no type, recompute nothing, and leave the aggregate empty
// for ever.
func (m *Materializer) recomputeEntityByID(ctx context.Context, entityID string, declared ...string) error {
	sysCtx := uow.WithSystemAccess(ctx)
	it := m.factory.New(sysCtx)
	types, err := m.typesOf(sysCtx, it, []string{entityID})
	if err != nil {
		return err
	}
	for _, typeID := range declared {
		if typeID != "" {
			types[typeID] = true
		}
	}
	var errs []error
	for typeID := range types {
		if err := m.Recompute(ctx, typeID, entityID); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
