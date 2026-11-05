package controlplane

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/sudatti624/AmuOS/internal/metrics"
	"github.com/sudatti624/AmuOS/internal/store"
)

// A full batch of samples is about 40 KiB; the limit leaves room for long
// numbers without allowing unbounded bodies.
const sampleBodyLimit = 64 * 1024

func validRunID(id string) bool {
	if len(id) < 16 || len(id) > 64 {
		return false
	}
	for _, c := range id {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func (s *Server) recordSamples(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RunID   string           `json:"run_id"`
		Samples []metrics.Sample `json:"samples"`
	}
	if !s.decodeLimit(w, r, &body, sampleBodyLimit) {
		return
	}
	if !validRunID(body.RunID) || len(body.Samples) == 0 || len(body.Samples) > metrics.MaxBatch {
		s.reject(w, r, 400, "invalid_input")
		return
	}
	p := agentFrom(r.Context())
	result, err := s.store.RecordSamples(r.Context(), p.Serial, p.AgentID, body.RunID, body.Samples, s.clock())
	if err != nil {
		s.failure(w, r, err)
		return
	}
	respond(w, 200, result)
}

func (s *Server) recordHostInfo(w http.ResponseWriter, r *http.Request) {
	var info metrics.HostInfo
	if !s.decodeLimit(w, r, &info, sampleBodyLimit) {
		return
	}
	p := agentFrom(r.Context())
	if err := s.store.RecordHostInfo(r.Context(), p.Serial, p.AgentID, info, s.clock()); err != nil {
		s.failure(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) hostDetail(w http.ResponseWriter, r *http.Request) {
	detail, err := s.store.HostDetail(r.Context(), sessionToken(r), chi.URLParam(r, "id"), s.clock())
	if err != nil {
		s.failure(w, r, err)
		return
	}
	respond(w, 200, detail)
}

func (s *Server) metricHistory(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("range")
	if name == "" {
		name = "1h"
	}
	if !store.ValidHistoryRange(name) {
		s.reject(w, r, 400, "invalid_input")
		return
	}
	history, err := s.store.MetricHistory(r.Context(), sessionToken(r), chi.URLParam(r, "id"), name, s.clock())
	if err != nil {
		s.failure(w, r, err)
		return
	}
	respond(w, 200, history)
}
