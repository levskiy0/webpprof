# Event and entity reference

All loggable entities can be recorded manually. Context-aware forms attach the
event to the current request; focused profiler packages record the same
contracts automatically.

## What is recorded

| Entity | Manual API | Automatic profiler | Recorded data |
| --- | --- | --- | --- |
| HTTP request | `LogRequest` | HTTP frameworks, gRPC | Scheme, method, real path, route, headers, bodies, status, sizes, duration, error |
| Middleware | `LogMiddlewareContext` | `http`, Gin | Name, state, inclusive duration, error |
| SQL query | `LogQueryContext`, `StartQuery` | Bun, GORM, pgx, `database/sql`, OTel | Connection, operation, SQL, rows, duration, callsite, optional EXPLAIN, error |
| Cache | `LogCacheContext` | go-cache, go-redis | Store, operation, key, hit, TTL, duration, error |
| Job | `LogJobContext` | go-queue, Asynq | Name, queue, state, attempts, duration, error |
| Log | `LogLogContext` | `slog`, Zap, zerolog | Level, message, structured fields, stack |
| Mail | `LogEmailContext` | email, go-mail | Transport, recipients, subject, state, duration, error |
| Outgoing HTTP | `LogHTTPCallContext` | `http` | Method, URL, headers, status, size, duration, error |
| Schedule | `LogScheduleContext` | schedule | Name, planned time, state, payload, duration, error or panic |
| Exception | `LogExceptionContext` | HTTP/Gin panic recovery | Type, message, stack |
| Custom event | `LogEventContext`, `StartEvent`, `Measure` | — | Kind, name, status, summary, fields, error |

## `Meta` contract

Every loggable entity embeds `webpprof.Meta`:

```go
type Meta struct {
    ID              string
    RequestID       string
    ParentID        string
    OriginRequestID string
    Process         string
    Instance        string
    StartedAt       time.Time
    Duration        time.Duration
    Tags            map[string]string
}
```

webpprof accepts zero values, so primary fields are a usage contract rather
than runtime validation.

| Field | Contract |
| --- | --- |
| `ID` | Unique entity ID; generated when empty. |
| `RequestID` | Related request ID; `Log*Context` fills it from the request capture. |
| `ParentID` | Optional immediate parent operation. |
| `OriginRequestID` | Optional originating request across a background or distributed boundary. |
| `Process` / `Instance` | Optional worker, service, or instance identity. |
| `StartedAt` | Operation start; defaults to current UTC time when empty. |
| `Duration` | Completed duration, normally `time.Since(started)`. |
| `Tags` | Searchable metadata; never put secrets here. |

Named middleware propagates its invocation ID as the current parent. A custom
integration can build the same tree explicitly:

```go
operationID := webpprof.NewID()
ctx = webpprof.WithParentEntry(ctx, operationID)

profiler.LogQueryContext(ctx, webpprof.Query{
    Meta: webpprof.Meta{StartedAt: startedAt, Duration: elapsed},
    SQL:  "SELECT ...",
})
```

## Logging forms

Choose the form based on profiler and request ownership:

| Form | Use it when |
| --- | --- |
| `LogQuery(entity)` | The entity is standalone or already has `Meta.RequestID`; use the default profiler. |
| `LogQueryContext(ctx, entity)` | The entity belongs to the current request; use the default profiler. |
| `p.LogQuery(entity)` | Keep an explicit `*Profiler` for a standalone entity. |
| `p.LogQueryContext(ctx, entity)` | Keep an explicit profiler and inherit request correlation. |

Replace `Query` with any supported entity type. Package functions are safe
no-ops before a default profiler is configured. Context-aware calls are also
no-ops when the context is wrapped with `webpprof.WithoutRecording(ctx)`.

## Measuring custom operations

Use `Measure` when an application service or unsupported dependency returns an
error. It creates one custom Event and returns the same timing and error for
application metrics or error handling:

```go
measurement := profiler.Measure(r.Context(), webpprof.Event{
    Kind:    "service",
    Name:    "players.refresh",
    Summary: "Refresh cached player data",
    Meta: webpprof.Meta{Tags: map[string]string{
        "tenant": "acme",
    }},
}, func(ctx context.Context) error {
    // Use this child context. Nested SQL/cache/HTTP entries get the custom
    // Event as ParentID and therefore appear beneath it in the timeline.
    return players.Refresh(ctx)
})

metrics.Record(measurement.Failed(), measurement.Duration)
if measurement.Err != nil {
    return measurement.Err
}
```

Successful events default to `succeeded`; errors default to `failed` and fill
`Event.Error`. A panic is stored as `panicked` with its type and stack, then
re-panicked so application recovery semantics do not change.

Use `MeasureValue` with the default profiler, or `MeasureValueWith` with an
explicit profiler, for `func(context.Context) (T, error)`. Use `StartEvent`,
`span.Context()`, and `FinishResult` when fields or status depend on the result:

```go
span := profiler.StartEvent(ctx, webpprof.Event{
    Kind: "feature-store",
    Name: "lookup",
})
value, err := client.Lookup(span.Context(), key)
measurement := span.FinishResult(webpprof.EventResult{
    Status: "found",
    Fields: map[string]any{"key": key, "found": value != ""},
    Err:    err,
})
```

