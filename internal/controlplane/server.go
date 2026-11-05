package controlplane

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/sudatti624/AmuOS/internal/security"
	"github.com/sudatti624/AmuOS/internal/store"
	"github.com/sudatti624/AmuOS/internal/webui"
)

const cookieName = "__Host-amuos_session"

type bucket struct {
	Count int
	Until time.Time
}
type Server struct {
	store      *store.Store
	origin     string
	clock      func() time.Time
	mu         sync.Mutex
	attempts   map[string]bucket
	authSlots  chan struct{}
	rejections rejectionLimiter
}

func New(db *store.Store, origin string) (http.Handler, error) {
	_, h, err := newServer(db, origin)
	return h, err
}

func newServer(db *store.Store, origin string) (*Server, http.Handler, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
		return nil, nil, errors.New("origin must be an HTTPS origin without a path")
	}
	s := &Server{store: db, origin: origin, clock: time.Now, attempts: map[string]bucket{}, authSlots: make(chan struct{}, 1), rejections: rejectionLimiter{clients: map[string]bucket{}}}
	r := chi.NewRouter()
	r.Use(s.headers)
	r.NotFound(func(w http.ResponseWriter, r *http.Request) { s.reject(w, r, 404, "not_found") })
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) { s.reject(w, r, 405, "method_not_allowed") })
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) { respond(w, 200, map[string]string{"status": "ok"}) })
	r.Post("/api/v1/setup", s.setup)
	r.Post("/api/v1/auth/login", s.login)
	r.Post("/api/v1/auth/logout", s.logout)
	r.Post("/api/v1/auth/reauthenticate", s.reauthenticate)
	r.Post("/api/v1/auth/password", s.changePassword)
	r.Get("/api/v1/users", s.users)
	r.Patch("/api/v1/users/{id}/enabled", s.setUserEnabled)
	r.Post("/api/v1/invitations", s.createInvitation)
	r.Post("/api/v1/invitations/accept", s.acceptInvitation)
	r.Delete("/api/v1/invitations/{id}", s.revokeInvitation)
	r.Get("/api/v1/groups", s.groups)
	r.Post("/api/v1/groups", s.createGroup)
	r.Put("/api/v1/groups/{id}/members", s.setGroupMembers)
	r.Put("/api/v1/groups/{id}/grants", s.setGroupGrants)
	r.Delete("/api/v1/groups/{id}", s.deleteGroup)
	r.Get("/api/v1/users/{id}/grants", s.userGrants)
	r.Put("/api/v1/users/{id}/grants", s.setUserGrants)
	r.Put("/api/v1/users/{id}/owner", s.setOwnership)
	r.Post("/api/v1/ownership/transfer", s.transferOwnership)
	r.Get("/api/v1/me", s.identity)
	r.Get("/api/v1/hosts", s.hosts)
	r.Post("/api/v1/hosts", s.createHost)
	r.Post("/api/v1/hosts/{id}/tasks", s.createTask)
	r.Get("/api/v1/tasks", s.tasks)
	r.Get("/api/v1/tasks/{id}", s.task)
	r.Post("/api/v1/tasks/{id}/cancel", s.cancelTask)
	r.Post("/api/v1/tasks/{id}/resolve", s.resolveTask)
	r.Get("/api/v1/audit", s.events)
	r.Get("/api/v1/audit/retention", s.auditRetention)
	r.Put("/api/v1/audit/retention", s.setAuditRetention)
	r.Post("/api/v1/audit/retention/run", s.purgeAudit)
	for _, page := range webui.Pages {
		r.Get(page, webui.Shell)
	}
	asset := webui.Asset(func(w http.ResponseWriter, r *http.Request) { s.reject(w, r, 404, "not_found") })
	r.Get("/assets/*", asset)
	r.Get("/favicon.png", asset)
	return s, r, nil
}

func (s *Server) headers(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := security.Token()
		r = r.WithContext(store.WithRequestID(r.Context(), id))
		w.Header().Set("X-Request-ID", id)
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		next.ServeHTTP(w, r)
	})
}

