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

func TestProfileMakesScheduleRootForNestedWork(t *testing.T) {
	mux := http.NewServeMux()
	profiler := webpprof.New(mux, webpprof.WithUnsafeUnauthenticatedAccess())
	t.Cleanup(func() { _ = profiler.Close() })
	capture := profiler.BeginRequest(webpprof.Request{Meta: webpprof.Meta{ID: "trigger-request"}, Method: http.MethodPost, Path: "/schedule/run"})
	ctx := webpprof.WithRequest(context.Background(), capture)
	ctx = webpprof.WithParentEntry(ctx, "request-handler")
	ctx = webpprof.WithTags(ctx, map[string]string{"tenant": "acme"})

	ProfileWith(profiler, "refresh", func(taskCtx context.Context) {
		profiler.LogQueryContext(taskCtx, webpprof.Query{Meta: webpprof.Meta{ID: "query-1"}, SQL: "SELECT 1"})
		profiler.LogLogContext(taskCtx, webpprof.Log{Meta: webpprof.Meta{ID: "log-1"}, Level: "INFO", Message: "refreshed"})
	})(ctx)
	capture.Finish(webpprof.RequestResult{Status: http.StatusAccepted})

	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/debug/webpprof/api/events?limit=10", nil))
	var payload struct {
		Events []webpprof.Entry `json:"events"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Events) != 4 {
		t.Fatalf("execution events = %+v", payload.Events)
	}

	var schedule, query, log webpprof.Entry
	for _, entry := range payload.Events {
		switch entry.Kind {
		case webpprof.KindSchedule:
			schedule = entry
		case webpprof.KindQuery:
			query = entry
		case webpprof.KindLog:
			log = entry
		}
	}
	if schedule.ID == "" || query.ID != "query-1" || log.ID != "log-1" {
		t.Fatalf("schedule, query, log = %+v, %+v, %+v", schedule, query, log)
	}
	if schedule.RequestID != "" || schedule.OriginRequestID != "" || schedule.ParentID != "" {
		t.Fatalf("schedule root = request %q, origin %q, parent %q", schedule.RequestID, schedule.OriginRequestID, schedule.ParentID)
	}
	if query.RequestID != "" || query.ParentID != schedule.ID || log.RequestID != "" || log.ParentID != schedule.ID {
		t.Fatalf("nested work: query request=%q parent=%q; log request=%q parent=%q; schedule=%q", query.RequestID, query.ParentID, log.RequestID, log.ParentID, schedule.ID)
	}
	if schedule.Tags["tenant"] != "acme" || query.Tags["tenant"] != "acme" || log.Tags["tenant"] != "acme" {
		t.Fatalf("schedule tags = %v, query tags = %v, log tags = %v", schedule.Tags, query.Tags, log.Tags)
	}
}

func TestProfileHonorsWithoutRecording(t *testing.T) {
	mux := http.NewServeMux()
	profiler := webpprof.New(mux, webpprof.WithUnsafeUnauthenticatedAccess())
	t.Cleanup(func() { _ = profiler.Close() })
	ctx := webpprof.WithoutRecording(context.Background())

	called := false
	ProfileWith(profiler, "cleanup", func(taskCtx context.Context) {
		called = true
		if webpprof.ParentEntryIDFromContext(taskCtx) != "" {
			t.Fatal("disabled profiling added a parent entry")
		}
	})(ctx)

	if !called {
		t.Fatal("profiled task was not called")
	}
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/debug/webpprof/api/events?kind=schedule&limit=10", nil))
	var payload struct {
		Events []webpprof.Entry `json:"events"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Events) != 0 {
		t.Fatalf("disabled schedule events = %+v", payload.Events)
	}
}
