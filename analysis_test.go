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

func TestAnalyzeScheduleProducesExecutionFindings(t *testing.T) {
	profiler := newProfiler()
	t.Cleanup(func() { _ = profiler.Close() })
	startedAt := time.Date(2026, time.August, 24, 10, 0, 0, 0, time.UTC)
	profiler.LogSchedule(Schedule{
		Meta:  Meta{ID: "schedule-analysis", StartedAt: startedAt, Duration: time.Second},
		Name:  "players.refresh",
		State: "failed",
		Error: "refresh failed",
	})
	for index := range 3 {
		profiler.LogQuery(Query{
			Meta: Meta{
				ID:        fmt.Sprintf("schedule-query-%d", index),
				ParentID:  "schedule-analysis",
				StartedAt: startedAt.Add(time.Duration(index) * 200 * time.Millisecond),
				Duration:  200 * time.Millisecond,
			},
			SQL: fmt.Sprintf("SELECT name FROM players WHERE id = %d", index+1),
		})
	}

	analysis, ok := profiler.AnalyzeSchedule("schedule-analysis")
	if !ok {
		t.Fatal("AnalyzeSchedule() did not find retained schedule")
	}
	if analysis.ScheduleID != "schedule-analysis" || analysis.ScheduleDurationNS != int64(time.Second) {
		t.Fatalf("schedule analysis metadata = %+v", analysis)
	}
	want := map[FindingCode]bool{
		FindingPossibleNPlusOne:     false,
		FindingSQLDominatesSchedule: false,
		FindingSlowSchedule:         false,
		FindingSlowQuery:            false,
		FindingFailedOperation:      false,
	}
	for _, finding := range analysis.Findings {
		if _, expected := want[finding.Code]; expected {
			want[finding.Code] = true
		}
	}
	for code, found := range want {
		if !found {
			t.Errorf("missing schedule finding %q: %+v", code, analysis.Findings)
		}
	}
	for _, finding := range analysis.Findings {
		if finding.Code == FindingSQLDominatesSchedule && finding.Title != "SQL consumed 60% of schedule" {
			t.Errorf("SQL schedule title = %q", finding.Title)
		}
	}
}

func TestAnalyzeScheduleRejectsOtherRoots(t *testing.T) {
	profiler := newProfiler()
	t.Cleanup(func() { _ = profiler.Close() })
	profiler.LogEvent(Event{Meta: Meta{ID: "not-schedule"}, Kind: "handler", Name: "refresh"})
	if _, ok := profiler.AnalyzeSchedule("not-schedule"); ok {
		t.Fatal("AnalyzeSchedule() accepted a non-schedule root")
	}
	if _, ok := profiler.AnalyzeSchedule("missing"); ok {
		t.Fatal("AnalyzeSchedule() accepted a missing root")
	}
}

func TestAnalyzeScheduleOnlyUsesParentIDDescendants(t *testing.T) {
	profiler := newProfiler()
	t.Cleanup(func() { _ = profiler.Close() })
	startedAt := time.Now().UTC()
	profiler.LogSchedule(Schedule{Meta: Meta{ID: "schedule", StartedAt: startedAt, Duration: 100 * time.Millisecond}, Name: "refresh", State: "succeeded"})
	profiler.LogQuery(Query{Meta: Meta{ID: "unrelated-slow-query", StartedAt: startedAt, Duration: time.Second}, SQL: "SELECT * FROM unrelated"})

	analysis, ok := profiler.AnalyzeSchedule("schedule")
	if !ok {
		t.Fatal("AnalyzeSchedule() did not find retained schedule")
	}
	if len(analysis.Findings) != 0 {
		t.Fatalf("unrelated work produced schedule findings: %+v", analysis.Findings)
	}
}

