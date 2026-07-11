package value

import (
	"fmt"

	"context"

	"github.com/zkrebbekx/flexitype/application/activity"
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

	target := make(map[valueobjects.AttributeDefinitionID]bool, len(cells))
	for _, c := range cells {
		id, err := valueobjects.ParseAttributeDefinitionID(c.AttributeDefinitionID)
		if err != nil {
			return domainerrors.NewValidation(err.Error())
		}
		target[id] = true
	}

	return i.uow.Execute(ctx, func(tx db.Transactor, c *uow.Collector) error {
		// Apply every target cell (upsert with full validation).
		for _, cell := range cells {
			raw, err := cellToRaw(valueobjects.DataType(cell.DataType), cell.Value)
			if err != nil {
				return domainerrors.NewValidation(err.Error())
			}
			if _, err := i.setWithin(ctx, tx, c, SetInput{
				AttributeDefinitionID: cell.AttributeDefinitionID,
				EntityID:              rawEntityID,
				TypeDefinitionID:      rawTypeDefID,
				Value:                 raw,
			}); err != nil {
				return err
			}
		}

		// Archive current values whose attribute is not in the target set.
		values := i.values.WithTx(tx)
		current, err := values.ListByEntity(ctx, domainvalue.EntityKey{
			TenantID: tenant, TypeDefinitionID: typeDefID, EntityID: entityID,
		})
		if err != nil {
			return fmt.Errorf("list current values: %w", err)
		}
		for _, av := range current {
			if target[av.AttributeDefinitionID()] {
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
