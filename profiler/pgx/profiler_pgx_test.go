package pgx

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/levskiy0/webpprof"
)

func TestQueryTracerRecordsNativePGXQuery(t *testing.T) {
	mux := http.NewServeMux()
	profiler := webpprof.New(mux, webpprof.WithUnsafeUnauthenticatedAccess(), webpprof.WithCallsiteKinds(webpprof.KindQuery))
	t.Cleanup(func() { _ = profiler.Close() })
	tracer := &queryProfiler{profiler: profiler, config: Config{Connection: "primary", Database: "app"}}

	ctx := tracer.TraceQueryStart(context.Background(), nil, pgx.TraceQueryStartData{SQL: " SELECT  id  FROM players "})
	tracer.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{CommandTag: pgconn.NewCommandTag("SELECT 2")})

	query := onlyQuery(t, mux)
	if query.SQL != "SELECT id FROM players" || query.Operation != "SELECT" || query.Driver != "pgx" {
		t.Fatalf("query = %+v", query)
	}
	if query.RowsAffected == nil || *query.RowsAffected != 2 {
		t.Fatalf("rows affected = %v", query.RowsAffected)
	}
	if len(query.Callsite) == 0 {
		t.Fatal("callsite was not captured")
	}
}

func TestQueryTracerRecordsError(t *testing.T) {
	mux := http.NewServeMux()
	profiler := webpprof.New(mux, webpprof.WithUnsafeUnauthenticatedAccess())
	t.Cleanup(func() { _ = profiler.Close() })
	tracer := &queryProfiler{profiler: profiler, config: Config{}}

	ctx := tracer.TraceQueryStart(context.Background(), nil, pgx.TraceQueryStartData{SQL: "DELETE FROM players"})
	tracer.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{Err: errors.New("connection lost")})

	if query := onlyQuery(t, mux); query.Error != "connection lost" {
		t.Fatalf("error = %q", query.Error)
	}
}

func TestQueryTracerCapturesExplainPlan(t *testing.T) {
	mux := http.NewServeMux()
	profiler := webpprof.New(mux, webpprof.WithUnsafeUnauthenticatedAccess())
	t.Cleanup(func() { _ = profiler.Close() })
	var explainedSQL string
	var explainedArgs []any
	tracer := &queryProfiler{
		profiler: profiler,
		config:   Config{Explain: true},
		explainRunner: func(_ context.Context, _ *pgx.Conn, query string, args []any, _, _ int) (string, error) {
			explainedSQL = query
			explainedArgs = append([]any(nil), args...)
			return "Seq Scan on players  (cost=0.00..12.00 rows=20000 width=8)", nil
		},
	}

	ctx := tracer.TraceQueryStart(context.Background(), nil, pgx.TraceQueryStartData{
		SQL:  "SELECT id FROM players WHERE team_id = $1",
		Args: []any{42},
	})
	tracer.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{CommandTag: pgconn.NewCommandTag("SELECT 1")})

	query := onlyQuery(t, mux)
	if query.Plan == nil || query.Plan.Text == "" || !strings.HasPrefix(query.Plan.Command, "EXPLAIN (FORMAT TEXT) SELECT") {
		t.Fatalf("plan = %+v", query.Plan)
	}
	if explainedSQL != query.Plan.Command || len(explainedArgs) != 1 || explainedArgs[0] != 42 {
		t.Fatalf("EXPLAIN query/args = %q / %#v", explainedSQL, explainedArgs)
	}
}

func TestQueryTracerKeepsExplainErrorSeparate(t *testing.T) {
	mux := http.NewServeMux()
	profiler := webpprof.New(mux, webpprof.WithUnsafeUnauthenticatedAccess())
	t.Cleanup(func() { _ = profiler.Close() })
	tracer := &queryProfiler{
		profiler: profiler,
		config:   Config{Explain: true},
		explainRunner: func(context.Context, *pgx.Conn, string, []any, int, int) (string, error) {
			return "", errors.New("plan unavailable")
		},
	}

	ctx := tracer.TraceQueryStart(context.Background(), nil, pgx.TraceQueryStartData{SQL: "SELECT 1"})
	tracer.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{CommandTag: pgconn.NewCommandTag("SELECT 1")})

	query := onlyQuery(t, mux)
	if query.Error != "" || query.Plan == nil || query.Plan.Error != "plan unavailable" {
		t.Fatalf("query/plan errors = %q / %+v", query.Error, query.Plan)
	}
}

func TestExplainableSQLRejectsMultipleStatements(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  bool
	}{
		{name: "select", query: "SELECT * FROM players", want: true},
		{name: "write", query: "DELETE FROM players WHERE id = $1", want: true},
		{name: "multiple statements", query: "SELECT 1; DELETE FROM players", want: false},
		{name: "schema change", query: "ALTER TABLE players ADD active bool", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := explainableSQL(test.query); got != test.want {
				t.Fatalf("explainableSQL(%q) = %v, want %v", test.query, got, test.want)
			}
		})
	}
}

func onlyQuery(t *testing.T, handler http.Handler) webpprof.Query {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/debug/webpprof/api/events?limit=20", nil))
	var payload struct {
		Events []webpprof.Entry `json:"events"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Events) != 1 || payload.Events[0].Kind != webpprof.KindQuery {
		t.Fatalf("events = %+v", payload.Events)
	}
	var query webpprof.Query
	if err := json.Unmarshal(payload.Events[0].Data, &query); err != nil {
		t.Fatal(err)
	}
	return query
}
