package value

import (
	"context"
	"fmt"

	"github.com/zkrebbekx/flexitype/application/appctx"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	domainattribute "github.com/zkrebbekx/flexitype/domain/attribute"
	domaindependency "github.com/zkrebbekx/flexitype/domain/dependency"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	domaintypedef "github.com/zkrebbekx/flexitype/domain/typedef"
	domainvalue "github.com/zkrebbekx/flexitype/domain/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
)

// requiredGateKey names the end-of-transaction check and its scratch state on
// the unit of work's collector.
const requiredGateKey = "value.required-on-write"

// entityRef identifies one entity a transaction wrote to.
type entityRef struct {
	tenant valueobjects.TenantID
	typeID valueobjects.TypeDefinitionID
	entity valueobjects.EntityID
}

// requiredGate accumulates the entities a transaction touched, so the
// dependency requirements that block a write are judged once, against the
// state the transaction is about to commit.
type requiredGate struct {
	seen  map[entityRef]bool
	order []entityRef
}

func (g *requiredGate) add(ref entityRef) {
	if g.seen[ref] {
		return
	}
	g.seen[ref] = true
	// Kept in first-touch order so the entity a caller is refused for is
	// stable, rather than whichever one a map iteration reached first.
	g.order = append(g.order, ref)
}

// noteWrite records that a caller wrote to an entity, and arms the
// end-of-transaction check.
//
// Internal writes are excluded. The materializer writes computed values in its
// own transaction, and gating those would make a recompute fail whenever the
// entity happened to be incomplete — turning a reporting gap into a broken
// derived value. The requirement is about what a CALLER submits.
func (i *Interactor) noteWrite(c *uow.Collector, tx db.Transactor, ref entityRef, internal bool) {
	if c == nil || internal {
		return
	}
	gate, _ := c.Stash(requiredGateKey, func() any {
		return &requiredGate{seen: make(map[entityRef]bool, 1)}
	}).(*requiredGate)
	if gate == nil {
		return
	}
	gate.add(ref)
	c.CheckBeforeCommitOnce(requiredGateKey, func(ctx context.Context) error {
		return i.enforceRequiredOnWrite(ctx, tx, gate)
	})
}

// enforceRequiredOnWrite refuses the transaction when an entity it wrote to
// leaves a dependency's demand unmet.
//
// Only a requirement that came from a MATCHED dependency is gated, and only
// one asking for on_write enforcement. An attribute's own declared required
// flag is never gated here: it describes the finished record, so gating it
// would make an entity impossible to create — the first value written always
// leaves the others empty.
func (i *Interactor) enforceRequiredOnWrite(ctx context.Context, tx db.Transactor, gate *requiredGate) error {
	if gate == nil || len(gate.order) == 0 {
		return nil
	}
	attrs := i.attrs.WithTx(tx)
	typeDefs := i.typeDefs.WithTx(tx)
	deps := i.deps.WithTx(tx)
	reads, ok := i.values.WithTx(tx).(appctx.ValueReader)
	if !ok {
		return nil
	}

	// One transaction commonly writes many entities of the same type — an
	// import chunk writes hundreds — so the schema walk is done once per type.
	schemas := make(map[valueobjects.TypeDefinitionID][]*domainattribute.Definition, 1)
	targeting := make(map[valueobjects.AttributeDefinitionID][]*domaindependency.Dependency, 1)

	for _, ref := range gate.order {
		values, err := reads.ListByEntity(ctx, domainvalue.EntityKey{
			TenantID:         ref.tenant,
			TypeDefinitionID: ref.typeID,
			EntityID:         ref.entity,
		})
		if err != nil {
			return fmt.Errorf("load entity values: %w", err)
		}
		// An entity with nothing left is one that was deleted, not one that is
		// incomplete. Refusing here would make an entity with a matched
		// requirement impossible to remove.
		if len(values) == 0 {
			continue
		}
		filled := make(map[valueobjects.AttributeDefinitionID][]valueobjects.Value, len(values))
		for _, av := range values {
			id := av.AttributeDefinitionID()
			filled[id] = append(filled[id], av.Value())
		}

		schema, err := gateSchema(ctx, typeDefs, attrs, schemas, ref.typeID)
		if err != nil {
			return err
		}
		for _, a := range schema {
			targets, ok := targeting[a.ID()]
			if !ok {
				targets, err = deps.ListByTarget(ctx, a.ID())
				if err != nil {
					return fmt.Errorf("load dependencies: %w", err)
				}
				targeting[a.ID()] = targets
			}
			if len(targets) == 0 {
				continue
			}
			// The same resolver every other path uses, with the caller's own
			// facts and the tenant-local day, so a rule cannot mean one thing
			// to the gate and another to the report that describes it.
			eff, err := domaindependency.ResolveEffectiveWithContext(
				a, targets, filled, uow.ContextValuesFromContext(ctx), uow.LocalNow(ctx))
			if err != nil {
				return err
			}
			if !eff.RequiredByDependency || !eff.Required {
				continue
			}
			if eff.RequiredEnforcement.Or(domaindependency.DefaultEnforcement) != domaindependency.EnforceOnWrite {
				continue
			}
			if _, ok := filled[a.ID()]; ok {
				continue
			}
			// The rule is enforced whether or not the caller may READ the
			// attribute it demands: it is an invariant about what gets stored,
			// and skipping it for a restricted principal would leave exactly
			// one way to write the state the rule forbids.
			//
			// Naming it is a different question. An unreadable attribute's
			// name is not disclosed, for the same reason completeness leaves
			// it out of Missing.
			if !uow.AccessFromContext(ctx).CanRead(a.InternalName()) {
				return domainerrors.NewDependencyViolation(
					"an attribute dependency requires a value this entity does not have",
					"entity", ref.entity.String(),
				)
			}
			return domainerrors.NewDependencyViolation(
				fmt.Sprintf("an attribute dependency requires a value for %q", a.InternalName()),
				"attribute", a.InternalName(),
				"entity", ref.entity.String(),
			)
		}
	}
	return nil
}

// gateSchema returns a type's full attribute set, including inherited ones,
// memoized for the life of one check.
func gateSchema(
	ctx context.Context,
	typeDefs domaintypedef.Repository,
	attrs domainattribute.Repository,
	cache map[valueobjects.TypeDefinitionID][]*domainattribute.Definition,
	typeID valueobjects.TypeDefinitionID,
) ([]*domainattribute.Definition, error) {
	if got, ok := cache[typeID]; ok {
		return got, nil
	}
	t, err := typeDefs.Get(ctx, typeID)
	if err != nil {
		return nil, err
	}
	chain, err := apptypedef.Chain(ctx, typeDefs, t)
	if err != nil {
		return nil, err
	}
	var out []*domainattribute.Definition
	seen := make(map[valueobjects.AttributeDefinitionID]bool)
	for _, link := range chain {
		list, err := domainattribute.ListAllForType(ctx, attrs, link.ID())
		if err != nil {
			return nil, err
		}
		for _, a := range list {
			if seen[a.ID()] {
				continue
			}
			seen[a.ID()] = true
			out = append(out, a)
		}
	}
	cache[typeID] = out
	return out, nil
}
