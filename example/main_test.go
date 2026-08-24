package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/levskiy0/webpprof"
	webpprofcallable "github.com/levskiy0/webpprof/profiler/callable"
	webpprofhttp "github.com/levskiy0/webpprof/profiler/http"
	webpprofschedule "github.com/levskiy0/webpprof/profiler/schedule"
	webpprofslog "github.com/levskiy0/webpprof/profiler/slog"
	webpprofsql "github.com/levskiy0/webpprof/profiler/sql"
	modernsqlite "modernc.org/sqlite"
)

func TestHomePageUsesAjaxActionsAndInlineResponse(t *testing.T) {
	response := httptest.NewRecorder()
	(&application{}).home(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("home status = %d, want %d", response.Code, http.StatusOK)
	}
	body := response.Body.String()
	for _, expected := range []string{
		`data-request="/api/players/42?tenant=acme"`,
		`data-method="POST"`,
		`data-request="/api/schedules/refresh-players?tenant=umbrella"`,
		`data-request="/api/callables/rebuild-player-index?tenant=acme"`,
		`data-request="/api/tasks/generate-player-report?tenant=umbrella"`,
		`data-request="/api/manual/custom-profiler?tenant=acme"`,
		`<code id="response" aria-live="polite">`,
		`await fetch(target`,
		`output.textContent`,
	} {
		if !strings.Contains(body, expected) {
			t.Errorf("home page does not contain %q", expected)
		}
	}
	if strings.Contains(body, `href="/api/`) {
		t.Fatal("API action still navigates away instead of using AJAX")
	}
}

