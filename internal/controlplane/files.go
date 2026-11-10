package controlplane

import (
	"github.com/go-chi/chi/v5"
	"github.com/sudatti624/AmuOS/internal/fileops"
	"github.com/sudatti624/AmuOS/internal/store"
	"net/http"
)

func (s *Server) fileRoots(w http.ResponseWriter, r *http.Request) {
	roots, err := s.store.FileRoots(r.Context(), sessionToken(r), chi.URLParam(r, "id"), s.clock())
	if err != nil {
		s.failure(w, r, err)
		return
	}
	respond(w, 200, map[string]any{"items": roots})
}
func (s *Server) setFileRoots(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(w, r) {
		return
	}
	var body struct {
		Roots []store.FileRoot `json:"roots"`
	}
	if !s.decode(w, r, &body) {
		return
	}
	if err := s.store.SetFileRoots(r.Context(), sessionToken(r), r.Header.Get("X-CSRF-Token"), chi.URLParam(r, "id"), body.Roots, s.clock()); err != nil {
		s.failure(w, r, err)
		return
	}
	respond(w, 200, map[string]string{"status": "updated"})
}
func (s *Server) createFileTask(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(w, r) {
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if !store.ValidIdempotencyKey(key) {
		s.reject(w, r, 400, "idempotency_key_required")
		return
	}
	var body fileops.Request
	if !s.decodeLimit(w, r, &body, fileops.MaxBody) {
		return
	}
	if body.Validate() != nil {
		s.reject(w, r, 400, "invalid_input")
		return
	}
	task, err := s.store.CreateFileTask(store.WithIdempotencyKey(r.Context(), key), sessionToken(r), r.Header.Get("X-CSRF-Token"), chi.URLParam(r, "id"), body, s.clock())
	if err != nil {
		s.failure(w, r, err)
		return
	}
	if task.Replayed {
		w.Header().Set("Idempotency-Replayed", "true")
	}
	respond(w, 201, task)
}
func (s *Server) fileResult(w http.ResponseWriter, r *http.Request) {
	result, err := s.store.FileTaskResult(r.Context(), sessionToken(r), chi.URLParam(r, "id"), s.clock())
	if err != nil {
		s.failure(w, r, err)
		return
	}
	respond(w, 200, result)
}
