package controlplane

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (s *Server) setOwnership(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(w, r) {
		return
	}
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if !s.decode(w, r, &body) {
		return
	}
	if body.Enabled == nil {
		s.reject(w, r, 400, "invalid_input")
		return
	}
	if err := s.store.SetOwnership(r.Context(), sessionToken(r), r.Header.Get("X-CSRF-Token"), chi.URLParam(r, "id"), *body.Enabled, s.clock()); err != nil {
		s.failure(w, r, err)
		return
	}
	respond(w, 200, map[string]string{"status": "updated"})
}

func (s *Server) transferOwnership(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(w, r) {
		return
	}
	var body struct {
		UserID string `json:"user_id"`
	}
	if !s.decode(w, r, &body) {
		return
	}
	if body.UserID == "" {
		s.reject(w, r, 400, "invalid_input")
		return
	}
	if err := s.store.TransferOwnership(r.Context(), sessionToken(r), r.Header.Get("X-CSRF-Token"), body.UserID, s.clock()); err != nil {
		s.failure(w, r, err)
		return
	}
	clearSession(w)
	respond(w, 200, map[string]string{"status": "transferred"})
}
