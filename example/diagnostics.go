package main

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/levskiy0/webpprof"
)

const diagnosticPlayerSQL = "SELECT id, email FROM players WHERE id = ?"

// logDiagnosticExamples is intentionally synthetic. The normal API routes use
// real SQLite timings; this isolated route records deterministic durations so
// every automatic analyzer rule remains easy to inspect during development.
func (m *manualExamples) logDiagnosticExamples(ctx context.Context, startedAt time.Time) {
	m.logNPlusOne(ctx, startedAt)
	m.logSQLShare(ctx, startedAt)
	m.logSlowHTTP(ctx, startedAt)
	m.logSlowMiddleware(ctx, startedAt)
	m.logFailedJob(ctx, startedAt)
	m.logCacheQueryBurst(ctx, startedAt)
	m.logSequentialHTTP(ctx, startedAt)
}

func (m *manualExamples) logNPlusOne(ctx context.Context, startedAt time.Time) {
	rows := int64(1)
	for index := range 47 {
		m.profiler.LogQueryContext(ctx, webpprof.Query{
			Meta: webpprof.Meta{
				StartedAt: startedAt.Add(time.Duration(index) * 2 * time.Millisecond),
				Duration:  2 * time.Millisecond,
				Tags:      map[string]string{"diagnostic": "n-plus-one"},
			},
			Connection:   "example",
			Driver:       "sqlite",
			Database:     "example.db",
			Operation:    "SELECT",
			SQL:          diagnosticPlayerSQL,
			RowsAffected: &rows,
		})
	}
}

func (m *manualExamples) logSQLShare(ctx context.Context, startedAt time.Time) {
	rows := int64(1)
	m.profiler.LogQueryContext(ctx, webpprof.Query{
		Meta: webpprof.Meta{
			StartedAt: startedAt.Add(100 * time.Millisecond),
			Duration:  575 * time.Millisecond,
			Tags:      map[string]string{"diagnostic": "sql-share"},
		},
		Connection:   "analytics",
		Driver:       "sqlite",
		Database:     "example.db",
		Operation:    "SELECT",
		SQL:          "SELECT * FROM audit_log ORDER BY created_at DESC",
		RowsAffected: &rows,
	})
}

func (m *manualExamples) logSlowHTTP(ctx context.Context, startedAt time.Time) {
	m.profiler.LogHTTPCallContext(ctx, webpprof.HTTPCall{
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
}

func (m *manualExamples) logSlowMiddleware(ctx context.Context, startedAt time.Time) {
	m.profiler.LogMiddlewareContext(ctx, webpprof.Middleware{
		Meta: webpprof.Meta{
			StartedAt: startedAt.Add(110 * time.Millisecond),
			Duration:  430 * time.Millisecond,
			Tags:      map[string]string{"diagnostic": "slow-middleware"},
		},
		Name:  "auth",
		State: "completed",
	})
}

func (m *manualExamples) logFailedJob(ctx context.Context, startedAt time.Time) {
	m.profiler.LogJobContext(ctx, webpprof.Job{
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
}

func (m *manualExamples) logCacheQueryBurst(ctx context.Context, startedAt time.Time) {
	m.profiler.LogCacheContext(ctx, webpprof.Cache{
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

	rows := int64(1)
	for index := range 18 {
		m.profiler.LogQueryContext(ctx, webpprof.Query{
			Meta: webpprof.Meta{
				StartedAt: startedAt.Add(645*time.Millisecond + time.Duration(index)*2*time.Millisecond),
				Duration:  time.Millisecond,
				Tags:      map[string]string{"diagnostic": "cache-query-burst"},
			},
			Connection:   "example",
			Driver:       "sqlite",
			Database:     "example.db",
			Operation:    "SELECT",
			SQL:          "SELECT permission FROM player_permissions WHERE player_id = ?",
			RowsAffected: &rows,
		})
	}
}

func (m *manualExamples) logSequentialHTTP(ctx context.Context, startedAt time.Time) {
	for index := range 3 {
		m.profiler.LogHTTPCallContext(ctx, webpprof.HTTPCall{
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
