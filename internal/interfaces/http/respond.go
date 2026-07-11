// Package http exposes flexitype's usecases as a versioned REST API for
// the standalone service. Handlers stay thin: decode, call an interactor,
// map the result.
package http

import (
	"encoding/json"
	"errors"
	"net/http"

	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/pkg/logger"
)

// errorBody is the wire form of every API error.
type errorBody struct {
	Error struct {
		Code    string         `json:"code"`
		Message string         `json:"message"`
		Details map[string]any `json:"details,omitempty"`
	} `json:"error"`
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

// writeError maps domain error codes onto HTTP statuses.
func writeError(w http.ResponseWriter, log *logger.Logger, err error) {
	var body errorBody
	body.Error.Code = "INTERNAL"
	body.Error.Message = "internal error"
	status := http.StatusInternalServerError

	var domainErr *domainerrors.Error
	if errors.As(err, &domainErr) {
		body.Error.Code = string(domainErr.Code)
		body.Error.Message = domainErr.Message
		body.Error.Details = domainErr.Details
		switch domainErr.Code {
		case domainerrors.CodeValidation, domainerrors.CodeDependency:
			status = http.StatusUnprocessableEntity
		case domainerrors.CodeNotFound:
			status = http.StatusNotFound
		case domainerrors.CodeConflict:
			status = http.StatusConflict
		case domainerrors.CodeArchived:
			status = http.StatusGone
		}
	} else if log != nil {
		log.Error().Err(err).Msg("request failed")
	}

	writeJSON(w, status, body)
}

// decode strictly parses a JSON request body into dst.
func decode(r *http.Request, dst any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return domainerrors.NewValidation("invalid request body", "error", err.Error())
	}
	return nil
}
