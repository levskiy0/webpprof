package sql

import (
	"context"
	stdlibsql "database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/levskiy0/webpprof"
)

type connectorStub struct{}

type driverStub struct{}

type connectionStub struct{}

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
}

func TestProfilerSQLDisabledReturnsOriginalConnector(t *testing.T) {
	connector := connectorStub{}
	if ProfileConnectorWith(nil, connector) != connector {
		t.Fatal("disabled profiler changed the connector")
	}
}
