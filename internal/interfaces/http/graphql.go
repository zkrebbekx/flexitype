package http

import (
	"encoding/json"
	"net/http"

	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
)

// graphqlRequest is the standard GraphQL-over-HTTP POST body.
type graphqlRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
	// OperationName is accepted for protocol compatibility; single-operation
	// documents are the norm here.
	OperationName string `json:"operationName"`
}

// graphqlQuery serves the read-only GraphQL API. The query comes from the POST
// body or, for GET, the "query" parameter. Tenant and access are already on
// the request context (auth + interactor middleware), so the engine builds the
// caller's own schema.
func (s *server) graphqlQuery(w http.ResponseWriter, r *http.Request) {
	if s.graphql == nil {
		writeError(w, s.log, domainerrors.NewValidation("the GraphQL API is not enabled in this deployment"))
		return
	}

	var req graphqlRequest
	if r.Method == http.MethodGet {
		req.Query = r.URL.Query().Get("query")
		if raw := r.URL.Query().Get("variables"); raw != "" {
			_ = json.Unmarshal([]byte(raw), &req.Variables)
		}
	} else if err := decode(r, &req); err != nil {
		writeError(w, s.log, err)
		return
	}
	if req.Query == "" {
		writeError(w, s.log, domainerrors.NewValidation("a GraphQL query is required"))
		return
	}

	result := s.graphql.Execute(r.Context(), req.Query, req.Variables)
	// GraphQL returns 200 with an "errors" array even for query-level errors;
	// that is the contract clients expect.
	writeJSON(w, http.StatusOK, result)
}
