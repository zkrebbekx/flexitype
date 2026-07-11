package http

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/zkrebbekx/flexitype/application"
	appchangeset "github.com/zkrebbekx/flexitype/application/changeset"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
)

type createChangeSetRequest struct {
	Name            string     `json:"name"`
	RequireApproval bool       `json:"require_approval,omitempty"`
	PublishAt       *time.Time `json:"publish_at,omitempty"`
}

func (s *server) createChangeSet(w http.ResponseWriter, r *http.Request) {
	var req createChangeSetRequest
	if err := decode(r, &req); err != nil {
		writeError(w, s.log, err)
		return
	}
	cs, err := application.FromContext(r.Context()).ChangeSets().Create(r.Context(), appchangeset.CreateInput{
		Name: req.Name, RequireApproval: req.RequireApproval, PublishAt: req.PublishAt,
	})
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusCreated, cs)
}

func (s *server) listChangeSets(w http.ResponseWriter, r *http.Request) {
	sets, err := application.FromContext(r.Context()).ChangeSets().List(r.Context())
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": sets})
}

func (s *server) getChangeSet(w http.ResponseWriter, r *http.Request) {
	cs, err := application.FromContext(r.Context()).ChangeSets().Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, cs)
}

type mutationRequest struct {
	Kind                  string          `json:"kind"`
	AttributeDefinitionID string          `json:"attribute_definition_id"`
	EntityID              string          `json:"entity_id"`
	TypeDefinitionID      string          `json:"type_definition_id,omitempty"`
	Locale                string          `json:"locale,omitempty"`
	Channel               string          `json:"channel,omitempty"`
	Value                 json.RawMessage `json:"value,omitempty"`
}

func (s *server) addChangeSetMutation(w http.ResponseWriter, r *http.Request) {
	var req mutationRequest
	if err := decode(r, &req); err != nil {
		writeError(w, s.log, err)
		return
	}
	cs, err := application.FromContext(r.Context()).ChangeSets().AddMutation(r.Context(), chi.URLParam(r, "id"), appvalue.Mutation{
		Kind:                  req.Kind,
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
	writeJSON(w, http.StatusOK, cs)
}

func (s *server) submitChangeSet(w http.ResponseWriter, r *http.Request) {
	cs, err := application.FromContext(r.Context()).ChangeSets().Submit(r.Context(), chi.URLParam(r, "id"))
	s.writeChangeSet(w, cs, err)
}

func (s *server) approveChangeSet(w http.ResponseWriter, r *http.Request) {
	cs, err := application.FromContext(r.Context()).ChangeSets().Approve(r.Context(), chi.URLParam(r, "id"))
	s.writeChangeSet(w, cs, err)
}

func (s *server) rejectChangeSet(w http.ResponseWriter, r *http.Request) {
	cs, err := application.FromContext(r.Context()).ChangeSets().Reject(r.Context(), chi.URLParam(r, "id"))
	s.writeChangeSet(w, cs, err)
}

func (s *server) publishChangeSet(w http.ResponseWriter, r *http.Request) {
	cs, err := application.FromContext(r.Context()).ChangeSets().Publish(r.Context(), chi.URLParam(r, "id"))
	s.writeChangeSet(w, cs, err)
}

func (s *server) writeChangeSet(w http.ResponseWriter, cs *appchangeset.ChangeSet, err error) {
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, cs)
}
