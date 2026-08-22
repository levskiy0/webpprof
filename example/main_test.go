package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/levskiy0/webpprof"
)

func TestDemoDiagnosticsScenarioRecordsEveryExample(t *testing.T) {
	mux := http.NewServeMux()
	profiler := webpprof.New(mux)
	t.Cleanup(func() { _ = profiler.Close() })
	app := &demoApp{profiler: profiler, client: demoClient{}, metrics: &demoMetrics{}}
	capture := profiler.BeginRequest(webpprof.Request{
		Meta:   webpprof.Meta{ID: "diagnostics-request", StartedAt: time.Now().UTC(), Tags: map[string]string{"scenario": "diagnostics"}},
		Method: http.MethodGet,
		Path:   "/demo",
	})

	request := httptest.NewRequest(http.MethodGet, "/demo?diagnostics=1", nil)
	request = request.WithContext(webpprof.WithRequest(request.Context(), capture))
	response := httptest.NewRecorder()
	app.demo(response, request)
	capture.Finish(webpprof.RequestResult{Status: response.Code})
	if response.Code != http.StatusOK {
		t.Fatalf("demo status = %d, want %d", response.Code, http.StatusOK)
	}

	entriesResponse := httptest.NewRecorder()
	mux.ServeHTTP(entriesResponse, httptest.NewRequest(http.MethodGet, "/debug/webpprof/api/events?limit=100", nil))
	if entriesResponse.Code != http.StatusOK {
		t.Fatalf("events status = %d, want %d", entriesResponse.Code, http.StatusOK)
	}
	var payload struct {
		Events []webpprof.Entry `json:"events"`
	}
	if err := json.Unmarshal(entriesResponse.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode events: %v", err)
	}

	duplicateQueries := 0
	cacheBurstQueries := 0
	sequentialCalls := 0
	found := map[string]bool{}
	for _, entry := range payload.Events {
		diagnostic := entry.Tags["diagnostic"]
		switch diagnostic {
		case "n-plus-one":
			duplicateQueries++
			if entry.Kind != webpprof.KindQuery {
				t.Fatalf("n-plus-one kind = %q, want query", entry.Kind)
			}
		case "sql-share":
			found[diagnostic] = entry.Kind == webpprof.KindQuery && entry.DurationNS == int64(575*time.Millisecond)
		case "slow-http":
			found[diagnostic] = entry.Kind == webpprof.KindHTTPCall && entry.DurationNS == int64(650*time.Millisecond)
		case "slow-middleware":
			var middleware webpprof.Middleware
			if err := json.Unmarshal(entry.Data, &middleware); err != nil {
				t.Fatalf("decode slow middleware: %v", err)
			}
			found[diagnostic] = entry.Kind == webpprof.KindMiddleware && entry.DurationNS == int64(430*time.Millisecond) && middleware.Name == "auth"
		case "failed-job":
			var job webpprof.Job
			if err := json.Unmarshal(entry.Data, &job); err != nil {
				t.Fatalf("decode failed job: %v", err)
			}
			found[diagnostic] = entry.Kind == webpprof.KindJob && job.State == "failed" && job.Error != ""
		case "cache-query-burst":
			found[diagnostic] = true
			if entry.Kind == webpprof.KindQuery {
				cacheBurstQueries++
			}
		case "sequential-http":
			if entry.Kind == webpprof.KindHTTPCall {
				sequentialCalls++
			}
		}
	}
	if duplicateQueries != 46 {
		t.Fatalf("duplicate diagnostic queries = %d, want 46", duplicateQueries)
	}
	if cacheBurstQueries != 18 {
		t.Fatalf("cache burst queries = %d, want 18", cacheBurstQueries)
	}
	if sequentialCalls != 3 {
		t.Fatalf("sequential HTTP calls = %d, want 3", sequentialCalls)
	}
	for _, diagnostic := range []string{"sql-share", "slow-http", "slow-middleware", "failed-job", "cache-query-burst"} {
		if !found[diagnostic] {
			t.Errorf("diagnostic example %q was not recorded correctly", diagnostic)
		}
	}

	analysis, ok := profiler.AnalyzeRequest("diagnostics-request")
	if !ok {
		t.Fatal("diagnostics request was not available for analysis")
	}
	findingCodes := make(map[webpprof.FindingCode]bool)
	for _, finding := range analysis.Findings {
		findingCodes[finding.Code] = true
	}
	for _, code := range []webpprof.FindingCode{
		webpprof.FindingPossibleNPlusOne,
		webpprof.FindingSQLDominatesRequest,
		webpprof.FindingSequentialHTTPCalls,
		webpprof.FindingCacheMissQueryBurst,
		webpprof.FindingSlowMiddleware,
	} {
		if !findingCodes[code] {
			t.Errorf("automatic finding %q was not produced", code)
		}
	}
}
