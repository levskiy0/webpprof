package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/levskiy0/webpprof"
	"github.com/levskiy0/webpprof/example/customprofiler"
)

// manualExamples isolates the custom integration and synthetic diagnostic
// entities from the normal application. Removing this file and the two
// /api/manual/* routes does not affect automatic profiling or handler spans.
type manualExamples struct {
	profiler *webpprof.Profiler
	client   customprofiler.Client
}

func newManualExamples(profiler *webpprof.Profiler) *manualExamples {
	return &manualExamples{
		profiler: profiler,
		client:   customprofiler.ProfileWith(profiler, demoClient{}),
	}
}

// customProfilerExample is deliberately separate from ordinary application
// routes. It demonstrates the one case where this example authors an Event.
func (a *application) customProfilerExample(w http.ResponseWriter, r *http.Request) {
	a.serveMeasured("demo.custom-profiler", func(ctx context.Context) error {
		value, err := a.manual.client.Lookup(ctx, "feature:developer-plan")
		if err != nil {
			return fmt.Errorf("custom client lookup: %w", err)
		}
		return writeJSON(w, http.StatusOK, map[string]any{
			"value": value,
			"note":  "this route demonstrates a manually implemented custom profiler event",
		})
	}, w, r)
}

func (a *application) diagnostics(w http.ResponseWriter, r *http.Request) {
	a.serveMeasured("demo.diagnostics", func(ctx context.Context) error {
		a.manual.logDiagnosticExamples(ctx, time.Now().UTC())
		a.logger.WarnContext(ctx, "diagnostic scenario recorded", "synthetic", true)
		return writeJSON(w, http.StatusOK, map[string]any{
			"ok":       true,
			"message":  "Open Automatic findings and Timeline for this request",
			"profiler": profilerURL(r, a.manual.profiler),
		})
	}, w, r)
}

type demoClient struct{}

func (demoClient) Lookup(_ context.Context, key string) (string, error) {
	if key == "player:404" {
		return "", errors.New("plan service: player not found")
	}
	return "developer", nil
}
