// Package savedview implements saved, shareable entity views: a named FQL
// query over a root type with chosen display columns and sort. Every
// usecase is scoped to the caller's tenant.
package savedview

import (
	"context"
	"time"

	"github.com/zkrebbekx/flexitype/application/uow"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

func tenantOf(ctx context.Context) valueobjects.TenantID { return uow.TenantFromContext(ctx) }

var namePattern = func(s string) bool { return len(s) >= 1 && len(s) <= 120 }

// View is a saved query over a root type.
type View struct {
	ID        ulid.ID               `json:"id"`
	TenantID  valueobjects.TenantID `json:"tenant_id"`
	Name      string                `json:"name"`
	RootType  string                `json:"root_type"`
	Query     string                `json:"query"`
	Columns   []string              `json:"columns"`
	Sort      string                `json:"sort"`
	CreatedAt time.Time             `json:"created_at"`
	UpdatedAt time.Time             `json:"updated_at"`
}

// Store persists saved views.
type Store interface {
	Create(ctx context.Context, v View) error
	Update(ctx context.Context, v View) error
	Delete(ctx context.Context, tenant valueobjects.TenantID, id ulid.ID) error
	Get(ctx context.Context, tenant valueobjects.TenantID, id ulid.ID) (View, error)
	List(ctx context.Context, tenant valueobjects.TenantID) ([]View, error)
}

// Interactor implements the saved-view usecases.
type Interactor struct {
	store Store
	now   func() time.Time
}

// NewInteractor wires the saved-view usecases.
func NewInteractor(store Store) *Interactor {
	return &Interactor{store: store, now: uow.UTCNow}
}

// Input carries the mutable fields of a view.
type Input struct {
	Name     string
	RootType string
	Query    string
	Columns  []string
	Sort     string
}

func (in Input) validate() error {
	if !namePattern(in.Name) {
		return domainerrors.NewValidation("name is required (1-120 chars)")
	}
	if in.RootType == "" {
		return domainerrors.NewValidation("root type is required")
	}
	return nil
}

// Create saves a new view for the caller's tenant.
func (i *Interactor) Create(ctx context.Context, in Input) (*View, error) {
	if err := in.validate(); err != nil {
		return nil, err
	}
	now := i.now()
	v := View{
		ID:        ulid.New(),
		TenantID:  tenantOf(ctx),
		Name:      in.Name,
		RootType:  in.RootType,
		Query:     in.Query,
		Columns:   normalizeColumns(in.Columns),
		Sort:      in.Sort,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := i.store.Create(ctx, v); err != nil {
		return nil, err
	}
	return &v, nil
}

// Update replaces a view's fields.
func (i *Interactor) Update(ctx context.Context, rawID string, in Input) (*View, error) {
	id, err := ulid.Parse(rawID)
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}
	if err := in.validate(); err != nil {
		return nil, err
	}
	existing, err := i.store.Get(ctx, tenantOf(ctx), id)
	if err != nil {
		return nil, err
	}
	existing.Name = in.Name
	existing.RootType = in.RootType
	existing.Query = in.Query
	existing.Columns = normalizeColumns(in.Columns)
	existing.Sort = in.Sort
	existing.UpdatedAt = i.now()
	if err := i.store.Update(ctx, existing); err != nil {
		return nil, err
	}
	return &existing, nil
}

// PatchInput is a sparse update: a nil field is left unchanged.
//
// PATCH on a saved view used to be a full replace — the handler decoded into
// a value struct, so any field the caller omitted was written back as its
// zero value. Editing a view's name through one client silently cleared the
// sort order another client had configured, and a saved view's whole point is
// that it reproduces a view.
type PatchInput struct {
	Name     *string
	RootType *string
	Query    *string
	Columns  *[]string
	Sort     *string
}

// Patch applies only the fields the caller supplied and leaves the rest as
// they are. The merged result is validated, so a patch cannot leave a view
// without a name or a root type.
func (i *Interactor) Patch(ctx context.Context, rawID string, in PatchInput) (*View, error) {
	id, err := ulid.Parse(rawID)
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}
	existing, err := i.store.Get(ctx, tenantOf(ctx), id)
	if err != nil {
		return nil, err
	}
	merged := Input{
		Name:     existing.Name,
		RootType: existing.RootType,
		Query:    existing.Query,
		Columns:  existing.Columns,
		Sort:     existing.Sort,
	}
	if in.Name != nil {
		merged.Name = *in.Name
	}
	if in.RootType != nil {
		merged.RootType = *in.RootType
	}
	if in.Query != nil {
		merged.Query = *in.Query
	}
	if in.Columns != nil {
		merged.Columns = *in.Columns
	}
	if in.Sort != nil {
		merged.Sort = *in.Sort
	}
	return i.Update(ctx, rawID, merged)
}

// Get loads one view.
func (i *Interactor) Get(ctx context.Context, rawID string) (*View, error) {
	id, err := ulid.Parse(rawID)
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}
	v, err := i.store.Get(ctx, tenantOf(ctx), id)
	if err != nil {
		return nil, err
	}
	return &v, nil
}

// List returns the tenant's saved views.
func (i *Interactor) List(ctx context.Context) ([]View, error) {
	views, err := i.store.List(ctx, tenantOf(ctx))
	if err != nil {
		return nil, err
	}
	// Always serialize a JSON array, never null: the console reads `.items`
	// as an array and a nil slice would marshal to `null`.
	if views == nil {
		views = []View{}
	}
	return views, nil
}

// Delete removes a view.
func (i *Interactor) Delete(ctx context.Context, rawID string) error {
	id, err := ulid.Parse(rawID)
	if err != nil {
		return domainerrors.NewValidation(err.Error())
	}
	if _, err := i.store.Get(ctx, tenantOf(ctx), id); err != nil {
		return err
	}
	return i.store.Delete(ctx, tenantOf(ctx), id)
}

func normalizeColumns(cols []string) []string {
	if cols == nil {
		return []string{}
	}
	return cols
}
