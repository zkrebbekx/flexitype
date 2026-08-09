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
	// fullView judges this entity over every value it holds rather than the
	// caller's readable subset. Set for a removal; see noteRemoval.
	fullView bool
}

// requiredGate accumulates the entities a transaction touched, so the
// dependency requirements that block a write are judged once, against the
// state the transaction is about to commit.
type requiredGate struct {
	seen  map[entityRef]bool
	order []entityRef
	// removed holds what a removal took away, so the check can reconstruct the
	// state before it and judge the transition rather than the outcome.
	removed map[valueobjects.EntityID][]*domainvalue.AttributeValue
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
	i.noteWriteFull(c, tx, ref, internal, false)
}

// noteRemoval records a removal, and what it took away.
//
// A removal is judged on the TRANSITION it causes, over the entity's full
// value set: refused only when the entity was not violating a rule before and
// is after. Two things have to hold at once, and only this shape gets both.
//
// It must not be judged over the caller's view alone. A principal permitted to
// remove the demanded value, but not to read the rule's source, would take the
// value away unopposed and leave the entity in exactly the state the rule
// forbids — after which every OTHER principal, admin included, is refused
// every write to that entity until the value comes back. One least-privileged
// account could do that across a tenant.
//
// It must also not be judged on the entity's state alone, which is what this
// did first. Resolving over the full set meant a restricted principal removing
// an UNRELATED readable value was refused exactly when a rule keyed on a value
// it cannot read happened to fire — one bit of that value per probe, and the
// refusal named the attribute. The justification offered at the time, that the
// caller could infer the same bit from a write, was simply wrong: the write
// path does not fire a rule keyed on an unreadable source.
//
// Judging the transition keeps the wedge closed — removing the demanded value
// still transitions the entity into the forbidden state, so it is still
// refused — while a pre-existing unmet requirement is no longer probeable,
// because it was already unmet before the removal.
func (i *Interactor) noteRemoval(
	c *uow.Collector,
	tx db.Transactor,
	ref entityRef,
	removed *domainvalue.AttributeValue,
	internal bool,
) {
	if c == nil || internal || removed == nil {
		return
	}
	gate, _ := c.Stash(requiredGateKey, func() any {
		return &requiredGate{seen: make(map[entityRef]bool, 1)}
	}).(*requiredGate)
	if gate == nil {
		return
	}
	ref.fullView = true
	gate.add(ref)
	if gate.removed == nil {
		gate.removed = make(map[valueobjects.EntityID][]*domainvalue.AttributeValue, 1)
	}
	gate.removed[ref.entity] = append(gate.removed[ref.entity], removed)
	c.CheckBeforeCommitOnce(requiredGateKey, func(ctx context.Context) error {
		return i.enforceRequiredOnWrite(ctx, tx, gate)
	})
}

func (i *Interactor) noteWriteFull(c *uow.Collector, tx db.Transactor, ref entityRef, internal, fullView bool) {
	if c == nil || internal {
		return
	}
	if fullView {
		ref.fullView = true
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
		if !ref.fullView {
			values, err = i.readableOnly(ctx, tx, values)
			if err != nil {
				return err
			}
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
			if !ref.fullView && !uow.AccessFromContext(ctx).CanRead(a.InternalName()) {
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
			// A removal is judged on the TRANSITION it caused. If the entity
			// was already violating this rule before, the removal did not
			// create the state and refusing would answer a question about
			// values the caller may not be able to read — see noteRemoval.
			if ref.fullView {
				was, berr := violatedBefore(ctx, a, targets, filled, gate.removed[ref.entity])
				if berr != nil {
					return berr
				}
				if was {
					continue
				}
			}
			// An unreadable attribute is never named. Completeness already
			// leaves such a name out of Missing, and a refusal that prints it
			// discloses exactly what the field ACL hides.
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

// violatedBefore reports whether this rule was ALREADY unsatisfied before the
// removal, by resolving it again over the entity's values plus what was taken
// away.
//
// Refusing only on the transition is what keeps a removal from answering a
// question about a hidden value: a requirement that was already unmet stays
// unmet, and the caller learns nothing it did not already cause.
func violatedBefore(
	ctx context.Context,
	a *domainattribute.Definition,
	targets []*domaindependency.Dependency,
	filled map[valueobjects.AttributeDefinitionID][]valueobjects.Value,
	removed []*domainvalue.AttributeValue,
) (bool, error) {
	if len(removed) == 0 {
		return false, nil
	}
	before := make(map[valueobjects.AttributeDefinitionID][]valueobjects.Value, len(filled)+1)
	for id, vs := range filled {
		before[id] = vs
	}
	for _, av := range removed {
		id := av.AttributeDefinitionID()
		before[id] = append(before[id], av.Value())
	}
	eff, err := domaindependency.ResolveEffectiveWithContext(
		a, targets, before, uow.ContextValuesFromContext(ctx), uow.LocalNow(ctx))
	if err != nil {
		return false, err
	}
	if !eff.Required ||
		eff.RequiredEnforcement.Or(domaindependency.DefaultEnforcement) != domaindependency.EnforceOnWrite {
		return false, nil
	}
	_, ok := before[a.ID()]
	return !ok, nil
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