func TestAnalyzeCallableProducesExecutionFindings(t *testing.T) {
	profiler := newProfiler()
	t.Cleanup(func() { _ = profiler.Close() })
	startedAt := time.Date(2026, time.August, 24, 11, 0, 0, 0, time.UTC)
	profiler.LogCallable(Callable{
		Meta:  Meta{ID: "callable-analysis", StartedAt: startedAt, Duration: time.Second},
		Name:  "search.rebuild",
		State: "failed",
		Error: "rebuild failed",
	})
	for index := range 3 {
		profiler.LogQuery(Query{
			Meta: Meta{ID: fmt.Sprintf("callable-query-%d", index), ParentID: "callable-analysis", StartedAt: startedAt.Add(time.Duration(index) * 200 * time.Millisecond), Duration: 200 * time.Millisecond},
			SQL:  fmt.Sprintf("SELECT name FROM players WHERE id = %d", index+1),
		})
	}

	analysis, ok := profiler.AnalyzeCallable("callable-analysis")
	if !ok {
		t.Fatal("AnalyzeCallable() did not find retained callable")
	}
	if analysis.CallableID != "callable-analysis" || analysis.CallableDurationNS != int64(time.Second) {
		t.Fatalf("callable analysis metadata = %+v", analysis)
	}
	want := map[FindingCode]bool{
		FindingPossibleNPlusOne:     false,
		FindingSQLDominatesCallable: false,
		FindingSlowCallable:         false,
		FindingSlowQuery:            false,
		FindingFailedOperation:      false,
	}
	for _, finding := range analysis.Findings {
		if _, expected := want[finding.Code]; expected {
			want[finding.Code] = true
		}
	}
	for code, found := range want {
		if !found {
			t.Errorf("missing callable finding %q: %+v", code, analysis.Findings)
		}
	}
}

func TestAnalyzeTaskProducesExecutionFindings(t *testing.T) {
	profiler := newProfiler()
	t.Cleanup(func() { _ = profiler.Close() })
	startedAt := time.Date(2026, time.August, 24, 12, 0, 0, 0, time.UTC)
	profiler.LogTask(Task{Meta: Meta{ID: "task-analysis", StartedAt: startedAt, Duration: 2 * time.Second}, Name: "reports.generate", State: "failed", Error: "render failed"})
	for index := range 3 {
		profiler.LogQuery(Query{Meta: Meta{ID: fmt.Sprintf("task-query-%d", index), ParentID: "task-analysis", StartedAt: startedAt.Add(time.Duration(index) * 400 * time.Millisecond), Duration: 400 * time.Millisecond}, SQL: fmt.Sprintf("SELECT value FROM report_rows WHERE id = %d", index+1)})
	}

	analysis, ok := profiler.AnalyzeTask("task-analysis")
	if !ok || analysis.TaskID != "task-analysis" || analysis.TaskDurationNS != int64(2*time.Second) {
		t.Fatalf("task analysis = %+v, ok=%v", analysis, ok)
	}
	want := map[FindingCode]bool{FindingPossibleNPlusOne: false, FindingSQLDominatesTask: false, FindingSlowTask: false, FindingSlowQuery: false, FindingFailedOperation: false}
	for _, finding := range analysis.Findings {
		if _, expected := want[finding.Code]; expected {
			want[finding.Code] = true
		}
	}
	for code, found := range want {
		if !found {
			t.Errorf("missing task finding %q: %+v", code, analysis.Findings)
		}
	}
}

