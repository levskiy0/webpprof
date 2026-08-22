package webpprof

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode"
)

// DashboardOption configures one dashboard widget.
type DashboardOption func(*dashboardConfig)

// DashboardValueFunc returns the current value for a custom dashboard metric.
// Implementations should honor context cancellation and return quickly.
type DashboardValueFunc func(context.Context) (float64, error)

// DashboardMetricMode describes how metric samples are interpreted.
type DashboardMetricMode string

const (
	// DashboardMetricValue renders the current sample as-is.
	DashboardMetricValue DashboardMetricMode = "value"
	// DashboardMetricRate treats samples as a monotonically increasing counter
	// and renders its change per second.
	DashboardMetricRate DashboardMetricMode = "rate"
)

// DashboardFormat controls value formatting in the browser.
type DashboardFormat string

const (
	// DashboardFormatNumber renders a regular decimal number.
	DashboardFormatNumber DashboardFormat = "number"
	// DashboardFormatBytes renders a byte count with a binary size suffix.
	DashboardFormatBytes DashboardFormat = "bytes"
	// DashboardFormatPercent renders a value on the 0–100 percent scale.
	DashboardFormatPercent DashboardFormat = "percent"
	// DashboardFormatDuration renders a duration supplied in nanoseconds.
	DashboardFormatDuration DashboardFormat = "duration"
)

// DashboardMetric configures a single custom metric card. Sparkline can be
// false when the card should contain only the current value.
type DashboardMetric struct {
	ID          string
	Title       string
	Description string
	Unit        string
	Format      DashboardFormat
	Mode        DashboardMetricMode
	Sparkline   bool
	Color       string
	Value       DashboardValueFunc
}

// DashboardSeries configures one line in a custom chart.
type DashboardSeries struct {
	ID    string
	Label string
	Color string
	Value DashboardValueFunc
}

// DashboardChart configures a time-series chart. Span is clamped to 1..4
// columns and defaults to 2.
type DashboardChart struct {
	ID          string
	Title       string
	Description string
	Unit        string
	Format      DashboardFormat
	Span        int
	Series      []DashboardSeries
}

// DashboardCounter configures one value inside a counter grid.
type DashboardCounter struct {
	ID     string
	Label  string
	Unit   string
	Format DashboardFormat
	Value  DashboardValueFunc
}

// DashboardCounterGrid groups related counters without charts. Span is
// clamped to 1..4 columns and defaults to 2.
type DashboardCounterGrid struct {
	ID          string
	Title       string
	Description string
	Span        int
	Counters    []DashboardCounter
}

type dashboardConfig struct {
	widgets []dashboardWidget
}

type dashboardWidget struct {
	ID          string
	Kind        string
	Builtin     string
	Title       string
	Description string
	Span        int
	Metric      *DashboardMetric
	Chart       *DashboardChart
	CounterGrid *DashboardCounterGrid
}

// DashboardSnapshot contains one sampled dashboard configuration and its
// custom values.
type DashboardSnapshot struct {
	RecordedAt time.Time                 `json:"recorded_at"`
	Widgets    []DashboardWidgetSnapshot `json:"widgets"`
}

// DashboardWidgetSnapshot is the browser-facing representation of one widget.
type DashboardWidgetSnapshot struct {
	ID          string                     `json:"id"`
	Kind        string                     `json:"kind"`
	Builtin     string                     `json:"builtin,omitempty"`
	Title       string                     `json:"title"`
	Description string                     `json:"description,omitempty"`
	Span        int                        `json:"span"`
	Unit        string                     `json:"unit,omitempty"`
	Format      DashboardFormat            `json:"format,omitempty"`
	Metric      *DashboardMetricSnapshot   `json:"metric,omitempty"`
	Series      []DashboardSeriesSnapshot  `json:"series,omitempty"`
	Counters    []DashboardCounterSnapshot `json:"counters,omitempty"`
}

// DashboardMetricSnapshot contains the latest sample for a metric card.
type DashboardMetricSnapshot struct {
	Value     float64             `json:"value"`
	Unit      string              `json:"unit,omitempty"`
	Format    DashboardFormat     `json:"format"`
	Mode      DashboardMetricMode `json:"mode"`
	Sparkline bool                `json:"sparkline"`
	Color     string              `json:"color,omitempty"`
	Error     string              `json:"error,omitempty"`
}

// DashboardSeriesSnapshot contains the latest sample for one chart series.
type DashboardSeriesSnapshot struct {
	ID    string  `json:"id"`
	Label string  `json:"label"`
	Color string  `json:"color,omitempty"`
	Value float64 `json:"value"`
	Error string  `json:"error,omitempty"`
}