func TestPlayerAPIRecordsRealSQLiteQueriesAndSlog(t *testing.T) {
	app, profiler, profilerMux := newTestApplication(t)
	capture := profiler.BeginRequest(webpprof.Request{
		Meta:   webpprof.Meta{ID: "player-request", StartedAt: time.Now().UTC()},
		Method: http.MethodGet,
		Path:   "/api/players/42",
	})
	request := httptest.NewRequest(http.MethodGet, "/api/players/42", nil)
	request = request.WithContext(webpprof.WithRequest(request.Context(), capture))
	response := httptest.NewRecorder()
	app.routes().ServeHTTP(response, request)
	capture.Finish(webpprof.RequestResult{Status: response.Code})
	if response.Code != http.StatusOK {
		t.Fatalf("player status = %d, body = %s", response.Code, response.Body.String())
	}
	var payload struct {
		Player player `json:"player"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode player response: %v", err)
	}
	if payload.Player.ID != 42 || payload.Player.Name != "Ada Lovelace" {
		t.Fatalf("player response = %+v", payload)
	}

	entries := requestEntries(t, profilerMux, "player-request")
	foundQuery := false
	foundLog := false
	handlerEventID := ""
	queryParentID := ""
	logParentID := ""
	for _, entry := range entries {
		switch entry.Kind {
		case webpprof.KindQuery:
			var query webpprof.Query
			if err := json.Unmarshal(entry.Data, &query); err != nil {
				t.Fatalf("decode query: %v", err)
			}
			isPlayerQuery := query.Driver == "sqlite" && query.Database == "example.db"
			foundQuery = isPlayerQuery && strings.Contains(query.SQL, "FROM players") && query.Plan != nil
			if foundQuery {
				queryParentID = entry.ParentID
			}
		case webpprof.KindLog:
			var record webpprof.Log
			if err := json.Unmarshal(entry.Data, &record); err != nil {
				t.Fatalf("decode log: %v", err)
			}
			foundLog = foundLog || record.Message == "player loaded"
			if record.Message == "player loaded" {
				logParentID = entry.ParentID
			}
		case webpprof.KindEvent:
			var event webpprof.Event
			if err := json.Unmarshal(entry.Data, &event); err != nil {
				t.Fatalf("decode handler event: %v", err)
			}
			if event.Kind == "handler" && event.Name == "players.get" && event.Status == "succeeded" {
				handlerEventID = entry.ID
			}
		}
	}
	if !foundQuery || !foundLog || handlerEventID == "" {
		t.Fatalf("related entries: query=%v log=%v handler=%q", foundQuery, foundLog, handlerEventID)
	}
	if queryParentID != handlerEventID || logParentID != handlerEventID {
		t.Fatalf("nested parents: query=%q log=%q handler=%q", queryParentID, logParentID, handlerEventID)
	}
}

func TestProfilerWiringCapturesAutomaticAndMeasuredEntries(t *testing.T) {
	app, profiler, profilerMux := newTestApplication(t)
	handler := webpprofhttp.ProfileMiddlewareWith(profiler, "security-headers", securityHeaders)(
		webpprofhttp.ProfileMiddlewareWith(profiler, "request-log", requestLog(app.logger))(app.routes()),
	)
	handler = requestTags(handler)
	handler = webpprofhttp.MiddlewareWith(profiler, handler)

	request := httptest.NewRequest(http.MethodGet, "/api/players/42?tenant=umbrella", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("player status = %d, body = %s", response.Code, response.Body.String())
	}

	requestID := ""
	for _, entry := range allEntries(t, profilerMux) {
		if entry.Kind != webpprof.KindRequest {
			continue
		}
		var recorded webpprof.Request
		if err := json.Unmarshal(entry.Data, &recorded); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if recorded.Path == "/api/players/42" {
			requestID = entry.RequestID
			if requestID == "" {
				requestID = entry.ID
			}
			if entry.Tags["tenant"] != "umbrella" {
				t.Fatalf("request tags = %v", entry.Tags)
			}
			break
		}
	}
	if requestID == "" {
		t.Fatal("automatic HTTP Request entry was not recorded")
	}

	foundQuery := false
	foundLog := false
	foundHandler := false
	middleware := map[string]bool{}
	for _, entry := range requestEntries(t, profilerMux, requestID) {
		switch entry.Kind {
		case webpprof.KindQuery:
			foundQuery = true
		case webpprof.KindLog:
			foundLog = true
		case webpprof.KindMiddleware:
			var recorded webpprof.Middleware
			if err := json.Unmarshal(entry.Data, &recorded); err != nil {
				t.Fatalf("decode middleware: %v", err)
			}
			middleware[recorded.Name] = true
		case webpprof.KindEvent:
			var event webpprof.Event
			if err := json.Unmarshal(entry.Data, &event); err != nil {
				t.Fatalf("decode handler event: %v", err)
			}
			foundHandler = foundHandler || event.Kind == "handler" && event.Name == "players.get"
		}
	}
	if !foundQuery || !foundLog || !foundHandler || !middleware["security-headers"] || !middleware["request-log"] {
		t.Fatalf(
			"automatic entities: query=%v log=%v handler=%v middleware=%v",
			foundQuery,
			foundLog,
			foundHandler,
			middleware,
		)
	}
}

func TestCustomProfilerIsIsolatedToManualRoute(t *testing.T) {
	app, profiler, profilerMux := newTestApplication(t)
	capture := profiler.BeginRequest(webpprof.Request{
		Meta:   webpprof.Meta{ID: "custom-profiler-request", StartedAt: time.Now().UTC()},
		Method: http.MethodGet,
		Path:   "/api/manual/custom-profiler",
	})
	request := httptest.NewRequest(http.MethodGet, "/api/manual/custom-profiler", nil)
	request = request.WithContext(webpprof.WithRequest(request.Context(), capture))
	response := httptest.NewRecorder()
	app.routes().ServeHTTP(response, request)
	capture.Finish(webpprof.RequestResult{Status: response.Code})
	if response.Code != http.StatusOK {
		t.Fatalf("custom profiler status = %d, body = %s", response.Code, response.Body.String())
	}

	handlerEventID := ""
	customParentID := ""
	for _, entry := range requestEntries(t, profilerMux, "custom-profiler-request") {
		if entry.Kind != webpprof.KindEvent {
			continue
		}
		var event webpprof.Event
		if err := json.Unmarshal(entry.Data, &event); err != nil {
			t.Fatalf("decode custom event: %v", err)
		}
		if event.Kind == "handler" && event.Name == "demo.custom-profiler" {
			handlerEventID = entry.ID
		}
		if event.Kind == "custom-client" && event.Name == "lookup" {
			customParentID = entry.ParentID
		}
	}
	if customParentID == "" {
		t.Fatal("manual custom-profiler route did not record its Event")
	}
	if customParentID != handlerEventID {
		t.Fatalf("custom profiler parent = %q, want handler %q", customParentID, handlerEventID)
	}
}

func TestIncrementViewsUsesTransaction(t *testing.T) {
	app, profiler, profilerMux := newTestApplication(t)
	capture := profiler.BeginRequest(webpprof.Request{
		Meta:   webpprof.Meta{ID: "increment-request", StartedAt: time.Now().UTC()},
		Method: http.MethodPost,
		Path:   "/api/players/42/views",
	})
	request := httptest.NewRequest(http.MethodPost, "/api/players/42/views", nil)
	request = request.WithContext(webpprof.WithRequest(request.Context(), capture))
	response := httptest.NewRecorder()
	app.routes().ServeHTTP(response, request)
	capture.Finish(webpprof.RequestResult{Status: response.Code})
	if response.Code != http.StatusOK {
		t.Fatalf("increment status = %d, body = %s", response.Code, response.Body.String())
	}
	var payload struct {
		Player player `json:"player"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode increment response: %v", err)
	}
	if payload.Player.Views != 8 {
		t.Fatalf("views = %d, want 8", payload.Player.Views)
	}

	foundUpdatePlan := false
	for _, entry := range requestEntries(t, profilerMux, "increment-request") {
		if entry.Kind != webpprof.KindQuery {
			continue
		}
		var query webpprof.Query
		if err := json.Unmarshal(entry.Data, &query); err != nil {
			t.Fatalf("decode increment query: %v", err)
		}
		if query.Operation == "UPDATE" {
			hasPlan := query.Plan != nil && query.Plan.Error == ""
			foundUpdatePlan = hasPlan && strings.Contains(query.Plan.Command, "EXPLAIN QUERY PLAN UPDATE")
		}
	}
	if !foundUpdatePlan {
		t.Fatal("real SQLite UPDATE did not capture an EXPLAIN QUERY PLAN")
	}
}

