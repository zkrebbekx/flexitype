package value

import (
	"context"
	"encoding/json"

	"github.com/zkrebbekx/flexitype/application/uow"
	domainattribute "github.com/zkrebbekx/flexitype/domain/attribute"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// quantityValue converts a {magnitude, unit} input to a quantity value in the
// attribute's unit family, computing the base-unit magnitude used for
// comparison. A unit outside the family, or a missing family, is a
// validation error.
func (i *Interactor) quantityValue(ctx context.Context, def *domainattribute.Definition, raw json.RawMessage) (valueobjects.Value, error) {
	if i.units == nil {
		return valueobjects.Value{}, domainerrors.NewValidation("unit families are not configured in this deployment")
	}
	if def.UnitFamilyID() == "" {
		return valueobjects.Value{}, domainerrors.NewValidation("quantity attribute has no unit family", "attribute", def.InternalName())
	}
	var in struct {
		Magnitude string `json:"magnitude"`
		Unit      string `json:"unit"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return valueobjects.Value{}, domainerrors.NewValidation("expected a quantity {magnitude, unit}: " + err.Error())
	}
	famID, err := ulid.Parse(def.UnitFamilyID())
	if err != nil {
		return valueobjects.Value{}, domainerrors.NewValidation(err.Error())
	}
	family, err := i.units.Get(ctx, uow.TenantFromContext(ctx), famID)
	if err != nil {
		return valueobjects.Value{}, err
	}
	base, err := family.ToBase(in.Magnitude, in.Unit)
	if err != nil {
		return valueobjects.Value{}, err
	}
	return valueobjects.NewQuantityValue(in.Magnitude, in.Unit, base)
}
