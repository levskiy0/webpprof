// Package pgx profiles native pgx queries without routing them through
// database/sql.
package pgx

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/multitracer"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/levskiy0/webpprof"
)

// Config names the connection and database shown in captured Query entries and
// controls optional non-executing EXPLAIN capture.
type Config struct {
	Connection     string
	Database       string
	Explain        bool
	ExplainTimeout time.Duration
	ExplainMaxRows int
}

type queryProfiler struct {
	profiler      *webpprof.Profiler
	config        Config
	explainRunner explainRunner
}

type queryTraceContextKey struct{}

type queryTrace struct {
	startedAt time.Time
	sql       string
	args      []any
	callsite  []webpprof.SourceFrame
}

// ProfileConfig returns a copy of config with the default profiler tracer
// appended. Existing pgx tracers remain active.
func ProfileConfig(config *pgx.ConnConfig, configs ...Config) *pgx.ConnConfig {
	return ProfileConfigWith(webpprof.Default(), config, configs...)
}

// ProfileConfigWith returns a copy of config with profiler attached. The
// original config is never mutated.
func ProfileConfigWith(profiler *webpprof.Profiler, config *pgx.ConnConfig, configs ...Config) *pgx.ConnConfig {
	if config == nil || profiler == nil {
		return config
	}
	profiled := config.Copy()
	tracer := &queryProfiler{profiler: profiler, config: resolveConfig(profiled, configs)}
	if profiled.Tracer == nil {
		profiled.Tracer = tracer
	} else {
		profiled.Tracer = multitracer.New(profiled.Tracer, tracer)
	}
	return profiled
}

// ProfilePoolConfig returns a copy of a pgxpool config with native query
// tracing attached to its connection config.
func ProfilePoolConfig(config *pgxpool.Config, configs ...Config) *pgxpool.Config {
	return ProfilePoolConfigWith(webpprof.Default(), config, configs...)
}

// ProfilePoolConfigWith is ProfilePoolConfig using an explicit profiler.
func ProfilePoolConfigWith(profiler *webpprof.Profiler, config *pgxpool.Config, configs ...Config) *pgxpool.Config {
	if config == nil || profiler == nil {
		return config
	}
	profiled := config.Copy()
	profiled.ConnConfig = ProfileConfigWith(profiler, profiled.ConnConfig, configs...)
	return profiled
}

func (p *queryProfiler) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if isExplainContext(ctx) {
		return ctx
	}
	return context.WithValue(ctx, queryTraceContextKey{}, queryTrace{
		startedAt: time.Now().UTC(),
		sql:       data.SQL,
		args:      append([]any(nil), data.Args...),
		callsite:  p.profiler.CaptureQueryCallsite(),
	})
}

func (p *queryProfiler) TraceQueryEnd(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryEndData) {
	trace, ok := ctx.Value(queryTraceContextKey{}).(queryTrace)
	if !ok {
		return
	}
	duration := time.Since(trace.startedAt)
	rows := data.CommandTag.RowsAffected()
	query := webpprof.Query{
		Meta:         webpprof.Meta{StartedAt: trace.startedAt, Duration: duration},
		Connection:   p.config.Connection,
		Driver:       "pgx",
		Database:     p.config.Database,
		Operation:    sqlOperation(trace.sql),
		SQL:          compactSQL(trace.sql),
		RowsAffected: &rows,
		Callsite:     trace.callsite,
		Plan:         p.explain(ctx, conn, trace.sql, trace.args),
	}
	if data.Err != nil {
		query.Error = data.Err.Error()
	}
	p.profiler.LogQueryContext(ctx, query)
}

func resolveConfig(config *pgx.ConnConfig, configs []Config) Config {
	var result Config
	if len(configs) > 0 {
		result = configs[0]
	}
	if result.Connection == "" {
		result.Connection = "pgx"
	}
	if result.Database == "" && config != nil {
		result.Database = config.Database
	}
	return result
}

func compactSQL(query string) string {
	return strings.Join(strings.Fields(query), " ")
}

func sqlOperation(query string) string {
	fields := strings.Fields(query)
	if len(fields) == 0 {
		return "SQL"
	}
	return strings.ToUpper(fields[0])
}

var _ pgx.QueryTracer = (*queryProfiler)(nil)
