package sql

import (
	"context"
	stdlibsql "database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/levskiy0/webpprof"
)

type connectorStub struct{}

type driverStub struct{}

type connectionStub struct{}

type rowsStub struct {
	columns []string
	rows    [][]driver.Value
	index   int
}

func (connectorStub) Connect(context.Context) (driver.Conn, error) {
	return connectionStub{}, nil
}

func (connectorStub) Driver() driver.Driver {
	return driverStub{}
}

func (driverStub) Open(string) (driver.Conn, error) {
	return connectionStub{}, nil
}

func (connectionStub) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare is not supported")
}

func (connectionStub) Close() error {
	return nil
}

func (connectionStub) Begin() (driver.Tx, error) {
	return nil, errors.New("transactions are not supported")
}

func (connectionStub) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	return driver.RowsAffected(3), nil
}

func (connectionStub) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	if strings.HasPrefix(query, "EXPLAIN QUERY PLAN ") {
		return &rowsStub{
			columns: []string{"id", "parent", "notused", "detail"},
			rows:    [][]driver.Value{{int64(2), int64(0), int64(0), "SEARCH players USING INTEGER PRIMARY KEY (rowid=?)"}},
		}, nil
	}
	return &rowsStub{columns: []string{"id"}}, nil
}

func (r *rowsStub) Columns() []string { return r.columns }
func (r *rowsStub) Close() error      { return nil }

func (r *rowsStub) Next(destination []driver.Value) error {
	if r.index >= len(r.rows) {
		return io.EOF
	}
	copy(destination, r.rows[r.index])
	r.index++
	return nil
}

func TestProfilerSQLConnectorRecordsContextQuery(t *testing.T) {
	mux := http.NewServeMux()
	profiler := webpprof.New(mux)
	t.Cleanup(func() { _ = profiler.Close() })
	connector := connectorStub{}
	profiled := ProfileConnectorWith(
		profiler,
		connector,
		Config{Connection: "primary", Driver: "fake", Database: "app"},
	)
	db := stdlibsql.OpenDB(profiled)
	t.Cleanup(func() { _ = db.Close() })
	capture := profiler.BeginRequest(webpprof.Request{Meta: webpprof.Meta{ID: "sql-request"}, Method: http.MethodPost, Path: "/players"})
	ctx := webpprof.WithRequest(context.Background(), capture)
	result, err := db.ExecContext(ctx, "update players set name = ?", "Ada")
	if err != nil {
		t.Fatal(err)
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 3 {
		t.Fatalf("rows = %d, error = %v", rows, err)
	}
	capture.Finish(webpprof.RequestResult{Status: http.StatusNoContent})
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/debug/webpprof/api/events?request_id=sql-request&limit=10", nil))
	var payload struct {
		Events []webpprof.Entry `json:"events"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Events) != 2 || payload.Events[0].Kind != webpprof.KindQuery || payload.Events[1].Kind != webpprof.KindRequest {
		t.Fatalf("events = %+v", payload.Events)
	}
	var query webpprof.Query
	if err := json.Unmarshal(payload.Events[0].Data, &query); err != nil {
		t.Fatal(err)
	}
	if query.Operation != "UPDATE" || query.RowsAffected == nil || *query.RowsAffected != 3 || query.Connection != "primary" || query.Driver != "fake" || query.Database != "app" {
		t.Fatalf("query = %+v", query)
	}
	if len(query.Callsite) == 0 || !strings.HasSuffix(query.Callsite[0].File, "profiler_sql_test.go") {
		t.Fatalf("callsite = %+v", query.Callsite)
	}
}

func TestProfilerSQLExplainCapturesPlanWithoutExecutingItAsTheQuery(t *testing.T) {
	mux := http.NewServeMux()
	profiler := webpprof.New(mux)
	t.Cleanup(func() { _ = profiler.Close() })
	profiled := ProfileConnectorWith(
		profiler,
		connectorStub{},
		Config{Connection: "primary", Driver: "sqlite", Database: "app", Explain: true},
	)
	db := stdlibsql.OpenDB(profiled)
	t.Cleanup(func() { _ = db.Close() })

	rows, err := db.QueryContext(context.Background(), "SELECT id FROM players WHERE id = ?", 42)
	if err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/debug/webpprof/api/events?kind=query&limit=10", nil))
	var payload struct {
		Events []webpprof.Entry `json:"events"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Events) != 1 {
		t.Fatalf("events = %+v", payload.Events)
	}
	var query webpprof.Query
	if err := json.Unmarshal(payload.Events[0].Data, &query); err != nil {
		t.Fatal(err)
	}
	if query.Plan == nil || query.Plan.Error != "" || !strings.Contains(query.Plan.Text, "SEARCH players") {
		t.Fatalf("plan = %+v", query.Plan)
	}
	if !strings.HasPrefix(query.Plan.Command, "EXPLAIN QUERY PLAN SELECT") || query.SQL != "SELECT id FROM players WHERE id = ?" {
		t.Fatalf("query = %+v", query)
	}
}

func TestProfilerSQLExplainCapturesUpdatePlan(t *testing.T) {
	mux := http.NewServeMux()
	profiler := webpprof.New(mux)
	t.Cleanup(func() { _ = profiler.Close() })
	profiled := ProfileConnectorWith(
		profiler,
		connectorStub{},
		Config{Connection: "primary", Driver: "sqlite", Database: "app", Explain: true},
	)
	db := stdlibsql.OpenDB(profiled)
	t.Cleanup(func() { _ = db.Close() })

	result, err := db.ExecContext(context.Background(), "UPDATE players SET active = 1 WHERE id = ?", 42)
	if err != nil {
		t.Fatal(err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 3 {
		t.Fatalf("rows affected = %d, error = %v", affected, err)
	}

	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/debug/webpprof/api/events?kind=query&limit=10", nil))
	var payload struct {
		Events []webpprof.Entry `json:"events"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Events) != 1 {
		t.Fatalf("events = %+v", payload.Events)
	}
	var query webpprof.Query
	if err := json.Unmarshal(payload.Events[0].Data, &query); err != nil {
		t.Fatal(err)
	}
	if query.Plan == nil || query.Plan.Error != "" || !strings.Contains(query.Plan.Text, "SEARCH players") {
		t.Fatalf("plan = %+v", query.Plan)
	}
	if !strings.HasPrefix(query.Plan.Command, "EXPLAIN QUERY PLAN UPDATE") || query.Operation != "UPDATE" {
		t.Fatalf("query = %+v", query)
	}
}

func TestExplainableSQLRejectsMultipleStatements(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  bool
	}{
		{name: "select", query: "SELECT 1", want: true},
		{name: "trailing semicolon", query: "SELECT 1;", want: true},
		{name: "multiple statements", query: "SELECT 1; DELETE FROM players", want: false},
		{name: "insert", query: "INSERT INTO players(id) VALUES (1)", want: true},
		{name: "update", query: "UPDATE players SET active = 1", want: true},
		{name: "delete", query: "DELETE FROM players WHERE id = 1", want: true},
		{name: "cte", query: "WITH players AS (SELECT 1) SELECT * FROM players", want: true},
		{name: "ddl", query: "DROP TABLE players", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := explainableSQL(test.query); got != test.want {
				t.Fatalf("explainableSQL(%q) = %v, want %v", test.query, got, test.want)
			}
		})
	}
}

func TestProfilerSQLDisabledReturnsOriginalConnector(t *testing.T) {
	connector := connectorStub{}
	if ProfileConnectorWith(nil, connector) != connector {
		t.Fatal("disabled profiler changed the connector")
	}
}
