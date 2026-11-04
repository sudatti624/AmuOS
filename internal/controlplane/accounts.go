package controlplane

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/sudatti624/AmuOS/internal/rbac"
)

func clearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
}

func (s *Server) reauthenticate(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(w, r) || !s.authAllowed(w, r) {
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if !decode(w, r, &body) {
		return
	}
	if len(body.Password) > 128 {
		respond(w, 400, map[string]string{"error": "invalid_input"})
		return
	}
	if !s.acquireAuth(w, r) {
		return
	}
	defer func() { <-s.authSlots }()
	if err := s.store.Reauthenticate(r.Context(), sessionToken(r), r.Header.Get("X-CSRF-Token"), body.Password, s.clock()); err != nil {
		failure(w, err)
		return
	}
	respond(w, 200, map[string]string{"status": "reauthenticated"})
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(w, r) || !s.authAllowed(w, r) {
		return
	}
	var body struct {
		Current string `json:"current_password"`
		New     string `json:"new_password"`
	}
	if !decode(w, r, &body) {
		return
	}
	if len(body.Current) > 128 || len(body.New) < 12 || len(body.New) > 128 {
		respond(w, 400, map[string]string{"error": "invalid_input"})
		return
	}
	if !s.acquireAuth(w, r) {
		return
	}
	defer func() { <-s.authSlots }()
	if err := s.store.ChangePassword(r.Context(), sessionToken(r), r.Header.Get("X-CSRF-Token"), body.Current, body.New, s.clock()); err != nil {
		failure(w, err)
		return
	}
	clearSession(w)
	respond(w, 200, map[string]string{"status": "password_changed"})
}

func (s *Server) users(w http.ResponseWriter, r *http.Request) {
	users, err := s.store.Users(r.Context(), sessionToken(r), s.clock())
	if err != nil {
		failure(w, err)
		return
	}
	respond(w, 200, map[string]any{"items": users})
}

func (s *Server) setUserEnabled(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(w, r) {
		return
	}
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Enabled == nil {
		respond(w, 400, map[string]string{"error": "invalid_input"})
		return
	}
	if err := s.store.SetUserEnabled(r.Context(), sessionToken(r), r.Header.Get("X-CSRF-Token"), chi.URLParam(r, "id"), *body.Enabled, s.clock()); err != nil {
		failure(w, err)
		return
	}
	respond(w, 200, map[string]string{"status": "updated"})
}

func (s *Server) createInvitation(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(w, r) {
		return
	}
	var body struct {
		Username string       `json:"username"`
		Grants   []rbac.Grant `json:"grants"`
	}
	if !decode(w, r, &body) {
		return
	}
	invitation, err := s.store.CreateInvitation(r.Context(), sessionToken(r), r.Header.Get("X-CSRF-Token"), body.Username, body.Grants, s.clock())
	if err != nil {
		failure(w, err)
		return
	}
	respond(w, 201, invitation)
}

func (s *Server) acceptInvitation(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(w, r) || !s.authAllowed(w, r) {
		return
	}
	var body struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if !decode(w, r, &body) {
		return
	}
	if len(body.Password) < 12 || len(body.Password) > 128 {
		respond(w, 400, map[string]string{"error": "invalid_input"})
		return
	}
	if !s.acquireAuth(w, r) {
		return
	}
	defer func() { <-s.authSlots }()
	user, err := s.store.AcceptInvitation(r.Context(), body.Token, body.Password, s.clock())
	if err != nil {
		failure(w, err)
		return
	}
	respond(w, 201, user)
}

func (s *Server) revokeInvitation(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(w, r) {
		return
	}
	if err := s.store.RevokeInvitation(r.Context(), sessionToken(r), r.Header.Get("X-CSRF-Token"), chi.URLParam(r, "id"), s.clock()); err != nil {
		failure(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
