// Package sql wraps database/sql drivers and connectors so queries are recorded
// without requiring changes to application query calls.
package sql

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"time"

	"github.com/levskiy0/webpprof"
)

// Config supplies connection metadata and optional non-executing EXPLAIN
// behavior for captured SQL queries.
type Config struct {
	Connection     string
	Driver         string
	Database       string
	Explain        bool
	ExplainTimeout time.Duration
	ExplainMaxRows int
}

type sqlConnectorProfiler struct {
	inner    driver.Connector
	profiler *webpprof.Profiler
	config   Config
}

type sqlDriverProfiler struct {
	inner    driver.Driver
	profiler *webpprof.Profiler
	config   Config
}

type sqlDSNConnector struct {
	driver driver.Driver
	dsn    string
}

type sqlConnProfiler struct {
	inner    driver.Conn
	profiler *webpprof.Profiler
	config   Config
}

type sqlStmtProfiler struct {
	inner    driver.Stmt
	conn     *sqlConnProfiler
	profiler *webpprof.Profiler
	config   Config
	query    string
}

// ProfilerSQLConnector implements webpprof.Integration for driver connectors.
type ProfilerSQLConnector struct {
	Config Config
}

// ProfilerSQLDriver implements webpprof.Integration for database drivers.
type ProfilerSQLDriver struct {
	Config Config
}

// NewConnector creates a connector integration. Only the first optional Config
// is used.
func NewConnector(configs ...Config) ProfilerSQLConnector {
	return ProfilerSQLConnector{Config: firstConfig(configs)}
}

// Name returns the connector integration cache namespace.
func (ProfilerSQLConnector) Name() string {
	return "database-sql-connector"
}

// Profile wraps connector so returned connections record SQL operations.
func (d ProfilerSQLConnector) Profile(scope webpprof.Scope, connector driver.Connector) driver.Connector {
	p := scope.Profiler()
	if p == nil || connector == nil {
		return connector
	}
	if profiled, ok := connector.(*sqlConnectorProfiler); ok && profiled.profiler == p {
		return connector
	}
	return &sqlConnectorProfiler{inner: connector, profiler: p, config: d.Config}
}

// NewDriver creates a driver integration. Only the first optional Config is
// used.
func NewDriver(configs ...Config) ProfilerSQLDriver {
	return ProfilerSQLDriver{Config: firstConfig(configs)}
}

// Name returns the driver integration cache namespace.
func (ProfilerSQLDriver) Name() string {
	return "database-sql-driver"
}

// Profile wraps dbDriver so opened connections record SQL operations.
func (d ProfilerSQLDriver) Profile(scope webpprof.Scope, dbDriver driver.Driver) driver.Driver {
	p := scope.Profiler()
	if p == nil || dbDriver == nil {
		return dbDriver
	}
	if profiled, ok := dbDriver.(*sqlDriverProfiler); ok && profiled.profiler == p {
		return dbDriver
	}
	return &sqlDriverProfiler{inner: dbDriver, profiler: p, config: d.Config}
}

// ProfileConnector instruments connector with the default profiler.
func ProfileConnector(connector driver.Connector, configs ...Config) driver.Connector {
	return webpprof.Profile(connector, NewConnector(configs...))
}

// ProfileConnectorWith instruments connector with an explicit profiler.
func ProfileConnectorWith(profiler *webpprof.Profiler, connector driver.Connector, configs ...Config) driver.Connector {
	return webpprof.ProfileWith(profiler, connector, NewConnector(configs...))
}

// ProfileDriver instruments dbDriver with the default profiler.
func ProfileDriver(dbDriver driver.Driver, configs ...Config) driver.Driver {
	return webpprof.Profile(dbDriver, NewDriver(configs...))
}

// ProfileDriverWith instruments dbDriver with an explicit profiler.
func ProfileDriverWith(profiler *webpprof.Profiler, dbDriver driver.Driver, configs ...Config) driver.Driver {
	return webpprof.ProfileWith(profiler, dbDriver, NewDriver(configs...))
}

func (c *sqlConnectorProfiler) Connect(ctx context.Context) (driver.Conn, error) {
	connection, err := c.inner.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return profileSQLConn(c.profiler, connection, c.config), nil
}

func (c *sqlConnectorProfiler) Driver() driver.Driver {
	return ProfileDriverWith(c.profiler, c.inner.Driver(), c.config)
}

