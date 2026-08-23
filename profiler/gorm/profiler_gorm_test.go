package gorm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/levskiy0/webpprof"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/schema"
)

func TestPluginRecordsGORMQuery(t *testing.T) {
	mux := http.NewServeMux()
	profiler := webpprof.New(mux, webpprof.WithUnsafeUnauthenticatedAccess(), webpprof.WithCallsiteKinds(webpprof.KindQuery))
	t.Cleanup(func() { _ = profiler.Close() })
	plugin := NewWith(profiler, Config{Connection: "primary", Database: "app"})
	db := testDB(context.Background())

	plugin.before(db)
	db.Statement.SQL.WriteString(" SELECT  id FROM players WHERE id = ? ")
	db.RowsAffected = 1
	plugin.after(db, "SELECT")

	query := onlyQuery(t, mux)
	if query.SQL != "SELECT id FROM players WHERE id = ?" || query.Operation != "SELECT" || query.Connection != "primary" {
		t.Fatalf("query = %+v", query)
	}
	if query.RowsAffected == nil || *query.RowsAffected != 1 || len(query.Callsite) == 0 {
		t.Fatalf("rows/callsite = %v / %+v", query.RowsAffected, query.Callsite)
	}
}

func TestPluginRecordsGORMError(t *testing.T) {
	mux := http.NewServeMux()
	profiler := webpprof.New(mux, webpprof.WithUnsafeUnauthenticatedAccess())
	t.Cleanup(func() { _ = profiler.Close() })
	plugin := NewWith(profiler)
	db := testDB(context.Background())

	plugin.before(db)
	db.Statement.SQL.WriteString("UPDATE players SET active = false")
	db.Error = errors.New("write failed")
	plugin.after(db, "UPDATE")

	if query := onlyQuery(t, mux); query.Error != "write failed" {
		t.Fatalf("error = %q", query.Error)
	}
}

func testDB(ctx context.Context) *gorm.DB {
	dialector := testDialector{}
	db := &gorm.DB{Config: &gorm.Config{Dialector: dialector}}
	db.Statement = &gorm.Statement{DB: db, Context: ctx}
	return db
}

type testDialector struct{}

func (testDialector) Name() string                                   { return "test" }
func (testDialector) Initialize(*gorm.DB) error                      { return nil }
func (testDialector) Migrator(*gorm.DB) gorm.Migrator                { return nil }
func (testDialector) DataTypeOf(*schema.Field) string                { return "" }
func (testDialector) DefaultValueOf(*schema.Field) clause.Expression { return nil }
func (testDialector) BindVarTo(clause.Writer, *gorm.Statement, any)  {}
func (testDialector) QuoteTo(clause.Writer, string)                  {}
func (testDialector) Explain(query string, _ ...any) string          { return query }

var _ gorm.Dialector = testDialector{}

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
