package controlplane

import (
	"context"
	"encoding/base64"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/sudatti624/AmuOS/internal/agentca"
	"github.com/sudatti624/AmuOS/internal/store"
)

const (
	agentRequestLimit  = 120
	agentRequestWindow = time.Minute
	agentClients       = 65536
)

type agentKey struct{}

func agentFrom(ctx context.Context) store.AgentPrincipal {
	p, _ := ctx.Value(agentKey{}).(store.AgentPrincipal)
	return p
}

// Handlers returns the browser/enrollment handler and the mTLS Agent handler.
// The Agent handler must only be served by a listener that requires and
// verifies client certificates against the Agent CA.
func Handlers(db *store.Store, origin string, issuer store.Issuer) (public, agent http.Handler, err error) {
	s, h, err := newServer(db, origin)
	if err != nil {
		return nil, nil, err
	}
	s.issuer = issuer
	return h, s.agentRouter(), nil
}

func (s *Server) agentRouter() http.Handler {
	r := chi.NewRouter()
	r.Use(s.headers, s.agentIdentity)
	r.NotFound(func(w http.ResponseWriter, r *http.Request) { s.reject(w, r, 404, "not_found") })
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) { s.reject(w, r, 405, "method_not_allowed") })
	r.Post("/agent/v1/heartbeat", s.heartbeat)
	r.Post("/agent/v1/credentials/renew", s.renewCredential)
	return r
}

// agentIdentity reads the identity from the verified client certificate and
// applies the per-Agent rate limit. Trust is decided by the database in the
// same transaction as each request's work.
func (s *Server) agentIdentity(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || len(r.TLS.VerifiedChains) == 0 {
			s.reject(w, r, 401, "unauthorized")
			return
		}
		serial, agentID, err := agentca.Identity(r.TLS.VerifiedChains[0][0])
		if err != nil {
			s.reject(w, r, 401, "unauthorized")
			return
		}
		if !s.agentAllowed(agentID) {
			w.Header().Set("Retry-After", "60")
			s.reject(w, r, 429, "rate_limited")
			return
		}
		ctx := context.WithValue(r.Context(), agentKey{}, store.AgentPrincipal{AgentID: agentID, Serial: serial})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) agentAllowed(agentID string) bool {
	now := s.clock()
	s.mu.Lock()
	defer s.mu.Unlock()
	prune(s.agentRequests, agentClients, now)
	b, exists := s.agentRequests[agentID]
	if !exists || !now.Before(b.Until) {
		if !exists && len(s.agentRequests) >= agentClients {
			return false
		}
		b = bucket{Until: now.Add(agentRequestWindow)}
	}
	if b.Count >= agentRequestLimit {
		return false
	}
	b.Count++
	s.agentRequests[agentID] = b
	return true
}

func decodeBase64(value string) ([]byte, bool) {
	data, err := base64.StdEncoding.DecodeString(value)
	return data, err == nil && len(data) > 0
}

func (s *Server) createEnrollment(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(w, r) {
		return
	}
	var body struct {
		TTLMinutes *int `json:"ttl_minutes"`
	}
	if !s.decode(w, r, &body) {
		return
	}
	ttl := 10
	if body.TTLMinutes != nil {
		ttl = *body.TTLMinutes
	}
	if ttl < 1 || ttl > 30 {
		s.reject(w, r, 400, "invalid_input")
		return
	}
	enrollment, err := s.store.CreateEnrollment(r.Context(), sessionToken(r), r.Header.Get("X-CSRF-Token"), chi.URLParam(r, "id"), time.Duration(ttl)*time.Minute, s.clock())
	if err != nil {
		s.failure(w, r, err)
		return
	}
	respond(w, 201, enrollment)
}

func (s *Server) changeHost(change func(context.Context, string, string, string, time.Time) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.sameOrigin(w, r) {
			return
		}
		if err := change(r.Context(), sessionToken(r), r.Header.Get("X-CSRF-Token"), chi.URLParam(r, "id"), s.clock()); err != nil {
			s.failure(w, r, err)
			return
		}
		respond(w, 200, map[string]string{"status": "ok"})
	}
}

