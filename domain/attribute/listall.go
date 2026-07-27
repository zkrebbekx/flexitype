package attribute

import (
	"context"

	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
)

// MaxPerType caps the attributes one type may declare.
//
// Every effective-schema walk used to request a single page of 500 and ignore
// the reported total, with nothing capping attributes per type. So attribute
// 501 existed but was invisible to validation, FQL, import and export, cycle
// detection and completeness scoring — the attribute was writable through its
// own endpoint and absent from every surface that reasons about the schema.
//
// A cap plus a loud read is the fix. The cap is enforced at Create, above any
// schema a person would model by hand, and ListAllForType refuses to return a
// truncated set, so a schema that somehow exceeds it fails visibly instead of
// silently losing its tail.
const MaxPerType = 1000

// ListAllForType returns every attribute a type declares, or an error if the
// store holds more than the cap. It never returns a partial set.
func ListAllForType(ctx context.Context, repo Repository, typeDefID valueobjects.TypeDefinitionID) ([]*Definition, error) {
	// Ask for one more than the cap so an over-cap type is detectable from the
	// page alone, and for the total so the error can name the real number.
	page := db.Page{Limit: MaxPerType + 1, WantTotal: true}
	items, total, err := repo.ListByTypeDefinition(ctx, typeDefID, page)
	if err != nil {
		return nil, err
	}
	if len(items) > MaxPerType || (total > 0 && total > len(items)) {
		return nil, domainerrors.NewValidation(
			"type declares more attributes than flexitype can resolve in one schema walk; "+
				"split the type or raise attribute.MaxPerType",
			"type_definition_id", typeDefID.String(),
			"attributes", total,
			"limit", MaxPerType)
	}
	return items, nil
}
