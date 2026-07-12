package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/zkrebbekx/flexitype/application"
	appunit "github.com/zkrebbekx/flexitype/application/unit"
)

type unitFamilyRequest struct {
	Name     string             `json:"name"`
	BaseUnit string             `json:"base_unit"`
	Units    map[string]float64 `json:"units"`
}

func (s *server) createUnitFamily(w http.ResponseWriter, r *http.Request) {
	var req unitFamilyRequest
	if err := decode(r, &req); err != nil {
		writeError(w, s.log, err)
		return
	}
	f, err := application.FromContext(r.Context()).Units().Create(r.Context(), appunit.CreateInput{
		Name: req.Name, BaseUnit: req.BaseUnit, Units: req.Units,
	})
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusCreated, f)
}

func (s *server) listUnitFamilies(w http.ResponseWriter, r *http.Request) {
	families, err := application.FromContext(r.Context()).Units().List(r.Context())
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": families})
}

func (s *server) getUnitFamily(w http.ResponseWriter, r *http.Request) {
	f, err := application.FromContext(r.Context()).Units().Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, f)
}

func (s *server) deleteUnitFamily(w http.ResponseWriter, r *http.Request) {
	if err := application.FromContext(r.Context()).Units().Delete(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeError(w, s.log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
