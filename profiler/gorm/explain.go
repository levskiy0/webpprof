package gorm

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/levskiy0/webpprof"
	"gorm.io/gorm"
)

const (
	defaultExplainTimeout  = 750 * time.Millisecond
	defaultExplainMaxRows  = 100
	defaultExplainMaxBytes = 64 << 10
)

func (p *Plugin) explain(db *gorm.DB, query, callbackOperation string) *webpprof.QueryPlan {
	if !p.config.Explain || db == nil || db.Statement == nil || db.Error != nil || callbackOperation == "ROW" || !explainableSQL(query) {
		return nil
	}
	command, err := explainCommand(db.Dialector.Name(), query)
	plan := &webpprof.QueryPlan{Command: compactSQL(command), Format: "text"}
	if err != nil {
		plan.Error = err.Error()
		return plan
	}
	if db.Statement.ConnPool == nil {
		plan.Error = "webpprof: GORM connection pool is unavailable for EXPLAIN"
		return plan
	}
	ctx := db.Statement.Context
	if ctx == nil {
		ctx = context.Background()
	}
	timeout := p.config.ExplainTimeout
	if timeout <= 0 {
		timeout = defaultExplainTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	startedAt := time.Now()
	// Query the active pool (or transaction) directly so GORM callbacks are not
	// re-entered and the plan is produced in the same database context.
	rows, err := db.Statement.ConnPool.QueryContext(ctx, command, db.Statement.Vars...)
	plan.Duration = time.Since(startedAt)
	if err != nil {
		plan.Error = err.Error()
		return plan
	}
	defer rows.Close()
	maxRows := p.config.ExplainMaxRows
	if maxRows <= 0 {
		maxRows = defaultExplainMaxRows
	}
	plan.Text, err = readSQLPlan(rows, maxRows, defaultExplainMaxBytes)
	if err != nil {
		plan.Error = err.Error()
		return plan
	}
	return plan
}

func explainableSQL(query string) bool {
	query = strings.TrimSpace(query)
	switch sqlOperation(query) {
	case "SELECT", "INSERT", "UPDATE", "DELETE", "WITH":
	default:
		return false
	}
	query = strings.TrimSuffix(query, ";")
	return !strings.Contains(query, ";")
}

func explainCommand(driverName, query string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(driverName)) {
	case "pg", "postgres", "postgresql", "pgx":
		return "EXPLAIN (FORMAT TEXT) " + query, nil
	case "sqlite", "sqlite3":
		return "EXPLAIN QUERY PLAN " + query, nil
	case "mysql", "mariadb":
		return "EXPLAIN " + query, nil
	default:
		return "", fmt.Errorf("webpprof: EXPLAIN is not supported for GORM dialector %q", driverName)
	}
}

func readSQLPlan(rows *sql.Rows, maxRows, maxBytes int) (string, error) {
	columns, err := rows.Columns()
	if err != nil {
		return "", err
	}
	values := make([]any, len(columns))
	destinations := make([]any, len(columns))
	var output strings.Builder
	for row := 0; rows.Next(); row++ {
		if row >= maxRows {
			return output.String() + "\n…", nil
		}
		for index := range values {
			destinations[index] = &values[index]
		}
		if err := rows.Scan(destinations...); err != nil {
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
	if err := rows.Err(); err != nil {
		return output.String(), err
	}
	return output.String(), nil
}

func formatPlanRow(columns []string, values []any) string {
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

func planValue(value any) string {
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
