package value

import (
	"context"
	"sort"

	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	domaintypedef "github.com/zkrebbekx/flexitype/domain/typedef"
	domainvalue "github.com/zkrebbekx/flexitype/domain/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
)

// maxFacetEntities bounds how many entities a facet computation scans.
const maxFacetEntities = 5000

// FacetBucket is one value of a faceted attribute and how many entities in
// the set hold it.
type FacetBucket struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

// FacetsOutput reports value counts per faceted attribute over a set of
// entities (the current filtered result set), so a console can render
// clickable facets that narrow the query.
type FacetsOutput struct {
	Facets    map[string][]FacetBucket `json:"facets"`
	Truncated bool                     `json:"truncated"`
}

// Facets counts the distinct values each requested attribute takes across
// the given entities. It reads only committed data; the caller supplies the
// entity set (typically the current FQL result set) so facets always
// reflect the active filter.
func (i *Interactor) Facets(ctx context.Context, rawTypeID string, attrNames, entityIDs []string) (*FacetsOutput, error) {
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
	tenant := t.TenantID()

	// Resolve the requested attribute names to ids over the effective schema.
	_, idByName, err := i.effectiveAttrIndex(ctx, t)
	if err != nil {
		return nil, err
	}
	i.dropUnreadable(ctx, idByName)
	wanted := map[valueobjects.AttributeDefinitionID]string{} // id -> internal name
	for _, name := range attrNames {
		id, ok := idByName[name]
		if !ok {
			return nil, domainerrors.NewValidation("attribute not in the type schema", "attribute", name)
		}
		wanted[id] = name
	}

	out := &FacetsOutput{Facets: map[string][]FacetBucket{}}
	counts := map[string]map[string]int{} // attr name -> value -> count
	for _, name := range attrNames {
		counts[name] = map[string]int{}
	}

	truncated := len(entityIDs) > maxFacetEntities
	if truncated {
		entityIDs = entityIDs[:maxFacetEntities]
		out.Truncated = true
	}
	ids := make([]valueobjects.EntityID, 0, len(entityIDs))
	for _, raw := range entityIDs {
		id, err := valueobjects.ParseEntityID(raw)
		if err != nil {
			return nil, domainerrors.NewValidation(err.Error())
		}
		ids = append(ids, id)
	}
	// One batched query per chunk instead of one per entity (up to 5000).
	if err := i.forEachValueBatched(ctx, tenant, ids, func(av *domainvalue.AttributeValue) {
		if name, ok := wanted[av.AttributeDefinitionID()]; ok {
			counts[name][av.Value().String()]++
		}
	}); err != nil {
		return nil, err
	}

	for name, byValue := range counts {
		buckets := make([]FacetBucket, 0, len(byValue))
		for v, c := range byValue {
			buckets = append(buckets, FacetBucket{Value: v, Count: c})
		}
		sort.SliceStable(buckets, func(a, b int) bool {
			if buckets[a].Count != buckets[b].Count {
				return buckets[a].Count > buckets[b].Count
			}
			return buckets[a].Value < buckets[b].Value
		})
		out.Facets[name] = buckets
	}
	return out, nil
}

// GridRow is one entity projected onto the chosen columns.
type GridRow struct {
	EntityID string            `json:"entity_id"`
	Values   map[string]string `json:"values"`
}

// GridOutput is a page of grid rows plus the column order.
type GridOutput struct {
	Columns []string  `json:"columns"`
	Rows    []GridRow `json:"rows"`
}