func TestScheduledRefreshCreatesStandaloneExecutionWithQueryAndLog(t *testing.T) {
	app, profiler, profilerMux := newTestApplication(t)
	capture := profiler.BeginRequest(webpprof.Request{
		Meta:   webpprof.Meta{ID: "schedule-example-request", StartedAt: time.Now().UTC()},
		Method: http.MethodPost,
		Path:   "/api/schedules/refresh-players",
	})
	ctx := webpprof.WithRequest(context.Background(), capture)
	ctx = webpprof.WithTags(ctx, map[string]string{"scenario": "schedule", "tenant": "umbrella"})
	request := httptest.NewRequest(http.MethodPost, "/api/schedules/refresh-players", nil).WithContext(ctx)
	response := httptest.NewRecorder()
	app.routes().ServeHTTP(response, request)
	capture.Finish(webpprof.RequestResult{Status: response.Code})
	if response.Code != http.StatusOK {
		t.Fatalf("schedule status = %d, body = %s", response.Code, response.Body.String())
	}

	scheduleID := ""
	for _, entry := range allEntries(t, profilerMux) {
		if entry.Kind == webpprof.KindSchedule {
			var schedule webpprof.Schedule
			if err := json.Unmarshal(entry.Data, &schedule); err != nil {
				t.Fatalf("decode schedule: %v", err)
			}
			if schedule.Name == "players.refresh-snapshot" && schedule.State == "succeeded" {
				scheduleID = entry.ID
				if entry.RequestID != "" || entry.ParentID != "" || entry.OriginRequestID != "" {
					t.Fatalf("schedule must be a standalone root: request=%q parent=%q origin=%q", entry.RequestID, entry.ParentID, entry.OriginRequestID)
				}
				if entry.Tags["scenario"] != "schedule" || entry.Tags["tenant"] != "umbrella" {
					t.Fatalf("schedule tags = %v", entry.Tags)
				}
			}
		}
	}
	if scheduleID == "" {
		t.Fatal("standalone schedule was not recorded")
	}

	queryParentID := ""
	logParentID := ""
	for _, entry := range scopeEntries(t, profilerMux, scheduleID) {
		switch entry.Kind {
		case webpprof.KindQuery:
			var query webpprof.Query
			if err := json.Unmarshal(entry.Data, &query); err != nil {
				t.Fatalf("decode scheduled query: %v", err)
			}
			if strings.Contains(query.SQL, "FROM players") {
				queryParentID = entry.ParentID
			}
		case webpprof.KindLog:
			var record webpprof.Log
			if err := json.Unmarshal(entry.Data, &record); err != nil {
				t.Fatalf("decode scheduled log: %v", err)
			}
			if record.Message == "scheduled player refresh completed" {
				logParentID = entry.ParentID
			}
		}
	}
	if queryParentID != scheduleID || logParentID != scheduleID {
		t.Fatalf("scheduled children: query=%q log=%q schedule=%q", queryParentID, logParentID, scheduleID)
	}
	analysis, ok := profiler.AnalyzeSchedule(scheduleID)
	if !ok || analysis.ScheduleID != scheduleID || analysis.GeneratedAt.IsZero() {
		t.Fatalf("schedule analysis = %+v, ok=%v", analysis, ok)
	}
	for _, entry := range requestEntries(t, profilerMux, "schedule-example-request") {
		if entry.ID == scheduleID || entry.ParentID == scheduleID {
			t.Fatalf("schedule execution leaked into request scope: %+v", entry)
		}
	}
}

