package dependency

import (
	"context"
	"sort"
	"time"

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

// MissingAttribute names a required attribute an entity has not yet filled.
type MissingAttribute struct {
	AttributeDefinitionID string `json:"attribute_definition_id"`
	InternalName          string `json:"internal_name"`
	DisplayName           string `json:"display_name"`
	Group                 string `json:"group,omitempty"`
}

// CompletenessOutput reports how much of an entity's effective required
// schema is filled. Requiredness is dependency-adjusted: an attribute a
// dependency makes required (or optional) for this entity's current values
// counts as its resolved state, not its declared flag. Score is filled /
// required in [0,1]; a schema with no required attributes scores 1.
type CompletenessOutput struct {
	EntityID         string             `json:"entity_id"`
	TypeDefinitionID string             `json:"type_definition_id"`
	Required         int                `json:"required"`
	Filled           int                `json:"filled"`
	Score            float64            `json:"score"`
	Missing          []MissingAttribute `json:"missing"`
}

// Completeness scores one entity against its type's effective required
// schema, listing the required attributes still missing a value.
func (i *Interactor) Completeness(ctx context.Context, rawTypeID, rawEntityID string) (*CompletenessOutput, error) {
	typeID, err := valueobjects.ParseTypeDefinitionID(rawTypeID)
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
	if err := uow.EnsureTenant(ctx, t.TenantID(), "type_definition", rawTypeID); err != nil {
		return nil, err
	}

	filled, err := i.entityFilled(ctx, t.TenantID(), typeID, entityID)
	if err != nil {
		return nil, err
	}
	out, err := i.scoreEntity(ctx, t, entityID, filled)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// entityFilled loads an entity's live values and returns the set of
// attribute IDs holding a value alongside the value map dependencies match
// against.
func (i *Interactor) entityFilled(
	ctx context.Context,
	tenant valueobjects.TenantID,
	typeID valueobjects.TypeDefinitionID,
	entityID valueobjects.EntityID,
) (map[valueobjects.AttributeDefinitionID]valueobjects.Value, error) {
	values, err := i.values.ListByEntity(ctx, domainvalue.EntityKey{
		TenantID:         tenant,
		TypeDefinitionID: typeID,
		EntityID:         entityID,
	})
	if err != nil {
		return nil, err
	}
	set := make(map[valueobjects.AttributeDefinitionID]valueobjects.Value, len(values))
	for _, av := range values {
		set[av.AttributeDefinitionID()] = av.Value()
	}
	return set, nil
}

// scoreEntity computes the completeness of one entity given its already
// loaded value set. It walks the type's inheritance chain, resolves each
// attribute's dependency-adjusted requiredness, and tallies filled vs
// required. Callers that score many entities of one type reuse this with
// per-entity value sets.
func (i *Interactor) scoreEntity(
	ctx context.Context,
	t *domaintypedef.TypeDefinition,
	entityID valueobjects.EntityID,
	filled map[valueobjects.AttributeDefinitionID]valueobjects.Value,
) (*CompletenessOutput, error) {
	chain, err := apptypedef.Chain(ctx, i.typeDefs, t)
	if err != nil {
		return nil, err
	}

	out := &CompletenessOutput{
		EntityID:         entityID.String(),
		TypeDefinitionID: t.ID().String(),
		Missing:          []MissingAttribute{},
	}
	seen := make(map[valueobjects.AttributeDefinitionID]bool)
	for _, link := range chain {
		attrs, _, err := i.attrs.ListByTypeDefinition(ctx, link.ID(), db.Page{Limit: 500})
		if err != nil {
			return nil, err
		}
		for _, a := range attrs {
			if seen[a.ID()] {
				continue
			}
			seen[a.ID()] = true

			required, err := i.attributeRequired(ctx, a, filled)
			if err != nil {
				return nil, err
			}
			if !required {
				continue
			}
			out.Required++
			if _, ok := filled[a.ID()]; ok {
				out.Filled++
				continue
			}
			s := a.Snapshot()
			out.Missing = append(out.Missing, MissingAttribute{
				AttributeDefinitionID: s.ID.String(),
				InternalName:          s.InternalName,
				DisplayName:           s.DisplayName,
				Group:                 s.Group,
			})
		}
	}
	sort.SliceStable(out.Missing, func(x, y int) bool {
		if out.Missing[x].Group != out.Missing[y].Group {
			return out.Missing[x].Group < out.Missing[y].Group
		}
		return out.Missing[x].InternalName < out.Missing[y].InternalName
	})
	out.Score = 1
	if out.Required > 0 {
		out.Score = float64(out.Filled) / float64(out.Required)
	}
	return out, nil
}

// EntityScore is one entity's completeness within a type aggregate.
type EntityScore struct {
	EntityID string  `json:"entity_id"`
	Score    float64 `json:"score"`
	Filled   int     `json:"filled"`
	Required int     `json:"required"`
}

// TypeCompletenessOutput aggregates completeness across a type's entities:
// the average score, how many are fully complete vs still incomplete, and a
// per-entity breakdown. Scoring is capped; when Count exceeds Scored the
// aggregate is over the first Scored entities and Truncated is set.
type TypeCompletenessOutput struct {
	TypeDefinitionID string        `json:"type_definition_id"`
	Count            int           `json:"count"`
	Scored           int           `json:"scored"`
	Truncated        bool          `json:"truncated"`
	AverageScore     float64       `json:"average_score"`
	Complete         int           `json:"complete"`
	Incomplete       int           `json:"incomplete"`
	Entities         []EntityScore `json:"entities"`
}

// maxCompletenessScan bounds how many entities a type aggregate scores in
// one call; beyond it the result is marked Truncated. A projection-backed
// rollup is the eventual answer for very large types.
const maxCompletenessScan = 1000

// TypeCompleteness scores every entity of a type (up to a cap) and returns
// the aggregate completeness. Entities of subtypes are scored against their
// own concrete type's schema.
func (i *Interactor) TypeCompleteness(ctx context.Context, rawTypeID string) (*TypeCompletenessOutput, error) {
	typeID, err := valueobjects.ParseTypeDefinitionID(rawTypeID)
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}
	t, err := i.typeDefs.Get(ctx, typeID)
	if err != nil {
		return nil, err
	}
	if err := uow.EnsureTenant(ctx, t.TenantID(), "type_definition", rawTypeID); err != nil {
		return nil, err
	}

	out := &TypeCompletenessOutput{TypeDefinitionID: typeID.String(), Entities: []EntityScore{}}
	types := map[valueobjects.TypeDefinitionID]*domaintypedef.TypeDefinition{typeID: t}
	var sum float64
	// Ask for the total once so Count reflects the full population even though
	// only the first maxCompletenessScan entities are scored.
	page := db.Page{Limit: 200, WantTotal: true}
	first := true
	for {
		summaries, total, err := i.values.ListEntities(ctx, t.TenantID(), []valueobjects.TypeDefinitionID{typeID}, page)
		if err != nil {
			return nil, err
		}
		if first {
			out.Count = total
			first = false
			page.WantTotal = false
		}
		for _, s := range summaries {
			et, ok := types[s.TypeDefinitionID]
			if !ok {
				et, err = i.typeDefs.Get(ctx, s.TypeDefinitionID)
				if err != nil {
					return nil, err
				}
				types[s.TypeDefinitionID] = et
			}
			filled, err := i.entityFilled(ctx, t.TenantID(), s.TypeDefinitionID, s.EntityID)
			if err != nil {
				return nil, err
			}
			score, err := i.scoreEntity(ctx, et, s.EntityID, filled)
			if err != nil {
				return nil, err
			}
			out.Entities = append(out.Entities, EntityScore{
				EntityID: score.EntityID, Score: score.Score,
				Filled: score.Filled, Required: score.Required,
			})
			sum += score.Score
			if score.Score >= 1 {
				out.Complete++
			} else {
				out.Incomplete++
			}
		}
		if len(out.Entities) >= maxCompletenessScan {
			out.Truncated = true
			break
		}
		// The repository over-fetches by one; a short page is the last one.
		if len(summaries) <= page.Limit {
			break
		}
		last := summaries[len(summaries)-1]
		page.Cursor = db.EncodeKeyset(last.LastUpdatedAt.UTC().Format(time.RFC3339Nano), last.EntityID.String())
	}
	out.Scored = len(out.Entities)
	if out.Scored > 0 {
		out.AverageScore = sum / float64(out.Scored)
	}
	return out, nil
}

// attributeRequired resolves whether an attribute is required for an entity
// given its current values, applying any dependency effect that overrides
// the declared required flag.
func (i *Interactor) attributeRequired(
	ctx context.Context,
	a *domainattribute.Definition,
	filled map[valueobjects.AttributeDefinitionID]valueobjects.Value,
) (bool, error) {
	deps, err := i.deps.ListByTarget(ctx, a.ID())
	if err != nil {
		return false, err
	}
	if len(deps) == 0 {
		return a.Required(), nil
	}
	schema, err := domaindependency.ResolveEffective(a, deps, filled, i.now())
	if err != nil {
		return false, err
	}
	return schema.Required, nil
}
