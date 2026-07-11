package http

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/zkrebbekx/flexitype/application"
	appquery "github.com/zkrebbekx/flexitype/application/query"
	"github.com/zkrebbekx/flexitype/pkg/db"
)

// maxFacetScan caps how many result-set entities a facet request collects.
const maxFacetScan = 5000

// gridEntities returns a page of a type's entities with the chosen attribute
// values projected as columns — one list/query round-trip plus one batched
// value load, no N+1. `attributes` chooses the value columns; `query` (FQL)
// filters the rows.
func (s *server) gridEntities(w http.ResponseWriter, r *http.Request) {
	typeID := chi.URLParam(r, "typeDefinitionID")
	attributes := splitCSVParam(strings.TrimSpace(r.URL.Query().Get("attributes")))

	ids, pageInfo, err := s.gridPage(r, typeID)
	if err != nil {
		writeError(w, s.log, err)
		return
	}

	out, err := application.FromContext(r.Context()).Values().GridRows(r.Context(), typeID, attributes, ids)
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"columns":   out.Columns,
		"rows":      out.Rows,
		"page_info": pageInfo,
	})
}

// gridPage resolves one page of entity ids and its page info: the FQL result
// set when `query` is present, otherwise the type's entity list.
func (s *server) gridPage(r *http.Request, typeID string) ([]string, db.PageInfo, error) {
	app := application.FromContext(r.Context())
	q := strings.TrimSpace(r.URL.Query().Get("query"))
	if q == "" {
		out, err := app.Values().ListEntities(r.Context(), typeID, boolQuery(r, "include_descendants"), pageArgs(r))
		if err != nil {
			return nil, db.PageInfo{}, err
		}
		ids := make([]string, 0, len(out.Items))
		for _, it := range out.Items {
			ids = append(ids, it.EntityID)
		}
		return ids, out.PageInfo, nil
	}

	t, err := app.TypeDefinitions().Get(r.Context(), typeID)
	if err != nil {
		return nil, db.PageInfo{}, err
	}
	out, err := app.Query().Execute(r.Context(), appquery.ExecuteInput{
		Type: t.InternalName, Query: q, Page: pageArgs(r),
	})
	if err != nil {
		return nil, db.PageInfo{}, err
	}
	ids := make([]string, 0, len(out.Items))
	for _, it := range out.Items {
		ids = append(ids, it.EntityID)
	}
	return ids, out.PageInfo, nil
}

// entityFacets returns value counts for the chosen attributes across the
// current result set (the FQL filter, if any), for rendering clickable
// facets that narrow the query.
func (s *server) entityFacets(w http.ResponseWriter, r *http.Request) {
	typeID := chi.URLParam(r, "typeDefinitionID")
	attributes := splitCSVParam(strings.TrimSpace(r.URL.Query().Get("attributes")))

	ids, err := s.resolveEntitySet(r, typeID)
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	out, err := application.FromContext(r.Context()).Values().Facets(r.Context(), typeID, attributes, ids)
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// resolveEntitySet collects the whole result set's entity ids (capped): the
// FQL matches when `query` is present, otherwise all of the type's entities.
func (s *server) resolveEntitySet(r *http.Request, typeID string) ([]string, error) {
	app := application.FromContext(r.Context())
	limit := 500
	q := strings.TrimSpace(r.URL.Query().Get("query"))

	var ids []string
	var cursor *string
	for {
		var items []string
		var next *string
		if q == "" {
			out, err := app.Values().ListEntities(r.Context(), typeID, boolQuery(r, "include_descendants"), db.PageArgs{Limit: &limit, Cursor: cursor})
			if err != nil {
				return nil, err
			}
			for _, it := range out.Items {
				items = append(items, it.EntityID)
			}
			next = out.PageInfo.NextCursor
		} else {
			t, err := app.TypeDefinitions().Get(r.Context(), typeID)
			if err != nil {
				return nil, err
			}
			out, err := app.Query().Execute(r.Context(), appquery.ExecuteInput{Type: t.InternalName, Query: q, Page: db.PageArgs{Limit: &limit, Cursor: cursor}})
			if err != nil {
				return nil, err
			}
			for _, it := range out.Items {
				items = append(items, it.EntityID)
			}
			next = out.PageInfo.NextCursor
		}
		ids = append(ids, items...)
		if next == nil || len(ids) >= maxFacetScan {
			break
		}
		cursor = next
	}
	return ids, nil
}
