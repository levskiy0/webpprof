package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/levskiy0/webpprof"
	"github.com/levskiy0/webpprof/example/customprofiler"
	webpprofhttp "github.com/levskiy0/webpprof/profiler/http"
)

const defaultAddress = "127.0.0.1:3030"

const playerLookupSQL = "SELECT id, email FROM players WHERE id = 42"

func main() {
	address := os.Getenv("WEBPPROF_ADDR")
	if address == "" {
		address = defaultAddress
	}
	mux := http.NewServeMux()
	metrics := &demoMetrics{}
	profiler := webpprof.New(
		mux,
		webpprof.WithRetention(time.Hour),
		webpprof.WithExcludedRequests("GET /favicon.ico"),
		// Capture the application stack only for operation types where jumping
		// from the viewer to the calling Go code is useful.
		webpprof.WithCallsiteKinds(
			webpprof.KindQuery,
			webpprof.KindCache,
			webpprof.KindEmail,
			webpprof.KindJob,
			webpprof.KindHTTPCall,
			webpprof.KindSchedule,
		),
		// Make every captured source frame open directly in VS Code. Replace this
		// URL builder with the deep-link format used by your editor.
		webpprof.WithSourceLink(func(frame webpprof.SourceFrame) string {
			return fmt.Sprintf("vscode://file/%s:%d", frame.File, frame.Line)
		}),
		// Dashboard replaces the default layout. Widgets are rendered in the
		// declared order on a responsive four-column grid.
		webpprof.Dashboard(
			webpprof.WithCPU(),
			webpprof.WithGoMemory(),
			webpprof.WithRequests(),
			webpprof.WithQueries(),
			webpprof.WithCustomMetric(webpprof.DashboardMetric{
				ID:          "demo-total",
				Title:       "Demo requests",
				Description: "Counter without a graph",
				Value:       metrics.totalValue,
			}),
			webpprof.WithCustomMetric(webpprof.DashboardMetric{
				ID:          "demo-rate",
				Title:       "Demo throughput",
				Description: "Requests per second",
				Unit:        "req/s",
				Mode:        webpprof.DashboardMetricRate,
				Sparkline:   true,
				Color:       "#17a36d",
				Value:       metrics.totalValue,
			}),
			webpprof.WithCounterGrid(webpprof.DashboardCounterGrid{
				ID: "demo-outcomes", Title: "Demo outcomes", Description: "Latest application counters", Span: 2,
				Counters: []webpprof.DashboardCounter{
					{ID: "success", Label: "Succeeded", Value: metrics.successValue},
					{ID: "failed", Label: "Failed", Value: metrics.failedValue},
					{ID: "last-duration", Label: "Last duration", Format: webpprof.DashboardFormatDuration, Value: metrics.lastDurationValue},
				},
			}),
			webpprof.WithCustomChart(webpprof.DashboardChart{
				ID: "demo-history", Title: "Demo result history", Description: "Cumulative handler outcomes", Span: 4,
				Series: []webpprof.DashboardSeries{
					{ID: "success", Label: "Succeeded", Color: "#17a36d", Value: metrics.successValue},
					{ID: "failed", Label: "Failed", Color: "#ba4a52", Value: metrics.failedValue},
				},
			}),
			webpprof.WithSlowestOperations(),
		),
	)
	defer profiler.Close()

	// A custom profiler wraps an existing dependency once in the composition
	// root. Application code continues to depend only on the original interface.
	client := customprofiler.ProfileWith(profiler, demoClient{})
	app := &demoApp{profiler: profiler, client: client, metrics: metrics}
	appMux := http.NewServeMux()
	appMux.HandleFunc("GET /", app.home)
	appMux.HandleFunc("GET /demo", app.demo)
	appMux.HandleFunc("GET /panic", app.panic)

	// The profiler routes are already registered on mux. The fallback route
	// wraps only the demo application and supplies request correlation context.
	// Named wrappers become Middleware entries under the captured request.
	profiledApp := webpprofhttp.ProfileMiddlewareWith(profiler, "security-headers", securityHeaders)(
		webpprofhttp.ProfileMiddlewareWith(profiler, "request-log", requestLog)(appMux),
	)
	// Tagging runs after request capture and before named middleware. WithTags
	// updates the request capture and is inherited by every child entity.
	taggedApp := demoTags(profiledApp)
	mux.Handle("/", recoverResponse(webpprofhttp.MiddlewareWith(profiler, taggedApp)))

	server := &http.Server{
		Addr:              address,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	serverErrors := make(chan error, 1)
	go func() {
		log.Printf("demo:     http://%s/", address)
		log.Printf("webpprof: http://%s%s/", address, profiler.BasePath())
		serverErrors <- server.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("shutdown: %v", err)
		}
	case err := <-serverErrors:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server: %v", err)
		}
	}
}

