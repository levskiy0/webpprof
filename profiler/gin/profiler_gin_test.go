package gin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/levskiy0/webpprof"
)

func TestMiddlewareCapturesPanicAsCorrelatedException(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mux := http.NewServeMux()
	profiler := webpprof.New(mux, webpprof.WithUnsafeUnauthenticatedAccess())
	t.Cleanup(func() { _ = profiler.Close() })
	router := gin.New()
	router.Use(MiddlewareWith(profiler))
	router.GET("/panic", func(*gin.Context) { panic("gin handler failed") })

	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("middleware did not propagate panic")
			}
		}()
		router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/panic", nil))
	}()

	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/debug/webpprof/api/events?limit=10", nil))
	var payload struct {
		Events []webpprof.Entry `json:"events"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Events) != 2 || payload.Events[0].Kind != webpprof.KindException || payload.Events[1].Kind != webpprof.KindRequest {
		t.Fatalf("events = %+v", payload.Events)
	}
	if payload.Events[0].RequestID == "" || payload.Events[0].RequestID != payload.Events[1].RequestID {
		t.Fatalf("exception request = %q, request = %q", payload.Events[0].RequestID, payload.Events[1].RequestID)
	}
	var exception webpprof.Exception
	if err := json.Unmarshal(payload.Events[0].Data, &exception); err != nil {
		t.Fatal(err)
	}
	if exception.Message != "gin handler failed" || !strings.Contains(exception.Stack, "TestMiddlewareCapturesPanicAsCorrelatedException") {
		t.Fatalf("exception = %+v", exception)
	}
}

func TestProfileMiddlewareCapturesNamedInvocation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mux := http.NewServeMux()
	profiler := webpprof.New(mux, webpprof.WithUnsafeUnauthenticatedAccess())
	t.Cleanup(func() { _ = profiler.Close() })
	router := gin.New()
	router.Use(MiddlewareWith(profiler))
	router.Use(ProfileMiddlewareWith(profiler, "authentication", func(c *gin.Context) { c.Next() }))
	router.GET("/profiled", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	request := httptest.NewRequest(http.MethodGet, "/profiled", nil)
	request = request.WithContext(webpprof.WithTags(request.Context(), map[string]string{"tenant": "acme"}))
	router.ServeHTTP(httptest.NewRecorder(), request)

	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/debug/webpprof/api/events?limit=10", nil))
	var payload struct {
		Events []webpprof.Entry `json:"events"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Events) != 2 || payload.Events[0].Kind != webpprof.KindMiddleware || payload.Events[1].Kind != webpprof.KindRequest {
		t.Fatalf("events = %+v", payload.Events)
	}
	if payload.Events[0].RequestID != payload.Events[1].RequestID || payload.Events[0].Tags["tenant"] != "acme" {
		t.Fatalf("middleware = %+v, request = %+v", payload.Events[0], payload.Events[1])
	}
}

func TestProfileMiddlewareBuildsParentTreeAndRestoresOuterContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mux := http.NewServeMux()
	profiler := webpprof.New(mux, webpprof.WithUnsafeUnauthenticatedAccess())
	t.Cleanup(func() { _ = profiler.Close() })
	type applicationContextKey struct{}

	router := gin.New()
	router.Use(MiddlewareWith(profiler))
	router.Use(ProfileMiddlewareWith(profiler, "outer", func(c *gin.Context) {
		profiler.LogQueryContext(c.Request.Context(), webpprof.Query{Meta: webpprof.Meta{ID: "outer-before"}, SQL: "SELECT 'outer-before'"})
		c.Next()
		if got := c.Request.Context().Value(applicationContextKey{}); got != "preserved" {
			t.Fatalf("application context after c.Next() = %v, want preserved", got)
		}
		profiler.LogQueryContext(c.Request.Context(), webpprof.Query{Meta: webpprof.Meta{ID: "outer-after"}, SQL: "SELECT 'outer-after'"})
	}))
	router.Use(ProfileMiddlewareWith(profiler, "inner", func(c *gin.Context) {
		ctx := context.WithValue(c.Request.Context(), applicationContextKey{}, "preserved")
		c.Request = c.Request.WithContext(ctx)
		profiler.LogQueryContext(c.Request.Context(), webpprof.Query{Meta: webpprof.Meta{ID: "inner-query"}, SQL: "SELECT 'inner'"})
		c.Next()
	}))
	router.GET("/profiled", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/profiled", nil))

	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/debug/webpprof/api/events?limit=10", nil))
	var payload struct {
		Events []webpprof.Entry `json:"events"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	entries := make(map[string]webpprof.Entry, len(payload.Events))
	middlewareIDs := make(map[string]string)
	for _, entry := range payload.Events {
		entries[entry.ID] = entry
		if entry.Kind != webpprof.KindMiddleware {
			continue
		}
		var invocation webpprof.Middleware
		if err := json.Unmarshal(entry.Data, &invocation); err != nil {
			t.Fatal(err)
		}
		middlewareIDs[invocation.Name] = entry.ID
	}
	outerID, innerID := middlewareIDs["outer"], middlewareIDs["inner"]
	if outerID == "" || innerID == "" {
		t.Fatalf("missing middleware entries: %+v", middlewareIDs)
	}
	if got := entries[innerID].ParentID; got != outerID {
		t.Fatalf("inner parent = %q, want outer %q", got, outerID)
	}
	for _, queryID := range []string{"outer-before", "outer-after"} {
		if got := entries[queryID].ParentID; got != outerID {
			t.Errorf("%s parent = %q, want outer %q", queryID, got, outerID)
		}
	}
	if got := entries["inner-query"].ParentID; got != innerID {
		t.Errorf("inner query parent = %q, want inner %q", got, innerID)
	}
}
