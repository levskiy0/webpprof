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
		FindingSQLDominatesRequest: "SQL consumed 73% of request",
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

func TestAnalyzeRequestCacheFindingsRequireEnoughRelatedEvidence(t *testing.T) {
	startedAt := time.Date(2026, time.August, 23, 5, 0, 0, 0, time.UTC)
	query := func(id string, offset time.Duration) Query {
		return Query{
			Meta: Meta{ID: id, StartedAt: startedAt.Add(offset), Duration: time.Millisecond},
			SQL:  "SELECT permission FROM player_permissions WHERE player_id = 42",
		}
	}
	tests := []struct {
		name         string
		cache        []Cache
		queries      []Query
		wantBurst    int
		wantMissRate int
	}{
		{
			name: "one miss and one loader query are normal cache-aside behavior",
			cache: []Cache{{
				Meta: Meta{ID: "miss", StartedAt: startedAt}, Operation: "get", Key: "player:42:permissions",
			}},
			queries: []Query{query("loader", 2*time.Millisecond)},
		},
		{
			name: "a miss populated by a write is not a miss-rate incident",
			cache: []Cache{
				{Meta: Meta{ID: "miss", StartedAt: startedAt}, Operation: "get", Key: "player:42:permissions"},
				{Meta: Meta{ID: "fill", StartedAt: startedAt.Add(4 * time.Millisecond)}, Operation: "put", Key: "player:42:permissions"},
			},
			queries: []Query{query("loader", 2*time.Millisecond)},
		},
		{
			name: "queries after the cache is populated are not assigned to the miss",
			cache: []Cache{
				{Meta: Meta{ID: "miss", StartedAt: startedAt}, Operation: "get", Key: "player:42:permissions"},
				{Meta: Meta{ID: "fill", StartedAt: startedAt.Add(4 * time.Millisecond)}, Operation: "put", Key: "player:42:permissions"},
			},
			queries: []Query{
				query("loader", 2*time.Millisecond),
				query("later-1", 6*time.Millisecond),
				query("later-2", 8*time.Millisecond),
				query("later-3", 10*time.Millisecond),
			},
		},
		{
			name: "write operations are not cache misses",
			cache: []Cache{
				{Meta: Meta{ID: "put", StartedAt: startedAt}, Operation: "put", Key: "one"},
				{Meta: Meta{ID: "set", StartedAt: startedAt.Add(time.Millisecond)}, Operation: "set", Key: "two"},
				{Meta: Meta{ID: "add", StartedAt: startedAt.Add(2 * time.Millisecond)}, Operation: "add", Key: "three"},
				{Meta: Meta{ID: "delete", StartedAt: startedAt.Add(3 * time.Millisecond)}, Operation: "delete", Key: "four"},
				{Meta: Meta{ID: "increment", StartedAt: startedAt.Add(4 * time.Millisecond)}, Operation: "increment", Key: "five"},
			},
		},
		{
			name: "two reads are too small a sample for miss rate",
			cache: []Cache{
				{Meta: Meta{ID: "miss", StartedAt: startedAt}, Operation: "get", Key: "one"},
				{Meta: Meta{ID: "hit", StartedAt: startedAt.Add(time.Millisecond)}, Operation: "get", Key: "two", Hit: true},
			},
		},
		{
			name: "failed reads are excluded from miss rate",
			cache: []Cache{
				{Meta: Meta{ID: "error-1", StartedAt: startedAt}, Operation: "get", Key: "one", Error: "timeout"},
				{Meta: Meta{ID: "error-2", StartedAt: startedAt.Add(time.Millisecond)}, Operation: "get", Key: "two", Error: "timeout"},
				{Meta: Meta{ID: "error-3", StartedAt: startedAt.Add(2 * time.Millisecond)}, Operation: "get", Key: "three", Error: "timeout"},
				{Meta: Meta{ID: "error-4", StartedAt: startedAt.Add(3 * time.Millisecond)}, Operation: "get", Key: "four", Error: "timeout"},
				{Meta: Meta{ID: "error-5", StartedAt: startedAt.Add(4 * time.Millisecond)}, Operation: "get", Key: "five", Error: "timeout"},
			},
		},
		{
			name: "a nearby repeated loader query is reported once",
			cache: []Cache{
				{Meta: Meta{ID: "older-miss", StartedAt: startedAt}, Operation: "get", Key: "player:42:permissions"},
				{Meta: Meta{ID: "nearest-miss", StartedAt: startedAt.Add(time.Millisecond)}, Operation: "get", Key: "player:42:permissions"},
			},
			queries: []Query{
				query("loader-1", 3*time.Millisecond),
				query("loader-2", 5*time.Millisecond),
				query("loader-3", 7*time.Millisecond),
			},
			wantBurst: 1,
		},
		{
			name: "distant identical queries are not attributed to an old miss",
			cache: []Cache{{
				Meta: Meta{ID: "miss", StartedAt: startedAt}, Operation: "get", Key: "player:42:permissions",
			}},
			queries: []Query{
				query("unrelated-1", time.Second),
				query("unrelated-2", time.Second+2*time.Millisecond),
				query("unrelated-3", time.Second+4*time.Millisecond),
			},
		},
		{
			name: "five cache reads are enough to calculate miss rate",
			cache: []Cache{
				{Meta: Meta{ID: "miss-1", StartedAt: startedAt}, Operation: "get", Key: "one"},
				{Meta: Meta{ID: "miss-2", StartedAt: startedAt.Add(time.Millisecond)}, Operation: "get", Key: "two"},
				{Meta: Meta{ID: "miss-3", StartedAt: startedAt.Add(2 * time.Millisecond)}, Operation: "get", Key: "three"},
				{Meta: Meta{ID: "hit-1", StartedAt: startedAt.Add(3 * time.Millisecond)}, Operation: "get", Key: "four", Hit: true},
				{Meta: Meta{ID: "hit-2", StartedAt: startedAt.Add(4 * time.Millisecond)}, Operation: "get", Key: "five", Hit: true},
			},
			wantMissRate: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			profiler := newProfiler()
			t.Cleanup(func() { _ = profiler.Close() })
			profiler.LogRequest(Request{
				Meta:    Meta{ID: "request", StartedAt: startedAt, Duration: 2 * time.Second},
				Cache:   test.cache,
				Queries: test.queries,
			})
			analysis, ok := profiler.AnalyzeRequest("request")
			if !ok {
				t.Fatal("AnalyzeRequest() did not find retained request")
			}
			if got := countFindings(analysis.Findings, FindingCacheMissQueryBurst); got != test.wantBurst {
				t.Errorf("cache-query findings = %d, want %d: %+v", got, test.wantBurst, analysis.Findings)
			}
			if got := countFindings(analysis.Findings, FindingHighCacheMissRate); got != test.wantMissRate {
				t.Errorf("cache miss-rate findings = %d, want %d: %+v", got, test.wantMissRate, analysis.Findings)
			}
		})
	}
}

