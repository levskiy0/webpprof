package webpprof

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAnalyzeRequestProducesActionableFindings(t *testing.T) {
	profiler := newProfiler()
	t.Cleanup(func() { _ = profiler.Close() })
	startedAt := time.Date(2026, time.August, 23, 4, 30, 0, 0, time.UTC)
	rows := int64(1)
	queries := make([]Query, 0, 66)
	for index := range 47 {
		queries = append(queries, Query{
			Meta:         Meta{ID: fmt.Sprintf("n-plus-one-%02d", index), StartedAt: startedAt.Add(time.Duration(index) * 5 * time.Millisecond), Duration: 5 * time.Millisecond},
			SQL:          fmt.Sprintf("SELECT id FROM players WHERE id = %d", index+1),
			RowsAffected: &rows,
		})
	}
	queries = append(queries, Query{
		Meta: Meta{ID: "large-query", StartedAt: startedAt.Add(250 * time.Millisecond), Duration: 495 * time.Millisecond},
		SQL:  "SELECT * FROM audit_log ORDER BY created_at DESC",
	})
	for index := range 18 {
		queries = append(queries, Query{
			Meta: Meta{ID: fmt.Sprintf("cache-query-%02d", index), StartedAt: startedAt.Add(610*time.Millisecond + time.Duration(index)*5*time.Millisecond), Duration: 5 * time.Millisecond},
			SQL:  "SELECT permission FROM player_permissions WHERE player_id = 42",
		})
	}

	profiler.LogRequest(Request{
		Meta:    Meta{ID: "request-analysis", StartedAt: startedAt, Duration: time.Second},
		Method:  http.MethodGet,
		Path:    "/players",
		Status:  http.StatusOK,
		Queries: queries,
		Cache: []Cache{{
			Meta:      Meta{ID: "permissions-miss", StartedAt: startedAt.Add(600 * time.Millisecond), Duration: time.Millisecond},
			Operation: "get",
			Key:       "player:42:permissions",
			Hit:       false,
		}},
		HTTPCalls: []HTTPCall{
			{Meta: Meta{ID: "http-1", StartedAt: startedAt.Add(10 * time.Millisecond), Duration: 30 * time.Millisecond}, Method: http.MethodGet, URL: "https://assets.example.test/one", Status: http.StatusOK},
			{Meta: Meta{ID: "http-2", StartedAt: startedAt.Add(50 * time.Millisecond), Duration: 30 * time.Millisecond}, Method: http.MethodGet, URL: "https://assets.example.test/two", Status: http.StatusOK},
			{Meta: Meta{ID: "http-3", StartedAt: startedAt.Add(90 * time.Millisecond), Duration: 30 * time.Millisecond}, Method: http.MethodGet, URL: "https://assets.example.test/three", Status: http.StatusOK},
		},
		Middlewares: []Middleware{{
			Meta: Meta{ID: "middleware-auth", StartedAt: startedAt, Duration: 430 * time.Millisecond},
			Name: "auth",
		}},
	})

	analysis, ok := profiler.AnalyzeRequest("request-analysis")
	if !ok {
		t.Fatal("AnalyzeRequest() did not find retained request")
	}
	if analysis.RequestDurationNS != int64(time.Second) {
		t.Fatalf("request duration = %s, want 1s", time.Duration(analysis.RequestDurationNS))
	}
	if len(analysis.Findings) < 5 {
		payload, _ := json.MarshalIndent(analysis.Findings, "", "  ")
		t.Fatalf("findings = %d, want at least 5:\n%s", len(analysis.Findings), payload)
	}

	wantTitles := map[FindingCode]string{
		FindingPossibleNPlusOne:    "Possible N+1: query repeated 47 times",
		FindingSQLDominatesRequest: "SQL consumed 82% of request",
		FindingSequentialHTTPCalls: "3 sequential HTTP calls could run concurrently",
		FindingCacheMissQueryBurst: "Cache miss followed by 18 identical queries",
		FindingSlowMiddleware:      "Middleware auth took 430 ms",
	}
	for _, finding := range analysis.Findings {
		want, expected := wantTitles[finding.Code]
		if !expected {
			continue
		}
		if finding.Title != want {
			t.Errorf("finding %q title = %q, want %q", finding.Code, finding.Title, want)
		}
		if finding.EntryID == "" || len(finding.RelatedEntryIDs) == 0 {
			t.Errorf("finding %q does not link to supporting entries", finding.Code)
		}
		if finding.Suggestion == "" {
			t.Errorf("finding %q has no actionable suggestion", finding.Code)
		}
		delete(wantTitles, finding.Code)
	}
	if len(wantTitles) != 0 {
		t.Fatalf("missing findings: %v", wantTitles)
	}
}

func TestAnalyzeRequestAvoidsWeakSignals(t *testing.T) {
	profiler := newProfiler()
	t.Cleanup(func() { _ = profiler.Close() })
	startedAt := time.Now().UTC()
	profiler.LogRequest(Request{
		Meta: Meta{ID: "healthy-request", StartedAt: startedAt, Duration: 400 * time.Millisecond},
		Queries: []Query{
			{Meta: Meta{StartedAt: startedAt, Duration: 20 * time.Millisecond}, SQL: "SELECT id FROM players WHERE id = 1"},
			{Meta: Meta{StartedAt: startedAt.Add(30 * time.Millisecond), Duration: 20 * time.Millisecond}, SQL: "SELECT id FROM players WHERE id = 2"},
		},
		Cache: []Cache{{Meta: Meta{StartedAt: startedAt}, Key: "players", Hit: true}},
		HTTPCalls: []HTTPCall{
			{Meta: Meta{StartedAt: startedAt, Duration: 10 * time.Millisecond}, URL: "https://api.example.test/one", Status: http.StatusOK},
			{Meta: Meta{StartedAt: startedAt.Add(20 * time.Millisecond), Duration: 10 * time.Millisecond}, URL: "https://api.example.test/two", Status: http.StatusOK},
		},
		Middlewares: []Middleware{{Meta: Meta{StartedAt: startedAt, Duration: 99 * time.Millisecond}, Name: "auth"}},
	})

	analysis, ok := profiler.AnalyzeRequest("healthy-request")
	if !ok {
		t.Fatal("AnalyzeRequest() did not find retained request")
	}
	if len(analysis.Findings) != 0 {
		t.Fatalf("unexpected findings: %+v", analysis.Findings)
	}
}

func TestRequestAnalysisEndpoint(t *testing.T) {
	profiler := newProfiler()
	t.Cleanup(func() { _ = profiler.Close() })
	profiler.LogRequest(Request{Meta: Meta{ID: "request-endpoint", StartedAt: time.Now().UTC(), Duration: time.Second}})

	response := httptest.NewRecorder()
	profiler.handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/requests/request-endpoint/analysis", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("analysis status = %d, want %d", response.Code, http.StatusOK)
	}
	if !strings.Contains(response.Body.String(), `"request_id":"request-endpoint"`) {
		t.Fatalf("analysis response = %s", response.Body.String())
	}

	missing := httptest.NewRecorder()
	profiler.handler().ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/api/requests/missing/analysis", nil))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing analysis status = %d, want %d", missing.Code, http.StatusNotFound)
	}
}
