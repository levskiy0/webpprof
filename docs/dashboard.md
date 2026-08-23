# Dashboard configuration

Without explicit dashboard configuration, webpprof enables all built-in
widgets using its default layout. Pass `Dashboard(...)` to select and order
widgets yourself. The desktop layout has four columns and collapses on smaller
screens; charts and counter groups can span one to four columns.

## Built-in widgets

The built-ins are `WithCPU`, `WithGoMemory`, `WithRequests`, `WithQueries`,
`WithCacheHitRate`, `WithGoroutines`, `WithEventMix`, `WithQueueHealth`, and
`WithSlowestOperations`.

```go
profiler := webpprof.New(
    mux,
    webpprof.Dashboard(
        webpprof.WithCPU(),
        webpprof.WithGoMemory(),
        webpprof.WithRequests(),
        webpprof.WithSlowestOperations(),
    ),
)
```

Widgets keep declaration order. `WithSlowestOperations` is a full-width panel.

## Custom metrics

A plain metric renders the current value. Rate mode expects a cumulative
counter; the browser derives the per-second change and draws its sparkline.

```go
var requests atomic.Uint64

profiler := webpprof.New(
    mux,
    webpprof.Dashboard(
        webpprof.WithCustomMetric(webpprof.DashboardMetric{
            ID:          "orders-total",
            Title:       "Orders",
            Description: "Accepted since process start",
            Value: func(context.Context) (float64, error) {
                return float64(requests.Load()), nil
            },
        }),
        webpprof.WithCustomMetric(webpprof.DashboardMetric{
            ID:        "orders-rate",
            Title:     "Order throughput",
            Unit:      "orders/s",
            Mode:      webpprof.DashboardMetricRate,
            Sparkline: true,
            Color:     "#17a36d",
            Value: func(context.Context) (float64, error) {
                return float64(requests.Load()), nil
            },
        }),
    ),
)
```

## Counter grids and charts

```go
var succeeded atomic.Uint64
var failed atomic.Uint64

profiler := webpprof.New(
    mux,
    webpprof.Dashboard(
        webpprof.WithCounterGrid(webpprof.DashboardCounterGrid{
            ID: "order-results", Title: "Order results", Span: 2,
            Counters: []webpprof.DashboardCounter{
                {ID: "ok", Label: "Succeeded", Value: func(context.Context) (float64, error) {
                    return float64(succeeded.Load()), nil
                }},
                {ID: "failed", Label: "Failed", Value: func(context.Context) (float64, error) {
                    return float64(failed.Load()), nil
                }},
            },
        }),
        webpprof.WithCustomChart(webpprof.DashboardChart{
            ID: "order-history", Title: "Order history", Span: 2,
            Series: []webpprof.DashboardSeries{
                {ID: "ok", Label: "Succeeded", Color: "#17a36d", Value: func(context.Context) (float64, error) {
                    return float64(succeeded.Load()), nil
                }},
                {ID: "failed", Label: "Failed", Color: "#ba4a52", Value: func(context.Context) (float64, error) {
                    return float64(failed.Load()), nil
                }},
            },
        }),
    ),
)
```

Custom callbacks run when the dashboard opens and on every two-second live
update. They must be concurrency-safe, return quickly, and honor cancellation.
`WithDashboardTimeout` supplies the deadline shared by one custom snapshot. A
callback error appears only on its widget.

Duration values use nanoseconds with `DashboardFormatDuration`, percentages use
the 0–100 scale with `DashboardFormatPercent`, and byte values use
`DashboardFormatBytes`. Keep widget IDs stable so the browser retains their
sparkline history across updates.