func TestAnalyzeTaskFindsMeasuredEventBottleneckAndPlanIssue(t *testing.T) {
	profiler := newProfiler()
	t.Cleanup(func() { _ = profiler.Close() })
	startedAt := time.Date(2026, time.August, 24, 13, 0, 0, 0, time.UTC)
	profiler.LogTask(Task{Meta: Meta{ID: "task-plan", StartedAt: startedAt, Duration: 1200 * time.Millisecond}, Name: "reports.generate", State: "succeeded"})
	profiler.LogEvent(Event{Meta: Meta{ID: "render", ParentID: "task-plan", StartedAt: startedAt.Add(50 * time.Millisecond), Duration: time.Second}, Kind: "step", Name: "reports.render"})
	profiler.LogQuery(Query{
		Meta:   Meta{ID: "slow-plan", ParentID: "render", StartedAt: startedAt.Add(100 * time.Millisecond), Duration: 150 * time.Millisecond},
		Driver: "sqlite",
		SQL:    "SELECT * FROM players ORDER BY created_at",
		Plan:   &QueryPlan{Text: "id=2 parent=0 detail=SCAN players"},
	})

	analysis, ok := profiler.AnalyzeTask("task-plan")
	if !ok {
		t.Fatal("AnalyzeTask() did not find retained task")
	}
	want := map[FindingCode]bool{
		FindingSlowEvent:           false,
		FindingExecutionBottleneck: false,
		FindingQueryPlanIssue:      false,
	}
	for _, finding := range analysis.Findings {
		if _, expected := want[finding.Code]; expected {
			want[finding.Code] = true
		}
		if finding.Code == FindingExecutionBottleneck && finding.EntryID != "render" {
			t.Errorf("bottleneck points to %q, want render", finding.EntryID)
		}
	}
	for code, found := range want {
		if !found {
			t.Errorf("missing finding %q: %+v", code, analysis.Findings)
		}
	}
}

func TestAnalyzeExecutionReportsFailedCoreOperations(t *testing.T) {
	profiler := newProfiler()
	t.Cleanup(func() { _ = profiler.Close() })
	startedAt := time.Now().UTC()
	profiler.LogRequest(Request{
		Meta:        Meta{ID: "failed-request", StartedAt: startedAt, Duration: 20 * time.Millisecond},
		Method:      http.MethodGet,
		Path:        "/players",
		Status:      http.StatusInternalServerError,
		Queries:     []Query{{Meta: Meta{ID: "failed-query", StartedAt: startedAt}, SQL: "SELECT 1", Error: "database closed"}},
		Cache:       []Cache{{Meta: Meta{ID: "failed-cache", StartedAt: startedAt}, Operation: "get", Key: "players", Error: "timeout"}},
		Events:      []Event{{Meta: Meta{ID: "failed-event", StartedAt: startedAt}, Kind: "step", Name: "load", Status: "failed"}},
		Middlewares: []Middleware{{Meta: Meta{ID: "failed-middleware", StartedAt: startedAt}, Name: "auth", Error: "denied"}},
		Exceptions:  []Exception{{Meta: Meta{ID: "exception", StartedAt: startedAt}, Type: "panic", Message: "boom"}},
	})

	analysis, ok := profiler.AnalyzeRequest("failed-request")
	if !ok {
		t.Fatal("AnalyzeRequest() did not find retained request")
	}
	ids := make(map[string]bool)
	for _, finding := range analysis.Findings {
		if finding.Code == FindingFailedOperation {
			ids[finding.EntryID] = true
		}
	}
	for _, id := range []string{"failed-request", "failed-query", "failed-cache", "failed-event", "failed-middleware", "exception"} {
		if !ids[id] {
			t.Errorf("missing failed-operation finding for %s: %+v", id, analysis.Findings)
		}
	}
}

