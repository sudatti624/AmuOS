package controlplane

import (
	"github.com/sudatti624/AmuOS/internal/store"
	"net/http"
)

func (s *Server) monitoringPolicy(w http.ResponseWriter, r *http.Request) {
	settings, err := s.store.MonitoringPolicy(r.Context(), sessionToken(r), s.clock())
	if err != nil {
		s.failure(w, r, err)
		return
	}
	respond(w, 200, settings)
}

func (s *Server) setMonitoringPolicy(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(w, r) {
		return
	}
	var body struct {
		Interval *int `json:"sample_interval_seconds"`
		Raw      *int `json:"raw_retention_days"`
		Rollup   *int `json:"rollup_retention_days"`
	}
	if !s.decode(w, r, &body) {
		return
	}
	if body.Interval == nil || body.Raw == nil || body.Rollup == nil {
		s.reject(w, r, 400, "invalid_input")
		return
	}
	if err := s.store.SetMonitoringPolicy(r.Context(), sessionToken(r), r.Header.Get("X-CSRF-Token"), store.MonitoringPolicy{SampleInterval: *body.Interval, RawDays: *body.Raw, RollupDays: *body.Rollup}, s.clock()); err != nil {
		s.failure(w, r, err)
		return
	}
	respond(w, 200, map[string]string{"status": "updated"})
}
