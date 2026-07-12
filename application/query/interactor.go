package query

import (
	"context"
	"time"

	appunit "github.com/zkrebbekx/flexitype/application/unit"
	"github.com/zkrebbekx/flexitype/application/uow"
	domainattribute "github.com/zkrebbekx/flexitype/domain/attribute"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	domainrelationship "github.com/zkrebbekx/flexitype/domain/relationship"
	domaintypedef "github.com/zkrebbekx/flexitype/domain/typedef"
	domainvalue "github.com/zkrebbekx/flexitype/domain/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/fql"
)

// Repository executes a bound query against storage.
type Repository interface {
	// Search returns the page of entities (drawn from rootTypeIDs) matching
	// the bound expression, plus the total match count.
	Search(ctx context.Context, tenant valueobjects.TenantID, rootTypeIDs []valueobjects.TypeDefinitionID, node BoundNode, scope valueobjects.Scope, page db.Page) ([]domainvalue.EntitySummary, int, error)
}

// Interactor implements the FQL usecases.
type Interactor struct {
	typeDefs    domaintypedef.Repository
	attrs       domainattribute.Repository
	relDefs     domainrelationship.DefinitionRepository
	repo        Repository
	searchIndex bool
	// units resolves unit families so quantity predicates can normalize a
	// unit-suffixed literal (`weight > 5.5 kg`) to the base unit. nil when
	// unit families are disabled.
	units appunit.Store
	now   func() time.Time
}

// NewInteractor wires the query usecases. searchIndex unlocks matches();
// units (nil-able) enables unit-suffixed quantity comparisons.
func NewInteractor(typeDefs domaintypedef.Repository, attrs domainattribute.Repository, relDefs domainrelationship.DefinitionRepository, repo Repository, searchIndex bool, units appunit.Store) *Interactor {
	return &Interactor{typeDefs: typeDefs, attrs: attrs, relDefs: relDefs, repo: repo, searchIndex: searchIndex, units: units, now: time.Now}
}

// ExecuteInput is one query run.
type ExecuteInput struct {
	// Type is the root type's internal name; subtypes are always included
	// (constrain with `type = x` to narrow).
	Type  string
	Query string
	Page  db.PageArgs
	// Scope restricts scoped-attribute predicates to one locale/channel;
	// zero matches base (unscoped) values.
	Scope valueobjects.Scope
}

// ResultRow is one matched entity.
type ResultRow struct {
	EntityID         string    `json:"entity_id"`
	TypeDefinitionID string    `json:"type_definition_id"`
	ValueCount       int       `json:"value_count"`
	LastUpdatedAt    time.Time `json:"last_updated_at"`
}

// ExecuteOutput is one page of query results.
type ExecuteOutput struct {
	Items    []ResultRow
	PageInfo db.PageInfo
}

// Execute parses, binds and runs a query.
func (i *Interactor) Execute(ctx context.Context, in ExecuteInput) (*ExecuteOutput, error) {
	page, err := in.Page.Resolve()
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}

	rootTypes, bound, err := i.prepare(ctx, in.Type, in.Query)
	if err != nil {
		return nil, err
	}

	items, total, err := i.repo.Search(ctx, uow.TenantFromContext(ctx), rootTypes, bound, in.Scope, page)
	if err != nil {
		return nil, err
	}

	items, info := db.KeysetPage(page, items, db.KeysetTotal(page, total), func(e domainvalue.EntitySummary) string {
		return db.EncodeKeyset(e.LastUpdatedAt.UTC().Format(time.RFC3339Nano), e.EntityID.String())
	})
	out := &ExecuteOutput{
		Items:    make([]ResultRow, 0, len(items)),
		PageInfo: info,
	}
	for _, e := range items {
		out.Items = append(out.Items, ResultRow{
			EntityID:         e.EntityID.String(),
			TypeDefinitionID: e.TypeDefinitionID.String(),
			ValueCount:       e.ValueCount,
			LastUpdatedAt:    e.LastUpdatedAt,
		})
	}
	return out, nil
}

// Validate parses and binds a query without running it — the console's
// as-you-type check. Errors carry a "position" detail.
func (i *Interactor) Validate(ctx context.Context, rootType, queryText string) error {
	_, _, err := i.prepare(ctx, rootType, queryText)
	return err
}

func (i *Interactor) prepare(ctx context.Context, rootType, queryText string) ([]valueobjects.TypeDefinitionID, BoundNode, error) {
	tenant := uow.TenantFromContext(ctx)

	root, err := i.typeDefs.GetByInternalName(ctx, tenant, rootType)
	if err != nil {
		return nil, nil, err
	}

	node, err := fql.Parse(queryText)
	if err != nil {
		if perr, ok := err.(*fql.Error); ok {
			return nil, nil, domainerrors.NewValidation(perr.Message, "position", perr.Pos)
		}
		return nil, nil, domainerrors.NewValidation(err.Error())
	}

	b := &binder{
		tenant:      tenant,
		searchIndex: i.searchIndex,
		access:      uow.AccessFromContext(ctx),
		typeDefs:    i.typeDefs,
		attrs:       i.attrs,
		relDefs:     i.relDefs,
		units:       i.units,
		typesByName: make(map[string]domaintypedef.Snapshot),
	}
	allTypes, _, err := i.typeDefs.List(ctx, domaintypedef.Filter{TenantID: tenant}, db.Page{Limit: 500})
	if err != nil {
		return nil, nil, err
	}
	for _, t := range allTypes {
		b.typesByName[t.InternalName()] = t.Snapshot()
	}

	s, rowTypes, err := b.scopeFor(ctx, root, 0)
	if err != nil {
		return nil, nil, err
	}
	bound, err := b.bind(ctx, node, s)
	if err != nil {
		return nil, nil, err
	}

	rootIDs := make([]valueobjects.TypeDefinitionID, 0, len(rowTypes))
	for _, t := range rowTypes {
		rootIDs = append(rootIDs, t.ID())
	}
	return rootIDs, bound, nil
}
