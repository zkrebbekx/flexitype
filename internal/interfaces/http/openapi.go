package http

import (
	"net/http"

	"github.com/zkrebbekx/flexitype/api"
)

func (s *server) openAPIJSON(w http.ResponseWriter, _ *http.Request) {
	spec, err := api.SpecJSON()
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(spec)
}

func (s *server) openAPIYAML(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/yaml")
	_, _ = w.Write(api.SpecYAML)
}
