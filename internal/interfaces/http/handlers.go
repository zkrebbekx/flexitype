package http

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/zkrebbekx/flexitype/application"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	appdependency "github.com/zkrebbekx/flexitype/application/dependency"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	"github.com/zkrebbekx/flexitype/pkg/db"
)

// pageArgs reads ?limit= and ?cursor=.
func pageArgs(r *http.Request) db.PageArgs {
	var args db.PageArgs
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			args.Limit = &n
		}
	}
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		args.Cursor = &raw
	}
	return args
}

func boolQuery(r *http.Request, key string) bool {
	v, _ := strconv.ParseBool(r.URL.Query().Get(key))
	return v
}

func csvQuery(r *http.Request, key string) []string {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := parts[:0]
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// listResponse is the standard paginated wire shape.
type listResponse struct {
	Items    any         `json:"items"`
	PageInfo db.PageInfo `json:"page_info"`
}

// --- type definitions -----------------------------------------------------

type typeDefinitionRequest struct {
	InternalName string `json:"internal_name,omitempty"`
	DisplayName  string `json:"display_name"`
	Description  string `json:"description,omitempty"`
}

func (s *server) createTypeDefinition(w http.ResponseWriter, r *http.Request) {
	var req typeDefinitionRequest
	if err := decode(r, &req); err != nil {
		writeError(w, s.log, err)
		return
	}
	snap, err := s.factory.New(r.Context()).TypeDefinitions().Create(r.Context(), apptypedef.CreateInput{
		InternalName: req.InternalName,
		DisplayName:  req.DisplayName,
		Description:  req.Description,
	})
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusCreated, snap)
}