type demoApp struct {
	profiler *webpprof.Profiler
	client   customprofiler.Client
	metrics  *demoMetrics
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

func (a *demoApp) home(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(homePage))
}

func (a *demoApp) demo(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	failed := r.URL.Query().Has("fail")
	requestStartedAt := time.Now()
	defer func() { a.metrics.record(failed, time.Since(requestStartedAt)) }()
	startedAt := time.Now().UTC()
	rows := int64(1)
	lookupKey := "player:42"
	if failed {
		lookupKey = "player:missing"
	}

	// This is a normal dependency call. The wrapper in customprofiler measures
	// it and records a request-related custom Event without changing this handler.
	_, _ = a.client.Lookup(ctx, lookupKey)

	// Each Log*Context call becomes a Related tab under this HTTP request.
	a.profiler.LogQueryContext(ctx, webpprof.Query{
		Meta: webpprof.Meta{
			StartedAt: startedAt,
			Duration:  12 * time.Millisecond,
			// This entity-specific tag is merged with app, tenant, and scenario
			// tags inherited from the request context.
			Tags: map[string]string{"repository": "players"},
		},
		Connection:   "example",
		Driver:       "sqlite",
		Database:     "local",
		Operation:    "SELECT",
		SQL:          playerLookupSQL,
		RowsAffected: &rows,
		// Automatic SQL integrations populate this with a real plain EXPLAIN.
		// The database-free demo supplies a representative SQLite plan.
		Plan: &webpprof.QueryPlan{
			Command:  "EXPLAIN QUERY PLAN " + playerLookupSQL,
			Format:   "text",
			Text:     "id=2  parent=0  notused=0  detail=SEARCH players USING INTEGER PRIMARY KEY (rowid=?)",
			Duration: 900 * time.Microsecond,
		},
	})
	a.profiler.LogCacheContext(ctx, webpprof.Cache{
		Meta:      webpprof.Meta{StartedAt: startedAt.Add(13 * time.Millisecond), Duration: 800 * time.Microsecond},
		Store:     "memory",
		Operation: "get",
		Key:       "player:42",
		Hit:       true,
		TTL:       time.Minute,
		Value:     `{"id":42,"name":"Ada"}`,
	})
	a.profiler.LogEmailContext(ctx, webpprof.Email{
		Meta:      webpprof.Meta{StartedAt: startedAt.Add(15 * time.Millisecond), Duration: 8 * time.Millisecond},
		Transport: "smtp",
		From:      webpprof.Address{Name: "Example", Email: "hello@example.test"},
		To:        []webpprof.Address{{Name: "Ada", Email: "ada@example.test"}},
		Subject:   "Welcome to webpprof",
		Text:      "This message was generated by the local example.",
		Status:    "sent",
	})
	a.profiler.LogJobContext(ctx, webpprof.Job{
		Meta:       webpprof.Meta{StartedAt: startedAt.Add(24 * time.Millisecond), Duration: 2 * time.Millisecond},
		Name:       "SendWelcomeEmail",
		Queue:      "mail",
		Connection: "sync",
		State:      "dispatched",
		Arguments:  []webpprof.Argument{{Name: "player_id", Type: "uint64", Value: "42"}},
	})
	a.profiler.LogLogContext(ctx, webpprof.Log{
		Meta:    webpprof.Meta{StartedAt: startedAt.Add(27 * time.Millisecond)},
		Level:   "INFO",
		Message: "example request handled",
		Fields:  map[string]any{"player_id": 42, "failed": failed},
	})
	a.profiler.LogHTTPCallContext(ctx, webpprof.HTTPCall{
		Meta:         webpprof.Meta{StartedAt: startedAt.Add(28 * time.Millisecond), Duration: 15 * time.Millisecond},
		Method:       http.MethodGet,
		URL:          "https://api.example.test/avatars/42",
		Status:       http.StatusOK,
		ResponseSize: 2048,
	})
	a.profiler.LogScheduleContext(ctx, webpprof.Schedule{
		Meta:      webpprof.Meta{StartedAt: startedAt.Add(44 * time.Millisecond), Duration: time.Millisecond},
		Name:      "refresh-player",
		State:     "succeeded",
		PlannedAt: startedAt,
		Payload: map[string]any{
			"player_id": 42,
			"tenant":    webpprof.TagsFromContext(ctx)["tenant"],
			"force":     failed,
			"resources": []string{"profile", "avatar", "permissions"},
		},
	})
	a.profiler.LogEventContext(ctx, webpprof.Event{
		Meta:    webpprof.Meta{StartedAt: startedAt.Add(46 * time.Millisecond)},
		Kind:    "player",
		Name:    "viewed",
		Status:  "recorded",
		Summary: "Player 42 was opened in the local example",
		Fields:  map[string]any{"player_id": 42},
	})
	if r.URL.Query().Has("diagnostics") {
		a.logDiagnosticExamples(ctx, startedAt.Add(50*time.Millisecond))
	}

	status := http.StatusOK
	if failed {
		status = http.StatusInternalServerError
		err := errors.New("synthetic example failure")
		a.profiler.LogExceptionContext(ctx, webpprof.Exception{
			Meta:    webpprof.Meta{StartedAt: startedAt.Add(47 * time.Millisecond)},
			Type:    fmt.Sprintf("%T", err),
			Message: err.Error(),
			Stack:   string(debug.Stack()),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":       !failed,
		"message":  "Open webpprof and inspect the newest request",
		"profiler": profilerURL(r, a.profiler),
	})
}

// logDiagnosticExamples records deterministic synthetic bottlenecks. They do
// not sleep or call external services; their captured durations exist only to
// demonstrate automatic backend findings in the request detail.
func (a *demoApp) logDiagnosticExamples(ctx context.Context, startedAt time.Time) {
	rows := int64(1)
	// Together with the normal player lookup above this creates 47 queries with
	// the same fingerprint, which the analyzer reports as a possible N+1.
	for index := range 46 {
		a.profiler.LogQueryContext(ctx, webpprof.Query{
			Meta: webpprof.Meta{
				StartedAt: startedAt.Add(time.Duration(index) * 2 * time.Millisecond),
				Duration:  2 * time.Millisecond,
				Tags:      map[string]string{"diagnostic": "n-plus-one"},
			},
			Connection:   "example",
			Driver:       "sqlite",
			Database:     "local",
			Operation:    "SELECT",
			SQL:          playerLookupSQL,
			RowsAffected: &rows,
		})
	}
	a.profiler.LogQueryContext(ctx, webpprof.Query{
		Meta: webpprof.Meta{
			StartedAt: startedAt.Add(100 * time.Millisecond),
			Duration:  575 * time.Millisecond,
			Tags:      map[string]string{"diagnostic": "sql-share"},
		},
		Connection:   "analytics",
		Driver:       "sqlite",
		Database:     "local",
		Operation:    "SELECT",
		SQL:          "SELECT * FROM audit_log ORDER BY created_at DESC",
		RowsAffected: &rows,
	})
	a.profiler.LogHTTPCallContext(ctx, webpprof.HTTPCall{
		Meta: webpprof.Meta{
			StartedAt: startedAt.Add(55 * time.Millisecond),
			Duration:  650 * time.Millisecond,
			Tags:      map[string]string{"diagnostic": "slow-http"},
		},
		Method:       http.MethodGet,
		URL:          "https://api.example.test/reports/daily",
		Status:       http.StatusOK,
		ResponseSize: 8192,
	})
	a.profiler.LogMiddlewareContext(ctx, webpprof.Middleware{
		Meta: webpprof.Meta{
			StartedAt: startedAt.Add(110 * time.Millisecond),
			Duration:  430 * time.Millisecond,
			Tags:      map[string]string{"diagnostic": "slow-middleware"},
		},
		Name:  "auth",
		State: "completed",
	})
	a.profiler.LogJobContext(ctx, webpprof.Job{
		Meta: webpprof.Meta{
			StartedAt: startedAt.Add(65 * time.Millisecond),
			Duration:  18 * time.Millisecond,
			Tags:      map[string]string{"diagnostic": "failed-job"},
		},
		Name:       "GenerateDailyReport",
		Queue:      "reports",
		Connection: "sync",
		State:      "failed",
		Attempt:    3,
		Error:      "synthetic diagnostics example: worker unavailable",
	})

	// A miss followed by repeated reads is different from a generic N+1: the
	// cache should be populated once, then reused for the rest of the request.
	a.profiler.LogCacheContext(ctx, webpprof.Cache{
		Meta: webpprof.Meta{
			StartedAt: startedAt.Add(640 * time.Millisecond),
			Duration:  time.Millisecond,
			Tags:      map[string]string{"diagnostic": "cache-query-burst"},
		},
		Store:     "memory",
		Operation: "get",
		Key:       "player:42:permissions",
		Hit:       false,
	})
	for index := range 18 {
		a.profiler.LogQueryContext(ctx, webpprof.Query{
			Meta: webpprof.Meta{
				StartedAt: startedAt.Add(645*time.Millisecond + time.Duration(index)*2*time.Millisecond),
				Duration:  time.Millisecond,
				Tags:      map[string]string{"diagnostic": "cache-query-burst"},
			},
			Connection:   "example",
			Driver:       "sqlite",
			Database:     "local",
			Operation:    "SELECT",
			SQL:          "SELECT permission FROM player_permissions WHERE player_id = 42",
			RowsAffected: &rows,
		})
	}

	// Same-host, successful, non-overlapping calls form a conservative
	// concurrency candidate. The analyzer intentionally does not group calls to
	// different hosts or overlapping calls.
	for index := range 3 {
		a.profiler.LogHTTPCallContext(ctx, webpprof.HTTPCall{
			Meta: webpprof.Meta{
				StartedAt: startedAt.Add(720*time.Millisecond + time.Duration(index)*30*time.Millisecond),
				Duration:  25 * time.Millisecond,
				Tags:      map[string]string{"diagnostic": "sequential-http"},
			},
			Method: http.MethodGet,
			URL:    fmt.Sprintf("https://assets.example.test/player/42/part/%d", index+1),
			Status: http.StatusOK,
		})
	}
}

func (a *demoApp) panic(http.ResponseWriter, *http.Request) {
	panic("synthetic example panic")
}

type demoClient struct{}

func (demoClient) Lookup(_ context.Context, key string) (string, error) {
	if key == "player:missing" {
		return "", errors.New("example client: player not found")
	}
	return "Ada", nil
}

func profilerURL(r *http.Request, profiler *webpprof.Profiler) string {
	return "http://" + r.Host + profiler.BasePath() + "/"
}

func recoverResponse(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				http.Error(w, fmt.Sprint(recovered), http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func demoTags(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tenant := r.URL.Query().Get("tenant")
		if tenant == "" {
			tenant = "acme"
		}
		scenario := "success"
		if r.URL.Query().Has("diagnostics") {
			scenario = "diagnostics"
		} else if r.URL.Query().Has("fail") || r.URL.Path == "/panic" {
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
		next.ServeHTTP(w, r)
	})
}

func requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s", r.Method, r.URL.RequestURI())
		next.ServeHTTP(w, r)
	})
}

