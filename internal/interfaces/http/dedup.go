package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/zkrebbekx/flexitype/application"
	appdedup "github.com/zkrebbekx/flexitype/application/dedup"
)

type matchRuleRequest struct {
	AttributeDefinitionID string  `json:"attribute_definition_id"`
	Strategy              string  `json:"strategy"`
	Threshold             float64 `json:"threshold"`
}

func (s *server) createMatchRule(w http.ResponseWriter, r *http.Request) {
	var req matchRuleRequest
	if err := decode(r, &req); err != nil {
		writeError(w, s.log, err)
		return
	}
	rule, err := application.FromContext(r.Context()).Dedup().CreateRule(r.Context(), appdedup.CreateRuleInput{
		TypeDefinitionID:      chi.URLParam(r, "id"),
		AttributeDefinitionID: req.AttributeDefinitionID,
		Strategy:              appdedup.Strategy(req.Strategy),
		Threshold:             req.Threshold,
	})
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusCreated, rule)
}

func (s *server) listMatchRules(w http.ResponseWriter, r *http.Request) {
	rules, err := application.FromContext(r.Context()).Dedup().ListRules(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeItems(w, rules)
}

func (s *server) deleteMatchRule(w http.ResponseWriter, r *http.Request) {
	if err := application.FromContext(r.Context()).Dedup().DeleteRule(r.Context(), chi.URLParam(r, "ruleID")); err != nil {
		writeError(w, s.log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) scanMatchRule(w http.ResponseWriter, r *http.Request) {
	out, err := application.FromContext(r.Context()).Dedup().Scan(r.Context(), chi.URLParam(r, "ruleID"))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type dismissRequest struct {
	EntityA string `json:"entity_a"`
	EntityB string `json:"entity_b"`
}

func (s *server) dismissMatch(w http.ResponseWriter, r *http.Request) {
	var req dismissRequest
	if err := decode(r, &req); err != nil {
		writeError(w, s.log, err)
		return
	}
	if err := application.FromContext(r.Context()).Dedup().Dismiss(r.Context(), chi.URLParam(r, "ruleID"), req.EntityA, req.EntityB); err != nil {
		writeError(w, s.log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
