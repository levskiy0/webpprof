package webpprof

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type queueStatsSourceStub struct {
	stats QueueStats
}

func (s queueStatsSourceStub) QueueStats(context.Context) (QueueStats, error) {
	return s.stats, nil
}

func TestPackageLogBeforeNewIsNoop(t *testing.T) {
	defaultProfiler.Store(nil)
	LogQuery(Query{SQL: "SELECT 1"})
	if Default() != nil {
		t.Fatal("default profiler must remain nil")
	}
}

func TestNewIfDisabledDoesNotInitializeProfiler(t *testing.T) {
	defaultProfiler.Store(nil)
	profiler := NewIf(false, nil, WithToken("unused"))
	if profiler != nil || Default() != nil || Enabled() || profiler.Enabled() {
		t.Fatal("disabled profiler was initialized")
	}
}

func TestProfilerLogsRequestAndChildren(t *testing.T) {
	mux := http.NewServeMux()
	profiler := New(mux)
	t.Cleanup(func() { _ = profiler.Close() })
	profiler.LogRequest(Request{
		Meta:        Meta{ID: "request-1", StartedAt: time.Now(), Duration: 10 * time.Millisecond},
		Method:      http.MethodGet,
		Path:        "/players",
		Status:      http.StatusOK,
		Queries:     []Query{{Meta: Meta{ID: "query-1", Duration: time.Millisecond}, Operation: "SELECT", SQL: "SELECT * FROM players"}},
		Emails:      []Email{{Meta: Meta{ID: "email-1"}, Subject: "Welcome"}},
		Cache:       []Cache{{Meta: Meta{ID: "cache-1"}, Operation: "get", Key: "player:1", Hit: true}},
		Jobs:        []Job{{Meta: Meta{ID: "job-1"}, Name: "SendWelcomeEmail", State: "dispatched"}},
		Logs:        []Log{{Meta: Meta{ID: "log-1"}, Level: "INFO", Message: "player loaded"}},
		HTTPCalls:   []HTTPCall{{Meta: Meta{ID: "http-call-1"}, Method: http.MethodGet, URL: "https://example.test/player/1"}},
		Schedules:   []Schedule{{Meta: Meta{ID: "schedule-1"}, Name: "refresh-player", State: "succeeded"}},
		Exceptions:  []Exception{{Meta: Meta{ID: "exception-1"}, Type: "exampleError", Message: "example failure"}},
		Events:      []Event{{Meta: Meta{ID: "event-1"}, Kind: "player", Name: "loaded"}},
		Middlewares: []Middleware{{Meta: Meta{ID: "middleware-1"}, Name: "authentication", State: "completed"}},
	})
	request := httptest.NewRequest(http.MethodGet, "/debug/webpprof/api/events?request_id=request-1", nil)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	var payload struct {
		Events []Entry `json:"events"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode events: %v", err)
	}
	wantKinds := []Kind{KindQuery, KindEmail, KindCache, KindJob, KindLog, KindHTTPCall, KindSchedule, KindException, KindEvent, KindMiddleware, KindRequest}
	if len(payload.Events) != len(wantKinds) {
		t.Fatalf("events = %+v", payload.Events)
	}
	for index, entry := range payload.Events {
		if entry.Kind != wantKinds[index] {
			t.Fatalf("event %d kind = %q, want %q", index, entry.Kind, wantKinds[index])
		}
		if entry.RequestID != "request-1" {
			t.Fatalf("event %d request id = %q", index, entry.RequestID)
		}
	}
}

func TestContextTagsAreInheritedAndEntityTagsOverrideThem(t *testing.T) {
	profiler := New(http.NewServeMux())
	t.Cleanup(func() { _ = profiler.Close() })
	capture := profiler.BeginRequest(Request{Meta: Meta{ID: "tagged-request"}, Method: http.MethodGet, Path: "/players"})
	ctx := WithRequest(context.Background(), capture)
	ctx = WithTags(ctx, map[string]string{"tenant": "acme", "environment": "dev"})
	ctx = WithParentEntry(ctx, "parent-operation")
	profiler.LogEventContext(ctx, Event{
		Meta: Meta{ID: "tagged-event", Tags: map[string]string{"environment": "prod"}},
		Kind: "player",
		Name: "viewed",
	})
	profiler.LogEventContext(ctx, Event{
		Meta: Meta{ID: "explicit-parent-event", ParentID: "manual-parent"},
		Kind: "player",
		Name: "updated",
	})
	capture.Finish(RequestResult{Status: http.StatusOK})

	requestEntry, ok := profiler.store.get("tagged-request")
	if !ok || requestEntry.Tags["tenant"] != "acme" || requestEntry.Tags["environment"] != "dev" {
		t.Fatalf("request tags = %+v, found = %v", requestEntry.Tags, ok)
	}
	eventEntry, ok := profiler.store.get("tagged-event")
	if !ok || eventEntry.Tags["tenant"] != "acme" || eventEntry.Tags["environment"] != "prod" {
		t.Fatalf("event tags = %+v, found = %v", eventEntry.Tags, ok)
	}
	if eventEntry.ParentID != "parent-operation" || ParentEntryIDFromContext(ctx) != "parent-operation" {
		t.Fatalf("event parent = %q, context parent = %q", eventEntry.ParentID, ParentEntryIDFromContext(ctx))
	}
	explicitParent, ok := profiler.store.get("explicit-parent-event")
	if !ok || explicitParent.ParentID != "manual-parent" {
		t.Fatalf("explicit event parent = %q, found = %v", explicitParent.ParentID, ok)
	}
	if tags := TagsFromContext(ctx); tags["tenant"] != "acme" {
		t.Fatalf("context tags = %+v", tags)
	}
}

func TestProfilerFiltersEventsByAllRequestedTags(t *testing.T) {
	mux := http.NewServeMux()
	profiler := New(mux)
	t.Cleanup(func() { _ = profiler.Close() })
	profiler.LogEvent(Event{Meta: Meta{ID: "acme-prod", Tags: map[string]string{"tenant": "acme", "environment": "prod"}}, Kind: "deploy", Name: "one"})
	profiler.LogEvent(Event{Meta: Meta{ID: "acme-dev", Tags: map[string]string{"tenant": "acme", "environment": "dev"}}, Kind: "deploy", Name: "two"})
	profiler.LogEvent(Event{Meta: Meta{ID: "other-prod", Tags: map[string]string{"tenant": "other", "environment": "prod"}}, Kind: "deploy", Name: "three"})

	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/debug/webpprof/api/events?tag=tenant%3Dacme&tag=environment%3Dprod", nil))
	var payload struct {
		Events []Entry `json:"events"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Events) != 1 || payload.Events[0].ID != "acme-prod" {
		t.Fatalf("filtered events = %+v", payload.Events)
	}
}

