package controlplane

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/sudatti624/AmuOS/internal/store"
)

func (s *Server) applications(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.Applications(r.Context(), sessionToken(r), chi.URLParam(r, "id"), s.clock())
	if err != nil {
		s.failure(w, r, err)
		return
	}
	respond(w, 200, map[string]any{"items": items})
}
func (s *Server) application(w http.ResponseWriter, r *http.Request) {
	a, err := s.store.Application(r.Context(), sessionToken(r), chi.URLParam(r, "id"), chi.URLParam(r, "name"), s.clock())
	if err != nil {
		s.failure(w, r, err)
		return
	}
	respond(w, 200, a)
}
func (s *Server) putApplication(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(w, r) {
		return
	}
	var body struct {
		ExpectedRevision int64                   `json:"expected_revision"`
		Config           store.ApplicationConfig `json:"config"`
	}
	if !s.decode(w, r, &body) {
		return
	}
	a, err := s.store.PutApplication(r.Context(), sessionToken(r), r.Header.Get("X-CSRF-Token"), chi.URLParam(r, "id"), chi.URLParam(r, "name"), body.ExpectedRevision, body.Config, s.clock())
	if err != nil {
		s.failure(w, r, err)
		return
	}
	respond(w, 200, a)
}
func (s *Server) archiveApplication(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(w, r) {
		return
	}
	var body struct {
		ExpectedRevision int64 `json:"expected_revision"`
	}
	if !s.decode(w, r, &body) {
		return
	}
	a, err := s.store.ArchiveApplication(r.Context(), sessionToken(r), r.Header.Get("X-CSRF-Token"), chi.URLParam(r, "id"), chi.URLParam(r, "name"), body.ExpectedRevision, s.clock())
	if err != nil {
		s.failure(w, r, err)
		return
	}
	respond(w, 200, a)
}
