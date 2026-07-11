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
	appquery "github.com/zkrebbekx/flexitype/application/query"
	apprelationship "github.com/zkrebbekx/flexitype/application/relationship"
	appsavedview "github.com/zkrebbekx/flexitype/application/savedview"
	appschema "github.com/zkrebbekx/flexitype/application/schema"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
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
	ExtendsID    string `json:"extends_id,omitempty"`
}

func (s *server) createTypeDefinition(w http.ResponseWriter, r *http.Request) {
	var req typeDefinitionRequest
	if err := decode(r, &req); err != nil {
		writeError(w, s.log, err)
		return
	}
	snap, err := application.FromContext(r.Context()).TypeDefinitions().Create(r.Context(), apptypedef.CreateInput{
		InternalName: req.InternalName,
		DisplayName:  req.DisplayName,
		Description:  req.Description,
		ExtendsID:    req.ExtendsID,
	})
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusCreated, snap)
}

func (s *server) getTypeDefinition(w http.ResponseWriter, r *http.Request) {
	snap, err := application.FromContext(r.Context()).TypeDefinitions().Get(r.Context(), chi.URLParam(r, "id"))
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
	snap, err := application.FromContext(r.Context()).TypeDefinitions().Update(r.Context(), apptypedef.UpdateInput{
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
	snap, err := application.FromContext(r.Context()).TypeDefinitions().Archive(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

func (s *server) restoreTypeDefinition(w http.ResponseWriter, r *http.Request) {
	snap, err := application.FromContext(r.Context()).TypeDefinitions().Restore(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

func (s *server) listTypeDefinitions(w http.ResponseWriter, r *http.Request) {
	out, err := application.FromContext(r.Context()).TypeDefinitions().List(r.Context(), apptypedef.ListInput{
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
	Localizable      bool            `json:"localizable,omitempty"`
	Scopable         bool            `json:"scopable,omitempty"`
	Computed         json.RawMessage `json:"computed,omitempty"`
	Constraints      json.RawMessage `json:"constraints,omitempty"`
	DefaultValue     json.RawMessage `json:"default_value,omitempty"`
	Group            string          `json:"group,omitempty"`
	SortOrder        int             `json:"sort_order,omitempty"`
	HelpText         string          `json:"help_text,omitempty"`
}

func (s *server) createAttribute(w http.ResponseWriter, r *http.Request) {
	var req attributeRequest
	if err := decode(r, &req); err != nil {
		writeError(w, s.log, err)
		return
	}
	snap, err := application.FromContext(r.Context()).Attributes().Create(r.Context(), appattribute.CreateInput{
		TypeDefinitionID: req.TypeDefinitionID,
		InternalName:     req.InternalName,
		DisplayName:      req.DisplayName,
		Description:      req.Description,
		DataType:         req.DataType,
		Required:         req.Required,
		MultiValued:      req.MultiValued,
		Unique:           req.Unique,
		Localizable:      req.Localizable,
		Scopable:         req.Scopable,
		Computed:         req.Computed,
		Constraints:      req.Constraints,
		DefaultValue:     req.DefaultValue,
		Group:            req.Group,
		SortOrder:        req.SortOrder,
		HelpText:         req.HelpText,
	})
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusCreated, snap)
}

func (s *server) getAttribute(w http.ResponseWriter, r *http.Request) {
	snap, err := application.FromContext(r.Context()).Attributes().Get(r.Context(), chi.URLParam(r, "id"))
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
	snap, err := application.FromContext(r.Context()).Attributes().Update(r.Context(), appattribute.UpdateInput{
		ID:           chi.URLParam(r, "id"),
		DisplayName:  req.DisplayName,
		Description:  req.Description,
		Required:     req.Required,
		MultiValued:  req.MultiValued,
		Unique:       req.Unique,
		Localizable:  req.Localizable,
		Scopable:     req.Scopable,
		Computed:     req.Computed,
		Constraints:  req.Constraints,
		DefaultValue: req.DefaultValue,
		Group:        req.Group,
		SortOrder:    req.SortOrder,
		HelpText:     req.HelpText,
	})
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

func (s *server) archiveAttribute(w http.ResponseWriter, r *http.Request) {
	snap, err := application.FromContext(r.Context()).Attributes().Archive(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

func (s *server) restoreAttribute(w http.ResponseWriter, r *http.Request) {
	snap, err := application.FromContext(r.Context()).Attributes().Restore(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

type validateValueRequest struct {
	Value json.RawMessage `json:"value"`
}

func (s *server) validateAttributeValue(w http.ResponseWriter, r *http.Request) {
	var req validateValueRequest
	if err := decode(r, &req); err != nil {
		writeError(w, s.log, err)
		return
	}
	if err := application.FromContext(r.Context()).Attributes().ValidateValue(r.Context(), chi.URLParam(r, "id"), req.Value); err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"valid": true})
}

func (s *server) effectiveAttributes(w http.ResponseWriter, r *http.Request) {
	items, err := application.FromContext(r.Context()).TypeDefinitions().EffectiveAttributes(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *server) typeChildren(w http.ResponseWriter, r *http.Request) {
	items, err := application.FromContext(r.Context()).TypeDefinitions().Children(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *server) listAttributesByTypeDefinition(w http.ResponseWriter, r *http.Request) {
	out, err := application.FromContext(r.Context()).Attributes().ListByTypeDefinition(r.Context(), chi.URLParam(r, "id"), pageArgs(r))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, listResponse{Items: out.Items, PageInfo: out.PageInfo})
}

func (s *server) listAttributes(w http.ResponseWriter, r *http.Request) {
	out, err := application.FromContext(r.Context()).Attributes().List(r.Context(), appattribute.ListInput{
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
	TypeDefinitionID      string          `json:"type_definition_id,omitempty"`
	Locale                string          `json:"locale,omitempty"`
	Channel               string          `json:"channel,omitempty"`
	Value                 json.RawMessage `json:"value"`
}

func (s *server) setValue(w http.ResponseWriter, r *http.Request) {
	var req setValueRequest
	if err := decode(r, &req); err != nil {
		writeError(w, s.log, err)
		return
	}
	snap, err := application.FromContext(r.Context()).Values().Set(r.Context(), appvalue.SetInput{
		AttributeDefinitionID: req.AttributeDefinitionID,
		EntityID:              req.EntityID,
		TypeDefinitionID:      req.TypeDefinitionID,
		Locale:                req.Locale,
		Channel:               req.Channel,
		Value:                 req.Value,
	})
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

type batchSetValueRequest struct {
	Items []setValueRequest `json:"items"`
}

func (s *server) setValuesBatch(w http.ResponseWriter, r *http.Request) {
	var req batchSetValueRequest
	if err := decode(r, &req); err != nil {
		writeError(w, s.log, err)
		return
	}
	items := make([]appvalue.SetInput, 0, len(req.Items))
	for _, it := range req.Items {
		items = append(items, appvalue.SetInput{
			AttributeDefinitionID: it.AttributeDefinitionID,
			EntityID:              it.EntityID,
			TypeDefinitionID:      it.TypeDefinitionID,
			Locale:                it.Locale,
			Channel:               it.Channel,
			Value:                 it.Value,
		})
	}
	out, err := application.FromContext(r.Context()).Values().SetBatch(r.Context(), appvalue.BatchSetInput{Items: items})
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out.Items})
}

type savedViewRequest struct {
	Name     string   `json:"name"`
	RootType string   `json:"root_type"`
	Query    string   `json:"query,omitempty"`
	Columns  []string `json:"columns,omitempty"`
	Sort     string   `json:"sort,omitempty"`
}

func (s *server) savedViewsInteractor(w http.ResponseWriter, r *http.Request) *appsavedview.Interactor {
	sv := application.FromContext(r.Context()).SavedViews()
	if sv == nil {
		s.featureDisabled(w, "saved views")
		return nil
	}
	return sv
}

func (s *server) listSavedViews(w http.ResponseWriter, r *http.Request) {
	sv := s.savedViewsInteractor(w, r)
	if sv == nil {
		return
	}
	views, err := sv.List(r.Context())
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": views})
}

func (s *server) createSavedView(w http.ResponseWriter, r *http.Request) {
	sv := s.savedViewsInteractor(w, r)
	if sv == nil {
		return
	}
	var req savedViewRequest
	if err := decode(r, &req); err != nil {
		writeError(w, s.log, err)
		return
	}
	v, err := sv.Create(r.Context(), appsavedview.Input{
		Name: req.Name, RootType: req.RootType, Query: req.Query, Columns: req.Columns, Sort: req.Sort,
	})
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusCreated, v)
}

func (s *server) getSavedView(w http.ResponseWriter, r *http.Request) {
	sv := s.savedViewsInteractor(w, r)
	if sv == nil {
		return
	}
	v, err := sv.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *server) updateSavedView(w http.ResponseWriter, r *http.Request) {
	sv := s.savedViewsInteractor(w, r)
	if sv == nil {
		return
	}
	var req savedViewRequest
	if err := decode(r, &req); err != nil {
		writeError(w, s.log, err)
		return
	}
	v, err := sv.Update(r.Context(), chi.URLParam(r, "id"), appsavedview.Input{
		Name: req.Name, RootType: req.RootType, Query: req.Query, Columns: req.Columns, Sort: req.Sort,
	})
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *server) deleteSavedView(w http.ResponseWriter, r *http.Request) {
	sv := s.savedViewsInteractor(w, r)
	if sv == nil {
		return
	}
	if err := sv.Delete(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeError(w, s.log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) relationshipRequirements(w http.ResponseWriter, r *http.Request) {
	reqs, err := application.FromContext(r.Context()).Relationships().RelationshipRequirements(r.Context(), chi.URLParam(r, "entityID"))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": reqs})
}

func (s *server) exportSchema(w http.ResponseWriter, r *http.Request) {
	bundle, err := application.FromContext(r.Context()).Schema().Export(r.Context())
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, bundle)
}

func (s *server) importSchema(w http.ResponseWriter, r *http.Request) {
	var bundle appschema.Bundle
	if err := decode(r, &bundle); err != nil {
		writeError(w, s.log, err)
		return
	}
	res, err := application.FromContext(r.Context()).Schema().Import(r.Context(), &bundle)
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *server) removeEntity(w http.ResponseWriter, r *http.Request) {
	out, err := application.FromContext(r.Context()).Values().RemoveEntity(
		r.Context(), chi.URLParam(r, "typeDefinitionID"), chi.URLParam(r, "entityID"))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"entity_id":          out.EntityID,
		"values_removed":     out.ValuesRemoved,
		"relationships_gone": out.RelationshipsGone,
	})
}

func (s *server) getValue(w http.ResponseWriter, r *http.Request) {
	snap, err := application.FromContext(r.Context()).Values().Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

func (s *server) removeValue(w http.ResponseWriter, r *http.Request) {
	snap, err := application.FromContext(r.Context()).Values().Remove(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

func (s *server) listValues(w http.ResponseWriter, r *http.Request) {
	out, err := application.FromContext(r.Context()).Values().List(r.Context(), appvalue.ListInput{
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

func (s *server) listEntitiesOfType(w http.ResponseWriter, r *http.Request) {
	out, err := application.FromContext(r.Context()).Values().ListEntities(r.Context(), chi.URLParam(r, "typeDefinitionID"), boolQuery(r, "include_descendants"), pageArgs(r))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, listResponse{Items: out.Items, PageInfo: out.PageInfo})
}

func (s *server) listEntityValues(w http.ResponseWriter, r *http.Request) {
	snaps, err := application.FromContext(r.Context()).Values().ListByEntity(r.Context(),
		chi.URLParam(r, "typeDefinitionID"), chi.URLParam(r, "entityID"))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": snaps})
}

func (s *server) effectiveSchema(w http.ResponseWriter, r *http.Request) {
	out, err := application.FromContext(r.Context()).Dependencies().EffectiveSchema(r.Context(),
		chi.URLParam(r, "attributeID"), chi.URLParam(r, "entityID"))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) entityCompleteness(w http.ResponseWriter, r *http.Request) {
	out, err := application.FromContext(r.Context()).Dependencies().Completeness(r.Context(),
		chi.URLParam(r, "typeDefinitionID"), chi.URLParam(r, "entityID"))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) typeCompleteness(w http.ResponseWriter, r *http.Request) {
	out, err := application.FromContext(r.Context()).Dependencies().TypeCompleteness(r.Context(),
		chi.URLParam(r, "id"))
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
	snap, err := application.FromContext(r.Context()).Dependencies().Create(r.Context(), appdependency.CreateInput{
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
	snap, err := application.FromContext(r.Context()).Dependencies().Get(r.Context(), chi.URLParam(r, "id"))
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
	snap, err := application.FromContext(r.Context()).Dependencies().Update(r.Context(), appdependency.UpdateInput{
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
	snap, err := application.FromContext(r.Context()).Dependencies().Archive(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

func (s *server) listDependencies(w http.ResponseWriter, r *http.Request) {
	out, err := application.FromContext(r.Context()).Dependencies().List(r.Context(), appdependency.ListInput{
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

// --- relationships ------------------------------------------------------------

type relationshipDefinitionRequest struct {
	InternalName        string `json:"internal_name,omitempty"`
	DisplayName         string `json:"display_name"`
	Description         string `json:"description,omitempty"`
	Kind                string `json:"kind,omitempty"`
	ParentTypeID        string `json:"parent_type_id,omitempty"`
	ChildTypeID         string `json:"child_type_id,omitempty"`
	ParentLabel         string `json:"parent_label,omitempty"`
	ChildLabel          string `json:"child_label,omitempty"`
	ExtendsID           string `json:"extends_id,omitempty"`
	ParentVersionPolicy string `json:"parent_version_policy,omitempty"`
	ChildVersionPolicy  string `json:"child_version_policy,omitempty"`
	MinChildren         *int   `json:"min_children,omitempty"`
	MaxChildren         *int   `json:"max_children,omitempty"`
	MinParents          *int   `json:"min_parents,omitempty"`
	MaxParents          *int   `json:"max_parents,omitempty"`
}

func (s *server) createRelationshipDefinition(w http.ResponseWriter, r *http.Request) {
	var req relationshipDefinitionRequest
	if err := decode(r, &req); err != nil {
		writeError(w, s.log, err)
		return
	}
	snap, err := application.FromContext(r.Context()).Relationships().CreateDefinition(r.Context(), apprelationship.CreateDefinitionInput{
		InternalName:        req.InternalName,
		DisplayName:         req.DisplayName,
		Description:         req.Description,
		Kind:                req.Kind,
		ParentTypeID:        req.ParentTypeID,
		ChildTypeID:         req.ChildTypeID,
		ParentLabel:         req.ParentLabel,
		ChildLabel:          req.ChildLabel,
		ExtendsID:           req.ExtendsID,
		ParentVersionPolicy: req.ParentVersionPolicy,
		ChildVersionPolicy:  req.ChildVersionPolicy,
		MinChildren:         req.MinChildren,
		MaxChildren:         req.MaxChildren,
		MinParents:          req.MinParents,
		MaxParents:          req.MaxParents,
	})
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusCreated, snap)
}

func (s *server) getRelationshipDefinition(w http.ResponseWriter, r *http.Request) {
	snap, err := application.FromContext(r.Context()).Relationships().GetDefinition(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

func (s *server) updateRelationshipDefinition(w http.ResponseWriter, r *http.Request) {
	var req relationshipDefinitionRequest
	if err := decode(r, &req); err != nil {
		writeError(w, s.log, err)
		return
	}
	snap, err := application.FromContext(r.Context()).Relationships().UpdateDefinition(r.Context(), apprelationship.UpdateDefinitionInput{
		ID:                  chi.URLParam(r, "id"),
		DisplayName:         req.DisplayName,
		Description:         req.Description,
		ParentLabel:         req.ParentLabel,
		ChildLabel:          req.ChildLabel,
		ParentVersionPolicy: req.ParentVersionPolicy,
		ChildVersionPolicy:  req.ChildVersionPolicy,
		MinChildren:         req.MinChildren,
		MaxChildren:         req.MaxChildren,
		MinParents:          req.MinParents,
		MaxParents:          req.MaxParents,
	})
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

func (s *server) archiveRelationshipDefinition(w http.ResponseWriter, r *http.Request) {
	snap, err := application.FromContext(r.Context()).Relationships().ArchiveDefinition(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

func (s *server) restoreRelationshipDefinition(w http.ResponseWriter, r *http.Request) {
	snap, err := application.FromContext(r.Context()).Relationships().RestoreDefinition(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

func (s *server) relationshipAttributeSets(w http.ResponseWriter, r *http.Request) {
	sets, err := application.FromContext(r.Context()).Relationships().AttributeSets(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"attribute_set_ids": sets})
}

func (s *server) listRelationshipDefinitions(w http.ResponseWriter, r *http.Request) {
	out, err := application.FromContext(r.Context()).Relationships().ListDefinitions(r.Context(), apprelationship.DefinitionListInput{
		TypeDefinitionID: r.URL.Query().Get("type_definition_id"),
		IncludeArchived:  boolQuery(r, "include_archived"),
		Page:             pageArgs(r),
	})
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, listResponse{Items: out.Items, PageInfo: out.PageInfo})
}

type linkRequest struct {
	RelationshipDefinitionID string `json:"relationship_definition_id"`
	ParentEntityID           string `json:"parent_entity_id"`
	ChildEntityID            string `json:"child_entity_id"`
	ParentTypeVersion        *int   `json:"parent_type_version,omitempty"`
	ChildTypeVersion         *int   `json:"child_type_version,omitempty"`
}

func (s *server) createRelationship(w http.ResponseWriter, r *http.Request) {
	var req linkRequest
	if err := decode(r, &req); err != nil {
		writeError(w, s.log, err)
		return
	}
	snap, err := application.FromContext(r.Context()).Relationships().Link(r.Context(), apprelationship.LinkInput{
		DefinitionID:  req.RelationshipDefinitionID,
		ParentEntity:  req.ParentEntityID,
		ChildEntity:   req.ChildEntityID,
		ParentVersion: req.ParentTypeVersion,
		ChildVersion:  req.ChildTypeVersion,
	})
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusCreated, snap)
}

func (s *server) getRelationship(w http.ResponseWriter, r *http.Request) {
	snap, err := application.FromContext(r.Context()).Relationships().Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

func (s *server) unlinkRelationship(w http.ResponseWriter, r *http.Request) {
	snap, err := application.FromContext(r.Context()).Relationships().Unlink(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

func (s *server) listRelationships(w http.ResponseWriter, r *http.Request) {
	out, err := application.FromContext(r.Context()).Relationships().List(r.Context(), apprelationship.ListInput{
		DefinitionID:    r.URL.Query().Get("relationship_definition_id"),
		ParentEntityID:  r.URL.Query().Get("parent_entity_id"),
		ChildEntityID:   r.URL.Query().Get("child_entity_id"),
		IncludeArchived: boolQuery(r, "include_archived"),
		Page:            pageArgs(r),
	})
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, listResponse{Items: out.Items, PageInfo: out.PageInfo})
}

func (s *server) listEntityRelationships(w http.ResponseWriter, r *http.Request) {
	links, err := application.FromContext(r.Context()).Relationships().ListByEntity(r.Context(), chi.URLParam(r, "entityID"))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": links})
}

// --- features --------------------------------------------------------------------

func (s *server) features(w http.ResponseWriter, r *http.Request) {
	f := application.FromContext(r.Context()).Features()
	writeJSON(w, http.StatusOK, map[string]bool{
		"search":         !f.DisableSearch,
		"activity":       !f.DisableActivity,
		"search_index":   f.SearchIndex,
		"event_delivery": f.EventDelivery,
	})
}

func (s *server) featureDisabled(w http.ResponseWriter, feature string) {
	var body errorBody
	body.Error.Code = "FEATURE_DISABLED"
	body.Error.Message = "the " + feature + " feature is disabled in this deployment"
	writeJSON(w, http.StatusNotImplemented, body)
}

// --- query ---------------------------------------------------------------------

func (s *server) runQuery(w http.ResponseWriter, r *http.Request) {
	if application.FromContext(r.Context()).Features().DisableSearch {
		s.featureDisabled(w, "search")
		return
	}
	out, err := application.FromContext(r.Context()).Query().Execute(r.Context(), appquery.ExecuteInput{
		Type:  r.URL.Query().Get("type"),
		Query: r.URL.Query().Get("q"),
		Page:  pageArgs(r),
		Scope: valueobjects.Scope{Locale: r.URL.Query().Get("locale"), Channel: r.URL.Query().Get("channel")},
	})
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, listResponse{Items: out.Items, PageInfo: out.PageInfo})
}

type validateQueryRequest struct {
	Type  string `json:"type"`
	Query string `json:"q"`
}

func (s *server) validateQuery(w http.ResponseWriter, r *http.Request) {
	if application.FromContext(r.Context()).Features().DisableSearch {
		s.featureDisabled(w, "search")
		return
	}
	var req validateQueryRequest
	if err := decode(r, &req); err != nil {
		writeError(w, s.log, err)
		return
	}
	if err := application.FromContext(r.Context()).Query().Validate(r.Context(), req.Type, req.Query); err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"valid": true})
}

func (s *server) reindexSearch(w http.ResponseWriter, r *http.Request) {
	if s.reindex == nil {
		s.featureDisabled(w, "search index")
		return
	}
	count, err := s.reindex(r.Context(), uow.TenantFromContext(r.Context()))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"reindexed": count})
}

// --- activity ----------------------------------------------------------------

func (s *server) listActivity(w http.ResponseWriter, r *http.Request) {
	if application.FromContext(r.Context()).Features().DisableActivity {
		s.featureDisabled(w, "activity history")
		return
	}
	out, err := application.FromContext(r.Context()).Activity().List(r.Context(), application.ActivityListInput{
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
