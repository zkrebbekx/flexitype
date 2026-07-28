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
	// Version increments on every write and guards against a lost update.
	//
	// Patch reads, merges and writes, so two concurrent patches — one setting
	// the sort, one renaming — each wrote the other's field back as it was
	// before: the same "one client silently clears what another set" outcome
	// the sparse decoder was added to remove, moved from an omitted field to
	// a concurrent write.
	Version int `json:"version"`
}

// Store persists saved views.
type Store interface {
	Create(ctx context.Context, v View) error
	// Update persists the view only if the stored version still matches
	// v.Version, and returns a conflict otherwise. It increments the stored
	// version on success.
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
		// A new view starts at 1, so the first read a client takes already
		// carries a version it can compare-and-swap against.
		Version: 1,
	}
	if err := i.store.Create(ctx, v); err != nil {
		return nil, err
	}
	return &v, nil
}

// Update replaces a view's fields, guarded by the version it reads.
func (i *Interactor) Update(ctx context.Context, rawID string, in Input) (*View, error) {
	id, err := ulid.Parse(rawID)
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}
	return i.update(ctx, id, in, 0)
}

// update writes a view. expectVersion of 0 means "whatever is stored now",
// which is what a full replace does; Patch passes the version it merged
// against, so a concurrent write is reported rather than discarded.
func (i *Interactor) update(ctx context.Context, id ulid.ID, in Input, expectVersion int) (*View, error) {
	if err := in.validate(); err != nil {
		return nil, err
	}
	existing, err := i.store.Get(ctx, tenantOf(ctx), id)
	if err != nil {
		return nil, err
	}
	if expectVersion > 0 {
		existing.Version = expectVersion
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
	existing.Version++
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
	// Version, when set, is the version the CALLER read. The patch is written
	// only if the stored version still matches, and a conflict is reported
	// otherwise.
	//
	// Without it the compare-and-swap was unreachable: Patch re-read the view
	// microseconds before writing it, so both of two users editing the same
	// view passed their own check and the second silently overwrote the
	// first. The 409 the store can raise only covered writes that interleaved
	// INSIDE one request, which is not the lost update that was reported.
	//
	// Nil keeps the old behaviour — last write wins — so a client that does
	// not track versions still works.
	Version *int
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
	// Merge and write against the version the CALLER read when it supplied
	// one, so an edit made between their read and their patch is reported
	// rather than overwritten. Without a caller version, the compare-and-swap
	// falls back to the version this request just read, which only detects a
	// write that interleaves inside the request.
	expect := existing.Version
	if in.Version != nil {
		expect = *in.Version
	}
	return i.update(ctx, id, merged, expect)
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
