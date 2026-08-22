package otel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/levskiy0/webpprof"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

func TestProfileRegistersOneProcessorAndMapsQuery(t *testing.T) {
	mux := http.NewServeMux()
	profiler := webpprof.New(mux)
	t.Cleanup(func() { _ = profiler.Close() })
	provider := sdktrace.NewTracerProvider()
	ProfileWith(profiler, provider)
	ProfileWith(profiler, provider)
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	_, span := provider.Tracer("profiler-test").Start(context.Background(), "SELECT players", trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(
		attribute.String("db.system.name", "postgresql"),
		attribute.String("db.namespace", "app"),
		attribute.String("db.operation.name", "SELECT"),
		attribute.String("db.query.text", "select id from players"),
	))
	span.End()
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/debug/webpprof/api/events?kind=query&limit=10", nil))
	var response struct {
		Events []webpprof.Entry `json:"events"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Events) != 1 {
		t.Fatalf("events = %+v", response.Events)
	}
	var query webpprof.Query
	if err := json.Unmarshal(response.Events[0].Data, &query); err != nil {
		t.Fatal(err)
	}
	if query.Driver != "postgresql" || query.Database != "app" || query.Operation != "SELECT" {
		t.Fatalf("query = %+v", query)
	}
}