func TestCallableCreatesStandaloneExecutionWithQueryLogAndAnalysis(t *testing.T) {
	app, profiler, profilerMux := newTestApplication(t)
	capture := profiler.BeginRequest(webpprof.Request{
		Meta:   webpprof.Meta{ID: "callable-example-request", StartedAt: time.Now().UTC()},
		Method: http.MethodPost,
		Path:   "/api/callables/rebuild-player-index",
	})
	ctx := webpprof.WithRequest(context.Background(), capture)
	ctx = webpprof.WithTags(ctx, map[string]string{"scenario": "callable", "tenant": "acme"})
	request := httptest.NewRequest(http.MethodPost, "/api/callables/rebuild-player-index", nil).WithContext(ctx)
	response := httptest.NewRecorder()
	app.routes().ServeHTTP(response, request)
	capture.Finish(webpprof.RequestResult{Status: response.Code})
	if response.Code != http.StatusOK {
		t.Fatalf("callable status = %d, body = %s", response.Code, response.Body.String())
	}

	callableID := ""
	for _, entry := range allEntries(t, profilerMux) {
		if entry.Kind != webpprof.KindCallable {
			continue
		}
		var callable webpprof.Callable
		if err := json.Unmarshal(entry.Data, &callable); err != nil {
			t.Fatalf("decode callable: %v", err)
		}
		if callable.Name != "players.rebuild-search-index" || callable.State != "succeeded" {
			continue
		}
		callableID = entry.ID
		if entry.RequestID != "" || entry.ParentID != "" || entry.OriginRequestID != "" {
			t.Fatalf("callable must be a standalone root: request=%q parent=%q origin=%q", entry.RequestID, entry.ParentID, entry.OriginRequestID)
		}
		if entry.Tags["scenario"] != "callable" || entry.Tags["tenant"] != "acme" {
			t.Fatalf("callable tags = %v", entry.Tags)
		}
	}
	if callableID == "" {
		t.Fatal("standalone callable was not recorded")
	}

	queryFound := false
	logFound := false
	for _, entry := range scopeEntries(t, profilerMux, callableID) {
		if entry.ParentID != callableID {
			continue
		}
		switch entry.Kind {
		case webpprof.KindQuery:
			var query webpprof.Query
			if json.Unmarshal(entry.Data, &query) == nil && strings.Contains(query.SQL, "FROM players") {
				queryFound = true
			}
		case webpprof.KindLog:
			var record webpprof.Log
			if json.Unmarshal(entry.Data, &record) == nil && record.Message == "player search index rebuilt" {
				logFound = true
			}
		}
	}
	if !queryFound || !logFound {
		t.Fatalf("callable descendants: query=%t log=%t", queryFound, logFound)
	}
	analysis, ok := profiler.AnalyzeCallable(callableID)
	if !ok || analysis.CallableID != callableID || analysis.GeneratedAt.IsZero() {
		t.Fatalf("callable analysis = %+v, ok=%v", analysis, ok)
	}
	for _, entry := range requestEntries(t, profilerMux, "callable-example-request") {
		if entry.ID == callableID || entry.ParentID == callableID {
			t.Fatalf("callable execution leaked into request scope: %+v", entry)
		}
	}
}

