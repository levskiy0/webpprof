package main

import (
	"fmt"
	"time"

	"github.com/levskiy0/webpprof"
)

// profilerOptions keeps optional retention, storage, callsite, and dashboard
// tuning out of the short integration block in main.go.
func profilerOptions(metrics *demoMetrics, storage webpprof.EntryStorage) []webpprof.Option {
	return []webpprof.Option{
		webpprof.WithRetention(2 * time.Hour),
		webpprof.WithMaxEvents(25_000),
		webpprof.WithMaxBytes(128 << 20),
		webpprof.WithBodyLimit(32 << 10),
		webpprof.WithUnsafeUnauthenticatedAccess(),
		// Profiler events use a separate SQLite file and survive restarts while
		// remaining bounded by the retention, event, and byte limits above.
		webpprof.WithStorage(storage),
		webpprof.WithExcludedRequests("GET /favicon.ico"),
		webpprof.WithCallsiteKinds(
			webpprof.KindQuery,
			webpprof.KindCache,
			webpprof.KindEmail,
			webpprof.KindJob,
			webpprof.KindHTTPCall,
			webpprof.KindSchedule,
		),
		webpprof.WithSourceLink(func(frame webpprof.SourceFrame) string {
			return fmt.Sprintf("vscode://file/%s:%d", frame.File, frame.Line)
		}),
		webpprof.Dashboard(
			webpprof.WithCPU(),
			webpprof.WithGoMemory(),
			webpprof.WithRequests(),
			webpprof.WithQueries(),
			webpprof.WithCustomMetric(webpprof.DashboardMetric{
				ID:          "demo-total",
				Title:       "API requests",
				Description: "Handled example requests",
				Value:       metrics.totalValue,
			}),
			webpprof.WithCustomMetric(webpprof.DashboardMetric{
				ID:          "demo-rate",
				Title:       "API throughput",
				Description: "Requests per second",
				Unit:        "req/s",
				Mode:        webpprof.DashboardMetricRate,
				Sparkline:   true,
				Color:       "#17a36d",
				Value:       metrics.totalValue,
			}),
			webpprof.WithCounterGrid(webpprof.DashboardCounterGrid{
				ID:          "demo-outcomes",
				Title:       "API outcomes",
				Description: "Application counters",
				Span:        2,
				Counters: []webpprof.DashboardCounter{
					{
						ID:    "success",
						Label: "Succeeded",
						Value: metrics.successValue,
					},
					{
						ID:    "failed",
						Label: "Failed",
						Value: metrics.failedValue,
					},
					{
						ID:     "last-duration",
						Label:  "Last duration",
						Format: webpprof.DashboardFormatDuration,
						Value:  metrics.lastDurationValue,
					},
				},
			}),
			webpprof.WithCustomChart(webpprof.DashboardChart{
				ID:          "demo-history",
				Title:       "Demo result history",
				Description: "Cumulative HTTP outcomes",
				Span:        4,
				Series: []webpprof.DashboardSeries{
					{
						ID:    "success",
						Label: "Succeeded",
						Color: "#17a36d",
						Value: metrics.successValue,
					},
					{
						ID:    "failed",
						Label: "Failed",
						Color: "#ba4a52",
						Value: metrics.failedValue,
					},
				},
			}),
			webpprof.WithSlowestOperations(),
		),
	}
}
