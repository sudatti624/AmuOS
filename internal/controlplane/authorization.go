package controlplane

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/sudatti624/AmuOS/internal/rbac"
)

func (s *Server) groups(w http.ResponseWriter, r *http.Request) {
	groups, err := s.store.Groups(r.Context(), sessionToken(r), s.clock())
	if err != nil {
		s.failure(w, r, err)
		return
	}
	respond(w, 200, map[string]any{"items": groups})
}

func (s *Server) createGroup(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(w, r) {
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if !s.decode(w, r, &body) {
		return
	}
	g, err := s.store.CreateGroup(r.Context(), sessionToken(r), r.Header.Get("X-CSRF-Token"), body.Name, s.clock())
	if err != nil {
		s.failure(w, r, err)
		return
	}
	respond(w, 201, g)
}

func (s *Server) setGroupMembers(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(w, r) {
		return
	}
	var body struct {
		UserIDs []string `json:"user_ids"`
	}
	if !s.decode(w, r, &body) {
		return
	}
	if err := s.store.SetGroupMembers(r.Context(), sessionToken(r), r.Header.Get("X-CSRF-Token"), chi.URLParam(r, "id"), body.UserIDs, s.clock()); err != nil {
		s.failure(w, r, err)
		return
	}
	respond(w, 200, map[string]string{"status": "updated"})
}

func (s *Server) setGroupGrants(w http.ResponseWriter, r *http.Request) { s.setGrants(w, r, true) }
func (s *Server) setUserGrants(w http.ResponseWriter, r *http.Request)  { s.setGrants(w, r, false) }

func (s *Server) setGrants(w http.ResponseWriter, r *http.Request, group bool) {
	if !s.sameOrigin(w, r) {
		return
	}
	var body struct {
		Grants []rbac.Grant `json:"grants"`
	}
	if !s.decode(w, r, &body) {
		return
	}
	var err error
	if group {
		err = s.store.SetGroupGrants(r.Context(), sessionToken(r), r.Header.Get("X-CSRF-Token"), chi.URLParam(r, "id"), body.Grants, s.clock())
	} else {
		err = s.store.SetUserGrants(r.Context(), sessionToken(r), r.Header.Get("X-CSRF-Token"), chi.URLParam(r, "id"), body.Grants, s.clock())
	}
	if err != nil {
		s.failure(w, r, err)
		return
	}
	respond(w, 200, map[string]string{"status": "updated"})
}

func (s *Server) deleteGroup(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(w, r) {
		return
	}
	if err := s.store.DeleteGroup(r.Context(), sessionToken(r), r.Header.Get("X-CSRF-Token"), chi.URLParam(r, "id"), s.clock()); err != nil {
		s.failure(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) userGrants(w http.ResponseWriter, r *http.Request) {
	grants, err := s.store.UserGrants(r.Context(), sessionToken(r), chi.URLParam(r, "id"), s.clock())
	if err != nil {
		s.failure(w, r, err)
		return
	}
	respond(w, 200, map[string]any{"items": grants})
}
