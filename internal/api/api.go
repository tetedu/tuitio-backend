// Package api serves the Tuitio REST API from the indexed read model.
package api

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"

	"github.com/adelekevictor12/tuitio-backend/internal/store"
	"github.com/jackc/pgx/v5"
)

type Server struct {
	store *store.Store
}

func New(st *store.Store) *Server {
	return &Server{store: st}
}

func (s *Server) Routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /api/institutions", s.institutions)
	mux.HandleFunc("GET /api/institutions/{address}", s.institution)
	mux.HandleFunc("GET /api/grants", s.grants)
	mux.HandleFunc("GET /api/grants/{id}", s.grant)
	mux.HandleFunc("GET /api/grants/{id}/terms", s.terms)
	mux.HandleFunc("GET /api/activity", s.activity)
	mux.HandleFunc("GET /api/stats", s.stats)
	return mux
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "store not ready")
		return
	}
	if _, err := s.store.Stats(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) institutions(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.Institutions(r.Context())
	if err != nil {
		writeInternal(w, "list institutions", err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) institution(w http.ResponseWriter, r *http.Request) {
	one, err := s.store.Institution(r.Context(), r.PathValue("address"))
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "institution not found")
		return
	}
	if err != nil {
		writeInternal(w, "get institution", err)
		return
	}
	writeJSON(w, http.StatusOK, one)
}

func (s *Server) grants(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	list, err := s.store.Grants(r.Context(), q.Get("sponsor"), q.Get("institution"), q.Get("status"))
	if err != nil {
		writeInternal(w, "list grants", err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) grant(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r.PathValue("id"))
	if !ok {
		return
	}
	one, err := s.store.Grant(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "grant not found")
		return
	}
	if err != nil {
		writeInternal(w, "get grant", err)
		return
	}
	writeJSON(w, http.StatusOK, one)
}

func (s *Server) terms(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r.PathValue("id"))
	if !ok {
		return
	}
	if _, err := s.store.Grant(r.Context(), id); errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "grant not found")
		return
	}
	list, err := s.store.Terms(r.Context(), id)
	if err != nil {
		writeInternal(w, "list terms", err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) activity(w http.ResponseWriter, r *http.Request) {
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil {
		limit = 50
	}
	list, err := s.store.Activity(r.Context(), limit)
	if err != nil {
		writeInternal(w, "list activity", err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) stats(w http.ResponseWriter, r *http.Request) {
	st, err := s.store.Stats(r.Context())
	if err != nil {
		writeInternal(w, "stats", err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// ----- helpers ---------------------------------------------------------------

func parseID(w http.ResponseWriter, raw string) (int64, bool) {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id < 0 {
		writeError(w, http.StatusBadRequest, "grant id must be a non-negative integer")
		return 0, false
	}
	return id, true
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if v == nil {
		v = struct{}{}
	}
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("write response: %v", err)
	}
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func writeInternal(w http.ResponseWriter, op string, err error) {
	log.Printf("%s: %v", op, err)
	writeError(w, http.StatusInternalServerError, "internal error")
}
