package bun

import (
	"context"
	"strings"
	"time"

	"github.com/levskiy0/webpprof"
	"github.com/uptrace/bun"
)

type ProfilerBun struct {
	Config Config
}

type bunQueryProfiler struct {
	profiler *webpprof.Profiler
	config   Config
}

type queryTraceContextKey struct{}

type queryTrace struct {
	startedAt time.Time
	callsite  []webpprof.SourceFrame
}

type Config struct {
	Connection string
	Driver     string
	Database   string
}

func New(configs ...Config) ProfilerBun {
	var config Config
	if len(configs) > 0 {
		config = configs[0]
	}
	return ProfilerBun{Config: config}
}

func (ProfilerBun) Name() string {
	return "bun"
}

func (d ProfilerBun) Profile(scope webpprof.Scope, db *bun.DB) *bun.DB {
	p := scope.Profiler()
	if p == nil || db == nil {
		return db
	}
	if profiled, ok := scope.Load(db); ok {
		return profiled.(*bun.DB)
	}
	profiled := db.WithQueryHook(&bunQueryProfiler{profiler: p, config: bunSQLConfig(db, d.Config)})
	actual, loaded := scope.LoadOrStore(db, profiled)
	if loaded {
		return actual.(*bun.DB)
	}
	scope.Store(profiled, profiled)
	return profiled
}

func Profile(db *bun.DB, configs ...Config) *bun.DB {
	return webpprof.Profile(db, New(configs...))
}

func ProfileWith(profiler *webpprof.Profiler, db *bun.DB, configs ...Config) *bun.DB {
	return webpprof.ProfileWith(profiler, db, New(configs...))
}

func (h *bunQueryProfiler) BeforeQuery(ctx context.Context, _ *bun.QueryEvent) context.Context {
	return context.WithValue(ctx, queryTraceContextKey{}, queryTrace{
		startedAt: time.Now().UTC(),
		callsite:  h.profiler.CaptureQueryCallsite(),
	})
}

func (h *bunQueryProfiler) AfterQuery(ctx context.Context, source *bun.QueryEvent) {
	startedAt := source.StartTime.UTC()
	var callsite []webpprof.SourceFrame
	if trace, ok := ctx.Value(queryTraceContextKey{}).(queryTrace); ok {
		startedAt = trace.startedAt
		callsite = trace.callsite
	}
	event := webpprof.Query{Meta: webpprof.Meta{StartedAt: startedAt, Duration: time.Since(startedAt)}, Connection: h.config.Connection, Driver: h.config.Driver, Database: h.config.Database, Operation: source.Operation(), SQL: compactSQL(source.QueryTemplate), Callsite: callsite}
	if event.SQL == "" {
		event.SQL = compactSQL(source.Query)
	}
	if source.Result != nil {
		if rows, err := source.Result.RowsAffected(); err == nil {
			event.RowsAffected = rowsPointer(rows)
		}
	}
	if source.Err != nil {
		event.Error = source.Err.Error()
	}
	h.profiler.LogQueryContext(ctx, event)
}

func bunSQLConfig(db *bun.DB, config Config) Config {
	if strings.TrimSpace(config.Connection) == "" {
		config.Connection = "default"
	}
	if strings.TrimSpace(config.Driver) == "" {
		config.Driver = db.Dialect().Name().String()
	}
	return config
}

func rowsPointer(rows int64) *int64 {
	return &rows
}

func compactSQL(query string) string {
	query = strings.Join(strings.Fields(query), " ")
	const maxLength = 8000
	if len(query) <= maxLength {
		return query
	}
	return query[:maxLength] + "…"
}

var _ webpprof.Integration[*bun.DB] = ProfilerBun{}