func respond(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func (s *Server) failure(w http.ResponseWriter, r *http.Request, err error) {
	status, code := http.StatusInternalServerError, "internal_error"
	switch {
	case errors.Is(err, store.ErrCredentials):
		status, code = 401, "unauthorized"
	case errors.Is(err, store.ErrDenied):
		status, code = 403, "forbidden"
	case errors.Is(err, store.ErrConflict):
		status, code = 409, "conflict"
	case errors.Is(err, store.ErrInput):
		status, code = 400, "invalid_input"
	case errors.Is(err, store.ErrNotFound):
		status, code = 404, "not_found"
	}
	if status < 500 {
		s.reject(w, r, status, code)
		return
	}
	slog.Error("request failed", "request_id", store.RequestID(r.Context()))
	respond(w, status, map[string]string{"error": code})
}

func (s *Server) decode(w http.ResponseWriter, r *http.Request, value any) bool {
	if strings.Split(r.Header.Get("Content-Type"), ";")[0] != "application/json" {
		s.reject(w, r, 415, "json_required")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		s.reject(w, r, 400, "invalid_json")
		return false
	}
	if err := d.Decode(new(any)); !errors.Is(err, io.EOF) {
		s.reject(w, r, 400, "invalid_json")
		return false
	}
	return true
}

func sessionToken(r *http.Request) string {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return ""
	}
	return c.Value
}

func (s *Server) sameOrigin(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("Origin") != s.origin {
		s.reject(w, r, 403, "origin_rejected")
		return false
	}
	return true
}

func (s *Server) acquireAuth(w http.ResponseWriter, r *http.Request) bool {
	select {
	case s.authSlots <- struct{}{}:
		return true
	default:
		w.Header().Set("Retry-After", "1")
		s.reject(w, r, 503, "auth_busy")
		return false
	}
}

func (s *Server) setup(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(w, r) || !s.authAllowed(w, r) {
		return
	}
	var body struct {
		Token    string `json:"token"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !s.decode(w, r, &body) {
		return
	}
	if len(body.Password) < 12 || len(body.Password) > 128 || len(body.Username) < 3 || len(body.Username) > 64 {
		s.reject(w, r, 400, "invalid_input")
		return
	}
	if !s.acquireAuth(w, r) {
		return
	}
	defer func() { <-s.authSlots }()
	if err := s.store.Setup(r.Context(), body.Token, body.Username, body.Password, s.clock()); err != nil {
		s.failure(w, r, err)
		return
	}
	respond(w, 201, map[string]string{"status": "initialized"})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(w, r) || !s.authAllowed(w, r) {
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !s.decode(w, r, &body) {
		return
	}
	if len(body.Password) > 128 || len(body.Username) > 64 {
		s.reject(w, r, 400, "invalid_input")
		return
	}
	if !s.acquireAuth(w, r) {
		return
	}
	defer func() { <-s.authSlots }()
	session, err := s.store.Login(r.Context(), body.Username, body.Password, s.clock())
	if err != nil {
		s.failure(w, r, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: session.Token, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: 12 * 60 * 60})
	respond(w, 200, map[string]string{"user_id": session.UserID, "csrf_token": session.CSRF})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(w, r) {
		return
	}
	if err := s.store.Logout(r.Context(), sessionToken(r), r.Header.Get("X-CSRF-Token"), s.clock()); err != nil {
		s.failure(w, r, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	respond(w, 200, map[string]string{"status": "logged_out"})
}

func (s *Server) identity(w http.ResponseWriter, r *http.Request) {
	p, username, err := s.store.Profile(r.Context(), sessionToken(r), s.clock())
	if err != nil {
		s.failure(w, r, err)
		return
	}
	respond(w, 200, map[string]any{"id": p.ID, "username": username, "grants": p.Grants, "authorization_version": p.Version})
}

func (s *Server) hosts(w http.ResponseWriter, r *http.Request) {
	hosts, err := s.store.Hosts(r.Context(), sessionToken(r), s.clock())
	if err != nil {
		s.failure(w, r, err)
		return
	}
	respond(w, 200, map[string]any{"items": hosts})
}

func (s *Server) createHost(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(w, r) {
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if !store.ValidIdempotencyKey(key) {
		s.reject(w, r, 400, "idempotency_key_required")
		return
	}
	r = r.WithContext(store.WithIdempotencyKey(r.Context(), key))
	var body struct {
		Name string `json:"name"`
	}
	if !s.decode(w, r, &body) {
		return
	}
	if len(strings.TrimSpace(body.Name)) < 1 || len(body.Name) > 128 {
		s.reject(w, r, 400, "invalid_input")
		return
	}
	host, err := s.store.CreateHost(r.Context(), sessionToken(r), r.Header.Get("X-CSRF-Token"), body.Name, s.clock())
	if err != nil {
		s.failure(w, r, err)
		return
	}
	if host.Replayed {
		w.Header().Set("Idempotency-Replayed", "true")
	}
	respond(w, 201, host)
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	events, err := s.store.Events(r.Context(), sessionToken(r), s.clock())
	if err != nil {
		s.failure(w, r, err)
		return
	}
	respond(w, 200, map[string]any{"items": events})
}
