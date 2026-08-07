package value

import (
	"encoding/json"
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
	if _, err := valueobjects.ParseTypeDefinitionID(rawTypeDefID); err != nil {
		return domainerrors.NewValidation(err.Error())
	}
	entityID, err := valueobjects.ParseEntityID(rawEntityID)
	if err != nil {
		return domainerrors.NewValidation(err.Error())
	}
	tenant := uow.TenantFromContext(ctx)

	// The target set is keyed by (attribute, scope, VALUE). A scoped value
	// lives at its own (locale, channel), so restore preserves and archives
	// each scope independently rather than collapsing them onto the base
	// value — and the value itself is part of the key because a multi-valued
	// attribute holds several values at one scope.
	//
	// Keying on (attribute, scope) alone meant an attribute that held [A] at
	// snapshot time and holds [A, B] now still held [A, B] after "restore":
	// B's presence satisfied the key, so nothing archived it. The restore
	// silently did not restore.
	type valueKey struct {
		attr  valueobjects.AttributeDefinitionID
		scope valueobjects.Scope
		value string
	}
	target := make(map[valueKey]bool, len(cells))
	for _, c := range cells {
		id, err := valueobjects.ParseAttributeDefinitionID(c.AttributeDefinitionID)
		if err != nil {
			return domainerrors.NewValidation(err.Error())
		}
		v, err := cellValue(c)
		if err != nil {
			return domainerrors.NewValidation(err.Error())
		}
		target[valueKey{
			attr:  id,
			scope: valueobjects.Scope{Locale: c.Locale, Channel: c.Channel},
			value: v.String(),
		}] = true
	}

	return i.uow.Execute(ctx, func(tx db.Transactor, c *uow.Collector) error {
		// Apply every target cell (upsert with full validation), at its scope.
		for _, cell := range cells {
			raw, err := cellRaw(cell)
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
		//
		// The read is keyed on (tenant, entity) and NOT on the caller's type.
		// The caller passes the type captured in the revision, and the write
		// path re-anchors every value row when the anchor narrows — so a
		// type-keyed read under the captured type matched zero rows after a
		// narrowing, archived nothing, and the restore silently kept values
		// the snapshot does not hold. The anchor is an invariant of the
		// entity, so the entity-keyed read returns exactly the rows the
		// type-keyed read returned whenever that one worked at all.
		values := i.values.WithTx(tx)
		reads := values.(appctx.ValueReader)
		current, err := reads.ListByEntities(ctx, tenant, []valueobjects.EntityID{entityID})
		if err != nil {
			return fmt.Errorf("list current values: %w", err)
		}
		for _, av := range current {
			if target[valueKey{
				attr:  av.AttributeDefinitionID(),
				scope: av.Scope(),
				value: av.Value().String(),
			}] {
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
			i.gcMediaAfterCommit(tx, before.ID, before.Value)
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

// cellValue decodes a snapshot cell to the value it represents.
//
// The typed form is authoritative when present: the display form is not JSON
// for media (a bare object key) or quantity ("10 kg"), so restoring either
// through the import decoder failed and aborted the whole unit of work. Cells
// from revisions captured before the typed form existed still take the old
// path, so their behaviour is unchanged.
func cellValue(c apprevision.SnapshotCell) (valueobjects.Value, error) {
	if len(c.Typed) > 0 {
		return valueobjects.UnmarshalTypedValue(c.Typed)
	}
	raw, err := cellToRaw(valueobjects.DataType(c.DataType), c.Value)
	if err != nil {
		return valueobjects.Value{}, err
	}
	return valueobjects.ParseValue(valueobjects.DataType(c.DataType), raw)
}

// cellRaw renders a snapshot cell as the natural JSON the value write path
// accepts. Going back through the write path (rather than storing the decoded
// value directly) is what re-runs validation — and, for a quantity, re-bases
// the magnitude against the unit family's CURRENT factors rather than the
// factors in force when the revision was taken.
func cellRaw(c apprevision.SnapshotCell) (json.RawMessage, error) {
	if len(c.Typed) == 0 {
		return cellToRaw(valueobjects.DataType(c.DataType), c.Value)
	}
	v, err := valueobjects.UnmarshalTypedValue(c.Typed)
	if err != nil {
		return nil, err
	}
	return v.MarshalJSON()
}
