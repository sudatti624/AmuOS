package controlplane

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/sudatti624/AmuOS/internal/store"
)

func rejectionRoute(r *http.Request) string {
	method := r.Method
	switch method {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS":
	default:
		method = "OTHER"
	}
	pattern := "unmatched"
	if rc := chi.RouteContext(r.Context()); rc != nil && rc.RoutePattern() != "" {
		pattern = rc.RoutePattern()
	}
	return method + " " + pattern
}

func (s *Server) reject(w http.ResponseWriter, r *http.Request, status int, code string) {
	// Preserve request metadata even if the peer disconnects, but bound the write.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Second)
	defer cancel()
	if err := s.store.RecordHTTPRejection(ctx, sessionToken(r), rejectionRoute(r), code, s.clock()); err != nil {
		slog.Error("audit write failed", "request_id", store.RequestID(r.Context()))
		w.Header().Set("Retry-After", "1")
		respond(w, 503, map[string]string{"error": "audit_unavailable"})
		return
	}
	respond(w, status, map[string]string{"error": code})
}
