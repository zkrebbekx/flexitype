package value

import (
	"context"

	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	domainattribute "github.com/zkrebbekx/flexitype/domain/attribute"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
)

// AppliedDefault names one attribute a run seeded, and the value it wrote.
type AppliedDefault struct {
	AttributeDefinitionID string `json:"attribute_definition_id"`
	InternalName          string `json:"internal_name"`
	Value                 string `json:"value"`
}

// ApplyDefaultsOutput reports what one run seeded.
type ApplyDefaultsOutput struct {
	EntityID string `json:"entity_id"`
	// Applied lists the attributes this run wrote, in schema order.
	Applied []AppliedDefault `json:"applied"`
	// Skipped counts the declared defaults the entity already satisfied.
	Skipped int `json:"skipped"`
}

// ApplyDefaults writes every declared default the entity does not already
// hold, resolving a dynamic default (now, today, a relative period) at the
// moment of the call.
//
// It exists because a declared default did nothing at all. Defaults were
// decoded, validated, stored, cloned, exported and rendered in the console,
// and DefaultFor had no caller outside its own tests: an attribute declared
// with `today` and required never received a value, and completeness scored
// it absent forever, with nothing reporting the gap.
//
// The rules it applies:
//
//   - Only the BASE scope is seeded. A default has no locale or channel, so
//     inventing one per scope would guess at a schema the author did not
//     write.
//   - An attribute that already holds a live value at the base scope is
//     left alone, whatever its value. Seeding is not repair.
//   - A COMPUTED attribute is skipped. Its value is derived, and the first
//     recompute would overwrite whatever a default put there.
//   - Every write goes through the ordinary value path, so validation,
//     constraints, dependencies, uniqueness, activity, events and the
//     computed materializer all behave exactly as they do for a written
//     value. A default that violates its own constraints is reported rather
//     than forced.
//
// Applying is EXPLICIT: it seeds one entity when called. Seeding implicitly
// on the write that creates an entity would put a probe on the hottest path
// and change what every existing deployment writes, which is a decision for
// the deployment rather than for this function.
func (i *Interactor) ApplyDefaults(ctx context.Context, rawTypeDefID, rawEntityID string) (*ApplyDefaultsOutput, error) {
	typeID, err := valueobjects.ParseTypeDefinitionID(rawTypeDefID)
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}
	entityID, err := valueobjects.ParseEntityID(rawEntityID)
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}
	t, err := i.typeDefs.Get(ctx, typeID)
	if err != nil {
		return nil, err
	}
	if err := uow.EnsureTenant(ctx, t.TenantID(), "type_definition", rawTypeDefID); err != nil {
		return nil, err
	}

	// The whole chain's attributes, nearest declaration first, so a subtype
	// that redeclares a name keeps its own default.
	chain, err := apptypedef.Chain(ctx, i.typeDefs, t)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var candidates []domainattribute.Snapshot
	for _, link := range chain {
		attrs, aerr := domainattribute.ListAllForType(ctx, i.attrs, link.ID())
		if aerr != nil {
			return nil, aerr
		}
		for _, a := range attrs {
			s := a.Snapshot()
			if seen[s.InternalName] {
				continue
			}
			seen[s.InternalName] = true
			if s.DefaultValue == nil || s.Computed != nil {
				continue
			}
			candidates = append(candidates, s)
		}
	}

	current, err := i.reads.ListByEntities(ctx, t.TenantID(), []valueobjects.EntityID{entityID})
	if err != nil {
		return nil, err
	}
	held := map[string]bool{}
	for _, av := range current {
		// The base scope only: a scoped value does not satisfy the base one.
		if av.Scope().Locale == "" && av.Scope().Channel == "" {
			held[av.AttributeDefinitionID().String()] = true
		}
	}

	out := &ApplyDefaultsOutput{EntityID: rawEntityID, Applied: []AppliedDefault{}}
	var pending []domainattribute.Snapshot
	for _, s := range candidates {
		if held[s.ID.String()] {
			out.Skipped++
			continue
		}
		pending = append(pending, s)
	}
	if len(pending) == 0 {
		return out, nil
	}

	now := i.now()
	err = i.uow.Execute(ctx, func(tx db.Transactor, c *uow.Collector) error {
		out.Applied = out.Applied[:0]
		for _, s := range pending {
			def := domainattribute.Rehydrate(s)
			v, derr := def.DefaultFor(now)
			if derr != nil {
				return domainerrors.NewValidation("cannot resolve the default",
					"attribute", s.InternalName, "error", derr.Error())
			}
			if v.IsZero() {
				continue
			}
			raw, merr := v.MarshalJSON()
			if merr != nil {
				return merr
			}
			if _, serr := i.setWithin(ctx, tx, c, SetInput{
				AttributeDefinitionID: s.ID.String(),
				EntityID:              rawEntityID,
				TypeDefinitionID:      rawTypeDefID,
				Value:                 raw,
			}); serr != nil {
				return serr
			}
			out.Applied = append(out.Applied, AppliedDefault{
				AttributeDefinitionID: s.ID.String(),
				InternalName:          s.InternalName,
				Value:                 v.String(),
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
