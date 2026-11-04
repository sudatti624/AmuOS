package controlplane

import "net/http"

func (s *Server) auditRetention(w http.ResponseWriter, r *http.Request) {
	settings, err := s.store.AuditRetention(r.Context(), sessionToken(r), s.clock())
	if err != nil {
		s.failure(w, r, err)
		return
	}
	respond(w, 200, settings)
}

func (s *Server) setAuditRetention(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(w, r) {
		return
	}
	var body struct {
		Days *int `json:"retention_days"`
	}
	if !s.decode(w, r, &body) {
		return
	}
	if body.Days == nil {
		s.reject(w, r, 400, "invalid_input")
		return
	}
	if err := s.store.SetAuditRetention(r.Context(), sessionToken(r), r.Header.Get("X-CSRF-Token"), *body.Days, s.clock()); err != nil {
		s.failure(w, r, err)
		return
	}
	respond(w, 200, map[string]string{"status": "updated"})
}

func (s *Server) purgeAudit(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(w, r) {
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
	result, err := s.store.PurgeAudit(r.Context(), sessionToken(r), r.Header.Get("X-CSRF-Token"), s.clock())
	if err != nil {
		s.failure(w, r, err)
		return
	}
	respond(w, 200, result)
}
