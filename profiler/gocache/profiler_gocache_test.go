package gocache

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	gocache "github.com/levskiy0/go-cache"
	"github.com/levskiy0/webpprof"
)

func TestProfileCorrelatesContextOperations(t *testing.T) {
	mux := http.NewServeMux()
	profiler := webpprof.New(mux, webpprof.WithUnsafeUnauthenticatedAccess())
	t.Cleanup(func() { _ = profiler.Close() })
	cache, err := gocache.NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	profiled := ProfileWith(profiler, cache, "memory")
	capture := profiler.BeginRequest(webpprof.Request{Meta: webpprof.Meta{ID: "cache-request"}, Method: http.MethodGet, Path: "/cache"})
	ctx := webpprof.WithRequest(context.Background(), capture)
	if err := profiled.WithContext(ctx).Put("player:1", "Ada", time.Minute); err != nil {
		t.Fatal(err)
	}
	if value := profiled.WithContext(ctx).GetString("player:1"); value != "Ada" {
		t.Fatalf("value = %q", value)
	}
	capture.Finish(webpprof.RequestResult{Status: http.StatusOK})
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/debug/webpprof/api/events?request_id=cache-request&limit=10", nil))
	var response struct {
		Events []webpprof.Entry `json:"events"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Events) != 3 || response.Events[0].Kind != webpprof.KindCache || response.Events[1].Kind != webpprof.KindCache || response.Events[2].Kind != webpprof.KindRequest {
		t.Fatalf("events = %+v", response.Events)
	}
}
