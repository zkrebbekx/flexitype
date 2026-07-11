// Package health provides liveness/readiness checking with pluggable
// dependency checks, served at /healthz (process up) and /readyz
// (dependencies up).
package health

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// Status of a single check or the whole service.
type Status string

// The recognised health statuses.
const (
	StatusOK   Status = "ok"
	StatusDown Status = "down"
)

// Check is one dependency's health at a point in time.
type Check struct {
	Name        string        `json:"name"`
	Status      Status        `json:"status"`
	Error       string        `json:"error,omitempty"`
	Duration    time.Duration `json:"duration_ns"`
	LastChecked time.Time     `json:"last_checked"`
}

// CheckFunc probes one dependency.
type CheckFunc func(ctx context.Context) error

// Service runs registered checks and serves the health endpoints.
type Service struct {
	name    string
	version string

	mu     sync.RWMutex
	checks map[string]CheckFunc
}

// NewService builds a health service.
func NewService(name, version string) *Service {
	return &Service{name: name, version: version, checks: make(map[string]CheckFunc)}
}

// RegisterCheckFunc adds a named dependency check.
func (s *Service) RegisterCheckFunc(name string, fn CheckFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.checks[name] = fn
}

type response struct {
	Service string  `json:"service"`
	Version string  `json:"version"`
	Status  Status  `json:"status"`
	Checks  []Check `json:"checks,omitempty"`
}

// LiveHandler serves liveness: the process is up.
func (s *Service) LiveHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, response{Service: s.name, Version: s.version, Status: StatusOK})
	}
}

// ReadyHandler serves readiness: every dependency check passes.
func (s *Service) ReadyHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		resp := response{Service: s.name, Version: s.version, Status: StatusOK}

		s.mu.RLock()
		names := make([]string, 0, len(s.checks))
		fns := make([]CheckFunc, 0, len(s.checks))
		for name, fn := range s.checks {
			names = append(names, name)
			fns = append(fns, fn)
		}
		s.mu.RUnlock()

		for i, fn := range fns {
			start := time.Now()
			err := fn(ctx)
			check := Check{
				Name:        names[i],
				Status:      StatusOK,
				Duration:    time.Since(start),
				LastChecked: time.Now(),
			}
			if err != nil {
				check.Status = StatusDown
				check.Error = err.Error()
				resp.Status = StatusDown
			}
			resp.Checks = append(resp.Checks, check)
		}

		code := http.StatusOK
		if resp.Status == StatusDown {
			code = http.StatusServiceUnavailable
		}
		writeJSON(w, code, resp)
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