const homePage = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>webpprof local example</title>
  <style>
    body { max-width: 760px; margin: 64px auto; padding: 0 24px; color: #252936; background: #f5f6f8; font: 16px/1.6 system-ui, sans-serif; }
    main { padding: 32px; border: 1px solid #dde0e6; border-radius: 12px; background: white; box-shadow: 0 14px 40px #1f29370f; }
    h1 { margin: 0 0 8px; line-height: 1.2; }
    p { color: #68707d; }
    nav { display: flex; flex-wrap: wrap; gap: 10px; margin-top: 24px; }
    a { padding: 9px 14px; border: 1px solid #d7d9e1; border-radius: 7px; color: #5154c9; text-decoration: none; }
    a:hover { border-color: #777ae0; background: #f0f1ff; }
    a.primary { border-color: #5558cf; color: white; background: #5558cf; }
    code { padding: 2px 5px; border-radius: 4px; background: #eff0f3; }
  </style>
</head>
<body>
  <main>
    <h1>webpprof local example</h1>
    <p>Generate a request, then open the profiler and inspect its Related tabs. Every demo request also passes through the custom profiler in <code>example/customprofiler</code>.</p>
    <nav>
      <a class="primary" href="/demo?tenant=acme">Acme success</a>
      <a href="/demo?tenant=umbrella">Umbrella success</a>
      <a href="/demo?tenant=acme&amp;fail=1">Acme failure</a>
      <a href="/demo?tenant=umbrella&amp;diagnostics=1">Diagnostics example</a>
      <a href="/panic?tenant=umbrella">Umbrella panic</a>
      <a href="/debug/webpprof/">Open webpprof</a>
    </nav>
    <p>Stop the server with <code>Ctrl+C</code>.</p>
  </main>
</body>
</html>`