func TestAnalyzeRequestDoesNotAttributeOriginWorkToRequest(t *testing.T) {
	profiler := newProfiler()
	t.Cleanup(func() { _ = profiler.Close() })
	startedAt := time.Date(2026, time.August, 23, 5, 30, 0, 0, time.UTC)
	profiler.LogRequest(Request{Meta: Meta{ID: "request", StartedAt: startedAt, Duration: 50 * time.Millisecond}})
	for index := range 3 {
		profiler.LogQuery(Query{
			Meta: Meta{
				ID:              fmt.Sprintf("async-query-%d", index),
				OriginRequestID: "request",
				StartedAt:       startedAt.Add(time.Second + time.Duration(index)*time.Millisecond),
				Duration:        40 * time.Millisecond,
			},
			SQL: fmt.Sprintf("SELECT id FROM jobs WHERE id = %d", index),
		})
	}

	analysis, ok := profiler.AnalyzeRequest("request")
	if !ok {
		t.Fatal("AnalyzeRequest() did not find retained request")
	}
	if len(analysis.Findings) != 0 {
		t.Fatalf("origin-only async work produced request findings: %+v", analysis.Findings)
	}
}

func TestAnalyzeRequestUsesWallClockSQLCoverage(t *testing.T) {
	profiler := newProfiler()
	t.Cleanup(func() { _ = profiler.Close() })
	startedAt := time.Date(2026, time.August, 23, 6, 0, 0, 0, time.UTC)
	profiler.LogRequest(Request{
		Meta: Meta{ID: "request", StartedAt: startedAt, Duration: 100 * time.Millisecond},
		Queries: []Query{
			{Meta: Meta{ID: "query-1", StartedAt: startedAt.Add(10 * time.Millisecond), Duration: 40 * time.Millisecond}, SQL: "SELECT 1"},
			{Meta: Meta{ID: "query-2", StartedAt: startedAt.Add(10 * time.Millisecond), Duration: 40 * time.Millisecond}, SQL: "SELECT 2"},
		},
	})

	analysis, ok := profiler.AnalyzeRequest("request")
	if !ok {
		t.Fatal("AnalyzeRequest() did not find retained request")
	}
	if got := countFindings(analysis.Findings, FindingSQLDominatesRequest); got != 0 {
		t.Fatalf("overlapping SQL was double-counted: %+v", analysis.Findings)
	}
}

func TestAnalyzeRequestDoesNotRecommendConcurrentUnsafeHTTPMethods(t *testing.T) {
	profiler := newProfiler()
	t.Cleanup(func() { _ = profiler.Close() })
	startedAt := time.Date(2026, time.August, 23, 6, 30, 0, 0, time.UTC)
	calls := make([]HTTPCall, 0, 3)
	for index := range 3 {
		calls = append(calls, HTTPCall{
			Meta:   Meta{ID: fmt.Sprintf("post-%d", index), StartedAt: startedAt.Add(time.Duration(index) * 20 * time.Millisecond), Duration: 10 * time.Millisecond},
			Method: http.MethodPost,
			URL:    "https://api.example.test/mutate",
			Status: http.StatusOK,
		})
	}
	profiler.LogRequest(Request{Meta: Meta{ID: "request", StartedAt: startedAt, Duration: 100 * time.Millisecond}, HTTPCalls: calls})

	analysis, ok := profiler.AnalyzeRequest("request")
	if !ok {
		t.Fatal("AnalyzeRequest() did not find retained request")
	}
	if got := countFindings(analysis.Findings, FindingSequentialHTTPCalls); got != 0 {
		t.Fatalf("unsafe HTTP methods were concurrency candidates: %+v", analysis.Findings)
	}
}

func countFindings(findings []Finding, code FindingCode) int {
	count := 0
	for _, finding := range findings {
		if finding.Code == code {
			count++
		}
	}
	return count
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
