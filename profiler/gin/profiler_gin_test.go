package gin

import (
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
	profiler := webpprof.New(mux)
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
	profiler := webpprof.New(mux)
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