func TestReportTaskCreatesStandaloneExecutionWithQueryLogAndAnalysis(t *testing.T) {
	app, profiler, profilerMux := newTestApplication(t)
	capture := profiler.BeginRequest(webpprof.Request{Meta: webpprof.Meta{ID: "task-example-request", StartedAt: time.Now().UTC()}, Method: http.MethodPost, Path: "/api/tasks/generate-player-report"})
	ctx := webpprof.WithRequest(context.Background(), capture)
	ctx = webpprof.WithTags(ctx, map[string]string{"scenario": "task", "tenant": "umbrella"})
	response := httptest.NewRecorder()
	app.routes().ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/tasks/generate-player-report", nil).WithContext(ctx))
	capture.Finish(webpprof.RequestResult{Status: response.Code})
	if response.Code != http.StatusOK {
		t.Fatalf("task status = %d, body = %s", response.Code, response.Body.String())
	}

	taskID := ""
	for _, entry := range allEntries(t, profilerMux) {
		if entry.Kind != webpprof.KindTask {
			continue
		}
		var task webpprof.Task
		if json.Unmarshal(entry.Data, &task) == nil && task.Name == "reports.players.generate" {
			taskID = entry.ID
			if entry.RequestID != "" || entry.ParentID != "" || entry.Tags["scenario"] != "task" {
				t.Fatalf("task root = %+v", entry)
			}
		}
	}
	if taskID == "" {
		t.Fatal("standalone report task was not recorded")
	}
	queryFound, logFound := false, false
	for _, entry := range scopeEntries(t, profilerMux, taskID) {
		if entry.ParentID != taskID {
			continue
		}
		queryFound = queryFound || entry.Kind == webpprof.KindQuery
		logFound = logFound || entry.Kind == webpprof.KindLog
	}
	analysis, ok := profiler.AnalyzeTask(taskID)
	if !queryFound || !logFound || !ok || analysis.TaskID != taskID {
		t.Fatalf("task execution: query=%t log=%t analysis=%+v ok=%v", queryFound, logFound, analysis, ok)
	}
}

func TestDatabaseFailureReturnsSafeError(t *testing.T) {
	app, _, profilerMux := newTestApplication(t)
	request := httptest.NewRequest(http.MethodGet, "/api/failure", nil)
	response := httptest.NewRecorder()
	app.routes().ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("failure status = %d, want %d", response.Code, http.StatusInternalServerError)
	}
	containsInternalName := strings.Contains(response.Body.String(), "missing_players")
	containsSafeMessage := strings.Contains(response.Body.String(), "internal server error")
	if containsInternalName || !containsSafeMessage {
		t.Fatalf("unsafe failure response = %s", response.Body.String())
	}

	handlerEventID := ""
	failureLogParentID := ""
	for _, entry := range allEntries(t, profilerMux) {
		switch entry.Kind {
		case webpprof.KindEvent:
			var event webpprof.Event
			if err := json.Unmarshal(entry.Data, &event); err != nil {
				t.Fatalf("decode failure event: %v", err)
			}
			isFailedMeasurement := event.Kind == "handler" && event.Name == "players.failure"
			if isFailedMeasurement && event.Status == "failed" && event.Error != "" {
				handlerEventID = entry.ID
			}
		case webpprof.KindLog:
			var record webpprof.Log
			if err := json.Unmarshal(entry.Data, &record); err != nil {
				t.Fatalf("decode failure log: %v", err)
			}
			if record.Message == "request failed" {
				failureLogParentID = entry.ParentID
			}
		}
	}
	if handlerEventID == "" {
		t.Fatal("database failure did not record a failed handler measurement")
	}
	if failureLogParentID != handlerEventID {
		t.Fatalf("failure log parent = %q, want handler %q", failureLogParentID, handlerEventID)
	}
}

