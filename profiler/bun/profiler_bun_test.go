package bun

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/levskiy0/webpprof"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	"github.com/uptrace/bun/driver/sqliteshim"
)

func TestProfileBunRecordsAvailableQueryMetadata(t *testing.T) {
	mux := http.NewServeMux()
	profiler := webpprof.New(mux, webpprof.WithUnsafeUnauthenticatedAccess())
	t.Cleanup(func() { _ = profiler.Close() })
	sqlDB, err := sql.Open(sqliteshim.ShimName, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	db := bun.NewDB(sqlDB, sqlitedialect.New())
	t.Cleanup(func() { _ = db.Close() })
	profiled := ProfileWith(profiler, db, Config{Database: "profile.db"})
	ctx := context.Background()
	if _, err := profiled.ExecContext(ctx, "CREATE TABLE players (id INTEGER PRIMARY KEY, name TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := profiled.ExecContext(ctx, "UPDATE players SET name = ? WHERE id = ?", "Ada", 42); err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/debug/webpprof/api/events?kind=query&limit=10", nil))
	var response struct {
		Events []webpprof.Entry `json:"events"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	var update map[string]any
	for _, entry := range response.Events {
		var data map[string]any
		if err := json.Unmarshal(entry.Data, &data); err != nil {
			t.Fatal(err)
		}
		if data["operation"] == "UPDATE" {
			update = data
		}
	}
	if update == nil {
		t.Fatal("UPDATE query was not recorded")
	}
	if update["connection"] != "default" || update["driver"] != "sqlite" || update["database"] != "profile.db" {
		t.Fatalf("query metadata = %+v", update)
	}
	if rows, ok := update["rows_affected"]; !ok || rows != float64(0) {
		t.Fatalf("rows affected = %#v, present = %v", rows, ok)
	}
	callsite, ok := update["callsite"].([]any)
	if !ok || len(callsite) == 0 {
		t.Fatalf("callsite = %#v", update["callsite"])
	}
}
