package webpprof

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDashboardSnapshotPreservesConfiguredWidgetOrderAndValues(t *testing.T) {
	profiler := newProfiler(Dashboard(
		WithCPU(),
		WithCustomMetric(DashboardMetric{Title: "Processed total", Mode: DashboardMetricRate, Sparkline: true, Value: dashboardConstant(42)}),
		WithCustomChart(DashboardChart{ID: "traffic", Title: "Traffic", Span: 8, Unit: "ops", Series: []DashboardSeries{{Label: "Reads", Value: dashboardConstant(12)}}}),
		WithCounterGrid(DashboardCounterGrid{ID: "results", Title: "Results", Span: 2, Counters: []DashboardCounter{{Label: "Failed", Value: dashboardConstant(3)}}}),
		WithSlowestOperations(),
	))
	t.Cleanup(func() { _ = profiler.Close() })

	snapshot := profiler.DashboardSnapshot(context.Background())
	if len(snapshot.Widgets) != 5 {
		t.Fatalf("widgets = %d, want 5", len(snapshot.Widgets))
	}
	if snapshot.Widgets[0].Builtin != "cpu" || snapshot.Widgets[1].ID != "processed-total" || snapshot.Widgets[4].Builtin != "slowest_operations" {
		t.Fatalf("widget order = %+v", snapshot.Widgets)
	}
	metric := snapshot.Widgets[1].Metric
	if metric == nil || metric.Value != 42 || metric.Mode != DashboardMetricRate || !metric.Sparkline {
		t.Fatalf("metric = %+v", metric)
	}
	chart := snapshot.Widgets[2]
	if chart.Span != 4 || chart.Unit != "ops" || len(chart.Series) != 1 || chart.Series[0].ID != "reads" || chart.Series[0].Value != 12 {
		t.Fatalf("chart = %+v", chart)
	}
	grid := snapshot.Widgets[3]
	if grid.Span != 2 || len(grid.Counters) != 1 || grid.Counters[0].Value != 3 {
		t.Fatalf("counter grid = %+v", grid)
	}
}

func TestDashboardSnapshotIsolatesCallbackErrorsAndPanics(t *testing.T) {
	profiler := newProfiler(Dashboard(
		WithCustomMetric(DashboardMetric{ID: "error", Title: "Error", Value: func(context.Context) (float64, error) {
			return 0, errors.New("metrics backend unavailable")
		}}),
		WithCustomMetric(DashboardMetric{ID: "panic", Title: "Panic", Value: func(context.Context) (float64, error) {
			panic("broken collector")
		}}),
		WithCustomMetric(DashboardMetric{ID: "nil", Title: "Nil"}),
	))
	t.Cleanup(func() { _ = profiler.Close() })

	snapshot := profiler.DashboardSnapshot(context.Background())
	if len(snapshot.Widgets) != 3 {
		t.Fatalf("widgets = %d, want 3", len(snapshot.Widgets))
	}
	if got := snapshot.Widgets[0].Metric.Error; got != "metrics backend unavailable" {
		t.Fatalf("error metric = %q", got)
	}
	if got := snapshot.Widgets[1].Metric.Error; !strings.Contains(got, "panicked: broken collector") {
		t.Fatalf("panic metric = %q", got)
	}
	if got := snapshot.Widgets[2].Metric.Error; !strings.Contains(got, "source is nil") {
		t.Fatalf("nil metric = %q", got)
	}
}

func TestProfilerServesDashboardSnapshot(t *testing.T) {
	mux := http.NewServeMux()
	profiler := newProfiler(WithUnsafeUnauthenticatedAccess(), Dashboard(WithCustomMetric(DashboardMetric{ID: "orders", Title: "Orders", Value: dashboardConstant(7)})))
	profiler.register(mux)
	t.Cleanup(func() { _ = profiler.Close() })

	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/debug/webpprof/api/dashboard", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	var snapshot DashboardSnapshot
	if err := json.Unmarshal(response.Body.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode dashboard: %v", err)
	}
	if len(snapshot.Widgets) != 1 || snapshot.Widgets[0].Metric == nil || snapshot.Widgets[0].Metric.Value != 7 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}

func TestDefaultDashboardContainsAllBuiltins(t *testing.T) {
	profiler := newProfiler()
	t.Cleanup(func() { _ = profiler.Close() })
	snapshot := profiler.DashboardSnapshot(context.Background())
	if len(snapshot.Widgets) != 9 {
		t.Fatalf("default widgets = %d, want 9", len(snapshot.Widgets))
	}
	if snapshot.Widgets[6].Builtin != "event_mix" || snapshot.Widgets[7].Span != 4 || snapshot.Widgets[8].Builtin != "slowest_operations" {
		t.Fatalf("default widgets = %+v", snapshot.Widgets)
	}
}

func dashboardConstant(value float64) DashboardValueFunc {
	return func(context.Context) (float64, error) { return value, nil }
}