func TestProfilerRedactsSensitiveFields(t *testing.T) {
	profiler := New(http.NewServeMux())
	t.Cleanup(func() { _ = profiler.Close() })
	profiler.LogLog(Log{Meta: Meta{ID: "log-1"}, Message: "login", Fields: map[string]any{"token": "secret-token", "nested": map[string]any{"api_key": "secret-key", "safe": "value"}}})
	entry, ok := profiler.store.get("log-1")
	if !ok {
		t.Fatal("log entry not found")
	}
	text := string(entry.Data)
	if strings.Contains(text, "secret-token") || strings.Contains(text, "secret-key") || !strings.Contains(text, "[REDACTED]") || !strings.Contains(text, "value") {
		t.Fatalf("redacted payload = %s", text)
	}
}

func TestProfilerLogsSchedulePayload(t *testing.T) {
	profiler := New(http.NewServeMux())
	t.Cleanup(func() { _ = profiler.Close() })
	profiler.LogSchedule(Schedule{
		Meta:  Meta{ID: "schedule-payload"},
		Name:  "refresh-player",
		State: "succeeded",
		Payload: map[string]any{
			"player_id": 42,
			"options": map[string]any{
				"force": true,
				"token": "schedule-secret",
			},
		},
	})

	entry, ok := profiler.store.get("schedule-payload")
	if !ok {
		t.Fatal("schedule entry not found")
	}
	var recorded Schedule
	if err := json.Unmarshal(entry.Data, &recorded); err != nil {
		t.Fatalf("decode schedule: %v", err)
	}
	payload, ok := recorded.Payload.(map[string]any)
	if !ok || payload["player_id"] != float64(42) {
		t.Fatalf("schedule payload = %#v", recorded.Payload)
	}
	options, ok := payload["options"].(map[string]any)
	if !ok || options["force"] != true || options["token"] != "[REDACTED]" {
		t.Fatalf("schedule payload options = %#v", payload["options"])
	}
}

