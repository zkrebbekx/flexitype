package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/zkrebbekx/flexitype/application/admin"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
)

// adminDisabled reports a 501 when provisioning isn't configured (no
// database-backed control plane).
func (s *server) adminDisabled(w http.ResponseWriter) {
	var body errorBody
	body.Error.Code = "FEATURE_DISABLED"
	body.Error.Message = "tenant provisioning is not enabled in this deployment"
	writeJSON(w, http.StatusNotImplemented, body)
}

// adminReady gates provisioning on both configuration and the admin scope.
func (s *server) adminReady(w http.ResponseWriter, r *http.Request) bool {
	if s.admin == nil {
		s.adminDisabled(w)
		return false
	}
	return s.requireAdmin(w, r)
}

type createTenantRequest struct {
	Name string `json:"name"`
}

func (s *server) createTenant(w http.ResponseWriter, r *http.Request) {
	if !s.adminReady(w, r) {
		return
	}
	var req createTenantRequest
	if err := decode(r, &req); err != nil {
		writeError(w, s.log, err)
		return
	}
	t, err := s.admin.CreateTenant(r.Context(), req.Name)
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusCreated, t)
}

func (s *server) listTenants(w http.ResponseWriter, r *http.Request) {
	if !s.adminReady(w, r) {
		return
	}
	tenants, err := s.admin.ListTenants(r.Context())
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": tenants})
}

type setActiveRequest struct {
	Active bool `json:"active"`
}

func (s *server) setTenantActive(w http.ResponseWriter, r *http.Request) {
	if !s.adminReady(w, r) {
		return
	}
	var req setActiveRequest
	if err := decode(r, &req); err != nil {
		writeError(w, s.log, err)
		return
	}
	if err := s.admin.SetTenantActive(r.Context(), chi.URLParam(r, "name"), req.Active); err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": chi.URLParam(r, "name"), "active": req.Active})
}

type createAccountRequest struct {
	TenantName string   `json:"tenant_name"`
	Name       string   `json:"name"`
	Scopes     []string `json:"scopes"`
}

func (s *server) createServiceAccount(w http.ResponseWriter, r *http.Request) {
	if !s.adminReady(w, r) {
		return
	}
	var req createAccountRequest
	if err := decode(r, &req); err != nil {
		writeError(w, s.log, err)
		return
	}
	out, err := s.admin.CreateAccount(r.Context(), admin.CreateAccountInput{
		TenantName: req.TenantName,
		Name:       req.Name,
		Scopes:     req.Scopes,
	})
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	// The token appears exactly once, here.
	writeJSON(w, http.StatusCreated, out)
}

func (s *server) listServiceAccounts(w http.ResponseWriter, r *http.Request) {
	if !s.adminReady(w, r) {
		return
	}
	tenant := r.URL.Query().Get("tenant_name")
	if tenant == "" {
		writeError(w, s.log, domainerrors.NewValidation("tenant_name is required"))
		return
	}
	accounts, err := s.admin.ListAccounts(r.Context(), tenant)
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": accounts})
}

func (s *server) rotateServiceAccount(w http.ResponseWriter, r *http.Request) {
	if !s.adminReady(w, r) {
		return
	}
	out, err := s.admin.RotateSecret(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) revokeServiceAccount(w http.ResponseWriter, r *http.Request) {
	if !s.adminReady(w, r) {
		return
	}
	if err := s.admin.Revoke(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeError(w, s.log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
