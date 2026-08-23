// Package gorm provides a GORM plugin that records generated SQL as webpprof
// Query entries.
package gorm

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/levskiy0/webpprof"
	"gorm.io/gorm"
)

const pluginName = "webpprof"

// Config names the connection and database shown in captured Query entries.
type Config struct {
	Connection string
	Database   string
}

// Plugin implements gorm.Plugin.
type Plugin struct {
	profiler *webpprof.Profiler
	config   Config
}

type queryTraceContextKey struct{}

type queryTrace struct {
	startedAt time.Time
	callsite  []webpprof.SourceFrame
}

// New creates a plugin using the default webpprof profiler.
func New(configs ...Config) *Plugin {
	return NewWith(webpprof.Default(), configs...)
}

// NewWith creates a plugin using an explicit profiler.
func NewWith(profiler *webpprof.Profiler, configs ...Config) *Plugin {
	var config Config
	if len(configs) > 0 {
		config = configs[0]
	}
	if config.Connection == "" {
		config.Connection = "gorm"
	}
	return &Plugin{profiler: profiler, config: config}
}

// Profile installs the plugin on db using the default profiler.
func Profile(db *gorm.DB, configs ...Config) error {
	if db == nil {
		return nil
	}
	return db.Use(New(configs...))
}

// ProfileWith installs the plugin on db using an explicit profiler.
func ProfileWith(profiler *webpprof.Profiler, db *gorm.DB, configs ...Config) error {
	if db == nil {
		return nil
	}
	return db.Use(NewWith(profiler, configs...))
}

// Name identifies the plugin in GORM's plugin registry.
func (*Plugin) Name() string {
	return pluginName
}

// Initialize registers callbacks around every SQL-producing GORM operation.
func (p *Plugin) Initialize(db *gorm.DB) error {
	if p == nil || p.profiler == nil || db == nil {
		return nil
	}
	type callbackPair struct {
		operation string
		before    func(string, func(*gorm.DB)) error
		after     func(string, func(*gorm.DB)) error
	}
	pairs := []callbackPair{
		{operation: "CREATE", before: db.Callback().Create().Before("gorm:create").Register, after: db.Callback().Create().After("gorm:after_create").Register},
		{operation: "SELECT", before: db.Callback().Query().Before("gorm:query").Register, after: db.Callback().Query().After("gorm:after_query").Register},
		{operation: "UPDATE", before: db.Callback().Update().Before("gorm:update").Register, after: db.Callback().Update().After("gorm:after_update").Register},
		{operation: "DELETE", before: db.Callback().Delete().Before("gorm:delete").Register, after: db.Callback().Delete().After("gorm:after_delete").Register},
		{operation: "ROW", before: db.Callback().Row().Before("gorm:row").Register, after: db.Callback().Row().After("gorm:row").Register},
		{operation: "RAW", before: db.Callback().Raw().Before("gorm:raw").Register, after: db.Callback().Raw().After("gorm:raw").Register},
	}
	var registerErrors []error
	for _, pair := range pairs {
		operation := pair.operation
		registerErrors = append(registerErrors,
			pair.before(pluginName+":before_"+strings.ToLower(operation), p.before),
			pair.after(pluginName+":after_"+strings.ToLower(operation), func(tx *gorm.DB) { p.after(tx, operation) }),
		)
	}
	return errors.Join(registerErrors...)
}

func (p *Plugin) before(db *gorm.DB) {
	if db == nil || db.Statement == nil {
		return
	}
	ctx := db.Statement.Context
	if ctx == nil {
		ctx = context.Background()
	}
	db.Statement.Context = context.WithValue(ctx, queryTraceContextKey{}, queryTrace{
		startedAt: time.Now().UTC(),
		callsite:  p.profiler.CaptureQueryCallsite(),
	})
}

func (p *Plugin) after(db *gorm.DB, fallbackOperation string) {
	if db == nil || db.Statement == nil {
		return
	}
	trace, ok := db.Statement.Context.Value(queryTraceContextKey{}).(queryTrace)
	if !ok {
		return
	}
	querySQL := compactSQL(db.Statement.SQL.String())
	operation := sqlOperation(querySQL)
	if operation == "SQL" {
		operation = fallbackOperation
	}
	rows := db.RowsAffected
	query := webpprof.Query{
		Meta:         webpprof.Meta{StartedAt: trace.startedAt, Duration: time.Since(trace.startedAt)},
		Connection:   p.config.Connection,
		Driver:       db.Dialector.Name(),
		Database:     p.config.Database,
		Operation:    operation,
		SQL:          querySQL,
		RowsAffected: &rows,
		Callsite:     trace.callsite,
	}
	if db.Error != nil {
		query.Error = db.Error.Error()
	}
	p.profiler.LogQueryContext(db.Statement.Context, query)
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

var _ gorm.Plugin = (*Plugin)(nil)