func TestProfilerRequiresSessionToken(t *testing.T) {
	mux := http.NewServeMux()
	profiler := New(mux, WithToken("profile-secret"))
	t.Cleanup(func() { _ = profiler.Close() })
	unauthorized := httptest.NewRecorder()
	mux.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/debug/webpprof/api/events", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", unauthorized.Code)
	}
	login := httptest.NewRecorder()
	loginRequest := httptest.NewRequest(http.MethodPost, "/debug/webpprof/session", strings.NewReader(`{"token":"profile-secret"}`))
	mux.ServeHTTP(login, loginRequest)
	if login.Code != http.StatusNoContent || len(login.Result().Cookies()) != 1 {
		t.Fatalf("login status = %d, cookies = %d", login.Code, len(login.Result().Cookies()))
	}
	if login.Result().Cookies()[0].Value == "profile-secret" {
		t.Fatal("session cookie contains the access token")
	}
	authorized := httptest.NewRecorder()
	authorizedRequest := httptest.NewRequest(http.MethodGet, "/debug/webpprof/api/events", nil)
	authorizedRequest.AddCookie(login.Result().Cookies()[0])
	mux.ServeHTTP(authorized, authorizedRequest)
	if authorized.Code != http.StatusOK {
		t.Fatalf("authorized status = %d", authorized.Code)
	}
}

func TestProfilerRateLimitsFailedLogins(t *testing.T) {
	mux := http.NewServeMux()
	profiler := New(mux, WithToken("profile-secret"))
	t.Cleanup(func() { _ = profiler.Close() })
	for attempt := 0; attempt < loginFailureLimit; attempt++ {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/debug/webpprof/session", strings.NewReader(`{"token":"wrong"}`))
		request.RemoteAddr = "192.0.2.10:1234"
		mux.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d status = %d", attempt+1, response.Code)
		}
	}
	blocked := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/debug/webpprof/session", strings.NewReader(`{"token":"profile-secret"}`))
	request.RemoteAddr = "192.0.2.10:1234"
	mux.ServeHTTP(blocked, request)
	if blocked.Code != http.StatusTooManyRequests || blocked.Header().Get("Retry-After") == "" {
		t.Fatalf("blocked status = %d, retry-after = %q", blocked.Code, blocked.Header().Get("Retry-After"))
	}
}

func TestCaptureControlsDisableKindsAndRequestSampling(t *testing.T) {
	profiler := New(http.NewServeMux(), WithRequestSampleRate(0), WithDisabledKinds(KindQuery, KindLog), WithBodyLimit(2048))
	t.Cleanup(func() { _ = profiler.Close() })
	if profiler.ShouldCaptureRequest(httptest.NewRequest(http.MethodGet, "/players", nil)) {
		t.Fatal("zero sample rate captured a request")
	}
	profiler.LogQuery(Query{Meta: Meta{ID: "disabled-query"}, SQL: "SELECT 1"})
	profiler.LogEvent(Event{Meta: Meta{ID: "enabled-event"}, Kind: "test", Name: "captured"})
	if _, ok := profiler.store.get("disabled-query"); ok {
		t.Fatal("disabled query kind was recorded")
	}
	if _, ok := profiler.store.get("enabled-event"); !ok {
		t.Fatal("enabled event kind was not recorded")
	}
	stats := profiler.store.stats()
	if stats.SampleRate != 0 || stats.BodyLimit != 2048 || len(stats.DisabledKinds) != 2 || stats.DisabledKinds[0] != KindLog || stats.DisabledKinds[1] != KindQuery {
		t.Fatalf("capture stats = %+v", stats)
	}
}