// DashboardCounterSnapshot contains the latest value in a counter grid.
type DashboardCounterSnapshot struct {
	ID     string          `json:"id"`
	Label  string          `json:"label"`
	Unit   string          `json:"unit,omitempty"`
	Format DashboardFormat `json:"format"`
	Value  float64         `json:"value"`
	Error  string          `json:"error,omitempty"`
}

// Dashboard replaces the default dashboard with the supplied widgets.
func Dashboard(options ...DashboardOption) Option {
	return func(c *config) {
		dashboard := dashboardConfig{}
		for _, option := range options {
			if option != nil {
				option(&dashboard)
			}
		}
		c.dashboard = dashboard
	}
}

// WithCPU adds the built-in process CPU card.
func WithCPU() DashboardOption {
	return withBuiltinDashboardWidget("cpu", "metric", "CPU usage", "Process CPU utilization", 1)
}

// WithGoMemory adds the built-in Go memory card.
func WithGoMemory() DashboardOption {
	return withBuiltinDashboardWidget("memory", "metric", "Go memory", "Memory held by the Go runtime", 1)
}

// WithRequests adds the built-in recorded request throughput card.
func WithRequests() DashboardOption {
	return withBuiltinDashboardWidget("requests", "metric", "HTTP requests", "Recorded request throughput", 1)
}

// WithQueries adds the built-in recorded query throughput card.
func WithQueries() DashboardOption {
	return withBuiltinDashboardWidget("queries", "metric", "Database queries", "Recorded query throughput", 1)
}

// WithCacheHitRate adds the built-in cache hit rate card.
func WithCacheHitRate() DashboardOption {
	return withBuiltinDashboardWidget("cache", "metric", "Cache hit rate", "Hits across recorded cache operations", 1)
}

// WithGoroutines adds the built-in goroutine count card.
func WithGoroutines() DashboardOption {
	return withBuiltinDashboardWidget("goroutines", "metric", "Goroutines", "Current Go goroutine count", 1)
}

// WithEventMix adds the built-in event distribution panel.
func WithEventMix() DashboardOption {
	return withBuiltinDashboardWidget("event_mix", "event_mix", "Event mix", "Events retained by the profiler", 2)
}

// WithQueueHealth adds the built-in queue health panel.
func WithQueueHealth() DashboardOption {
	return withBuiltinDashboardWidget("queue_health", "queue_health", "Queue health", "Backlog and worker capacity by queue", 4)
}

// WithSlowestOperations adds the built-in slow operations panel.
func WithSlowestOperations() DashboardOption {
	return withBuiltinDashboardWidget("slowest_operations", "slowest_operations", "Slowest operations", "Requests, queries and HTTP calls", 4)
}

// WithCustomMetric adds a custom metric card. Rate mode expects Value to
// return a cumulative counter; the UI derives its per-second change.
func WithCustomMetric(metric DashboardMetric) DashboardOption {
	return func(c *dashboardConfig) {
		metric.ID = dashboardID(metric.ID, metric.Title, "metric")
		metric.Format = normalizedDashboardFormat(metric.Format)
		if metric.Mode == "" {
			metric.Mode = DashboardMetricValue
		}
		if metric.Mode != DashboardMetricValue && metric.Mode != DashboardMetricRate {
			metric.Mode = DashboardMetricValue
		}
		if strings.TrimSpace(metric.Title) == "" {
			metric.Title = metric.ID
		}
		widget := dashboardWidget{ID: metric.ID, Kind: "custom_metric", Title: metric.Title, Description: metric.Description, Span: 1, Metric: &metric}
		c.set(widget)
	}
}

// WithCustomChart adds a custom multi-series time chart.
func WithCustomChart(chart DashboardChart) DashboardOption {
	return func(c *dashboardConfig) {
		chart.ID = dashboardID(chart.ID, chart.Title, "chart")
		chart.Format = normalizedDashboardFormat(chart.Format)
		chart.Span = dashboardSpan(chart.Span, 2)
		for index := range chart.Series {
			chart.Series[index].ID = dashboardID(chart.Series[index].ID, chart.Series[index].Label, fmt.Sprintf("series-%d", index+1))
		}
		if strings.TrimSpace(chart.Title) == "" {
			chart.Title = chart.ID
		}
		widget := dashboardWidget{ID: chart.ID, Kind: "custom_chart", Title: chart.Title, Description: chart.Description, Span: chart.Span, Chart: &chart}
		c.set(widget)
	}
}

// WithCounterGrid adds a grid of counters without sparklines.
func WithCounterGrid(grid DashboardCounterGrid) DashboardOption {
	return func(c *dashboardConfig) {
		grid.ID = dashboardID(grid.ID, grid.Title, "counter-grid")
		grid.Span = dashboardSpan(grid.Span, 2)
		for index := range grid.Counters {
			grid.Counters[index].ID = dashboardID(grid.Counters[index].ID, grid.Counters[index].Label, fmt.Sprintf("counter-%d", index+1))
			grid.Counters[index].Format = normalizedDashboardFormat(grid.Counters[index].Format)
		}
		if strings.TrimSpace(grid.Title) == "" {
			grid.Title = grid.ID
		}
		widget := dashboardWidget{ID: grid.ID, Kind: "counter_grid", Title: grid.Title, Description: grid.Description, Span: grid.Span, CounterGrid: &grid}
		c.set(widget)
	}
}

