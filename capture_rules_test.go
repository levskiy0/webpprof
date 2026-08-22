package webpprof

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRequestRetentionRules(t *testing.T) {
	tests := []struct {
		name    string
		options []Option
		request Request
		want    bool
	}{
		{name: "exact status matches", options: []Option{WithHTTPStatusCodes(http.StatusOK, http.StatusInternalServerError)}, request: Request{Status: http.StatusInternalServerError}, want: true},
		{name: "exact status rejects", options: []Option{WithHTTPStatusCodes(http.StatusOK)}, request: Request{Status: http.StatusNotFound}, want: false},
		{name: "minimum status matches", options: []Option{WithHTTPStatusAtLeast(500)}, request: Request{Status: http.StatusBadGateway}, want: true},
		{name: "minimum duration matches boundary", options: []Option{WithMinRequestDuration(500 * time.Millisecond)}, request: Request{Meta: Meta{Duration: 500 * time.Millisecond}}, want: true},
		{name: "minimum duration rejects", options: []Option{WithMinRequestDuration(500 * time.Millisecond)}, request: Request{Meta: Meta{Duration: 499 * time.Millisecond}}, want: false},
		{name: "tags match", options: []Option{WithRequestTags(map[string]string{"tenant": "acme", "env": "dev"})}, request: Request{Meta: Meta{Tags: map[string]string{"tenant": "acme", "env": "dev", "feature": "billing"}}}, want: true},
		{name: "tags reject", options: []Option{WithRequestTags(map[string]string{"tenant": "acme"})}, request: Request{Meta: Meta{Tags: map[string]string{"tenant": "umbrella"}}}, want: false},
		{name: "custom rule", options: []Option{WithRequestRetentionFilter(func(request Request) bool { return request.Path == "/checkout" })}, request: Request{Path: "/checkout"}, want: true},
		{name: "rules combine with and", options: []Option{WithHTTPStatusAtLeast(500), WithRequestTags(map[string]string{"tenant": "acme"})}, request: Request{Meta: Meta{Tags: map[string]string{"tenant": "umbrella"}}, Status: http.StatusInternalServerError}, want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			profiler := newProfiler(test.options...)
			t.Cleanup(func() { _ = profiler.Close() })
			test.request.ID = "request"
			profiler.LogRequest(test.request)
			_, recorded := profiler.store.get("request")
			if recorded != test.want {
				t.Fatalf("request recorded = %v, want %v", recorded, test.want)
			}
		})
	}
}

func TestRejectedRequestDropsBufferedRelatedEntities(t *testing.T) {
	profiler := newProfiler(WithHTTPStatusAtLeast(500))
	t.Cleanup(func() { _ = profiler.Close() })
	capture := profiler.BeginRequest(Request{Meta: Meta{ID: "rejected-request"}, Method: http.MethodGet, Path: "/players"})
	capture.LogQuery(Query{Meta: Meta{ID: "rejected-query"}, SQL: "SELECT 1"})
	capture.Finish(RequestResult{Status: http.StatusOK})

	if entries := profiler.store.list("", "", nil, 0, 10); len(entries) != 0 {
		t.Fatalf("recorded entries = %+v", entries)
	}
}

func TestNextRequestsIsConcurrencySafe(t *testing.T) {
	const limit = 20
	profiler := newProfiler(WithNextRequests(limit))
	t.Cleanup(func() { _ = profiler.Close() })
	request := httptest.NewRequest(http.MethodGet, "/players", nil)
	var captured atomic.Int64
	var wait sync.WaitGroup
	for range 100 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if profiler.ShouldCaptureRequest(request) {
				captured.Add(1)
			}
		}()
	}
	wait.Wait()
	if got := captured.Load(); got != limit {
		t.Fatalf("captured = %d, want %d", got, limit)
	}
}

func TestBrowserSessionMatchesHeaderOrCookie(t *testing.T) {
	profiler := newProfiler(WithBrowserSession("developer-a"))
	t.Cleanup(func() { _ = profiler.Close() })

	headerRequest := httptest.NewRequest(http.MethodGet, "/players", nil)
	headerRequest.Header.Set(CaptureSessionHeader, "developer-a")
	cookieRequest := httptest.NewRequest(http.MethodGet, "/players", nil)
	cookieRequest.AddCookie(&http.Cookie{Name: CaptureSessionCookie, Value: "developer-a"})
	otherRequest := httptest.NewRequest(http.MethodGet, "/players", nil)
	otherRequest.Header.Set(CaptureSessionHeader, "developer-b")

	if !profiler.ShouldCaptureRequest(headerRequest) {
		t.Fatal("matching header session was rejected")
	}
	if !profiler.ShouldCaptureRequest(cookieRequest) {
		t.Fatal("matching cookie session was rejected")
	}
	if profiler.ShouldCaptureRequest(otherRequest) {
		t.Fatal("different browser session was captured")
	}
}
