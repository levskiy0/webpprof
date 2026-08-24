package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/levskiy0/webpprof"
)

type application struct {
	profiler             *webpprof.Profiler
	players              *playerRepository
	logger               *slog.Logger
	metrics              *demoMetrics
	manual               *manualExamples
	refreshPlayers       func(context.Context)
	rebuildPlayerIndex   func(context.Context) error
	generatePlayerReport func(context.Context) error
}

func (a *application) routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", a.home)
	mux.HandleFunc("GET /api/players", a.listPlayers)
	mux.HandleFunc("GET /api/players/{id}", a.getPlayer)
	mux.HandleFunc("POST /api/players/{id}/views", a.incrementPlayerViews)
	mux.HandleFunc("POST /api/schedules/refresh-players", a.runPlayerRefresh)
	mux.HandleFunc("POST /api/callables/rebuild-player-index", a.runPlayerIndexRebuild)
	mux.HandleFunc("POST /api/tasks/generate-player-report", a.runPlayerReport)
	mux.HandleFunc("GET /api/failure", a.databaseFailure)
	if a.manual != nil {
		mux.HandleFunc("GET /api/manual/custom-profiler", a.customProfilerExample)
		mux.HandleFunc("GET /api/manual/diagnostics", a.diagnostics)
	}
	mux.HandleFunc("GET /panic", a.panicExample)
	return mux
}

func (a *application) listPlayers(w http.ResponseWriter, r *http.Request) {
	a.serveMeasured("players.list", func(ctx context.Context) error {
		players, err := a.players.list(ctx)
		if err != nil {
			return err
		}
		a.logger.InfoContext(ctx, "players listed", "count", len(players))
		return writeJSON(w, http.StatusOK, map[string]any{"players": players})
	}, w, r)
}

func (a *application) getPlayer(w http.ResponseWriter, r *http.Request) {
	a.serveMeasured("players.get", func(ctx context.Context) error {
		id, err := parsePlayerID(r)
		if err != nil {
			return err
		}
		player, err := a.players.find(ctx, id)
		if err != nil {
			return err
		}
		a.logger.InfoContext(ctx, "player loaded", "player_id", id, "views", player.Views)
		return writeJSON(w, http.StatusOK, map[string]any{"player": player})
	}, w, r)
}

func (a *application) incrementPlayerViews(w http.ResponseWriter, r *http.Request) {
	a.serveMeasured("players.increment-views", func(ctx context.Context) error {
		id, err := parsePlayerID(r)
		if err != nil {
			return err
		}
		player, err := a.players.incrementViews(ctx, id)
		if err != nil {
			return err
		}
		a.logger.InfoContext(ctx, "player views incremented", "player_id", id, "views", player.Views)
		return writeJSON(w, http.StatusOK, map[string]any{"player": player})
	}, w, r)
}

func (a *application) databaseFailure(w http.ResponseWriter, r *http.Request) {
	a.serveMeasured("players.failure", func(ctx context.Context) error {
		return a.players.forceFailure(ctx)
	}, w, r)
}

func (a *application) runPlayerRefresh(w http.ResponseWriter, r *http.Request) {
	a.serveMeasured("schedules.refresh-players", func(ctx context.Context) error {
		if a.refreshPlayers == nil {
			return errors.New("scheduled player refresh is not configured")
		}
		// The schedule wrapper removes HTTP correlation itself while preserving
		// cancellation, application values, and business tags.
		a.refreshPlayers(ctx)
		return writeJSON(w, http.StatusOK, map[string]any{
			"ok":      true,
			"message": "Scheduled player snapshot refresh completed",
		})
	}, w, r)
}

func (a *application) runPlayerIndexRebuild(w http.ResponseWriter, r *http.Request) {
	a.serveMeasured("callables.rebuild-player-index", func(ctx context.Context) error {
		if a.rebuildPlayerIndex == nil {
			return errors.New("player index callable is not configured")
		}
		// The callable wrapper removes HTTP correlation itself while preserving
		// cancellation, application values, and business tags.
		if err := a.rebuildPlayerIndex(ctx); err != nil {
			return err
		}
		return writeJSON(w, http.StatusOK, map[string]any{
			"ok":      true,
			"message": "Player search index rebuilt",
		})
	}, w, r)
}