// GridRows projects a set of entities onto the chosen attribute columns for
// a value grid. It loads every entity's values in ONE query (no fan-out per
// row) and renders each cell via Value.String. Row order follows entityIDs.
func (i *Interactor) GridRows(ctx context.Context, rawTypeID string, attrNames, entityIDs []string) (*GridOutput, error) {
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

	_, idByName, err := i.effectiveAttrIndex(ctx, t)
	if err != nil {
		return nil, err
	}
	i.dropUnreadable(ctx, idByName)
	wanted := make(map[valueobjects.AttributeDefinitionID]string, len(attrNames)) // id -> column name
	for _, name := range attrNames {
		id, ok := idByName[name]
		if !ok {
			return nil, domainerrors.NewValidation("attribute not in the type schema", "attribute", name)
		}
		wanted[id] = name
	}

	ids := make([]valueobjects.EntityID, 0, len(entityIDs))
	for _, raw := range entityIDs {
		id, err := valueobjects.ParseEntityID(raw)
		if err != nil {
			return nil, domainerrors.NewValidation(err.Error())
		}
		ids = append(ids, id)
	}

	vals, err := i.reads.ListByEntities(ctx, t.TenantID(), ids)
	if err != nil {
		return nil, err
	}
	byEntity := make(map[string]map[string]string, len(entityIDs))
	for _, av := range vals {
		if name, ok := wanted[av.AttributeDefinitionID()]; ok {
			cells := byEntity[av.EntityID().String()]
			if cells == nil {
				cells = map[string]string{}
				byEntity[av.EntityID().String()] = cells
			}
			cells[name] = av.Value().String()
		}
	}

	out := &GridOutput{Columns: append([]string{"entity_id"}, attrNames...), Rows: make([]GridRow, 0, len(entityIDs))}
	for _, eid := range entityIDs {
		cells := byEntity[eid]
		if cells == nil {
			cells = map[string]string{}
		}
		out.Rows = append(out.Rows, GridRow{EntityID: eid, Values: cells})
	}
	return out, nil
}

// forEachValueBatched loads every live value of the given entities in bounded
// chunks — one query per chunk, not one per entity — and calls fn for each.
// The IN list is capped so a large entity set can't produce an unbounded query.
func (i *Interactor) forEachValueBatched(ctx context.Context, tenant valueobjects.TenantID, ids []valueobjects.EntityID, fn func(*domainvalue.AttributeValue)) error {
	const chunk = 500
	for start := 0; start < len(ids); start += chunk {
		end := start + chunk
		if end > len(ids) {
			end = len(ids)
		}
		vals, err := i.reads.ListByEntities(ctx, tenant, ids[start:end])
		if err != nil {
			return err
		}
		for _, av := range vals {
			fn(av)
		}
	}
	return nil
}

// dropUnreadable removes, in place, the attribute names the calling principal
// may not read from a name→id index. Requesting an unreadable column then
// resolves as "attribute not in the type schema" — indistinguishable from an
// absent one, so grid/facets/export never leak or oracle a restricted
// attribute (mirrors redactUnreadable and the FQL binder). Admins and
// unauthenticated development keep every name.
func (i *Interactor) dropUnreadable(ctx context.Context, idByName map[string]valueobjects.AttributeDefinitionID) {
	access := uow.AccessFromContext(ctx)
	if access.Admin {
		return
	}
	for name := range idByName {
		if !access.CanRead(name) {
			delete(idByName, name)
		}
	}
}

// effectiveAttrIndex maps an entity type's effective attributes both ways
// (id↔internal name) across its inheritance chain.
func (i *Interactor) effectiveAttrIndex(ctx context.Context, t *domaintypedef.TypeDefinition) (map[valueobjects.AttributeDefinitionID]string, map[string]valueobjects.AttributeDefinitionID, error) {
	chain, err := apptypedef.Chain(ctx, i.typeDefs, t)
	if err != nil {
		return nil, nil, err
	}
	nameByID := map[valueobjects.AttributeDefinitionID]string{}
	idByName := map[string]valueobjects.AttributeDefinitionID{}
	for _, link := range chain {
		attrs, _, err := i.attrs.ListByTypeDefinition(ctx, link.ID(), db.Page{Limit: 500})
		if err != nil {
			return nil, nil, err
		}
		for _, a := range attrs {
			s := a.Snapshot()
			if _, seen := idByName[s.InternalName]; seen {
				continue
			}
			idByName[s.InternalName] = s.ID
			nameByID[s.ID] = s.InternalName
		}
	}
	return nameByID, idByName, nil
}