func TestStoreEvictsAndUpdatesEntries(t *testing.T) {
	profiler := New(http.NewServeMux(), WithMaxEvents(2))
	t.Cleanup(func() { _ = profiler.Close() })
	profiler.LogJob(Job{Meta: Meta{ID: "job-1"}, Name: "first", State: "running"})
	profiler.LogJob(Job{Meta: Meta{ID: "job-2"}, Name: "second"})
	profiler.LogJob(Job{Meta: Meta{ID: "job-1"}, Name: "first", State: "succeeded"})
	profiler.LogJob(Job{Meta: Meta{ID: "job-3"}, Name: "third"})
	if _, ok := profiler.store.get("job-2"); ok {
		t.Fatal("oldest entry was not evicted")
	}
	entry, ok := profiler.store.get("job-1")
	if !ok || !strings.Contains(string(entry.Data), "succeeded") {
		t.Fatalf("updated job = %s, found=%v", entry.Data, ok)
	}
}

func TestStorageJournalSurvivesRestart(t *testing.T) {
	storagePath := filepath.Join(t.TempDir(), "webpprof.jsonl")
	first := New(http.NewServeMux(), WithStoragePath(storagePath))
	first.LogQuery(Query{Meta: Meta{ID: "persistent-query", Tags: map[string]string{"tenant": "acme"}}, SQL: "SELECT 1"})
	if err := first.Close(); err != nil {
		t.Fatalf("close first profiler: %v", err)
	}

	second := New(http.NewServeMux(), WithStoragePath(storagePath))
	t.Cleanup(func() { _ = second.Close() })
	entry, ok := second.store.get("persistent-query")
	if !ok || entry.Tags["tenant"] != "acme" || !strings.Contains(string(entry.Data), "SELECT 1") {
		t.Fatalf("replayed entry = %+v, found = %v", entry, ok)
	}
	stats := second.store.stats()
	if stats.Storage != "disk" || stats.Events != 1 || stats.StorageError != "" {
		t.Fatalf("storage stats = %+v", stats)
	}
	info, err := os.Stat(storagePath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("journal permissions = %o, want owner-only", info.Mode().Perm())
	}
}

