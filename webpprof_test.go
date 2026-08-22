package webpprof

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	profiler.LogRequest(Request{Meta: Meta{ID: "request-1", StartedAt: time.Now(), Duration: 10 * time.Millisecond}, Method: http.MethodGet, Path: "/players", Status: http.StatusOK, Queries: []Query{{Meta: Meta{ID: "query-1", Duration: time.Millisecond}, Operation: "SELECT", SQL: "SELECT * FROM players"}}})
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
	if len(payload.Events) != 2 || payload.Events[0].Kind != KindQuery || payload.Events[1].Kind != KindRequest {
		t.Fatalf("events = %+v", payload.Events)
	}
	if payload.Events[0].RequestID != "request-1" {
		t.Fatalf("query request id = %q", payload.Events[0].RequestID)
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
	if entries := profiler.store.list("", "request-concurrent", 0, 200); len(entries) != 101 {
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
	if connected.Runtime == nil || connected.Queues == nil || len(connected.Queues.Sources) != 1 || connected.Queues.Sources[0].Source != "jobs" {
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
	if update.Type != "stats.updated" || update.Runtime == nil || update.Queues == nil {
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
