package controlplane

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/sudatti624/AmuOS/internal/store"
)

func (s *Server) createTask(w http.ResponseWriter, r *http.Request) {
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
		Operation string `json:"operation"`
	}
	if !s.decode(w, r, &body) {
		return
	}
	if body.Operation != "collect" {
		s.reject(w, r, 400, "invalid_input")
		return
	}
	task, err := s.store.CreateTask(r.Context(), sessionToken(r), r.Header.Get("X-CSRF-Token"), chi.URLParam(r, "id"), body.Operation, s.clock())
	if err != nil {
		s.failure(w, r, err)
		return
	}
	if task.Replayed {
		w.Header().Set("Idempotency-Replayed", "true")
	}
	respond(w, 201, task)
}

func (s *Server) tasks(w http.ResponseWriter, r *http.Request) {
	tasks, err := s.store.Tasks(r.Context(), sessionToken(r), s.clock())
	if err != nil {
		s.failure(w, r, err)
		return
	}
	respond(w, 200, map[string]any{"items": tasks})
}

func (s *Server) task(w http.ResponseWriter, r *http.Request) {
	task, err := s.store.Task(r.Context(), sessionToken(r), chi.URLParam(r, "id"), s.clock())
	if err != nil {
		s.failure(w, r, err)
		return
	}
	respond(w, 200, task)
}

func (s *Server) cancelTask(w http.ResponseWriter, r *http.Request) {
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
	task, err := s.store.CancelTask(r.Context(), sessionToken(r), r.Header.Get("X-CSRF-Token"), chi.URLParam(r, "id"), s.clock())
	if err != nil {
		s.failure(w, r, err)
		return
	}
	respond(w, 200, task)
}

func (s *Server) resolveTask(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(w, r) {
		return
	}
	var body struct {
		Resolution string `json:"resolution"`
		Evidence   string `json:"evidence"`
	}
	if !s.decode(w, r, &body) {
		return
	}
	task, err := s.store.ResolveTask(r.Context(), sessionToken(r), r.Header.Get("X-CSRF-Token"), chi.URLParam(r, "id"), body.Resolution, body.Evidence, s.clock())
	if err != nil {
		s.failure(w, r, err)
		return
	}
	respond(w, 200, task)
}
