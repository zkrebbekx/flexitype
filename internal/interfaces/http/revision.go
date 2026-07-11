package http

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/zkrebbekx/flexitype/application"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
)

type createRevisionRequest struct {
	Label string `json:"label,omitempty"`
}

func (s *server) createRevision(w http.ResponseWriter, r *http.Request) {
	var req createRevisionRequest
	if r.ContentLength > 0 {
		if err := decode(r, &req); err != nil {
			writeError(w, s.log, err)
			return
		}
	}
	rev, err := application.FromContext(r.Context()).Revisions().Create(r.Context(),
		chi.URLParam(r, "typeDefinitionID"), chi.URLParam(r, "entityID"), req.Label)
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusCreated, rev)
}

func (s *server) listRevisions(w http.ResponseWriter, r *http.Request) {
	revs, err := application.FromContext(r.Context()).Revisions().List(r.Context(),
		chi.URLParam(r, "typeDefinitionID"), chi.URLParam(r, "entityID"))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": revs})
}

func (s *server) getRevision(w http.ResponseWriter, r *http.Request) {
	rev, err := application.FromContext(r.Context()).Revisions().Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, rev)
}

func (s *server) entityAsOf(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("at")
	at, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		writeError(w, s.log, domainerrors.NewValidation("at must be an RFC3339 timestamp", "at", raw))
		return
	}
	rev, err := application.FromContext(r.Context()).Revisions().AsOf(r.Context(),
		chi.URLParam(r, "typeDefinitionID"), chi.URLParam(r, "entityID"), at)
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, rev)
}

func (s *server) diffRevisions(w http.ResponseWriter, r *http.Request) {
	to := r.URL.Query().Get("to")
	if to == "" {
		writeError(w, s.log, domainerrors.NewValidation("to (revision id) is required"))
		return
	}
	out, err := application.FromContext(r.Context()).Revisions().Diff(r.Context(), chi.URLParam(r, "id"), to)
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) restoreRevision(w http.ResponseWriter, r *http.Request) {
	rev, err := application.FromContext(r.Context()).Revisions().Restore(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusCreated, rev)
}
