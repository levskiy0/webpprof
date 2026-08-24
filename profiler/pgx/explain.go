package pgx

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/levskiy0/webpprof"
)

const (
	defaultExplainTimeout  = 750 * time.Millisecond
	defaultExplainMaxRows  = 100
	defaultExplainMaxBytes = 64 << 10
)

type explainContextKey struct{}

type explainRunner func(context.Context, *pgx.Conn, string, []any, int, int) (string, error)

type planRows interface {
	Next() bool
	Values() ([]any, error)
	Err() error
	Close()
}

func (p *queryProfiler) explain(ctx context.Context, conn *pgx.Conn, query string, args []any) *webpprof.QueryPlan {
	if !p.config.Explain || !explainableSQL(query) {
		return nil
	}
	command := "EXPLAIN (FORMAT TEXT) " + query
	plan := &webpprof.QueryPlan{Command: compactSQL(command), Format: "text"}
	if conn == nil && p.explainRunner == nil {
		plan.Error = "webpprof: pgx connection is unavailable for EXPLAIN"
		return plan
	}
	timeout := p.config.ExplainTimeout
	if timeout <= 0 {
		timeout = defaultExplainTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	maxRows := p.config.ExplainMaxRows
	if maxRows <= 0 {
		maxRows = defaultExplainMaxRows
	}
	runner := p.explainRunner
	if runner == nil {
		runner = runExplain
	}
	startedAt := time.Now()
	plan.Text, plan.Error = runPlan(runner, ctx, conn, command, args, maxRows, defaultExplainMaxBytes)
	plan.Duration = time.Since(startedAt)
	return plan
}

func runPlan(runner explainRunner, ctx context.Context, conn *pgx.Conn, command string, args []any, maxRows, maxBytes int) (string, string) {
	text, err := runner(ctx, conn, command, args, maxRows, maxBytes)
	if err != nil {
		return text, err.Error()
	}
	return text, ""
}

func runExplain(ctx context.Context, conn *pgx.Conn, command string, args []any, maxRows, maxBytes int) (string, error) {
	// Mark the nested plan query so this tracer does not record it recursively.
	rows, err := conn.Query(context.WithValue(ctx, explainContextKey{}, true), command, args...)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	return readPGXPlan(rows, maxRows, maxBytes)
}

func readPGXPlan(rows planRows, maxRows, maxBytes int) (string, error) {
	var output strings.Builder
	for row := 0; rows.Next(); row++ {
		if row >= maxRows {
			return output.String() + "\n…", nil
		}
		values, err := rows.Values()
		if err != nil {
			return output.String(), err
		}
		line := formatPlanValues(values)
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

func formatPlanValues(values []any) string {
	if len(values) == 1 {
		return planValue(values[0])
	}
	parts := make([]string, len(values))
	for index, value := range values {
		parts[index] = fmt.Sprintf("column_%d=%s", index+1, planValue(value))
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

func isExplainContext(ctx context.Context) bool {
	explain, _ := ctx.Value(explainContextKey{}).(bool)
	return explain
}