func (d *sqlDriverProfiler) Open(dsn string) (driver.Conn, error) {
	connection, err := d.inner.Open(dsn)
	if err != nil {
		return nil, err
	}
	return profileSQLConn(d.profiler, connection, d.config), nil
}

func (d *sqlDriverProfiler) OpenConnector(dsn string) (driver.Connector, error) {
	var connector driver.Connector
	if contextual, ok := d.inner.(driver.DriverContext); ok {
		created, err := contextual.OpenConnector(dsn)
		if err != nil {
			return nil, err
		}
		connector = created
	} else {
		connector = &sqlDSNConnector{driver: d.inner, dsn: dsn}
	}
	return &sqlConnectorProfiler{inner: connector, profiler: d.profiler, config: d.config}, nil
}

func (c *sqlDSNConnector) Connect(ctx context.Context) (driver.Conn, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
		return c.driver.Open(c.dsn)
	}
}

func (c *sqlDSNConnector) Driver() driver.Driver {
	return c.driver
}

func profileSQLConn(p *webpprof.Profiler, connection driver.Conn, config Config) driver.Conn {
	if profiled, ok := connection.(*sqlConnProfiler); ok && profiled.profiler == p {
		return connection
	}
	return &sqlConnProfiler{inner: connection, profiler: p, config: config}
}

func (c *sqlConnProfiler) Prepare(query string) (driver.Stmt, error) {
	statement, err := c.inner.Prepare(query)
	if err != nil {
		return nil, err
	}
	return &sqlStmtProfiler{inner: statement, conn: c, profiler: c.profiler, config: c.config, query: query}, nil
}

func (c *sqlConnProfiler) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	var statement driver.Stmt
	var err error
	if contextual, ok := c.inner.(driver.ConnPrepareContext); ok {
		statement, err = contextual.PrepareContext(ctx, query)
	} else {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
			statement, err = c.inner.Prepare(query)
		}
	}
	if err != nil {
		return nil, err
	}
	return &sqlStmtProfiler{inner: statement, conn: c, profiler: c.profiler, config: c.config, query: query}, nil
}

func (c *sqlConnProfiler) Close() error {
	return c.inner.Close()
}

func (c *sqlConnProfiler) Begin() (driver.Tx, error) {
	return c.inner.Begin()
}

func (c *sqlConnProfiler) BeginTx(ctx context.Context, options driver.TxOptions) (driver.Tx, error) {
	if contextual, ok := c.inner.(driver.ConnBeginTx); ok {
		return contextual.BeginTx(ctx, options)
	}
	if options.Isolation != driver.IsolationLevel(0) {
		return nil, errors.New("webpprof: driver does not support non-default isolation level")
	}
	if options.ReadOnly {
		return nil, errors.New("webpprof: driver does not support read-only transactions")
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
		return c.inner.Begin()
	}
}

func (c *sqlConnProfiler) Exec(query string, args []driver.Value) (driver.Result, error) {
	execer, ok := c.inner.(driver.Execer)
	if !ok {
		return nil, driver.ErrSkip
	}
	callsite := c.profiler.CaptureQueryCallsite()
	plan := c.explain(context.Background(), query, namedValues(args))
	startedAt := time.Now().UTC()
	result, err := execer.Exec(query, args)
	c.record(context.Background(), startedAt, query, result, err, callsite, plan)
	return result, err
}

func (c *sqlConnProfiler) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	callsite := c.profiler.CaptureQueryCallsite()
	plan := c.explain(ctx, query, args)
	startedAt := time.Now().UTC()
	var result driver.Result
	var err error
	if contextual, ok := c.inner.(driver.ExecerContext); ok {
		result, err = contextual.ExecContext(ctx, query, args)
	} else if legacy, ok := c.inner.(driver.Execer); ok {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		values, conversionErr := sqlNamedValues(args)
		if conversionErr != nil {
			return nil, conversionErr
		}
		result, err = legacy.Exec(query, values)
	} else {
		return nil, driver.ErrSkip
	}
	c.record(ctx, startedAt, query, result, err, callsite, plan)
	return result, err
}

func (c *sqlConnProfiler) Query(query string, args []driver.Value) (driver.Rows, error) {
	queryer, ok := c.inner.(driver.Queryer)
	if !ok {
		return nil, driver.ErrSkip
	}
	callsite := c.profiler.CaptureQueryCallsite()
	plan := c.explain(context.Background(), query, namedValues(args))
	startedAt := time.Now().UTC()
	rows, err := queryer.Query(query, args)
	c.record(context.Background(), startedAt, query, nil, err, callsite, plan)
	return rows, err
}