func TestEventsEndpointPaginatesOlderEntries(t *testing.T) {
	mux := http.NewServeMux()
	profiler := New(mux)
	t.Cleanup(func() { _ = profiler.Close() })
	for index := range 5 {
		profiler.LogEvent(Event{Meta: Meta{ID: "event-" + string(rune('a'+index))}, Kind: "page", Name: "event"})
	}

	decodePage := func(target string) struct {
		Events  []Entry `json:"events"`
		HasMore bool    `json:"has_more"`
	} {
		t.Helper()
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
		var page struct {
			Events  []Entry `json:"events"`
			HasMore bool    `json:"has_more"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		return page
	}

	latest := decodePage("/debug/webpprof/api/events?limit=2")
	if len(latest.Events) != 2 || !latest.HasMore || latest.Events[0].ID != "event-d" || latest.Events[1].ID != "event-e" {
		t.Fatalf("latest page = %+v", latest)
	}
	older := decodePage("/debug/webpprof/api/events?limit=2&before=" + strconv.FormatUint(latest.Events[0].Cursor, 10))
	if len(older.Events) != 2 || !older.HasMore || older.Events[0].ID != "event-b" || older.Events[1].ID != "event-c" {
		t.Fatalf("older page = %+v", older)
	}
}

func TestRequestCaptureIsConcurrentAndFinishesOnce(t *testing.T) {
	profiler := New(http.NewServeMux())
	t.Cleanup(func() { _ = profiler.Close() })
	capture := BeginRequest(Request{Meta: Meta{ID: "request-concurrent"}, Method: http.MethodGet, Path: "/"})
	ctx := WithRequest(context.Background(), capture)
	if RequestFromContext(ctx) != capture {
		t.Fatal("capture was not stored in context")
	}
	var wait sync.WaitGroup
	for range 100 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			capture.LogQuery(Query{SQL: "SELECT 1"})
		}()
	}
	wait.Wait()
	capture.Finish(RequestResult{Status: http.StatusOK})
	capture.Finish(RequestResult{Status: http.StatusInternalServerError})
	if entries := profiler.store.list("", "request-concurrent", nil, 0, 200); len(entries) != 101 {
		t.Fatalf("events = %d, want 101", len(entries))
	}
}

func TestWebSocketReceivesLiveEvent(t *testing.T) {
	mux := http.NewServeMux()
	profiler := New(mux)
	profiler.RegisterQueueStats(queueStatsSourceStub{stats: QueueStats{Pending: 3}}, "jobs")
	t.Cleanup(func() { _ = profiler.Close() })
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	webSocketURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/debug/webpprof/ws"
	connection, _, err := websocket.DefaultDialer.Dial(webSocketURL, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	var connected streamMessage
	if err := connection.ReadJSON(&connected); err != nil || connected.Type != "connected" {
		t.Fatalf("connected = %+v, err=%v", connected, err)
	}
	if connected.Stats == nil || connected.Runtime == nil || connected.Queues == nil || connected.Dashboard == nil || len(connected.Queues.Sources) != 1 || connected.Queues.Sources[0].Source != "jobs" {
		t.Fatalf("connected stats = %+v", connected)
	}
	profiler.LogQuery(Query{Meta: Meta{ID: "live-query"}, SQL: "SELECT 1"})
	_ = connection.SetReadDeadline(time.Now().Add(2 * time.Second))
	var update streamMessage
	if err := connection.ReadJSON(&update); err != nil {
		t.Fatalf("read event: %v", err)
	}
	if update.Type != "event.created" || update.Event == nil || update.Event.ID != "live-query" {
		t.Fatalf("update = %+v", update)
	}
}

func TestWebSocketStreamsStats(t *testing.T) {
	mux := http.NewServeMux()
	profiler := New(mux)
	t.Cleanup(func() { _ = profiler.Close() })
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	webSocketURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/debug/webpprof/ws"
	connection, _, err := websocket.DefaultDialer.Dial(webSocketURL, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	_ = connection.SetReadDeadline(time.Now().Add(4 * time.Second))
	var connected streamMessage
	if err := connection.ReadJSON(&connected); err != nil {
		t.Fatalf("read connected: %v", err)
	}
	var update streamMessage
	if err := connection.ReadJSON(&update); err != nil {
		t.Fatalf("read stats update: %v", err)
	}
	if update.Type != "stats.updated" || update.Runtime == nil || update.Queues == nil || update.Dashboard == nil {
		t.Fatalf("stats update = %+v", update)
	}
}

func TestProfilerServesNativeUI(t *testing.T) {
	mux := http.NewServeMux()
	profiler := New(mux)
	t.Cleanup(func() { _ = profiler.Close() })
	for _, target := range []string{"/debug/webpprof/", "/debug/webpprof/app.js", "/debug/webpprof/app.css", "/debug/webpprof/theme.css", "/debug/webpprof/details.css", "/debug/webpprof/logo.svg"} {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
		if response.Code != http.StatusOK || response.Body.Len() == 0 {
			t.Fatalf("GET %s: status=%d bytes=%d", target, response.Code, response.Body.Len())
		}
	}

	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/debug/webpprof/", nil))
	if !strings.Contains(response.Body.String(), `href="https://github.com/levskiy0/webpprof"`) {
		t.Fatal("profiler header does not contain the GitHub link")
	}
	if !strings.Contains(response.Body.String(), `id="tag-watcher"`) {
		t.Fatal("profiler header does not contain the tag watcher")
	}
	for _, marker := range []string{`id="filter-toggle"`, `id="filters-drawer"`, `id="entity-filters"`, `id="time-range"`, `id="import-session"`, `id="export-session"`, `id="load-more"`, `id="capacity-status"`} {
		if !strings.Contains(response.Body.String(), marker) {
			t.Fatalf("profiler UI does not contain %s", marker)
		}
	}
	for _, header := range []string{"Content-Security-Policy", "Cross-Origin-Opener-Policy", "Permissions-Policy", "Cache-Control"} {
		if response.Header().Get(header) == "" {
			t.Fatalf("profiler UI response does not contain %s", header)
		}
	}

	application := httptest.NewRecorder()
	mux.ServeHTTP(application, httptest.NewRequest(http.MethodGet, "/debug/webpprof/app.js", nil))
	if !strings.Contains(application.Body.String(), `"middleware"`) || !strings.Contains(application.Body.String(), "watchedTags") || !strings.Contains(application.Body.String(), "data-copy-request") || !strings.Contains(application.Body.String(), "requestHAR") || !strings.Contains(application.Body.String(), "requestFindingsPanel") || !strings.Contains(application.Body.String(), "diagnostic-severity") || !strings.Contains(application.Body.String(), "tab-badge") || !strings.Contains(application.Body.String(), "dashboard-grid") || !strings.Contains(application.Body.String(), "customDashboardChart") || !strings.Contains(application.Body.String(), "updateFilterPanel") || !strings.Contains(application.Body.String(), "timelineCriticalPath") || !strings.Contains(application.Body.String(), "timelineBreakdown") || !strings.Contains(application.Body.String(), "timelineTreeConnector") || !strings.Contains(application.Body.String(), "queryTab") || !strings.Contains(application.Body.String(), "entityTabDefinitions") || !strings.Contains(application.Body.String(), "data-copy-query-replay") || !strings.Contains(application.Body.String(), "function cardTabs(") || !strings.Contains(application.Body.String(), `role="tab"`) {
		t.Fatal("profiler UI does not include middleware, tag watcher, request export, HAR, diagnostics, configurable dashboard, Gantt timeline, and entity analysis tabs")
	}
	if strings.Contains(application.Body.String(), "query-tabs") || strings.Contains(application.Body.String(), `<span class="card-tab`) {
		t.Fatal("profiler UI still contains a legacy card-tab renderer")
	}

	theme := httptest.NewRecorder()
	mux.ServeHTTP(theme, httptest.NewRequest(http.MethodGet, "/debug/webpprof/theme.css", nil))
	if strings.Contains(theme.Body.String(), "query-tabs") {
		t.Fatal("profiler theme still contains legacy query-tab styling")
	}
}

func TestEntryStoreConcurrentLoad(t *testing.T) {
	configuration := defaultConfig()
	configuration.maxEvents = 5_000
	store := newEntryStore(configuration)
	t.Cleanup(store.close)
	const workers = 16
	const entriesPerWorker = 200
	var wait sync.WaitGroup
	for worker := range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for index := range entriesPerWorker {
				store.put(Entry{ID: fmt.Sprintf("load-%d-%d", worker, index), Kind: KindEvent, RecordedAt: time.Now(), StartedAt: time.Now(), Data: json.RawMessage(`{"kind":"load","name":"event"}`)})
				if index%25 == 0 {
					_ = store.stats()
					_ = store.list(KindEvent, "", nil, 0, 50)
				}
			}
		}()
	}
	wait.Wait()
	if stats := store.stats(); stats.Events != workers*entriesPerWorker || stats.DroppedEvents != 0 {
		t.Fatalf("load stats = %+v", stats)
	}
}

