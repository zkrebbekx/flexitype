package value

import (
	"fmt"

	"context"

	"github.com/zkrebbekx/flexitype/application/activity"
	"github.com/zkrebbekx/flexitype/application/appctx"
	apprevision "github.com/zkrebbekx/flexitype/application/revision"
	"github.com/zkrebbekx/flexitype/application/uow"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	domainvalue "github.com/zkrebbekx/flexitype/domain/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
)

// ApplySnapshot makes an entity's live values match a target set in one unit
// of work: every target cell is set through the normal validation path and
// any current value whose attribute is absent from the target is archived.
// It is the write half of a revision restore. The value strings are the
// canonical Value.String forms, inverted per data type via cellToRaw.
func (i *Interactor) ApplySnapshot(ctx context.Context, rawTypeDefID, rawEntityID string, cells []apprevision.SnapshotCell) error {
	typeDefID, err := valueobjects.ParseTypeDefinitionID(rawTypeDefID)
	if err != nil {
		return domainerrors.NewValidation(err.Error())
	}
	entityID, err := valueobjects.ParseEntityID(rawEntityID)
	if err != nil {
		return domainerrors.NewValidation(err.Error())
	}
	tenant := uow.TenantFromContext(ctx)

	// The target set is keyed by (attribute, scope): a scoped value lives at
	// its own (locale, channel), so restore must preserve and archive each
	// scope independently rather than collapsing them onto the base value.
	type scopeKey struct {
		attr  valueobjects.AttributeDefinitionID
		scope valueobjects.Scope
	}
	target := make(map[scopeKey]bool, len(cells))
	for _, c := range cells {
		id, err := valueobjects.ParseAttributeDefinitionID(c.AttributeDefinitionID)
		if err != nil {
			return domainerrors.NewValidation(err.Error())
		}
		target[scopeKey{attr: id, scope: valueobjects.Scope{Locale: c.Locale, Channel: c.Channel}}] = true
	}

	return i.uow.Execute(ctx, func(tx db.Transactor, c *uow.Collector) error {
		// Apply every target cell (upsert with full validation), at its scope.
		for _, cell := range cells {
			raw, err := cellToRaw(valueobjects.DataType(cell.DataType), cell.Value)
			if err != nil {
				return domainerrors.NewValidation(err.Error())
			}
			if _, err := i.setWithin(ctx, tx, c, SetInput{
				AttributeDefinitionID: cell.AttributeDefinitionID,
				EntityID:              rawEntityID,
				TypeDefinitionID:      rawTypeDefID,
				Locale:                cell.Locale,
				Channel:               cell.Channel,
				Value:                 raw,
			}); err != nil {
				return err
			}
		}

		// Archive current values whose (attribute, scope) is not in the target.
		values := i.values.WithTx(tx)
		reads := values.(appctx.ValueReader)
		current, err := reads.ListByEntity(ctx, domainvalue.EntityKey{
			TenantID: tenant, TypeDefinitionID: typeDefID, EntityID: entityID,
		})
		if err != nil {
			return fmt.Errorf("list current values: %w", err)
		}
		for _, av := range current {
			if target[scopeKey{attr: av.AttributeDefinitionID(), scope: av.Scope()}] {
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
		return nil
	})
}