func (s *server) getTypeDefinition(w http.ResponseWriter, r *http.Request) {
	snap, err := s.factory.New(r.Context()).TypeDefinitions().Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

func (s *server) updateTypeDefinition(w http.ResponseWriter, r *http.Request) {
	var req typeDefinitionRequest
	if err := decode(r, &req); err != nil {
		writeError(w, s.log, err)
		return
	}
	snap, err := s.factory.New(r.Context()).TypeDefinitions().Update(r.Context(), apptypedef.UpdateInput{
		ID:          chi.URLParam(r, "id"),
		DisplayName: req.DisplayName,
		Description: req.Description,
	})
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

func (s *server) archiveTypeDefinition(w http.ResponseWriter, r *http.Request) {
	snap, err := s.factory.New(r.Context()).TypeDefinitions().Archive(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

func (s *server) restoreTypeDefinition(w http.ResponseWriter, r *http.Request) {
	snap, err := s.factory.New(r.Context()).TypeDefinitions().Restore(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

func (s *server) listTypeDefinitions(w http.ResponseWriter, r *http.Request) {
	out, err := s.factory.New(r.Context()).TypeDefinitions().List(r.Context(), apptypedef.ListInput{
		InternalNames:   csvQuery(r, "internal_name"),
		IncludeArchived: boolQuery(r, "include_archived"),
		Page:            pageArgs(r),
	})
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, listResponse{Items: out.Items, PageInfo: out.PageInfo})
}

// --- attribute definitions ------------------------------------------------

type attributeRequest struct {
	TypeDefinitionID string          `json:"type_definition_id,omitempty"`
	InternalName     string          `json:"internal_name,omitempty"`
	DisplayName      string          `json:"display_name"`
	Description      string          `json:"description,omitempty"`
	DataType         string          `json:"data_type,omitempty"`
	Required         bool            `json:"required,omitempty"`
	MultiValued      bool            `json:"multi_valued,omitempty"`
	Unique           bool            `json:"unique,omitempty"`
	Constraints      json.RawMessage `json:"constraints,omitempty"`
	DefaultValue     json.RawMessage `json:"default_value,omitempty"`
}

func (s *server) createAttribute(w http.ResponseWriter, r *http.Request) {
	var req attributeRequest
	if err := decode(r, &req); err != nil {
		writeError(w, s.log, err)
		return
	}
	snap, err := s.factory.New(r.Context()).Attributes().Create(r.Context(), appattribute.CreateInput{
		TypeDefinitionID: req.TypeDefinitionID,
		InternalName:     req.InternalName,
		DisplayName:      req.DisplayName,
		Description:      req.Description,
		DataType:         req.DataType,
		Required:         req.Required,
		MultiValued:      req.MultiValued,
		Unique:           req.Unique,
		Constraints:      req.Constraints,
		DefaultValue:     req.DefaultValue,
	})
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusCreated, snap)
}

func (s *server) getAttribute(w http.ResponseWriter, r *http.Request) {
	snap, err := s.factory.New(r.Context()).Attributes().Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

func (s *server) updateAttribute(w http.ResponseWriter, r *http.Request) {
	var req attributeRequest
	if err := decode(r, &req); err != nil {
		writeError(w, s.log, err)
		return
	}
	snap, err := s.factory.New(r.Context()).Attributes().Update(r.Context(), appattribute.UpdateInput{
		ID:           chi.URLParam(r, "id"),
		DisplayName:  req.DisplayName,
		Description:  req.Description,
		Required:     req.Required,
		MultiValued:  req.MultiValued,
		Unique:       req.Unique,
		Constraints:  req.Constraints,
		DefaultValue: req.DefaultValue,
	})
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

func (s *server) archiveAttribute(w http.ResponseWriter, r *http.Request) {
	snap, err := s.factory.New(r.Context()).Attributes().Archive(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

func (s *server) restoreAttribute(w http.ResponseWriter, r *http.Request) {
	snap, err := s.factory.New(r.Context()).Attributes().Restore(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

func (s *server) listAttributesByTypeDefinition(w http.ResponseWriter, r *http.Request) {
	out, err := s.factory.New(r.Context()).Attributes().ListByTypeDefinition(r.Context(), chi.URLParam(r, "id"), pageArgs(r))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, listResponse{Items: out.Items, PageInfo: out.PageInfo})
}

func (s *server) listAttributes(w http.ResponseWriter, r *http.Request) {
	out, err := s.factory.New(r.Context()).Attributes().List(r.Context(), appattribute.ListInput{
		TypeDefinitionID: r.URL.Query().Get("type_definition_id"),
		InternalNames:    csvQuery(r, "internal_name"),
		DataTypes:        csvQuery(r, "data_type"),
		IncludeArchived:  boolQuery(r, "include_archived"),
		Page:             pageArgs(r),
	})
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, listResponse{Items: out.Items, PageInfo: out.PageInfo})
}

// --- values -----------------------------------------------------------------

type setValueRequest struct {
	AttributeDefinitionID string          `json:"attribute_definition_id"`
	EntityID              string          `json:"entity_id"`
	Value                 json.RawMessage `json:"value"`
}

func (s *server) setValue(w http.ResponseWriter, r *http.Request) {
	var req setValueRequest
	if err := decode(r, &req); err != nil {
		writeError(w, s.log, err)
		return
	}
	snap, err := s.factory.New(r.Context()).Values().Set(r.Context(), appvalue.SetInput{
		AttributeDefinitionID: req.AttributeDefinitionID,
		EntityID:              req.EntityID,
		Value:                 req.Value,
	})
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

func (s *server) getValue(w http.ResponseWriter, r *http.Request) {
	snap, err := s.factory.New(r.Context()).Values().Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

func (s *server) removeValue(w http.ResponseWriter, r *http.Request) {
	snap, err := s.factory.New(r.Context()).Values().Remove(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

func (s *server) listValues(w http.ResponseWriter, r *http.Request) {
	out, err := s.factory.New(r.Context()).Values().List(r.Context(), appvalue.ListInput{
		TypeDefinitionID:      r.URL.Query().Get("type_definition_id"),
		AttributeDefinitionID: r.URL.Query().Get("attribute_definition_id"),
		EntityID:              r.URL.Query().Get("entity_id"),
		IncludeArchived:       boolQuery(r, "include_archived"),
		Page:                  pageArgs(r),
	})
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, listResponse{Items: out.Items, PageInfo: out.PageInfo})
}

func (s *server) listEntityValues(w http.ResponseWriter, r *http.Request) {
	snaps, err := s.factory.New(r.Context()).Values().ListByEntity(r.Context(),
		chi.URLParam(r, "typeDefinitionID"), chi.URLParam(r, "entityID"))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": snaps})
}

func (s *server) effectiveSchema(w http.ResponseWriter, r *http.Request) {
	out, err := s.factory.New(r.Context()).Dependencies().EffectiveSchema(r.Context(),
		chi.URLParam(r, "attributeID"), chi.URLParam(r, "entityID"))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// --- dependencies -----------------------------------------------------------

type dependencyRequest struct {
	SourceAttributeID string          `json:"source_attribute_id,omitempty"`
	TargetAttributeID string          `json:"target_attribute_id,omitempty"`
	Conditions        json.RawMessage `json:"conditions,omitempty"`
	Effect            json.RawMessage `json:"effect,omitempty"`
	Description       string          `json:"description,omitempty"`
}

func (s *server) createDependency(w http.ResponseWriter, r *http.Request) {
	var req dependencyRequest
	if err := decode(r, &req); err != nil {
		writeError(w, s.log, err)
		return
	}
	snap, err := s.factory.New(r.Context()).Dependencies().Create(r.Context(), appdependency.CreateInput{
		SourceAttributeID: req.SourceAttributeID,
		TargetAttributeID: req.TargetAttributeID,
		Conditions:        req.Conditions,
		Effect:            req.Effect,
		Description:       req.Description,
	})
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusCreated, snap)
}

func (s *server) getDependency(w http.ResponseWriter, r *http.Request) {
	snap, err := s.factory.New(r.Context()).Dependencies().Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

func (s *server) updateDependency(w http.ResponseWriter, r *http.Request) {
	var req dependencyRequest
	if err := decode(r, &req); err != nil {
		writeError(w, s.log, err)
		return
	}
	snap, err := s.factory.New(r.Context()).Dependencies().Update(r.Context(), appdependency.UpdateInput{
		ID:          chi.URLParam(r, "id"),
		Conditions:  req.Conditions,
		Effect:      req.Effect,
		Description: req.Description,
	})
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

func (s *server) archiveDependency(w http.ResponseWriter, r *http.Request) {
	snap, err := s.factory.New(r.Context()).Dependencies().Archive(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

func (s *server) listDependencies(w http.ResponseWriter, r *http.Request) {
	out, err := s.factory.New(r.Context()).Dependencies().List(r.Context(), appdependency.ListInput{
		SourceAttributeID: r.URL.Query().Get("source_attribute_id"),
		TargetAttributeID: r.URL.Query().Get("target_attribute_id"),
		IncludeArchived:   boolQuery(r, "include_archived"),
		Page:              pageArgs(r),
	})
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, listResponse{Items: out.Items, PageInfo: out.PageInfo})
}

// --- activity ----------------------------------------------------------------

func (s *server) listActivity(w http.ResponseWriter, r *http.Request) {
	out, err := s.factory.New(r.Context()).Activity().List(r.Context(), application.ActivityListInput{
		Entity:   r.URL.Query().Get("entity"),
		EntityID: r.URL.Query().Get("entity_id"),
		Actor:    r.URL.Query().Get("actor"),
		Page:     pageArgs(r),
	})
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, listResponse{Items: out.Items, PageInfo: out.PageInfo})
}