func defaultDashboardConfig() dashboardConfig {
	configuration := dashboardConfig{}
	options := []DashboardOption{WithCPU(), WithGoMemory(), WithRequests(), WithQueries(), WithCacheHitRate(), WithGoroutines(), WithEventMix(), WithQueueHealth(), WithSlowestOperations()}
	for _, option := range options {
		option(&configuration)
	}
	return configuration
}

func withBuiltinDashboardWidget(id, kind, title, description string, span int) DashboardOption {
	return func(c *dashboardConfig) {
		c.set(dashboardWidget{ID: id, Kind: kind, Builtin: id, Title: title, Description: description, Span: dashboardSpan(span, 1)})
	}
}

func (c *dashboardConfig) set(widget dashboardWidget) {
	for index := range c.widgets {
		if c.widgets[index].ID == widget.ID {
			c.widgets[index] = widget
			return
		}
	}
	c.widgets = append(c.widgets, widget)
}

// DashboardSnapshot samples every configured custom dashboard value.
func (p *Profiler) DashboardSnapshot(ctx context.Context) DashboardSnapshot {
	snapshot := DashboardSnapshot{RecordedAt: time.Now().UTC(), Widgets: []DashboardWidgetSnapshot{}}
	if p == nil {
		return snapshot
	}
	snapshot.Widgets = make([]DashboardWidgetSnapshot, 0, len(p.config.dashboard.widgets))
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, p.config.dashboardTimeout)
	defer cancel()
	for _, widget := range p.config.dashboard.widgets {
		current := DashboardWidgetSnapshot{ID: widget.ID, Kind: widget.Kind, Builtin: widget.Builtin, Title: widget.Title, Description: widget.Description, Span: widget.Span}
		switch widget.Kind {
		case "custom_metric":
			metric := widget.Metric
			value, err := dashboardValue(ctx, metric.Value)
			current.Metric = &DashboardMetricSnapshot{Value: value, Unit: metric.Unit, Format: metric.Format, Mode: metric.Mode, Sparkline: metric.Sparkline, Color: metric.Color, Error: dashboardError(err)}
		case "custom_chart":
			current.Unit = widget.Chart.Unit
			current.Format = widget.Chart.Format
			for _, series := range widget.Chart.Series {
				value, err := dashboardValue(ctx, series.Value)
				current.Series = append(current.Series, DashboardSeriesSnapshot{ID: series.ID, Label: series.Label, Color: series.Color, Value: value, Error: dashboardError(err)})
			}
		case "counter_grid":
			for _, counter := range widget.CounterGrid.Counters {
				value, err := dashboardValue(ctx, counter.Value)
				current.Counters = append(current.Counters, DashboardCounterSnapshot{ID: counter.ID, Label: counter.Label, Unit: counter.Unit, Format: counter.Format, Value: value, Error: dashboardError(err)})
			}
		}
		snapshot.Widgets = append(snapshot.Widgets, current)
	}
	return snapshot
}

func dashboardValue(ctx context.Context, source DashboardValueFunc) (value float64, err error) {
	if source == nil {
		return 0, fmt.Errorf("dashboard value source is nil")
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("dashboard value source panicked: %v", recovered)
		}
	}()
	value, err = source(ctx)
	if err == nil && (math.IsNaN(value) || math.IsInf(value, 0)) {
		return 0, fmt.Errorf("dashboard value is not finite")
	}
	return value, err
}

func dashboardError(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func dashboardID(id, title, fallback string) string {
	value := strings.TrimSpace(id)
	if value == "" {
		value = strings.TrimSpace(title)
	}
	var result strings.Builder
	previousSeparator := false
	for _, current := range strings.ToLower(value) {
		if unicode.IsLetter(current) || unicode.IsDigit(current) {
			result.WriteRune(current)
			previousSeparator = false
			continue
		}
		if result.Len() > 0 && !previousSeparator {
			result.WriteByte('-')
			previousSeparator = true
		}
	}
	normalized := strings.Trim(result.String(), "-")
	if normalized == "" {
		return fallback
	}
	return normalized
}

func dashboardSpan(span, fallback int) int {
	if span == 0 {
		span = fallback
	}
	return min(4, max(1, span))
}

func normalizedDashboardFormat(format DashboardFormat) DashboardFormat {
	switch format {
	case DashboardFormatBytes, DashboardFormatPercent, DashboardFormatDuration:
		return format
	default:
		return DashboardFormatNumber
	}
}