func (a *application) runPlayerReport(w http.ResponseWriter, r *http.Request) {
	a.serveMeasured("tasks.generate-player-report", func(ctx context.Context) error {
		if a.generatePlayerReport == nil {
			return errors.New("player report task is not configured")
		}
		measurement := a.profiler.MeasureTask(ctx, webpprof.Task{
			Name:   "reports.players.generate",
			Fields: map[string]any{"format": "pdf"},
		}, a.generatePlayerReport)
		if measurement.Err != nil {
			return measurement.Err
		}
		return writeJSON(w, http.StatusOK, map[string]any{"ok": true, "duration_ms": measurement.Duration.Milliseconds(), "message": "Player report generated"})
	}, w, r)
}

func (a *application) panicExample(w http.ResponseWriter, r *http.Request) {
	a.serveMeasured("demo.panic", func(context.Context) error {
		panic("synthetic example panic")
	}, w, r)
}

// serveMeasured turns an application operation into a named Event while the
// HTTP, SQL, and slog integrations continue recording their own entities.
func (a *application) serveMeasured(
	name string,
	handler func(context.Context) error,
	w http.ResponseWriter,
	r *http.Request,
) {
	route := r.Pattern
	if route == "" {
		route = r.URL.Path
	}
	measurement := a.profiler.Measure(r.Context(), webpprof.Event{
		Kind:    "handler",
		Name:    name,
		Summary: r.Method + " " + r.URL.Path,
		Fields: map[string]any{
			"method": r.Method,
			"route":  route,
		},
	}, func(ctx context.Context) error {
		err := handler(ctx)
		if err != nil {
			a.handleError(w, r.WithContext(ctx), err)
		}
		return err
	})

	a.metrics.record(measurement.Failed(), measurement.Duration)
}

func (a *application) handleError(w http.ResponseWriter, r *http.Request, err error) {
	status := http.StatusInternalServerError
	message := "internal server error"
	level := slog.LevelError
	if errors.Is(err, errInvalidPlayerID) {
		status = http.StatusBadRequest
		message = err.Error()
		level = slog.LevelWarn
	} else if errors.Is(err, errPlayerNotFound) {
		status = http.StatusNotFound
		message = "player not found"
		level = slog.LevelInfo
	}
	a.logger.LogAttrs(r.Context(), level, "request failed",
		slog.Int("status", status),
		slog.String("method", r.Method),
		slog.String("path", r.URL.Path),
		slog.Any("error", err),
	)
	_ = writeJSON(w, status, map[string]any{"error": message})
}

type demoMetrics struct {
	total        atomic.Uint64
	succeeded    atomic.Uint64
	failed       atomic.Uint64
	lastDuration atomic.Int64
}

func (m *demoMetrics) record(failed bool, elapsed time.Duration) {
	m.total.Add(1)
	m.lastDuration.Store(int64(elapsed))
	if failed {
		m.failed.Add(1)
		return
	}
	m.succeeded.Add(1)
}

func (m *demoMetrics) totalValue(context.Context) (float64, error) {
	return float64(m.total.Load()), nil
}

func (m *demoMetrics) successValue(context.Context) (float64, error) {
	return float64(m.succeeded.Load()), nil
}

func (m *demoMetrics) failedValue(context.Context) (float64, error) {
	return float64(m.failed.Load()), nil
}

func (m *demoMetrics) lastDurationValue(context.Context) (float64, error) {
	return float64(m.lastDuration.Load()), nil
}

func parsePlayerID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, errInvalidPlayerID
	}
	return id, nil
}

func writeJSON(w http.ResponseWriter, status int, value any) error {
	var payload bytes.Buffer
	if err := json.NewEncoder(&payload).Encode(value); err != nil {
		return fmt.Errorf("encode response: %w", err)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if _, err := w.Write(payload.Bytes()); err != nil {
		return fmt.Errorf("write response: %w", err)
	}
	return nil
}

func profilerURL(r *http.Request, profiler *webpprof.Profiler) string {
	return "http://" + r.Host + profiler.BasePath() + "/"
}