`Finish` and `FinishResult` are idempotent. They measure even when profiling is
disabled, but do not create an Event when there is no profiler or the context
uses `WithoutRecording`.

## Entity fields

| Entity | Logging API | Primary fields | Useful optional fields |
| --- | --- | --- | --- |
| `Request` | `LogRequest`; normally middleware or `BeginRequest`/`Finish` | `Method`, `Path`, `Status` | `Scheme`, `Route`, `Query`, sizes, messages, `Error` |
| `Query` | `LogQuery`, `LogQueryContext`, `StartQuery` | `SQL` | Connection, driver, database, operation, rows, callsite, plan, error |
| `Email` | `LogEmail`, `LogEmailContext` | `From`, `To`, `Subject` | Transport, CC/BCC, text/HTML, status, callsite, error |
| `Cache` | `LogCache`, `LogCacheContext` | `Operation`, `Key`, `Hit` | Store, TTL, size, value, truncation, callsite, error |
| `Job` | `LogJob`, `LogJobContext` | `Name`, `State` | Queue, connection, attempts, availability, wait, arguments, callsite, error |
| `Log` | `LogLog`, `LogLogContext` | `Message` | Level, fields, stack |
| `HTTPCall` | `LogHTTPCall`, `LogHTTPCallContext` | `Method`, `URL` | Status, request/response, size, callsite, error |
| `Schedule` | `LogSchedule`, `LogScheduleContext` | `Name`, `State` | Planned time, payload, callsite, error, panic |
| `Exception` | `LogException`, `LogExceptionContext` | `Message` | Type, stack; use `PanicException(recovered)` for panics |
| `Event` | `LogEvent`, `LogEventContext`, `StartEvent`, `Measure` | `Kind`, `Name` | Status, summary, fields, error |
| `Middleware` | `LogMiddleware`, `LogMiddlewareContext` | `Name`, `State` | Inclusive duration, error |

`State`, `Status`, `Kind`, and `Operation` are free-form. Keep vocabularies
stable within an integration, for example `dispatched`, `succeeded`, and
`failed` for jobs or `get`, `set`, and `delete` for cache operations.

## Nested values

| Value | Fields and meaning |
| --- | --- |
| `HTTPMessage` | Headers, content type, body, original byte size, and truncation flag. |
| `Address` | Optional display name and primary email; used by mail addresses. |
| `Argument` | Optional name, type, rendered value, original size, and truncation flag. |
| `SourceFrame` | Function, file, line, and optional editor/source-browser URL. |
| `QueryPlan` | Plain EXPLAIN command, format, text, lookup duration, and plan-only error. |

Durations serialize as nanoseconds (`duration_ns`, `ttl_ns`, and `wait_ns`).
`Query.RowsAffected` is a pointer so an actual zero differs from an unknown
value.

## Request-related operations

Use the context form inside a request:

```go
func loadUser(ctx context.Context, id int64) error {
    started := time.Now().UTC()

    var name string
    err := db.QueryRowContext(ctx, "SELECT name FROM users WHERE id = ?", id).Scan(&name)
    errorMessage := ""
    if err != nil {
        errorMessage = err.Error()
    }

    webpprof.LogQueryContext(ctx, webpprof.Query{
        Meta: webpprof.Meta{
            StartedAt: started,
            Duration:  time.Since(started),
            Tags:      map[string]string{"repository": "users"},
        },
        Connection: "primary",
        Operation:  "select",
        SQL:        "SELECT name FROM users WHERE id = ?",
        Error:      errorMessage,
    })
    return err
}
```

`StartQuery` provides a finish contract:

```go
span := webpprof.StartQuery(ctx, webpprof.Query{
    Connection: "primary",
    SQL:        "UPDATE users SET active = ? WHERE id = ?",
})

result, err := db.ExecContext(ctx, "UPDATE users SET active = ? WHERE id = ?", true, 42)
if err != nil {
    span.Finish(err)
    return err
}

rows, rowsErr := result.RowsAffected()
span.FinishRows(rows, rowsErr)
```

## Background and dispatched operations

Use a non-context form for work executing outside the original HTTP request.
Set `OriginRequestID` when a request caused the background operation:

```go
webpprof.LogJob(webpprof.Job{
    Meta: webpprof.Meta{
        OriginRequestID: originRequestID,
        StartedAt:       started,
        Duration:        time.Since(started),
        Tags:            map[string]string{"tenant": tenantID},
    },
    Name:       "emails.send-welcome",
    Queue:      "mail",
    Connection: "redis",
    State:      "dispatched",
    Attempt:    1,
    Arguments: []webpprof.Argument{
        {Name: "user_id", Type: "int64", Value: "42"},
    },
})
```

Embedded entities in a manually logged `Request` are normalized into separate,
addressable entries and linked back to that request. Prefer middleware or
`BeginRequest`/`WithRequest`/`Finish` for live collection.

All payloads pass through configured redaction before storage. Integrations
capturing bodies or values must enforce their limit and set `Truncated`. The
[bundled example](../example/main.go) emits every entity type.
