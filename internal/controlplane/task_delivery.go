package controlplane

import (
	"github.com/go-chi/chi/v5"
	"github.com/sudatti624/AmuOS/internal/metrics"
	"github.com/sudatti624/AmuOS/internal/store"
	"net/http"
)

func (s *Server) pollTasks(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Receipt *store.TaskReceipt `json:"receipt"`
	}
	if !s.decode(w, r, &body) {
		return
	}
	p := agentFrom(r.Context())
	task, err := s.store.PollTasks(r.Context(), p.Serial, p.AgentID, body.Receipt, s.clock())
	if err != nil {
		s.failure(w, r, err)
		return
	}
	respond(w, 200, map[string]any{"task": task})
}
func (s *Server) taskResult(w http.ResponseWriter, r *http.Request) {
	var body store.TaskResult
	if !s.decode(w, r, &body) {
		return
	}
	if body.ID != chi.URLParam(r, "id") {
		s.reject(w, r, 400, "invalid_input")
		return
	}
	p := agentFrom(r.Context())
	if err := s.store.RecordTaskResult(r.Context(), p.Serial, p.AgentID, body, s.clock()); err != nil {
		s.failure(w, r, err)
		return
	}
	respond(w, 200, map[string]bool{"acknowledged": true})
}
func (s *Server) recordSystemd(w http.ResponseWriter, r *http.Request) {
	var body metrics.SystemdSnapshot
	if !s.decodeLimit(w, r, &body, 256*1024) {
		return
	}
	p := agentFrom(r.Context())
	if err := s.store.RecordSystemd(r.Context(), p.Serial, p.AgentID, body, s.clock()); err != nil {
		s.failure(w, r, err)
		return
	}
	w.WriteHeader(204)
}
func (s *Server) systemd(w http.ResponseWriter, r *http.Request) {
	value, err := s.store.Systemd(r.Context(), sessionToken(r), chi.URLParam(r, "id"), s.clock())
	if err != nil {
		s.failure(w, r, err)
		return
	}
	respond(w, 200, value)
}
