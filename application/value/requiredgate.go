package value

import (
	"context"
	"fmt"

	"github.com/zkrebbekx/flexitype/application/appctx"
	"github.com/zkrebbekx/flexitype/application/fieldacl"
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

	// One question, once per transaction: does this tenant have any rule that
	// refuses a write? Enforcement is opt-in per rule, so for almost every
	// deployment the answer is no and the whole check costs a single indexed
	// query.
	//
	// This walked the entity's full schema instead, asking for the
	// dependencies targeting each attribute in turn. That is one query per
	// attribute on EVERY value write — a 200-attribute type paid 213 queries
	// where it used to pay 9 — and it was paid by tenants using no rules at
	// all, inside the transaction, while the attribute-definition row lock
	// was held. Measured, it took concurrent writes on such a type from 133/s
	// to 8.6/s.
	blocking, err := deps.ListEnforcedOnWrite(ctx, gate.order[0].tenant)
	if err != nil {
		return fmt.Errorf("load enforced dependencies: %w", err)
	}
	if len(blocking) == 0 {
		return nil
	}
	// Only the attributes some rule actually blocks on are candidates. The
	// rest of the schema is irrelevant no matter how large it is.
	candidates := make(map[valueobjects.AttributeDefinitionID]bool, len(blocking))
	for _, d := range blocking {
		candidates[d.TargetAttributeID()] = true
	}
	// Asserted, not tested: everywhere else in this package the same
	// assertion is unchecked. Returning nil on a failed assertion would make
	// enforcement disappear silently on a backend that does not satisfy it,
	// which is the one failure mode a rule that refuses writes must not have.
	reads := i.values.WithTx(tx).(appctx.ValueReader)

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
		// Resolve against the caller's READABLE values, which is what every
		// other path that resolves a dependency does — completeness and the
		// effective schema both filter, and both say why: a rule keyed on a
		// value the caller cannot see turns the answer into an oracle over
		// that value.
		//
		// This gate is a louder oracle than a report, because it answers with
		// a refusal on a write to ANY attribute of the entity. Reading raw
		// values here also made the gate disagree with the effective schema,
		// which told the same caller the attribute was not required and then
		// watched the write be refused for that requirement. A validation
		// feature whose description and enforcement disagree is the specific
		// failure this codebase keeps paying for.
		//
		// The cost is stated rather than hidden: a principal who cannot see a
		// rule's source can write a state that principal cannot see is
		// forbidden. That is the same trade the read paths already make, and
		// it is the one that keeps the three answers consistent.
		values, err = i.readableOnly(ctx, tx, values)
		if err != nil {
			return err
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
			// Not a target of any blocking rule in this tenant — the common
			// case for all but a handful of attributes.
			if !candidates[a.ID()] {
				continue
			}
			// An attribute the caller may not read is skipped entirely, the
			// same way completeness leaves it out of Missing. With the value
			// set filtered above, a restricted attribute always looks absent,
			// so enforcing it would refuse every write this caller makes to
			// the entity, for a requirement it cannot see, name or satisfy.
			if !uow.AccessFromContext(ctx).CanRead(a.InternalName()) {
				continue
			}
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
			// Only a MATCHED dependency asking for on_write blocks. An
			// attribute's own declared required flag never reaches here,
			// because the resolver raises the mode only for a dependency that
			// demanded the value — see EffectiveSchema.RequiredEnforcement.
			if !eff.Required ||
				eff.RequiredEnforcement.Or(domaindependency.DefaultEnforcement) != domaindependency.EnforceOnWrite {
				continue
			}
			if _, ok := filled[a.ID()]; ok {
				continue
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

// readableOnly drops the values of attributes the principal may not read, so
// the gate resolves rules over the same value set the read paths do. It
// mirrors application/dependency.readableOnly, bound to this transaction.
func (i *Interactor) readableOnly(
	ctx context.Context,
	tx db.Transactor,
	values []*domainvalue.AttributeValue,
) ([]*domainvalue.AttributeValue, error) {
	if len(values) == 0 || uow.AccessFromContext(ctx).Admin {
		return values, nil
	}
	ids := make([]valueobjects.AttributeDefinitionID, 0, len(values))
	for _, av := range values {
		ids = append(ids, av.AttributeDefinitionID())
	}
	readable, err := fieldacl.New(i.attrs.WithTx(tx)).Readable(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := values[:0]
	for _, av := range values {
		if readable[av.AttributeDefinitionID().String()] {
			out = append(out, av)
		}
	}
	return out, nil
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
