package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/zkrebbekx/flexitype/application"
	appschema "github.com/zkrebbekx/flexitype/application/schema"
)

type cloneTypeRequest struct {
	InternalName string `json:"internal_name"`
	DisplayName  string `json:"display_name"`
}

func (s *server) cloneType(w http.ResponseWriter, r *http.Request) {
	var req cloneTypeRequest
	if err := decode(r, &req); err != nil {
		writeError(w, s.log, err)
		return
	}
	res, err := application.FromContext(r.Context()).Schema().Clone(r.Context(), appschema.CloneInput{
		SourceTypeID: chi.URLParam(r, "id"),
		InternalName: req.InternalName,
		DisplayName:  req.DisplayName,
	})
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusCreated, res)
}
