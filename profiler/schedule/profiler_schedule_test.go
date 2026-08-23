package schedule

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/levskiy0/webpprof"
)

func TestProfileRecordsSuccessfulAndPanickedTasks(t *testing.T) {
	mux := http.NewServeMux()
	profiler := webpprof.New(mux, webpprof.WithUnsafeUnauthenticatedAccess())
	t.Cleanup(func() { _ = profiler.Close() })

	called := false
	ProfileWith(profiler, "refresh", func(context.Context) { called = true })(context.Background())
	if !called {
		t.Fatal("profiled task was not called")
	}

	var recovered any
	func() {
		defer func() { recovered = recover() }()
		ProfileWith(profiler, "cleanup", func(context.Context) { panic("boom") })(context.Background())
	}()
	if recovered != "boom" {
		t.Fatalf("recovered panic = %#v", recovered)
	}

	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/debug/webpprof/api/events?kind=schedule&limit=10", nil))
	var payload struct {
		Events []webpprof.Entry `json:"events"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Events) != 2 {
		t.Fatalf("schedule events = %+v", payload.Events)
	}
	var succeeded, panicked webpprof.Schedule
	if err := json.Unmarshal(payload.Events[0].Data, &succeeded); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(payload.Events[1].Data, &panicked); err != nil {
		t.Fatal(err)
	}
	if succeeded.Name != "refresh" || succeeded.State != "succeeded" {
		t.Fatalf("succeeded schedule = %+v", succeeded)
	}
	if panicked.Name != "cleanup" || panicked.State != "panicked" || panicked.Panic != "boom" {
		t.Fatalf("panicked schedule = %+v", panicked)
	}
}

func TestProfilePreservesNilTask(t *testing.T) {
	if profiled := ProfileWith(nil, "cleanup", nil); profiled != nil {
		t.Fatalf("nil task became %#v", profiled)
	}
}
