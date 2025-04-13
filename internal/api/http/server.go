package http

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/oklog/ulid"
	"github.com/zkrebbekx/flexitype/internal/domain/model"
	"github.com/zkrebbekx/flexitype/internal/domain/service"
)

// Server represents the HTTP server
type Server struct {
	service *service.Service
	server  *http.Server
}

// NewServer creates a new HTTP server
func NewServer(service *service.Service, addr string) *Server {
	s := &Server{
		service: service,
		server: &http.Server{
			Addr:         addr,
			ReadTimeout:  10 * time.Second,
			WriteTimeout: 10 * time.Second,
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/attributes", s.handleAttributes)
	mux.HandleFunc("/api/v1/attributes/", s.handleAttribute)
	mux.HandleFunc("/api/v1/values", s.handleValues)
	mux.HandleFunc("/api/v1/values/", s.handleValue)
	mux.HandleFunc("/api/v1/links", s.handleLinks)
	mux.HandleFunc("/api/v1/links/", s.handleLink)
	mux.HandleFunc("/api/v1/search", s.handleSearch)

	s.server.Handler = mux
	return s
}

// Start starts the HTTP server
func (s *Server) Start() error {
	return s.server.ListenAndServe()
}

// Shutdown gracefully shuts down the server
func (s *Server) Shutdown(ctx context.Context) error {
	return s.server.Shutdown(ctx)
}

// handleAttributes handles attribute collection operations
func (s *Server) handleAttributes(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.listAttributes(w, r)
	case http.MethodPost:
		s.createAttribute(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleAttribute handles single attribute operations
func (s *Server) handleAttribute(w http.ResponseWriter, r *http.Request) {
	id, err := ulid.Parse(r.URL.Path[len("/api/v1/attributes/"):])
	if err != nil {
		http.Error(w, "Invalid ID", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.getAttribute(w, r, id)
	case http.MethodPut:
		s.updateAttribute(w, r, id)
	case http.MethodDelete:
		s.deleteAttribute(w, r, id)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleValues handles attribute value collection operations
func (s *Server) handleValues(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.listValues(w, r)
	case http.MethodPost:
		s.createValue(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleValue handles single attribute value operations
func (s *Server) handleValue(w http.ResponseWriter, r *http.Request) {
	id, err := ulid.Parse(r.URL.Path[len("/api/v1/values/"):])
	if err != nil {
		http.Error(w, "Invalid ID", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.getValue(w, r, id)
	case http.MethodPut:
		s.updateValue(w, r, id)
	case http.MethodDelete:
		s.deleteValue(w, r, id)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleLinks handles type link collection operations
func (s *Server) handleLinks(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.listLinks(w, r)
	case http.MethodPost:
		s.createLink(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleLink handles single type link operations
func (s *Server) handleLink(w http.ResponseWriter, r *http.Request) {
	id, err := ulid.Parse(r.URL.Path[len("/api/v1/links/"):])
	if err != nil {
		http.Error(w, "Invalid ID", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.getLink(w, r, id)
	case http.MethodPut:
		s.updateLink(w, r, id)
	case http.MethodDelete:
		s.deleteLink(w, r, id)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleSearch handles search operations
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	query := r.URL.Query().Get("q")
	if query == "" {
		http.Error(w, "Query parameter 'q' is required", http.StatusBadRequest)
		return
	}

	// TODO: Implement search functionality
}

// Helper functions for handling requests
func (s *Server) createAttribute(w http.ResponseWriter, r *http.Request) {
	var attr model.Attribute
	if err := json.NewDecoder(r.Body).Decode(&attr); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if err := s.service.CreateAttribute(r.Context(), &attr); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(attr)
}

func (s *Server) getAttribute(w http.ResponseWriter, r *http.Request, id ulid.ULID) {
	attr, err := s.service.GetAttribute(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(attr)
}

func (s *Server) updateAttribute(w http.ResponseWriter, r *http.Request, id ulid.ULID) {
	var attr model.Attribute
	if err := json.NewDecoder(r.Body).Decode(&attr); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	attr.ID = id
	if err := s.service.UpdateAttribute(r.Context(), &attr); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(attr)
}

func (s *Server) deleteAttribute(w http.ResponseWriter, r *http.Request, id ulid.ULID) {
	if err := s.service.DeleteAttribute(r.Context(), id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listAttributes(w http.ResponseWriter, r *http.Request) {
	attrs, err := s.service.ListAttributes(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(attrs)
}

// Similar helper functions for attribute values and type links...
// TODO: Implement the remaining helper functions 