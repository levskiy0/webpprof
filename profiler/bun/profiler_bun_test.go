package bun

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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
	profiled := ProfileWith(profiler, db, Config{Database: "profile.db", Explain: true})
	ctx := context.Background()
	if _, err := profiled.ExecContext(ctx, "CREATE TABLE players (id INTEGER PRIMARY KEY, name TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := profiled.ExecContext(ctx, "INSERT INTO players (id, name) VALUES (?, ?)", 7, "Grace"); err != nil {
		t.Fatal(err)
	}
	if _, err := profiled.ExecContext(ctx, "UPDATE players SET name = ? WHERE id = ?", "Ada", 42); err != nil {
		t.Fatal(err)
	}
	var name string
	if err := profiled.QueryRowContext(ctx, "SELECT name FROM players WHERE id = ?", 7).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "Grace" {
		t.Fatalf("selected name = %q", name)
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
	plan, ok := update["plan"].(map[string]any)
	if !ok {
		t.Fatalf("plan = %#v", update["plan"])
	}
	command, _ := plan["command"].(string)
	text, _ := plan["text"].(string)
	if !strings.HasPrefix(command, "EXPLAIN QUERY PLAN UPDATE players") || text == "" {
		t.Fatalf("plan command/text = %q / %q", command, text)
	}
	if strings.Contains(command, "Ada") || strings.Contains(command, "42") {
		t.Fatalf("plan command persisted bind values: %q", command)
	}
}

func TestExplainableSQLRejectsMultipleStatements(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  bool
	}{
		{name: "select", query: "SELECT * FROM players", want: true},
		{name: "write", query: "UPDATE players SET active = false", want: true},
		{name: "multiple statements", query: "SELECT 1; DELETE FROM players", want: false},
		{name: "schema change", query: "DROP TABLE players", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := explainableSQL(test.query); got != test.want {
				t.Fatalf("explainableSQL(%q) = %v, want %v", test.query, got, test.want)
			}
		})
	}
}