func (c *sqlConnProfiler) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	callsite := c.profiler.CaptureQueryCallsite()
	plan := c.explain(ctx, query, args)
	startedAt := time.Now().UTC()
	var rows driver.Rows
	var err error
	if contextual, ok := c.inner.(driver.QueryerContext); ok {
		rows, err = contextual.QueryContext(ctx, query, args)
	} else if legacy, ok := c.inner.(driver.Queryer); ok {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		values, conversionErr := sqlNamedValues(args)
		if conversionErr != nil {
			return nil, conversionErr
		}
		rows, err = legacy.Query(query, values)
	} else {
		return nil, driver.ErrSkip
	}
	c.record(ctx, startedAt, query, nil, err, callsite, plan)
	return rows, err
}

func (c *sqlConnProfiler) Ping(ctx context.Context) error {
	if pinger, ok := c.inner.(driver.Pinger); ok {
		return pinger.Ping(ctx)
	}
	return nil
}

func (c *sqlConnProfiler) ResetSession(ctx context.Context) error {
	if resetter, ok := c.inner.(driver.SessionResetter); ok {
		return resetter.ResetSession(ctx)
	}
	return nil
}

func (c *sqlConnProfiler) IsValid() bool {
	if validator, ok := c.inner.(driver.Validator); ok {
		return validator.IsValid()
	}
	return true
}

func (c *sqlConnProfiler) CheckNamedValue(value *driver.NamedValue) error {
	if checker, ok := c.inner.(driver.NamedValueChecker); ok {
		return checker.CheckNamedValue(value)
	}
	return driver.ErrSkip
}

func (c *sqlConnProfiler) record(ctx context.Context, startedAt time.Time, query string, result driver.Result, err error, callsite []webpprof.SourceFrame, plan *webpprof.QueryPlan) {
	if err == driver.ErrSkip {
		return
	}
	event := webpprof.Query{Meta: webpprof.Meta{StartedAt: startedAt, Duration: time.Since(startedAt)}, Connection: c.config.Connection, Driver: c.config.Driver, Database: c.config.Database, Operation: sqlOperation(query), SQL: compactSQL(query), Callsite: callsite, Plan: plan}
	if result != nil {
		if rows, rowsErr := result.RowsAffected(); rowsErr == nil {
			event.RowsAffected = int64Pointer(rows)
		}
	}
	if err != nil {
		event.Error = err.Error()
	}
	c.profiler.LogQueryContext(ctx, event)
}

func (s *sqlStmtProfiler) Close() error {
	return s.inner.Close()
}

func (s *sqlStmtProfiler) NumInput() int {
	return s.inner.NumInput()
}

func (s *sqlStmtProfiler) ColumnConverter(index int) driver.ValueConverter {
	if converter, ok := s.inner.(driver.ColumnConverter); ok {
		return converter.ColumnConverter(index)
	}
	return driver.DefaultParameterConverter
}

func (s *sqlStmtProfiler) CheckNamedValue(value *driver.NamedValue) error {
	if checker, ok := s.inner.(driver.NamedValueChecker); ok {
		return checker.CheckNamedValue(value)
	}
	return driver.ErrSkip
}

func (s *sqlStmtProfiler) Exec(args []driver.Value) (driver.Result, error) {
	callsite := s.profiler.CaptureQueryCallsite()
	plan := s.conn.explain(context.Background(), s.query, namedValues(args))
	startedAt := time.Now().UTC()
	result, err := s.inner.Exec(args)
	s.record(context.Background(), startedAt, result, err, callsite, plan)
	return result, err
}

func (s *sqlStmtProfiler) Query(args []driver.Value) (driver.Rows, error) {
	callsite := s.profiler.CaptureQueryCallsite()
	plan := s.conn.explain(context.Background(), s.query, namedValues(args))
	startedAt := time.Now().UTC()
	rows, err := s.inner.Query(args)
	s.record(context.Background(), startedAt, nil, err, callsite, plan)
	return rows, err
}

func (s *sqlStmtProfiler) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	callsite := s.profiler.CaptureQueryCallsite()
	plan := s.conn.explain(ctx, s.query, args)
	startedAt := time.Now().UTC()
	var result driver.Result
	var err error
	if contextual, ok := s.inner.(driver.StmtExecContext); ok {
		result, err = contextual.ExecContext(ctx, args)
	} else {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		values, conversionErr := sqlNamedValues(args)
		if conversionErr != nil {
			return nil, conversionErr
		}
		result, err = s.inner.Exec(values)
	}
	s.record(ctx, startedAt, result, err, callsite, plan)
	return result, err
}