func BenchmarkEntryStorePutAndList(b *testing.B) {
	configuration := defaultConfig()
	configuration.maxEvents = max(configuration.maxEvents, b.N)
	store := newEntryStore(configuration)
	b.Cleanup(store.close)
	b.ReportAllocs()
	for index := 0; index < b.N; index++ {
		store.put(Entry{ID: fmt.Sprintf("benchmark-%d", index), Kind: KindEvent, RecordedAt: time.Now(), StartedAt: time.Now(), Data: json.RawMessage(`{"kind":"benchmark","name":"event"}`)})
		if index%100 == 0 {
			_ = store.list(KindEvent, "", nil, 0, 100)
		}
	}
}

func TestProfilerServesRuntimeStats(t *testing.T) {
	mux := http.NewServeMux()
	profiler := New(mux)
	t.Cleanup(func() { _ = profiler.Close() })
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/debug/webpprof/api/runtime", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	var stats RuntimeStats
	if err := json.Unmarshal(response.Body.Bytes(), &stats); err != nil {
		t.Fatalf("decode runtime stats: %v", err)
	}
	if stats.RecordedAt.IsZero() || stats.UptimeNS < 0 || stats.GOMAXPROCS < 1 || stats.Goroutines < 1 || stats.MemoryBytes == 0 {
		t.Fatalf("runtime stats = %+v", stats)
	}
}

