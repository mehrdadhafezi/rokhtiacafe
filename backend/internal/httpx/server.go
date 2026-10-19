package httpx

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
)

// Pinger reports whether a dependency (the database) is reachable.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Deps are the collaborators the router needs. Modules add their handlers here as they land.
type Deps struct {
	Logger     *slog.Logger
	DB         Pinger
	Production bool
	API        http.Handler // mounted at /api/v1 when set
}

// NewRouter builds the HTTP handler tree.
func NewRouter(d Deps) http.Handler {
	r := chi.NewRouter()
	r.Use(RequestID, Recover(d.Logger), AccessLog(d.Logger), SecurityHeaders(d.Production))

	r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		Error(w, http.StatusNotFound, "NOT_FOUND", "not found")
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
		Error(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
	})

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		JSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	r.Get("/readyz", readyz(d))

	if d.API != nil {
		r.Mount("/api/v1", d.API)
	}
	return r
}

func readyz(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if d.DB == nil {
			Error(w, http.StatusServiceUnavailable, "NOT_READY", "database not configured")
			return
		}
		if err := d.DB.Ping(ctx); err != nil {
			d.Logger.Warn("readiness check failed", slog.String("request_id", RequestIDFrom(r.Context())), slog.Any("err", err))
			Error(w, http.StatusServiceUnavailable, "NOT_READY", "database unavailable")
			return
		}
		JSON(w, http.StatusOK, map[string]string{"status": "ready"})
	}
}

// NewServer wraps the handler with the timeouts from docs/SECURITY.md §8.
func NewServer(addr string, h http.Handler, logger *slog.Logger) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    64 << 10,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}
}