func TestNPlusOneRequiresSameReadLocality(t *testing.T) {
	queries := []analyzedQuery{
		{entry: Entry{ID: "select-1", ParentID: "load-a"}, query: Query{SQL: "SELECT id FROM players WHERE id = 1"}, fingerprint: queryFingerprint("SELECT id FROM players WHERE id = 1")},
		{entry: Entry{ID: "select-2", ParentID: "load-a"}, query: Query{SQL: "SELECT id FROM players WHERE id = 2"}, fingerprint: queryFingerprint("SELECT id FROM players WHERE id = 2")},
		{entry: Entry{ID: "select-3", ParentID: "load-b"}, query: Query{SQL: "SELECT id FROM players WHERE id = 3"}, fingerprint: queryFingerprint("SELECT id FROM players WHERE id = 3")},
		{entry: Entry{ID: "update-1", ParentID: "load-a"}, query: Query{SQL: "UPDATE players SET active = 1 WHERE id = 1"}, fingerprint: queryFingerprint("UPDATE players SET active = 1 WHERE id = 1")},
		{entry: Entry{ID: "update-2", ParentID: "load-a"}, query: Query{SQL: "UPDATE players SET active = 1 WHERE id = 2"}, fingerprint: queryFingerprint("UPDATE players SET active = 1 WHERE id = 2")},
		{entry: Entry{ID: "update-3", ParentID: "load-a"}, query: Query{SQL: "UPDATE players SET active = 1 WHERE id = 3"}, fingerprint: queryFingerprint("UPDATE players SET active = 1 WHERE id = 3")},
	}
	if findings := nPlusOneFindings(queries, nil); len(findings) != 0 {
		t.Fatalf("unrelated branches or writes produced N+1 findings: %+v", findings)
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
	profiler := newProfiler(WithUnsafeUnauthenticatedAccess())
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

func TestScheduleAnalysisEndpoint(t *testing.T) {
	profiler := newProfiler(WithUnsafeUnauthenticatedAccess())
	t.Cleanup(func() { _ = profiler.Close() })
	profiler.LogSchedule(Schedule{Meta: Meta{ID: "schedule-endpoint", StartedAt: time.Now().UTC(), Duration: time.Second}, Name: "refresh", State: "succeeded"})

	response := httptest.NewRecorder()
	profiler.handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/schedules/schedule-endpoint/analysis", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("analysis status = %d, want %d", response.Code, http.StatusOK)
	}
	if !strings.Contains(response.Body.String(), `"schedule_id":"schedule-endpoint"`) || !strings.Contains(response.Body.String(), `"slow_schedule"`) {
		t.Fatalf("analysis response = %s", response.Body.String())
	}

	missing := httptest.NewRecorder()
	profiler.handler().ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/api/schedules/missing/analysis", nil))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing analysis status = %d, want %d", missing.Code, http.StatusNotFound)
	}
}

func TestCallableAnalysisEndpoint(t *testing.T) {
	profiler := newProfiler(WithUnsafeUnauthenticatedAccess())
	t.Cleanup(func() { _ = profiler.Close() })
	profiler.LogCallable(Callable{Meta: Meta{ID: "callable-endpoint", StartedAt: time.Now().UTC(), Duration: time.Second}, Name: "rebuild", State: "succeeded"})

	response := httptest.NewRecorder()
	profiler.handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/callables/callable-endpoint/analysis", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("analysis status = %d, want %d", response.Code, http.StatusOK)
	}
	if !strings.Contains(response.Body.String(), `"callable_id":"callable-endpoint"`) || !strings.Contains(response.Body.String(), `"slow_callable"`) {
		t.Fatalf("analysis response = %s", response.Body.String())
	}

	missing := httptest.NewRecorder()
	profiler.handler().ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/api/callables/missing/analysis", nil))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing analysis status = %d, want %d", missing.Code, http.StatusNotFound)
	}
}

func TestTaskAnalysisEndpoint(t *testing.T) {
	mux := http.NewServeMux()
	profiler := New(mux, WithUnsafeUnauthenticatedAccess())
	t.Cleanup(func() { _ = profiler.Close() })
	profiler.LogTask(Task{Meta: Meta{ID: "task-endpoint", StartedAt: time.Now().UTC(), Duration: 2 * time.Second}, Name: "report", State: "succeeded"})
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/debug/webpprof/api/tasks/task-endpoint/analysis", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"task_id":"task-endpoint"`) || !strings.Contains(response.Body.String(), `"slow_task"`) {
		t.Fatalf("task analysis response = %d %s", response.Code, response.Body.String())
	}
}