func TestProfilerServesRegisteredQueueStats(t *testing.T) {
	mux := http.NewServeMux()
	profiler := New(mux)
	t.Cleanup(func() { _ = profiler.Close() })
	profiler.RegisterQueueStats(queueStatsSourceStub{stats: QueueStats{WorkersActive: 2, WorkersTotal: 5, Pending: 7, Processed: 11, Queues: []QueueState{{Name: "mail", WorkersActive: 2, WorkersTotal: 3, Pending: 7}}}}, "primary")
	profiler.RegisterQueueStats(queueStatsSourceStub{stats: QueueStats{WorkersActive: 1, WorkersTotal: 4, Pending: 6, Processed: 12, Queues: []QueueState{{Name: "mail", WorkersActive: 1, WorkersTotal: 4, Pending: 6}}}}, "primary")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/debug/webpprof/api/queues", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	var stats QueueStatsResponse
	if err := json.Unmarshal(response.Body.Bytes(), &stats); err != nil {
		t.Fatalf("decode queue stats: %v", err)
	}
	if stats.RecordedAt.IsZero() || len(stats.Sources) != 1 {
		t.Fatalf("queue stats = %+v", stats)
	}
	source := stats.Sources[0]
	if source.Source != "primary" || source.WorkersActive != 1 || source.WorkersTotal != 4 || source.Pending != 6 || source.Processed != 12 || len(source.Queues) != 1 || source.Queues[0].Name != "mail" {
		t.Fatalf("queue source = %+v", source)
	}
}

func TestQueueStatsAppliesProviderDeadline(t *testing.T) {
	profiler := New(http.NewServeMux(), WithQueueStatsTimeout(20*time.Millisecond))
	t.Cleanup(func() { _ = profiler.Close() })
	profiler.RegisterQueueStats(QueueStatsSourceFunc(func(ctx context.Context) (QueueStats, error) {
		<-ctx.Done()
		return QueueStats{}, ctx.Err()
	}), "blocked")
	startedAt := time.Now()
	stats := profiler.QueueStats(context.Background())
	if time.Since(startedAt) > time.Second {
		t.Fatal("queue stats provider ignored the configured deadline")
	}
	if len(stats.Sources) != 1 || stats.Sources[0].Source != "blocked" || !strings.Contains(stats.Sources[0].Error, "deadline exceeded") {
		t.Fatalf("queue stats = %+v", stats)
	}
}

func TestQueueStatsSupportsConcurrentRegistrationAndSnapshots(t *testing.T) {
	profiler := New(http.NewServeMux())
	t.Cleanup(func() { _ = profiler.Close() })
	var group sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		group.Add(1)
		go func(worker int) {
			defer group.Done()
			for iteration := 0; iteration < 50; iteration++ {
				name := "primary"
				if worker%2 == 1 {
					name = "secondary"
				}
				profiler.RegisterQueueStats(queueStatsSourceStub{stats: QueueStats{Pending: int64(iteration)}}, name)
				_ = profiler.QueueStats(context.Background())
			}
		}(worker)
	}
	group.Wait()
	stats := profiler.QueueStats(context.Background())
	if len(stats.Sources) != 2 || stats.Sources[0].Source != "primary" || stats.Sources[1].Source != "secondary" {
		t.Fatalf("queue stats = %+v", stats)
	}
}
