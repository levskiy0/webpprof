package gorm

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestPluginCapturesExplainPlan(t *testing.T) {
	mux := http.NewServeMux()
	profiler := webpprof.New(mux, webpprof.WithUnsafeUnauthenticatedAccess())
	t.Cleanup(func() { _ = profiler.Close() })
	plugin := NewWith(profiler, Config{Explain: true})
	db := testDB(context.Background())
	db.Config.Dialector = testDialector{name: "sqlite"}
	planDB, err := sql.Open(gormPlanDriverName, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = planDB.Close() })
	pool := &recordingConnPool{DB: planDB}
	db.Statement.ConnPool = pool

	plugin.before(db)
	db.Statement.SQL.WriteString("UPDATE players SET active = ? WHERE id = ?")
	db.Statement.Vars = []any{false, 42}
	plugin.after(db, "UPDATE")

	query := onlyQuery(t, mux)
	if query.Plan == nil || !strings.HasPrefix(query.Plan.Command, "EXPLAIN QUERY PLAN UPDATE") {
		t.Fatalf("plan = %+v", query.Plan)
	}
	if query.Plan.Text != "id=3  parent=0  notused=0  detail=SCAN players" {
		t.Fatalf("plan text = %q", query.Plan.Text)
	}
	if pool.query != query.Plan.Command {
		t.Fatalf("executed EXPLAIN = %q, recorded = %q", pool.query, query.Plan.Command)
	}
	if len(pool.args) != 2 || pool.args[1] != 42 {
		t.Fatalf("EXPLAIN args = %#v", pool.args)
	}
}

func TestPluginSkipsExplainForOpenRows(t *testing.T) {
	mux := http.NewServeMux()
	profiler := webpprof.New(mux, webpprof.WithUnsafeUnauthenticatedAccess())
	t.Cleanup(func() { _ = profiler.Close() })
	plugin := NewWith(profiler, Config{Explain: true})
	db := testDB(context.Background())
	db.Config.Dialector = testDialector{name: "sqlite"}
	planDB, err := sql.Open(gormPlanDriverName, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = planDB.Close() })
	pool := &recordingConnPool{DB: planDB}
	db.Statement.ConnPool = pool

	plugin.before(db)
	db.Statement.SQL.WriteString("SELECT id FROM players")
	plugin.after(db, "ROW")

	query := onlyQuery(t, mux)
	if query.Plan != nil || pool.query != "" {
		t.Fatalf("ROW plan/query = %+v / %q", query.Plan, pool.query)
	}
}

func testDB(ctx context.Context) *gorm.DB {
	dialector := testDialector{}
	db := &gorm.DB{Config: &gorm.Config{Dialector: dialector}}
	db.Statement = &gorm.Statement{DB: db, Context: ctx}
	return db
}

type testDialector struct{ name string }

func (d testDialector) Name() string {
	if d.name == "" {
		return "test"
	}
	return d.name
}
func (testDialector) Initialize(*gorm.DB) error                      { return nil }
func (testDialector) Migrator(*gorm.DB) gorm.Migrator                { return nil }
func (testDialector) DataTypeOf(*schema.Field) string                { return "" }
func (testDialector) DefaultValueOf(*schema.Field) clause.Expression { return nil }
func (testDialector) BindVarTo(clause.Writer, *gorm.Statement, any)  {}
func (testDialector) QuoteTo(clause.Writer, string)                  {}
func (testDialector) Explain(query string, _ ...any) string          { return query }

var _ gorm.Dialector = testDialector{}

const gormPlanDriverName = "webpprof-gorm-plan"

func init() {
	sql.Register(gormPlanDriverName, planDriver{})
}

type recordingConnPool struct {
	*sql.DB
	query string
	args  []any
}

func (p *recordingConnPool) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	p.query = query
	p.args = append([]any(nil), args...)
	return p.DB.QueryContext(ctx, query, args...)
}

type planDriver struct{}

func (planDriver) Open(string) (driver.Conn, error) { return planConn{}, nil }

type planConn struct{}

func (planConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare is unsupported")
}
func (planConn) Close() error              { return nil }
func (planConn) Begin() (driver.Tx, error) { return nil, errors.New("transactions are unsupported") }
func (planConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return &planRows{}, nil
}

type planRows struct{ read bool }

func (*planRows) Columns() []string { return []string{"id", "parent", "notused", "detail"} }
func (*planRows) Close() error      { return nil }
func (r *planRows) Next(dest []driver.Value) error {
	if r.read {
		return io.EOF
	}
	r.read = true
	dest[0], dest[1], dest[2], dest[3] = int64(3), int64(0), int64(0), "SCAN players"
	return nil
}

var _ driver.QueryerContext = planConn{}

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
