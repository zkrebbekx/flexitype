package value

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/zkrebbekx/flexitype/application/activity"
	"github.com/zkrebbekx/flexitype/application/uow"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	domainvalue "github.com/zkrebbekx/flexitype/domain/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
)

// Mutation is one deferred value change collected in a change-set: a scoped
// value set, or the removal of a scoped value.
type Mutation struct {
	Kind                  string          `json:"kind"` // set | remove
	AttributeDefinitionID string          `json:"attribute_definition_id"`
	EntityID              string          `json:"entity_id"`
	TypeDefinitionID      string          `json:"type_definition_id,omitempty"`
	Locale                string          `json:"locale,omitempty"`
	Channel               string          `json:"channel,omitempty"`
	Value                 json.RawMessage `json:"value,omitempty"`
}

// The supported mutation kinds.
const (
	MutationSet    = "set"
	MutationRemove = "remove"
)

// ApplyMutations applies a batch of value mutations in one unit of work:
// either every mutation lands (with its events and activity) or the whole
// batch rolls back. The first failing mutation's error is returned, so a
// change-set publish is atomic and reports why it failed.
func (i *Interactor) ApplyMutations(ctx context.Context, muts []Mutation) error {
	if len(muts) == 0 {
		return nil
	}
	tenant := uow.TenantFromContext(ctx)
	return i.uow.Execute(ctx, func(tx db.Transactor, c *uow.Collector) error {
		for idx, m := range muts {
			switch m.Kind {
			case MutationSet:
				if _, err := i.setWithin(ctx, tx, c, SetInput{
					AttributeDefinitionID: m.AttributeDefinitionID,
					EntityID:              m.EntityID,
					TypeDefinitionID:      m.TypeDefinitionID,
					Locale:                m.Locale,
					Channel:               m.Channel,
					Value:                 m.Value,
				}); err != nil {
					return &batchItemError{index: idx, err: err}
				}
			case MutationRemove:
				if err := i.removeScopedWithin(ctx, tx, c, tenant, m); err != nil {
					return &batchItemError{index: idx, err: err}
				}
			default:
				return &batchItemError{index: idx, err: domainerrors.NewValidation("unknown mutation kind", "kind", m.Kind)}
			}
		}
		return nil
	})
}

// Preview returns an entity's live values with a change-set's mutations
// overlaid in memory — the live data is untouched. Set mutations override
// or add a value for their scope; remove mutations drop it.
func (i *Interactor) Preview(ctx context.Context, rawTypeID, rawEntityID string, muts []Mutation) ([]domainvalue.Snapshot, error) {
	live, err := i.ListByEntity(ctx, rawTypeID, rawEntityID)
	if err != nil {
		return nil, err
	}
	// Key live values by (attribute, locale, channel).
	type key struct{ attr, locale, channel string }
	byKey := map[key]domainvalue.Snapshot{}
	order := []key{}
	for _, s := range live {
		k := key{s.AttributeDefinitionID.String(), s.Locale, s.Channel}
		if _, ok := byKey[k]; !ok {
			order = append(order, k)
		}
		byKey[k] = s
	}
	for _, m := range muts {
		if m.EntityID != rawEntityID {
			continue
		}
		k := key{m.AttributeDefinitionID, m.Locale, m.Channel}
		switch m.Kind {
		case MutationRemove:
			delete(byKey, k)
		case MutationSet:
			defID, err := valueobjects.ParseAttributeDefinitionID(m.AttributeDefinitionID)
			if err != nil {
				return nil, domainerrors.NewValidation(err.Error())
			}
			def, err := i.attrs.Get(ctx, defID)
			if err != nil {
				return nil, err
			}
			v, err := valueobjects.ParseValue(def.DataType(), m.Value)
			if err != nil {
				return nil, domainerrors.NewValidation(err.Error())
			}
			if _, ok := byKey[k]; !ok {
				order = append(order, k)
			}
			byKey[k] = domainvalue.Snapshot{
				AttributeDefinitionID: defID,
				EntityID:              valueobjects.EntityID(rawEntityID),
				Locale:                m.Locale,
				Channel:               m.Channel,
				Value:                 v,
			}
		}
	}
	out := make([]domainvalue.Snapshot, 0, len(byKey))
	for _, k := range order {
		if s, ok := byKey[k]; ok {
			out = append(out, s)
		}
	}
	return out, nil
}

// removeScopedWithin archives the live value identified by (entity,
// attribute, locale, channel) inside an existing unit of work.
func (i *Interactor) removeScopedWithin(ctx context.Context, tx db.Transactor, c *uow.Collector, tenant valueobjects.TenantID, m Mutation) error {
	defID, err := valueobjects.ParseAttributeDefinitionID(m.AttributeDefinitionID)
	if err != nil {
		return domainerrors.NewValidation(err.Error())
	}
	entityID, err := valueobjects.ParseEntityID(m.EntityID)
	if err != nil {
		return domainerrors.NewValidation(err.Error())
	}
	values := i.values.WithTx(tx)
	existing, err := values.FindByDefinitionAndEntity(ctx, defID, entityID)
	if err != nil {
		return fmt.Errorf("load value to remove: %w", err)
	}
	for _, av := range existing {
		if av.Scope().Locale != m.Locale || av.Scope().Channel != m.Channel {
			continue
		}
		before := av.Snapshot()
		evts, err := av.Remove(i.now())
		if err != nil {
			return err
		}
		if err := values.Save(ctx, av); err != nil {
			return fmt.Errorf("archive value: %w", err)
		}
		i.gcMediaAfterCommit(tx, before.Value)
		c.CollectEvents(evts...)
		c.RecordChange(activity.Change{
			Entity:   domainvalue.AggregateType,
			EntityID: av.ID().String(),
			Action:   activity.ActionRemoved,
			Before:   before,
		})
	}
	_ = tenant
	return nil
}
