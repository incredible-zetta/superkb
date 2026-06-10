package http

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// NewRouter builds the application HTTP router with middleware and routes.
// auth guards the /api/v1 group; /healthz stays open for liveness probes.
func NewRouter(docs *DocumentHandler, kbs *KnowledgeBaseHandler, auth func(http.Handler) http.Handler) http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(60 * time.Second))

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	r.Route("/api/v1", func(r chi.Router) {
		if auth != nil {
			r.Use(auth)
		}
		docs.Routes(r)
		kbs.Routes(r)
	})

	return r
}