func TestPanicRouteRecordsPanickedHandlerEvent(t *testing.T) {
	app, profiler, profilerMux := newTestApplication(t)
	handler := webpprofhttp.ProfileMiddlewareWith(profiler, "security-headers", securityHeaders)(
		webpprofhttp.ProfileMiddlewareWith(profiler, "request-log", requestLog(app.logger))(app.routes()),
	)
	handler = requestTags(handler)
	handler = webpprofhttp.MiddlewareWith(profiler, handler)
	handler = recoverResponse(handler)

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/panic?tenant=umbrella", nil))
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("panic status = %d, want %d", response.Code, http.StatusInternalServerError)
	}

	foundPanickedEvent := false
	for _, entry := range allEntries(t, profilerMux) {
		if entry.Kind != webpprof.KindEvent {
			continue
		}
		var event webpprof.Event
		if err := json.Unmarshal(entry.Data, &event); err != nil {
			t.Fatalf("decode panic event: %v", err)
		}
		stack, _ := event.Fields["panic_stack"].(string)
		isPanicHandler := event.Kind == "handler" && event.Name == "demo.panic"
		foundPanickedEvent = isPanicHandler && event.Status == "panicked" && event.Error != "" && stack != ""
	}
	if !foundPanickedEvent {
		t.Fatal("panic route did not record a panicked handler Event with a stack")
	}
}

