package main

import (
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/levskiy0/webpprof"
)

func recoverResponse(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				slog.ErrorContext(r.Context(), "request panicked", "panic", recovered, "stack", string(debug.Stack()))
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func requestTags(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tenant := r.URL.Query().Get("tenant")
		if tenant == "" {
			tenant = "acme"
		}
		scenario := "application"
		if r.URL.Path == "/api/manual/diagnostics" {
			scenario = "diagnostics"
		} else if r.URL.Path == "/api/manual/custom-profiler" {
			scenario = "custom-profiler"
		} else if r.URL.Path == "/api/schedules/refresh-players" {
			scenario = "schedule"
		} else if r.URL.Path == "/api/callables/rebuild-player-index" {
			scenario = "callable"
		} else if r.URL.Path == "/api/tasks/generate-player-report" {
			scenario = "task"
		} else if r.URL.Path == "/api/failure" || r.URL.Path == "/panic" {
			scenario = "failure"
		}
		ctx := webpprof.WithTags(r.Context(), map[string]string{
			"app":      "example",
			"tenant":   tenant,
			"scenario": scenario,
		})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

func requestLog(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			startedAt := time.Now()
			response := &statusResponseWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(response, r)
			logger.InfoContext(r.Context(), "http request handled",
				"method", r.Method,
				"path", r.URL.Path,
				"status", response.status,
				"duration", time.Since(startedAt),
			)
		})
	}
}

type statusResponseWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (w *statusResponseWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.status = status
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusResponseWriter) Write(payload []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(payload)
}

// Unwrap lets http.ResponseController reach optional interfaces implemented by
// the original response writer.
func (w *statusResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}
