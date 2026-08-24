package callable

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/levskiy0/webpprof"
)

func TestProfileCreatesIndependentExecutionRoot(t *testing.T) {
	mux := http.NewServeMux()
	profiler := webpprof.New(mux, webpprof.WithUnsafeUnauthenticatedAccess())
	t.Cleanup(func() { _ = profiler.Close() })
	capture := profiler.BeginRequest(webpprof.Request{Meta: webpprof.Meta{ID: "trigger-request"}, Method: http.MethodPost, Path: "/commands/rebuild"})
	ctx := webpprof.WithRequest(context.Background(), capture)
	ctx = webpprof.WithParentEntry(ctx, "request-handler")
	ctx = webpprof.WithTags(ctx, map[string]string{"tenant": "acme"})

	command := ProfileWith(profiler, "search.rebuild", func(commandCtx context.Context) error {
		profiler.LogQueryContext(commandCtx, webpprof.Query{Meta: webpprof.Meta{ID: "query-1"}, SQL: "SELECT 1"})
		profiler.LogLogContext(commandCtx, webpprof.Log{Meta: webpprof.Meta{ID: "log-1"}, Level: "INFO", Message: "rebuilt"})
		return nil
	})
	if err := command(ctx); err != nil {
		t.Fatal(err)
	}
	capture.Finish(webpprof.RequestResult{Status: http.StatusAccepted})

	entries := listEntries(t, mux)
	var callable, query, log webpprof.Entry
	for _, entry := range entries {
		switch entry.Kind {
		case webpprof.KindCallable:
			callable = entry
		case webpprof.KindQuery:
			query = entry
		case webpprof.KindLog:
			log = entry
		}
	}
	if callable.ID == "" || query.ID == "" || log.ID == "" {
		t.Fatalf("callable execution = %+v", entries)
	}
	if callable.RequestID != "" || callable.ParentID != "" || callable.OriginRequestID != "" {
		t.Fatalf("callable root correlation = %+v", callable)
	}
	if query.RequestID != "" || query.ParentID != callable.ID || log.RequestID != "" || log.ParentID != callable.ID {
		t.Fatalf("callable children: callable=%q query=%+v log=%+v", callable.ID, query, log)
	}
	if callable.Tags["tenant"] != "acme" || query.Tags["tenant"] != "acme" || log.Tags["tenant"] != "acme" {
		t.Fatalf("callable tags: root=%v query=%v log=%v", callable.Tags, query.Tags, log.Tags)
	}
}

func TestProfileRecordsReturnedErrorAndPanic(t *testing.T) {
	mux := http.NewServeMux()
	profiler := webpprof.New(mux, webpprof.WithUnsafeUnauthenticatedAccess())
	t.Cleanup(func() { _ = profiler.Close() })
	wantErr := errors.New("rebuild failed")
	if err := ProfileWith(profiler, "failed", func(context.Context) error { return wantErr })(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("returned error = %v", err)
	}
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		_ = ProfileWith(profiler, "panicked", func(context.Context) error { panic("boom") })(context.Background())
	}()
	if recovered != "boom" {
		t.Fatalf("panic = %#v", recovered)
	}

	states := map[string]webpprof.Callable{}
	for _, entry := range listEntries(t, mux) {
		if entry.Kind != webpprof.KindCallable {
			continue
		}
		var callable webpprof.Callable
		if err := json.Unmarshal(entry.Data, &callable); err != nil {
			t.Fatal(err)
		}
		states[callable.Name] = callable
	}
	if states["failed"].State != "failed" || states["failed"].Error != wantErr.Error() {
		t.Fatalf("failed callable = %+v", states["failed"])
	}
	if states["panicked"].State != "panicked" || states["panicked"].Panic != "boom" {
		t.Fatalf("panicked callable = %+v", states["panicked"])
	}
}

func TestProfileHonorsWithoutRecording(t *testing.T) {
	mux := http.NewServeMux()
	profiler := webpprof.New(mux, webpprof.WithUnsafeUnauthenticatedAccess())
	t.Cleanup(func() { _ = profiler.Close() })
	called := false
	command := ProfileWith(profiler, "disabled", func(context.Context) error {
		called = true
		return nil
	})
	if err := command(webpprof.WithoutRecording(context.Background())); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("command was not called")
	}
	if entries := listEntries(t, mux); len(entries) != 0 {
		t.Fatalf("disabled entries = %+v", entries)
	}
}

func listEntries(t *testing.T, mux *http.ServeMux) []webpprof.Entry {
	t.Helper()
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/debug/webpprof/api/events?limit=20", nil))
	var payload struct {
		Events []webpprof.Entry `json:"events"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	return payload.Events
}
