package sql

import (
	"context"
	"database/sql/driver"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/levskiy0/webpprof"
)

const (
	defaultExplainTimeout  = 750 * time.Millisecond
	defaultExplainMaxRows  = 100
	defaultExplainMaxBytes = 64 << 10
)

func (c *sqlConnProfiler) explain(ctx context.Context, query string, args []driver.NamedValue) *webpprof.QueryPlan {
	if !c.config.Explain || !explainableSQL(query) {
		return nil
	}
	command, err := explainCommand(c.config.Driver, query)
	plan := &webpprof.QueryPlan{Command: compactSQL(command), Format: "text"}
	if err != nil {
		plan.Error = err.Error()
		return plan
	}
	timeout := c.config.ExplainTimeout
	if timeout <= 0 {
		timeout = defaultExplainTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	startedAt := time.Now()
	rows, err := queryDriver(ctx, c.inner, command, args)
	plan.Duration = time.Since(startedAt)
	if err != nil {
		plan.Error = err.Error()
		return plan
	}
	defer rows.Close()
	maxRows := c.config.ExplainMaxRows
	if maxRows <= 0 {
		maxRows = defaultExplainMaxRows
	}
	plan.Text, err = readPlan(rows, maxRows, defaultExplainMaxBytes)
	if err != nil {
		plan.Error = err.Error()
	}
	return plan
}

func explainableSQL(query string) bool {
	query = strings.TrimSpace(query)
	switch sqlOperation(query) {
	case "SELECT", "INSERT", "UPDATE", "DELETE", "WITH":
		// Plain EXPLAIN returns the execution plan without running the DML.
	default:
		return false
	}
	query = strings.TrimSuffix(query, ";")
	return !strings.Contains(query, ";")
}

func explainCommand(driverName, query string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(driverName)) {
	case "postgres", "postgresql", "pgx":
		return "EXPLAIN (FORMAT TEXT) " + query, nil
	case "sqlite", "sqlite3":
		return "EXPLAIN QUERY PLAN " + query, nil
	case "mysql", "mariadb":
		return "EXPLAIN " + query, nil
	default:
		return "", fmt.Errorf("webpprof: EXPLAIN is not supported for SQL driver %q", driverName)
	}
}

func queryDriver(ctx context.Context, connection driver.Conn, query string, args []driver.NamedValue) (driver.Rows, error) {
	if contextual, ok := connection.(driver.QueryerContext); ok {
		return contextual.QueryContext(ctx, query, args)
	}
	legacy, ok := connection.(driver.Queryer)
	if !ok {
		return nil, errorsUnsupportedExplain()
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	values, err := sqlNamedValues(args)
	if err != nil {
		return nil, err
	}
	return legacy.Query(query, values)
}

func readPlan(rows driver.Rows, maxRows, maxBytes int) (string, error) {
	columns := rows.Columns()
	values := make([]driver.Value, len(columns))
	var output strings.Builder
	for row := 0; row < maxRows; row++ {
		err := rows.Next(values)
		if err == io.EOF {
			return output.String(), nil
		}
		if err != nil {
			return output.String(), err
		}
		line := formatPlanRow(columns, values)
		if output.Len() > 0 {
			line = "\n" + line
		}
		if !appendPlanText(&output, line, maxBytes) {
			return output.String() + "\n…", nil
		}
	}
	return output.String() + "\n…", nil
}

func formatPlanRow(columns []string, values []driver.Value) string {
	if len(values) == 1 {
		return planValue(values[0])
	}
	parts := make([]string, 0, len(values))
	for index, value := range values {
		name := fmt.Sprintf("column_%d", index+1)
		if index < len(columns) && columns[index] != "" {
			name = columns[index]
		}
		parts = append(parts, name+"="+planValue(value))
	}
	return strings.Join(parts, "  ")
}

func planValue(value driver.Value) string {
	switch typed := value.(type) {
	case nil:
		return "NULL"
	case []byte:
		return string(typed)
	case time.Time:
		return typed.Format(time.RFC3339Nano)
	default:
		return fmt.Sprint(typed)
	}
}

func appendPlanText(output *strings.Builder, value string, maxBytes int) bool {
	remaining := maxBytes - output.Len()
	if remaining <= 0 {
		return false
	}
	if len(value) <= remaining {
		output.WriteString(value)
		return true
	}
	value = value[:remaining]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	output.WriteString(value)
	return false
}

func errorsUnsupportedExplain() error {
	return fmt.Errorf("webpprof: SQL driver does not expose query execution for EXPLAIN")
}
