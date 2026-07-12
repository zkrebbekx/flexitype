package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/zkrebbekx/flexitype/application"
	"github.com/zkrebbekx/flexitype/application/schema/templates"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
)

func (s *server) listTemplates(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"items": templates.List()})
}

func (s *server) getTemplate(w http.ResponseWriter, r *http.Request) {
	t, ok := templates.Get(chi.URLParam(r, "name"))
	if !ok {
		writeError(w, s.log, domainerrors.NewNotFound("template", chi.URLParam(r, "name")))
		return
	}
	writeJSON(w, http.StatusOK, t)
}

// applyTemplate imports a curated template bundle into the caller's tenant.
// Import is idempotent, so applying a template twice is safe.
func (s *server) applyTemplate(w http.ResponseWriter, r *http.Request) {
	t, ok := templates.Get(chi.URLParam(r, "name"))
	if !ok {
		writeError(w, s.log, domainerrors.NewNotFound("template", chi.URLParam(r, "name")))
		return
	}
	res, err := application.FromContext(r.Context()).Schema().Import(r.Context(), t.Bundle)
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}
