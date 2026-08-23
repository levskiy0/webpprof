package pgx

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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
