# SQL profiling: callsites, EXPLAIN, and replay

webpprof can attach Go source frames and plain database query plans to captured
SQL. The browser also generates a Go replay skeleton from the stored statement.

## Operation callsites

Query callsites are captured by default. Use `WithCallsiteKinds` to replace that
default with an explicit set of operation types:

```go
profiler := webpprof.New(
    mux,
    webpprof.WithCallsiteKinds(
        webpprof.KindQuery,
        webpprof.KindCache,
        webpprof.KindEmail,
        webpprof.KindJob,
        webpprof.KindHTTPCall,
        webpprof.KindSchedule,
        webpprof.KindCallable,
        webpprof.KindTask,
    ),
    webpprof.WithSourceLink(func(frame webpprof.SourceFrame) string {
        return fmt.Sprintf("vscode://file/%s:%d", frame.File, frame.Line)
    }),
)
```

The viewer shows captured frames in a **Callsite** panel. A source-link callback
can point to an editor or source browser. `WithCallsiteKinds` without arguments
disables all automatic callsite capture. The older
`WithQueryCallsite(false)` option remains available for compatibility.

Callsite capture uses `runtime.Callers`, so enable only operation types where
the allocation and stored paths are useful. Builds using `-trimpath` store
trimmed paths; editor links must map them back to the local checkout.

## Plain SQL EXPLAIN

The `database/sql`, Bun, GORM, and native pgx profilers can execute a real plan.
It is disabled by default, and all four integrations use the same controls:

```go
profiledConnector := webpprofsql.ProfileConnectorWith(
    profiler,
    connector,
    webpprofsql.Config{
        Connection:     "primary",
        Driver:         "postgresql", // postgresql/pgx, sqlite/sqlite3, mysql/mariadb
        Database:       "app",
        Explain:        true,
        ExplainTimeout: 500 * time.Millisecond,
        ExplainMaxRows: 100,
    },
)
db := sql.OpenDB(profiledConnector)
```

Set `Explain`, `ExplainTimeout`, and `ExplainMaxRows` on
`webpprofsql.Config`, `webpprofbun.Config`, `webpprofgorm.Config`, or
`webpprofpgx.Config`. One `SELECT`, `INSERT`, `UPDATE`, `DELETE`, or `WITH`
statement is eligible. webpprof runs a driver-specific plain `EXPLAIN`, never
`EXPLAIN ANALYZE`, and records plan duration separately. Plain EXPLAIN plans a
write without applying it. A plan failure never replaces `Query.Error` or
changes the original database result.

GORM's `Row`/`Rows` callback is deliberately excluded because the returned
`*sql.Row` or `*sql.Rows` can still own the active connection. OTel query spans
cannot be explained automatically because a completed span has neither a
database handle nor bind arguments.

For SQLite, PostgreSQL/pgx, and MySQL/MariaDB, the profiler also stores a small
normalized `QueryPlan.Issues` list. The analyzer understands full scans,
explicit temporary sorts, and estimates of at least 10,000 rows. It only raises
full-scan or sort findings when the recorded query took at least 100 ms, so a
fast scan of a small table is not labeled a bottleneck. These are hints, not a
replacement for database-specific plan review.

For `database/sql` queries, duration ends when the returned rows reach EOF,
return an iteration error, or are closed. It therefore includes row decoding
and driver streaming time instead of measuring only the initial `Query` call.
Always close rows and check `rows.Err()`.

Use EXPLAIN only with development or read-only credentials. Plans may expose
schema names, indexes, predicates, and other sensitive database details.

## Custom query plans and frames

An integration can populate the same contract directly:

```go
planText := "Index Scan using players_pkey on players ..."
webpprof.LogQueryContext(ctx, webpprof.Query{
    SQL: "SELECT id FROM players WHERE id = ?",
    Callsite: []webpprof.SourceFrame{{
        Function: "players.(*Repository).Find",
        File:     "/workspace/players/repository.go",
        Line:     42,
    }},
    Plan: &webpprof.QueryPlan{
        Command: "EXPLAIN SELECT id FROM players WHERE id = ?",
        Format:  "text",
        Text:    planText,
        Issues:  webpprof.DetectQueryPlanIssues("postgresql", planText),
    },
})
```

## Go replay

The browser generates the **Go replay** card from captured SQL. Bind argument
values are never persisted, so placeholders remain explicit `TODO` values. This
prevents credentials or personal data from being copied into profiler storage.

See [Integrations](integrations.md#sql-pgx-gorm-and-bun) for database setup and
[Event reference](event-reference.md) for the `Query` contract.
