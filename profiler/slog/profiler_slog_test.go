package slog

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	stdlibslog "log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/levskiy0/webpprof"
)

func TestProfilerSlogRecordsFieldsAndPreservesHandler(t *testing.T) {
	mux := http.NewServeMux()
	profiler := webpprof.New(mux, webpprof.WithUnsafeUnauthenticatedAccess())
	t.Cleanup(func() { _ = profiler.Close() })
	handler := stdlibslog.NewTextHandler(io.Discard, nil)
	profiled := ProfileWith(profiler, handler)
	if ProfileWith(profiler, profiled) != profiled {
		t.Fatal("handler was wrapped twice")
	}
	logger := stdlibslog.New(profiled).With("component", "worker")
	logger.WarnContext(context.Background(), "retrying", "attempt", 2, "error", errors.New("temporary failure"))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/debug/webpprof/api/events?kind=log&limit=10", nil))
	var payload struct {
		Events []webpprof.Entry `json:"events"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Events) != 1 {
		t.Fatalf("events = %+v", payload.Events)
	}
	var event webpprof.Log
	if err := json.Unmarshal(payload.Events[0].Data, &event); err != nil {
		t.Fatal(err)
	}
	if event.Level != "WARN" || event.Message != "retrying" || event.Fields["component"] != "worker" || event.Fields["attempt"] != float64(2) {
		t.Fatalf("event = %+v", event)
	}
	if event.Fields["error"] != "temporary failure" {
		t.Fatalf("error field = %#v", event.Fields["error"])
	}
}

func TestProfilerSlogDisabledReturnsOriginalHandler(t *testing.T) {
	handler := stdlibslog.NewTextHandler(io.Discard, nil)
	if ProfileWith(nil, handler) != handler {
		t.Fatal("disabled profiler changed the handler")
	}
}
