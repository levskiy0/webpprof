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
	webpprofhttp "github.com/levskiy0/webpprof/profiler/http"
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
	profiler := webpprof.New(profilerMux)
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
	app := &application{
		profiler: profiler,
		players:  &playerRepository{database: database},
		logger:   logger,
		metrics:  &demoMetrics{},
		manual:   newManualExamples(profiler),
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
