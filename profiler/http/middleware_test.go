package http

import (
	"context"
	"encoding/json"
	"io"
	stdlibhttp "net/http"
	"net/http/httptest"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"github.com/levskiy0/webpprof"
)

func TestDisabledMiddlewareReturnsOriginalHandler(t *testing.T) {
	called := false
	handler := stdlibhttp.HandlerFunc(func(stdlibhttp.ResponseWriter, *stdlibhttp.Request) {
		called = true
	})
	MiddlewareWith(nil, handler).ServeHTTP(
		httptest.NewRecorder(),
		httptest.NewRequest(stdlibhttp.MethodGet, "/", nil),
	)
	if !called {
		t.Fatal("disabled middleware did not pass the request through")
	}
}

func TestMiddlewareCapturesCorrelatedEventsAndBodies(t *testing.T) {
	mux := stdlibhttp.NewServeMux()
	profiler := webpprof.New(mux, webpprof.WithBodyLimit(1024))
	t.Cleanup(func() { _ = profiler.Close() })
	handler := MiddlewareWith(profiler, stdlibhttp.HandlerFunc(func(w stdlibhttp.ResponseWriter, r *stdlibhttp.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil || string(body) != `{"name":"Ada","token":"request-secret"}` {
			t.Fatalf("request body = %q, error = %v", body, err)
		}
		webpprof.LogQueryContext(r.Context(), webpprof.Query{Operation: "SELECT", SQL: "SELECT 1"})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(stdlibhttp.StatusCreated)
		_, _ = w.Write([]byte(`{"ok":true,"api_key":"response-secret"}`))
	}))
	request := httptest.NewRequest(
		stdlibhttp.MethodPost,
		"/players?token=secret&limit=10",
		strings.NewReader(`{"name":"Ada","token":"request-secret"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != stdlibhttp.StatusCreated {
		t.Fatalf("status = %d", response.Code)
	}
	entries := readEvents(t, mux, "")
	if len(entries) != 2 || entries[0].Kind != webpprof.KindQuery || entries[1].Kind != webpprof.KindRequest {
		t.Fatalf("entries = %+v", entries)
	}
	if entries[0].RequestID == "" || entries[0].RequestID != entries[1].RequestID {
		t.Fatalf("query request = %q, request = %q", entries[0].RequestID, entries[1].RequestID)
	}
	var recorded webpprof.Request
	if err := json.Unmarshal(entries[1].Data, &recorded); err != nil {
		t.Fatal(err)
	}
	if recorded.Query != "limit=10&token=[REDACTED]" {
		t.Fatalf("query = %q", recorded.Query)
	}
	if recorded.Scheme != "http" {
		t.Fatalf("scheme = %q", recorded.Scheme)
	}
	if strings.Contains(recorded.Request.Body, "request-secret") || strings.Contains(recorded.Response.Body, "response-secret") {
		t.Fatalf("request body = %q, response body = %q", recorded.Request.Body, recorded.Response.Body)
	}
}

func TestRequestSchemeUsesForwardedProtocol(t *testing.T) {
	request := httptest.NewRequest(stdlibhttp.MethodGet, "http://internal.test/players", nil)
	request.Header.Set("X-Forwarded-Proto", "https")
	if scheme := RequestScheme(request); scheme != "http" {
		t.Fatalf("URL scheme = %q, want http", scheme)
	}
	request.URL.Scheme = ""
	if scheme := RequestScheme(request); scheme != "https" {
		t.Fatalf("forwarded scheme = %q, want https", scheme)
	}
}

func TestMiddlewareExcludesRequestContext(t *testing.T) {
	mux := stdlibhttp.NewServeMux()
	profiler := webpprof.New(mux, webpprof.WithExcludedRequests("GET /health"))
	t.Cleanup(func() { _ = profiler.Close() })
	handler := MiddlewareWith(profiler, stdlibhttp.HandlerFunc(func(w stdlibhttp.ResponseWriter, r *stdlibhttp.Request) {
		webpprof.LogEventContext(r.Context(), webpprof.Event{Name: "health"})
		w.WriteHeader(stdlibhttp.StatusNoContent)
	}))
	handler.ServeHTTP(
		httptest.NewRecorder(),
		httptest.NewRequest(stdlibhttp.MethodGet, "/health", nil),
	)
	if entries := readEvents(t, mux, ""); len(entries) != 0 {
		t.Fatalf("events = %+v", entries)
	}
}

func TestMiddlewareCapturesPanicAsCorrelatedException(t *testing.T) {
	mux := stdlibhttp.NewServeMux()
	profiler := webpprof.New(mux)
	t.Cleanup(func() { _ = profiler.Close() })
	handler := MiddlewareWith(profiler, stdlibhttp.HandlerFunc(func(stdlibhttp.ResponseWriter, *stdlibhttp.Request) {
		panic("handler failed")
	}))

	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("middleware did not propagate panic")
			}
		}()
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(stdlibhttp.MethodGet, "/panic", nil))
	}()

	entries := readEvents(t, mux, "")
	if len(entries) != 2 || entries[0].Kind != webpprof.KindException || entries[1].Kind != webpprof.KindRequest {
		t.Fatalf("entries = %+v", entries)
	}
	if entries[0].RequestID == "" || entries[0].RequestID != entries[1].RequestID {
		t.Fatalf("exception request = %q, request = %q", entries[0].RequestID, entries[1].RequestID)
	}
	var exception webpprof.Exception
	if err := json.Unmarshal(entries[0].Data, &exception); err != nil {
		t.Fatal(err)
	}
	if exception.Type != "string" || exception.Message != "handler failed" || !strings.Contains(exception.Stack, "TestMiddlewareCapturesPanicAsCorrelatedException") {
		t.Fatalf("exception = %+v, current stack = %s", exception, debug.Stack())
	}
}

func TestProfileMiddlewareCapturesNamedInvocation(t *testing.T) {
	mux := stdlibhttp.NewServeMux()
	profiler := webpprof.New(mux)
	t.Cleanup(func() { _ = profiler.Close() })
	named := ProfileMiddlewareWith(profiler, "authentication", func(next stdlibhttp.Handler) stdlibhttp.Handler {
		return stdlibhttp.HandlerFunc(func(w stdlibhttp.ResponseWriter, r *stdlibhttp.Request) {
			time.Sleep(time.Microsecond)
			next.ServeHTTP(w, r)
		})
	})
	handler := MiddlewareWith(profiler, named(stdlibhttp.HandlerFunc(func(w stdlibhttp.ResponseWriter, _ *stdlibhttp.Request) {
		w.WriteHeader(stdlibhttp.StatusNoContent)
	})))
	request := httptest.NewRequest(stdlibhttp.MethodGet, "/profiled", nil)
	request = request.WithContext(webpprof.WithTags(request.Context(), map[string]string{"tenant": "acme"}))
	handler.ServeHTTP(httptest.NewRecorder(), request)

	entries := readEvents(t, mux, "")
	if len(entries) != 2 || entries[0].Kind != webpprof.KindMiddleware || entries[1].Kind != webpprof.KindRequest {
		t.Fatalf("entries = %+v", entries)
	}
	if entries[0].RequestID == "" || entries[0].RequestID != entries[1].RequestID || entries[0].Tags["tenant"] != "acme" {
		t.Fatalf("middleware = %+v, request = %+v", entries[0], entries[1])
	}
	var invocation webpprof.Middleware
	if err := json.Unmarshal(entries[0].Data, &invocation); err != nil {
		t.Fatal(err)
	}
	if invocation.Name != "authentication" || invocation.State != "completed" || invocation.Duration <= 0 {
		t.Fatalf("middleware invocation = %+v", invocation)
	}
}

func TestTransportDisabledProfilerUsesOriginalTransport(t *testing.T) {
	transport := stdlibhttp.DefaultTransport
	if ProfileTransportWith(nil, transport) != transport {
		t.Fatal("disabled profiler changed the transport")
	}
}

func readEvents(t *testing.T, mux stdlibhttp.Handler, requestID string) []webpprof.Entry {
	t.Helper()
	target := "/debug/webpprof/api/events?limit=20"
	if requestID != "" {
		target += "&request_id=" + requestID
	}
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequestWithContext(context.Background(), stdlibhttp.MethodGet, target, nil))
	var payload struct {
		Events []webpprof.Entry `json:"events"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	return payload.Events
}