func (s *sqlStmtProfiler) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	callsite := s.profiler.CaptureQueryCallsite()
	plan := s.conn.explain(ctx, s.query, args)
	startedAt := time.Now().UTC()
	var rows driver.Rows
	var err error
	if contextual, ok := s.inner.(driver.StmtQueryContext); ok {
		rows, err = contextual.QueryContext(ctx, args)
	} else {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		values, conversionErr := sqlNamedValues(args)
		if conversionErr != nil {
			return nil, conversionErr
		}
		rows, err = s.inner.Query(values)
	}
	s.record(ctx, startedAt, nil, err, callsite, plan)
	return rows, err
}

func (s *sqlStmtProfiler) record(ctx context.Context, startedAt time.Time, result driver.Result, err error, callsite []webpprof.SourceFrame, plan *webpprof.QueryPlan) {
	event := webpprof.Query{Meta: webpprof.Meta{StartedAt: startedAt, Duration: time.Since(startedAt)}, Connection: s.config.Connection, Driver: s.config.Driver, Database: s.config.Database, Operation: sqlOperation(s.query), SQL: compactSQL(s.query), Callsite: callsite, Plan: plan}
	if result != nil {
		if rows, rowsErr := result.RowsAffected(); rowsErr == nil {
			event.RowsAffected = int64Pointer(rows)
		}
	}
	if err != nil {
		event.Error = err.Error()
	}
	s.profiler.LogQueryContext(ctx, event)
}

func firstConfig(configs []Config) Config {
	if len(configs) == 0 {
		return Config{}
	}
	return configs[0]
}

func sqlNamedValues(args []driver.NamedValue) ([]driver.Value, error) {
	values := make([]driver.Value, len(args))
	for index, arg := range args {
		if arg.Name != "" {
			return nil, errors.New("sql: driver does not support the use of Named Parameters")
		}
		values[index] = arg.Value
	}
	return values, nil
}

func namedValues(args []driver.Value) []driver.NamedValue {
	values := make([]driver.NamedValue, len(args))
	for index, arg := range args {
		values[index] = driver.NamedValue{Ordinal: index + 1, Value: arg}
	}
	return values
}

func sqlOperation(query string) string {
	fields := strings.Fields(query)
	if len(fields) == 0 {
		return ""
	}
	return strings.ToUpper(fields[0])
}

func compactSQL(query string) string {
	query = strings.Join(strings.Fields(query), " ")
	if len(query) <= 8000 {
		return query
	}
	return query[:8000] + "…"
}

func int64Pointer(value int64) *int64 {
	return &value
}

var _ driver.Connector = (*sqlConnectorProfiler)(nil)
var _ driver.Driver = (*sqlDriverProfiler)(nil)
var _ driver.DriverContext = (*sqlDriverProfiler)(nil)
var _ driver.Conn = (*sqlConnProfiler)(nil)
var _ driver.ConnPrepareContext = (*sqlConnProfiler)(nil)
var _ driver.Execer = (*sqlConnProfiler)(nil)
var _ driver.ExecerContext = (*sqlConnProfiler)(nil)
var _ driver.Queryer = (*sqlConnProfiler)(nil)
var _ driver.QueryerContext = (*sqlConnProfiler)(nil)
var _ driver.ConnBeginTx = (*sqlConnProfiler)(nil)
var _ driver.Pinger = (*sqlConnProfiler)(nil)
var _ driver.SessionResetter = (*sqlConnProfiler)(nil)
var _ driver.Validator = (*sqlConnProfiler)(nil)
var _ driver.NamedValueChecker = (*sqlConnProfiler)(nil)
var _ driver.Stmt = (*sqlStmtProfiler)(nil)
var _ driver.ColumnConverter = (*sqlStmtProfiler)(nil)
var _ driver.NamedValueChecker = (*sqlStmtProfiler)(nil)
var _ driver.StmtExecContext = (*sqlStmtProfiler)(nil)
var _ driver.StmtQueryContext = (*sqlStmtProfiler)(nil)
var _ webpprof.Integration[driver.Connector] = ProfilerSQLConnector{}
var _ webpprof.Integration[driver.Driver] = ProfilerSQLDriver{}
