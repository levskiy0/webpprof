package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/levskiy0/webpprof"
)

func TestStorageSurvivesRestartAndPreservesCursor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "webpprof.db")
	firstMux := http.NewServeMux()
	first := newProfiler(t, firstMux, path)
	first.LogEvent(webpprof.Event{Meta: webpprof.Meta{ID: "before-clear"}, Kind: "test", Name: "before"})
	firstCursor := readStats(t, firstMux).Cursor
	clear := httptest.NewRecorder()
	firstMux.ServeHTTP(clear, httptest.NewRequest(http.MethodDelete, "/debug/webpprof/api/events", nil))
	if clear.Code != http.StatusNoContent {
		t.Fatalf("clear status = %d", clear.Code)
	}
	clearedCursor := readStats(t, firstMux).Cursor
	first.LogEvent(webpprof.Event{Meta: webpprof.Meta{ID: "after-clear"}, Kind: "test", Name: "after"})
	lastCursor := readStats(t, firstMux).Cursor
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	secondMux := http.NewServeMux()
	second := newProfiler(t, secondMux, path)
	t.Cleanup(func() { _ = second.Close() })
	entries := readEvents(t, secondMux)
	if len(entries) != 1 || entries[0].ID != "after-clear" {
		t.Fatalf("restored entries = %+v", entries)
	}
	stats := readStats(t, secondMux)
	if stats.Storage != "sqlite" || stats.Cursor != lastCursor || stats.StorageError != "" {
		t.Fatalf("storage stats = %+v", stats)
	}
	if !(firstCursor < clearedCursor && clearedCursor < lastCursor) {
		t.Fatalf("cursors = %d, %d, %d", firstCursor, clearedCursor, lastCursor)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("permissions = %o", info.Mode().Perm())
	}
}

func TestStoragePersistsFIFOEviction(t *testing.T) {
	path := filepath.Join(t.TempDir(), "webpprof.db")
	firstMux := http.NewServeMux()
	storage, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	first := webpprof.New(firstMux, webpprof.WithUnsafeUnauthenticatedAccess(), webpprof.WithStorage(storage), webpprof.WithMaxEvents(2))
	for index := 1; index <= 3; index++ {
		first.LogEvent(webpprof.Event{Meta: webpprof.Meta{ID: fmt.Sprintf("event-%d", index)}, Kind: "fifo", Name: "recorded"})
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	secondMux := http.NewServeMux()
	storage, err = Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	second := webpprof.New(secondMux, webpprof.WithUnsafeUnauthenticatedAccess(), webpprof.WithStorage(storage), webpprof.WithMaxEvents(2))
	t.Cleanup(func() { _ = second.Close() })
	entries := readEvents(t, secondMux)
	if len(entries) != 2 || entries[0].ID != "event-2" || entries[1].ID != "event-3" {
		t.Fatalf("restored FIFO entries = %+v", entries)
	}
}

func TestStoragePersistsRetentionPruning(t *testing.T) {
	path := filepath.Join(t.TempDir(), "webpprof.db")
	storage, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := storage.Put(context.Background(), webpprof.Entry{ID: "expired", Cursor: 1, Kind: webpprof.KindEvent, RecordedAt: now.Add(-2 * time.Hour), Data: json.RawMessage(`{"kind":"test"}`)}, 1); err != nil {
		t.Fatal(err)
	}
	if err := storage.Put(context.Background(), webpprof.Entry{ID: "fresh", Cursor: 2, Kind: webpprof.KindEvent, RecordedAt: now, Data: json.RawMessage(`{"kind":"test"}`)}, 2); err != nil {
		t.Fatal(err)
	}
	if err := storage.Close(); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	storage, err = Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	profiler := webpprof.New(mux, webpprof.WithUnsafeUnauthenticatedAccess(), webpprof.WithStorage(storage), webpprof.WithRetention(time.Hour))
	if entries := readEvents(t, mux); len(entries) != 1 || entries[0].ID != "fresh" {
		t.Fatalf("retained entries = %+v", entries)
	}
	if err := profiler.Close(); err != nil {
		t.Fatal(err)
	}

	mux = http.NewServeMux()
	storage, err = Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	profiler = webpprof.New(mux, webpprof.WithUnsafeUnauthenticatedAccess(), webpprof.WithStorage(storage), webpprof.WithRetention(24*time.Hour))
	t.Cleanup(func() { _ = profiler.Close() })
	if entries := readEvents(t, mux); len(entries) != 1 || entries[0].ID != "fresh" {
		t.Fatalf("pruned entry reappeared: %+v", entries)
	}
}

func BenchmarkProfilerSQLiteSteadyStateEviction(b *testing.B) {
	const maxEvents = 10_000
	path := filepath.Join(b.TempDir(), "webpprof.db")
	storage, err := Open(context.Background(), path)
	if err != nil {
		b.Fatal(err)
	}
	profiler := webpprof.New(
		http.NewServeMux(),
		webpprof.WithStorage(storage),
		webpprof.WithMaxEvents(maxEvents),
	)
	b.Cleanup(func() {
		if err := profiler.Close(); err != nil {
			b.Error(err)
		}
	})

	events := make([]webpprof.Event, maxEvents+1)
	startedAt := time.Now()
	for index := range events {
		events[index] = webpprof.Event{
			Meta: webpprof.Meta{ID: fmt.Sprintf("benchmark-%d", index), StartedAt: startedAt},
			Kind: "benchmark",
			Name: "event",
		}
	}
	for index := 0; index < maxEvents; index++ {
		profiler.LogEvent(events[index])
	}

	b.ReportAllocs()
	index := maxEvents
	for b.Loop() {
		profiler.LogEvent(events[index])
		index++
		if index == len(events) {
			index = 0
		}
	}
}

func newProfiler(t *testing.T, mux *http.ServeMux, path string) *webpprof.Profiler {
	t.Helper()
	storage, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	return webpprof.New(mux, webpprof.WithUnsafeUnauthenticatedAccess(), webpprof.WithStorage(storage))
}

func readEvents(t *testing.T, mux *http.ServeMux) []webpprof.Entry {
	t.Helper()
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/debug/webpprof/api/events?limit=100", nil))
	var payload struct {
		Events []webpprof.Entry `json:"events"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	return payload.Events
}

func readStats(t *testing.T, mux *http.ServeMux) webpprof.Stats {
	t.Helper()
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/debug/webpprof/api/stats", nil))
	var stats webpprof.Stats
	if err := json.Unmarshal(response.Body.Bytes(), &stats); err != nil {
		t.Fatal(err)
	}
	return stats
}