// Enrollment endpoints are called by Agents that have no session yet. They
// share the per-address limit of the other credential checks.
func (s *Server) enrollAgent(w http.ResponseWriter, r *http.Request) {
	if !s.authAllowed(w, r) {
		return
	}
	var body struct {
		Code string `json:"code"`
		CSR  string `json:"csr"`
	}
	if !s.decode(w, r, &body) {
		return
	}
	request, ok := decodeBase64(body.CSR)
	if !ok || body.Code == "" {
		s.reject(w, r, 400, "invalid_input")
		return
	}
	if s.issuer == nil {
		respond(w, 503, map[string]string{"error": "agents_unavailable"})
		return
	}
	credential, err := s.store.Enroll(r.Context(), body.Code, request, s.issuer, s.clock())
	if err != nil {
		s.failure(w, r, err)
		return
	}
	respond(w, 201, credential)
}

func (s *Server) enrollmentChallenge(w http.ResponseWriter, r *http.Request) {
	if !s.authAllowed(w, r) {
		return
	}
	var body *struct{}
	if !s.decode(w, r, &body) {
		return
	}
	if body == nil {
		s.reject(w, r, 400, "invalid_input")
		return
	}
	id, nonce, err := s.store.EnrollmentChallenge(r.Context(), chi.URLParam(r, "id"), s.clock())
	if err != nil {
		s.failure(w, r, err)
		return
	}
	respond(w, 201, map[string]any{"challenge_id": id, "nonce": nonce, "expires_in": 30})
}

func (s *Server) recoverEnrollment(w http.ResponseWriter, r *http.Request) {
	if !s.authAllowed(w, r) {
		return
	}
	var body struct {
		ChallengeID string `json:"challenge_id"`
		Nonce       string `json:"nonce"`
		Signature   string `json:"signature"`
	}
	if !s.decode(w, r, &body) {
		return
	}
	signature, ok := decodeBase64(body.Signature)
	if !ok || body.ChallengeID == "" || body.Nonce == "" {
		s.reject(w, r, 400, "invalid_input")
		return
	}
	credential, err := s.store.RecoverEnrollment(r.Context(), chi.URLParam(r, "id"), body.ChallengeID, body.Nonce, signature, s.clock())
	if err != nil {
		s.failure(w, r, err)
		return
	}
	respond(w, 200, credential)
}

func validAgentText(value string, max int) bool {
	if len(value) > max {
		return false
	}
	for _, c := range value {
		if c < 0x20 || c > 0x7e {
			return false
		}
	}
	return true
}

func (s *Server) heartbeat(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Version      string   `json:"version"`
		Capabilities []string `json:"capabilities"`
	}
	if !s.decode(w, r, &body) {
		return
	}
	if !validAgentText(body.Version, 64) || len(body.Capabilities) > 32 {
		s.reject(w, r, 400, "invalid_input")
		return
	}
	if body.Capabilities == nil {
		body.Capabilities = []string{}
	}
	for _, c := range body.Capabilities {
		if c == "" || !validAgentText(c, 64) {
			s.reject(w, r, 400, "invalid_input")
			return
		}
	}
	p := agentFrom(r.Context())
	result, err := s.store.Heartbeat(r.Context(), p.Serial, p.AgentID, body.Version, body.Capabilities, s.clock())
	if err != nil {
		s.failure(w, r, err)
		return
	}
	respond(w, 200, result)
}

func (s *Server) renewCredential(w http.ResponseWriter, r *http.Request) {
	var body struct {
		CSR string `json:"csr"`
	}
	if !s.decode(w, r, &body) {
		return
	}
	request, ok := decodeBase64(body.CSR)
	if !ok {
		s.reject(w, r, 400, "invalid_input")
		return
	}
	if s.issuer == nil {
		respond(w, 503, map[string]string{"error": "agents_unavailable"})
		return
	}
	p := agentFrom(r.Context())
	credential, err := s.store.RenewCredential(r.Context(), p.Serial, p.AgentID, request, s.issuer, s.clock())
	if err != nil {
		s.failure(w, r, err)
		return
	}
	respond(w, 200, credential)
}