func TestDiagnosticsScenarioRecordsEveryFindingExample(t *testing.T) {
	app, profiler, profilerMux := newTestApplication(t)
	capture := profiler.BeginRequest(webpprof.Request{
		Meta: webpprof.Meta{
			ID:        "diagnostics-request",
			StartedAt: time.Now().UTC(),
			Tags:      map[string]string{"scenario": "diagnostics"},
		},
		Method: http.MethodGet,
		Path:   "/api/manual/diagnostics",
	})
	request := httptest.NewRequest(http.MethodGet, "/api/manual/diagnostics", nil)
	request = request.WithContext(webpprof.WithRequest(request.Context(), capture))
	response := httptest.NewRecorder()
	app.routes().ServeHTTP(response, request)
	capture.Finish(webpprof.RequestResult{Status: response.Code})
	if response.Code != http.StatusOK {
		t.Fatalf("diagnostics status = %d, want %d", response.Code, http.StatusOK)
	}

	entries := requestEntries(t, profilerMux, "diagnostics-request")
	duplicateQueries := 0
	cacheBurstQueries := 0
	sequentialCalls := 0
	found := map[string]bool{}
	for _, entry := range entries {
		diagnostic := entry.Tags["diagnostic"]
		switch diagnostic {
		case "n-plus-one":
			duplicateQueries++
		case "sql-share", "slow-http", "slow-middleware", "failed-job":
			found[diagnostic] = true
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
	if duplicateQueries != 47 || cacheBurstQueries != 18 || sequentialCalls != 3 {
		t.Fatalf("diagnostic counts: duplicate=%d cache=%d HTTP=%d", duplicateQueries, cacheBurstQueries, sequentialCalls)
	}
	for _, diagnostic := range []string{"sql-share", "slow-http", "slow-middleware", "failed-job", "cache-query-burst"} {
		if !found[diagnostic] {
			t.Errorf("diagnostic example %q was not recorded", diagnostic)
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

func newTestApplication(t *testing.T) (*application, *webpprof.Profiler, *http.ServeMux) {
	t.Helper()
	profilerMux := http.NewServeMux()
	profiler := webpprof.New(profilerMux, webpprof.WithUnsafeUnauthenticatedAccess())
	t.Cleanup(func() { _ = profiler.Close() })
	databasePath := filepath.Join(t.TempDir(), "example.db")
	databaseDriver := webpprofsql.ProfileDriverWith(profiler, &modernsqlite.Driver{}, webpprofsql.Config{
		Connection:     "example",
		Driver:         "sqlite",
		Database:       filepath.Base(databasePath),
		Explain:        true,
		ExplainTimeout: 500 * time.Millisecond,
		ExplainMaxRows: 50,
	})
	database, err := openPlayerDatabase(context.Background(), databaseDriver, databasePath)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	logger := slog.New(webpprofslog.ProfileWith(profiler, slog.NewJSONHandler(io.Discard, nil)))
	players := &playerRepository{database: database}
	refreshPlayers := webpprofschedule.ProfileWith(profiler, "players.refresh-snapshot", func(ctx context.Context) {
		refreshed, refreshErr := players.list(ctx)
		if refreshErr != nil {
			logger.ErrorContext(ctx, "scheduled player refresh failed", "error", refreshErr)
			return
		}
		logger.InfoContext(ctx, "scheduled player refresh completed", "count", len(refreshed))
	})
	rebuildPlayerIndex := webpprofcallable.ProfileWith(profiler, "players.rebuild-search-index", func(ctx context.Context) error {
		indexed, rebuildErr := players.list(ctx)
		if rebuildErr != nil {
			logger.ErrorContext(ctx, "player search index rebuild failed", "error", rebuildErr)
			return rebuildErr
		}
		logger.InfoContext(ctx, "player search index rebuilt", "count", len(indexed))
		return nil
	})
	generatePlayerReport := func(ctx context.Context) error {
		playersForReport, reportErr := players.list(ctx)
		if reportErr != nil {
			return reportErr
		}
		logger.InfoContext(ctx, "player report generated", "format", "pdf", "rows", len(playersForReport))
		return nil
	}
	app := &application{
		profiler:             profiler,
		players:              players,
		logger:               logger,
		metrics:              &demoMetrics{},
		manual:               newManualExamples(profiler),
		refreshPlayers:       refreshPlayers,
		rebuildPlayerIndex:   rebuildPlayerIndex,
		generatePlayerReport: generatePlayerReport,
	}
	return app, profiler, profilerMux
}

func requestEntries(t *testing.T, mux *http.ServeMux, requestID string) []webpprof.Entry {
	t.Helper()
	response := httptest.NewRecorder()
	url := "/debug/webpprof/api/events?request_id=" + requestID + "&limit=200"
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, url, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("events status = %d, want %d", response.Code, http.StatusOK)
	}
	var payload struct {
		Events []webpprof.Entry `json:"events"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode events: %v", err)
	}
	return payload.Events
}

func scopeEntries(t *testing.T, mux *http.ServeMux, scopeID string) []webpprof.Entry {
	t.Helper()
	response := httptest.NewRecorder()
	url := "/debug/webpprof/api/events?scope_id=" + scopeID + "&limit=200"
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, url, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("scope events status = %d, want %d", response.Code, http.StatusOK)
	}
	var payload struct {
		Events []webpprof.Entry `json:"events"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode scope events: %v", err)
	}
	return payload.Events
}

func allEntries(t *testing.T, mux *http.ServeMux) []webpprof.Entry {
	t.Helper()
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/debug/webpprof/api/events?limit=200", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("events status = %d, want %d", response.Code, http.StatusOK)
	}
	var payload struct {
		Events []webpprof.Entry `json:"events"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode events: %v", err)
	}
	return payload.Events
}
