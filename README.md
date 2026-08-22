# webpprof

[![Go Reference](https://pkg.go.dev/badge/github.com/levskiy0/webpprof.svg)](https://pkg.go.dev/github.com/levskiy0/webpprof)
[![CI](https://github.com/levskiy0/webpprof/actions/workflows/ci.yml/badge.svg)](https://github.com/levskiy0/webpprof/actions/workflows/ci.yml)

[https://github.com/levskiy0/webpprof](https://github.com/levskiy0/webpprof)

`webpprof` is a Telescope-like, in-memory application profiler for Go. It collects related application events in a searchable UI with live WebSocket updates. It is intended for development and short investigations, not durable storage.

![webpprof query details with a correlated request and highlighted SQL](docs/images/webpprof-query-details.png)

## Install

```sh
go get github.com/levskiy0/webpprof
```

## Local development

Run the bundled example from the repository root:

```sh
go run ./example
```

Open [http://127.0.0.1:3030/](http://127.0.0.1:3030/), generate a successful,
failed, or panic request, then inspect it at
[http://127.0.0.1:3030/debug/webpprof/](http://127.0.0.1:3030/debug/webpprof/).
The example generates every related event type without external services. See
[example/README.md](example/README.md) for details.

## Quick start

Initialize webpprof once in the composition root. The variables below represent
dependencies already created by the application:

```go
handler := http.Handler(applicationHandler)

if enabled {
    // Start a private profiler server. Keep it on loopback or a private network.
    profiler, err := webpprof.Start(
        "127.0.0.1:6061",
        webpprof.WithToken(token),
        webpprof.WithExcludedRequests("GET /health", "GET *.js", "GET *.webp"),
    )
    if err != nil {
        return err
    }

    // webpprof owns only its server and in-memory store. The application still
    // owns and closes db, cache, queue, mailClient, and the HTTP client.
    defer profiler.Shutdown(context.Background())

    // Wrap shared dependencies once, before injecting them into handlers.
    db = webpprofbun.ProfileWith(profiler, db, webpprofbun.Config{
        Connection: "default",
        Driver:     databaseDriver,
        Database:   databaseName,
    })
    cache = webpprofgocache.ProfileWith(profiler, cache, "redis")
    queue = webpprofgoqueue.ProfileWith(profiler, queue, "default")
    logger = slog.New(webpprofslog.ProfileWith(profiler, logger.Handler()))
    client.Transport = webpprofhttp.ProfileTransportWith(profiler, client.Transport)
    mailClient = webpprofgomail.ProfileWith(profiler, mailClient)

    // The middleware puts the request correlation ID into r.Context().
    handler = webpprofhttp.MiddlewareWith(profiler, handler)

    log.Printf("webpprof: %s", profiler.URL())
}
```

Open `profiler.URL()`, normally `http://127.0.0.1:6061/debug/webpprof/`, and enter the token.

The request detail card can render and copy the captured request as payload,
headers, raw HTTP, or a ready-to-run cURL command. Exported values use the
already redacted and size-limited capture; a truncated body cannot reproduce
the complete original request.

When profiling is disabled, do not call `Start`, `New`, or any profiler wrapper. Package-level `Log*` functions are safe no-ops without an active profiler.

Use `webpprof.New(router, options...)` when the application owns the HTTP server. `Start` creates a private listener.

## Request correlation

Related tabs are built from the request ID carried by `context.Context`:

```mermaid
flowchart LR
    A["Incoming HTTP request"] --> B["webpprof middleware"]
    B --> C["r.Context() with request ID"]
    C --> D["SQL / cache / mail / logs"]
    C --> E["job dispatch / HTTP client / custom events"]
    C --> F["exceptions"]
    D --> G["Request → Related tabs"]
    E --> G
    F --> G
```

Pass the handler context through each operation. This example intentionally
shows the calls that are easy to miss:

```go
func createPlayer(w http.ResponseWriter, r *http.Request) {
    ctx := r.Context() // Contains the webpprof request capture.

    // Bun/database/sql, go-redis, slog, mail, and the HTTP transport read the
    // correlation ID from the context passed to their normal APIs.
    if err := db.NewSelect().Model(&player).Where("id = ?", 42).Scan(ctx); err != nil {
        recordException(ctx, err)
        http.Error(w, "database error", http.StatusInternalServerError)
        return
    }
    logger.InfoContext(ctx, "player loaded", "player_id", player.ID)
    _ = mailClient.DialAndSendWithContext(ctx, welcomeMessage)

    request, _ := http.NewRequestWithContext(ctx, http.MethodGet, avatarURL, nil)
    _, _ = client.Do(request)

    // go-cache has context-free methods, so select a context-bound view first.
    requestCache := cache.WithContext(ctx)
    _ = requestCache.Put("player:42", player, time.Minute)

    // go-queue's Task.Dispatch method has no context parameter. JobContext (or
    // ChainContext) carries the current request into the dispatch event.
    task := webpprofgoqueue.JobContext(ctx, queue, sendWelcomeEmailJob, nil)
    if err := task.OnQueue("mail").Dispatch(); err != nil {
        recordException(ctx, err)
    }

    // Manual events must use a Context suffix to appear under this request.
    webpprof.LogEventContext(ctx, webpprof.Event{
        Kind:    "player",
        Name:    "created",
        Summary: "Player 42 was created",
    })
}

func recordException(ctx context.Context, err error) {
    webpprof.LogExceptionContext(ctx, webpprof.Exception{
        Type:    fmt.Sprintf("%T", err),
        Message: err.Error(),
        Stack:   string(debug.Stack()),
    })
}
```

The HTTP and Gin middleware automatically record recovered panics as related
exceptions, including a stack trace, and then propagate the panic as before.

### Tags and the tag watcher

Use `WithTags` once near the start of a request. The tags are added to the
request and inherited by every `Log*Context` entity, including entities emitted
by integrations. Entity-specific `Meta.Tags` replace inherited values with the
same key.

```go
func tagRequest(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        ctx := webpprof.WithTags(r.Context(), map[string]string{
            "tenant":      tenantFromRequest(r),
            "environment": "development",
        })
        next.ServeHTTP(w, r.WithContext(ctx))
    })
}

// The request capture must be outside the tagging middleware. WithTags then
// updates that capture and passes the tags to downstream operations.
handler := webpprofhttp.MiddlewareWith(profiler, tagRequest(applicationHandler))
```

Open **Tags** in the profiler header, search by key or value, and select
`tenant=acme`, for example. The panel renders at most five matching tags; with
an empty search it shows the five most frequently recorded tags.
The watcher applies globally to navigation counts, tables, request relations,
dashboard event metrics, and live WebSocket updates. Multiple selected tags use
`AND` semantics. The selection is stored as repeated `tag` parameters in the
URL, so the watched view can be bookmarked or shared.

The events API accepts the same predicate:

```text
GET /debug/webpprof/api/events?tag=tenant%3Dacme&tag=environment%3Ddevelopment
```

A `tag=tenant` predicate matches any entity containing that key. A
`tag=tenant%3Dacme` predicate also requires the exact value. Tags are searchable
metadata and must not contain secrets. Keep tag keys non-empty and do not use
`=` inside a key because `key=value` is the watcher and API filter syntax.

### HTTP middleware timing

Go cannot discover names from an already composed middleware chain. Wrap each
middleware you want to see with `ProfileMiddlewareWith`. The request profiler
must remain the outer wrapper so middleware entries receive the request ID.

```go
profiled := webpprofhttp.ProfileMiddlewareWith(profiler, "authentication", authentication)(
    webpprofhttp.ProfileMiddlewareWith(profiler, "rate-limit", rateLimit)(
        applicationHandler,
    ),
)
handler := webpprofhttp.MiddlewareWith(profiler, profiled)
```

For Gin, register the request profiler before named middleware:

```go
router.Use(webpprofgin.MiddlewareWith(profiler))
router.Use(webpprofgin.ProfileMiddlewareWith(profiler, "authentication", authentication))
```

Middleware duration is inclusive: it includes the named middleware and the
downstream handlers it invokes. Panicking middleware is recorded with state
`panicked`, then the panic is propagated.

| Integration or API | How to keep the event related |
| --- | --- |
| Bun, `database/sql`, go-redis, email, gomail, slog, outgoing HTTP | Pass `r.Context()` to the normal operation |
| `go-cache` | Call `cache.WithContext(r.Context())` before the operation |
| `go-queue` dispatch | Build the task with `JobContext` or `ChainContext` |
| Manual events | Use `LogQueryContext`, `LogCacheContext`, `LogEmailContext`, `LogJobContext`, and the other `Log*Context` helpers |
| Background work | Use a non-context API; the event is intentionally shown as standalone |

For a custom request adapter, use `BeginRequest`, put the capture into the
operation context with `WithRequest`, and call `Finish` once the request ends:

```go
capture := profiler.BeginRequest(webpprof.Request{
    Method: "RPC",
    Path:   "/players.Create",
})
ctx = webpprof.WithRequest(ctx, capture)

// Every Log*Context call made with ctx is buffered under this request.
webpprof.LogCacheContext(ctx, webpprof.Cache{
    Store:     "redis",
    Operation: "get",
    Key:       "player:42",
    Hit:       true,
})

capture.Finish(webpprof.RequestResult{Status: http.StatusOK})
```

## What is recorded

| Entity | Manual API | Automatic profiler | Recorded data |
| --- | --- | --- | --- |
| HTTP request | `LogRequest` | `http`, `gin` | Scheme, method, real path, route, headers, bodies, status, sizes, duration, error |
| Middleware | `LogMiddlewareContext` | `http`, `gin` (`ProfileMiddlewareWith`) | Name, state, inclusive duration, error |
| SQL query | `LogQueryContext`, `StartQuery` | `bun`, `sql`, `otel` | Connection, driver, database, operation, SQL, rows, duration, error |
| Cache | `LogCacheContext` | `gocache`, `goredis` | Store, operation, key, hit, TTL, duration, error |
| Job | `LogJobContext` | `goqueue` (`JobContext`/`ChainContext` for related dispatches) | Name, queue, connection, state, attempts, duration, error |
| Log | `LogLogContext` | `slog`, `zap` | Level, message, structured fields, stack |
| Mail | `LogEmailContext` | `email`, `gomail` | Transport, sender, recipients, subject, state, duration, error |
| Outgoing HTTP | `LogHTTPCallContext` | `http` | Method, URL, headers, status, size, duration, error |
| Schedule | `LogScheduleContext` | `schedule` | Name, planned time, state, payload, duration, error or panic |
| Exception | `LogExceptionContext` | `http`, `gin` (panics) | Type, message, stack |
| Custom event | `LogEventContext` | — | Kind, name, status, summary, fields, error |

## Entity logging contract

Every loggable entity embeds `webpprof.Meta`. webpprof does not reject zero values, so the fields described as **primary** below are a usage contract rather than runtime validation: populate them to get meaningful rows, filters, and details in the UI.

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

| `Meta` field | Contract |
| --- | --- |
| `ID` | Unique entity ID. Generated automatically when empty. |
| `RequestID` | ID of the related request. `Log*Context` fills it automatically when the context contains a request capture. |
| `ParentID` | Optional immediate parent operation, for example the job or event that caused this entity. |
| `OriginRequestID` | Optional original request across a background or distributed boundary. |
| `Process` / `Instance` | Optional worker, service, or instance identity. |
| `StartedAt` | Operation start time. Defaults to the current UTC time when empty. |
| `Duration` | Completed operation duration. Use `time.Since(started)` after the operation finishes. |
| `Tags` | Searchable integration-specific metadata. Do not put secrets here. |

Choose the logging form according to ownership:

| Form | Use it when |
| --- | --- |
| `LogQuery(entity)` | The entity is standalone or you set `Meta.RequestID` yourself. It uses the default profiler. |
| `LogQueryContext(ctx, entity)` | The entity belongs to the current request. Replace `Query` with any supported entity name. |
| `p.LogQuery(entity)` | You keep an explicit `*Profiler` instead of using the package default. |
| `p.LogQueryContext(ctx, entity)` | You need both an explicit profiler and automatic request correlation. |

Package-level functions are safe no-ops before a default profiler is configured. Context-aware calls also become no-ops for a context wrapped with `webpprof.WithoutRecording(ctx)`.

### Entity fields

| Entity | Logging API | Primary fields | Useful optional fields |
| --- | --- | --- | --- |
| `Request` | `LogRequest`; normally `webpprofhttp.MiddlewareWith` or `BeginRequest` / `Finish` | `Method`, `Path`, `Status` | `Scheme`, `Route`, `Query`, sizes, `Request`, `Response`, `Error` |
| `Query` | `LogQuery`, `LogQueryContext`, `StartQuery` | `SQL` | `Connection`, `Driver`, `Database`, `Operation`, `RowsAffected`, `Error` |
| `Email` | `LogEmail`, `LogEmailContext` | `From`, `To`, `Subject` | `Transport`, `CC`, `BCC`, `Text`, `HTML`, `Status`, `Error` |
| `Cache` | `LogCache`, `LogCacheContext` | `Operation`, `Key`, `Hit` | `Store`, `TTL`, `Size`, `Value`, `Truncated`, `Error` |
| `Job` | `LogJob`, `LogJobContext` | `Name`, `State` | `Queue`, `Connection`, attempts, `AvailableAt`, `Wait`, `Arguments`, `Error` |
| `Log` | `LogLog`, `LogLogContext` | `Message` | `Level`, `Fields`, `Stack` |
| `HTTPCall` | `LogHTTPCall`, `LogHTTPCallContext` | `Method`, `URL` | `Status`, `Request`, `Response`, `ResponseSize`, `Error` |
| `Schedule` | `LogSchedule`, `LogScheduleContext` | `Name`, `State` | `PlannedAt`, `Payload`, `Error`, `Panic` |
| `Exception` | `LogException`, `LogExceptionContext` | `Message` | `Type`, `Stack`; use `PanicException(recovered)` for recovered panics |
| `Event` | `LogEvent`, `LogEventContext` | `Kind`, `Name` | `Status`, `Summary`, `Fields`, `Error` |
| `Middleware` | `LogMiddleware`, `LogMiddlewareContext` | `Name`, `State` | Inclusive `Duration`, `Error` |

`State`, `Status`, `Kind`, and `Operation` are free-form strings: the core stores them unchanged. Keep their vocabulary stable inside an integration—for example `dispatched`, `succeeded`, `failed` for jobs and `get`, `set`, `delete` for cache operations.

Nested values have their own small contracts:

| Value | Fields and meaning |
| --- | --- |
| `HTTPMessage` | `Headers`, `ContentType`, `Body`, original byte `Size`, and `Truncated` when only part of the body is stored. |
| `Address` | Optional display `Name` and the primary `Email` value. Used by `Email.From`, `To`, `CC`, and `BCC`. |
| `Argument` | Optional `Name`, Go or domain `Type`, rendered `Value`, original byte `Size`, and `Truncated`. Used by `Job.Arguments`. |

Durations are Go `time.Duration` values and are serialized as nanoseconds (`duration_ns`, `ttl_ns`, and `wait_ns`). `Query.RowsAffected` is a pointer so that an actual zero can be distinguished from an unknown value.

### Request-related operation

Use the context form inside an HTTP handler. The middleware-provided context attaches the entity to the request even though the entity may be completed before the request itself:

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
        Error:      errorMessage, // Keep error text safe; never include credentials.
    })

    return err
}
```

For queries, `StartQuery` records the start time and provides a convenient finish contract:

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

### Background or dispatched operation

A job running outside the original HTTP context should use the non-context form. Carry `OriginRequestID` explicitly when a request caused the job:

```go
func recordDispatch(originRequestID, tenantID string, started time.Time) {
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
}
```

When logging a `Request` manually, its embedded `Queries`, `Emails`, `Cache`, `Jobs`, `Logs`, `HTTPCalls`, `Schedules`, `Exceptions`, `Events`, and `Middlewares` are normalized into separately addressable entries and linked to that request. Prefer middleware or `BeginRequest` / `WithRequest` / `Finish` for live request collection.

All entity payloads pass through the configured redaction rules before storage. Integrations that capture bodies or values are responsible for applying their size limit and setting the corresponding `Truncated` flag. A runnable example that emits every entity type is available in [`example/main.go`](example/main.go).

## Available profilers

Each profiler is a separate package. Importing the core does not pull every integration into the build.

| Package | Use | Integration point |
| --- | --- | --- |
| `profiler/http` | `Middleware`, `ProfileMiddleware`, `ProfileTransport` | `http.Handler`, HTTP middleware, `http.RoundTripper` |
| `profiler/gin` | `Middleware`, `MiddlewareWith`, `ProfileMiddlewareWith` | `gin.HandlerFunc` with the Gin route template |
| `profiler/bun` | `Profile` | Bun `QueryHook` |
| `profiler/sql` | `ProfileConnector`, `ProfileDriver` | `database/sql/driver` before creating the pool |
| `profiler/gocache` | `Profile` | `github.com/levskiy0/go-cache` cache and lock contracts |
| `profiler/goredis` | `Profile` | go-redis command and pipeline hooks |
| `profiler/goqueue` | `Profile`, `ProfileJobs`, `JobContext`, `ChainContext` | `github.com/levskiy0/go-queue` dispatch, execution, and queue statistics |
| `profiler/gomail` | `Profile` | `github.com/wneessen/go-mail` client |
| `profiler/email` | `Profile` | Dependency-neutral `Sender` contract |
| `profiler/slog` | `Profile` | Standard `slog.Handler` |
| `profiler/zap` | `Profile` | `zapcore.Core` |
| `profiler/schedule` | `Profile` | `func(context.Context)` callbacks |
| `profiler/otel` | `Profile`, `NewSpanProcessor` | OpenTelemetry SDK spans |

Profile dependencies in the composition root and inject the returned value. The application still owns and closes the original dependency. Use one profiler per operation path; combining Bun, SQL driver, and OpenTelemetry instrumentation on the same database records duplicates.

## Writing a profiler

A profiler adapts one dependency to the generic integration contract:

```go
type Integration[T any] interface {
    Name() string
    Profile(Scope, T) T
}
```

Keep the intercepted interface and wrapper inside the profiler package. Keep only shared event entities in the webpprof core.

For a complete runnable implementation, see [`example/customprofiler/profiler.go`](example/customprofiler/profiler.go) and its installation in [`example/main.go`](example/main.go).

```go
package acmeprofile

type Client interface {
    Call(context.Context, string) error
}

type ProfilerAcme struct{}

type profiledClient struct {
    inner    Client
    profiler *webpprof.Profiler
}

func (ProfilerAcme) Name() string {
    return "acme"
}

func (ProfilerAcme) Profile(scope webpprof.Scope, client Client) Client {
    if client == nil {
        return client
    }
    if current, ok := scope.Load(client); ok {
        if profiled, ok := current.(Client); ok {
            return profiled
        }
    }

    wrapped := &profiledClient{
        inner:    client,
        profiler: scope.Profiler(),
    }
    actual, _ := scope.LoadOrStore(client, Client(wrapped))
    if profiled, ok := actual.(Client); ok {
        return profiled
    }
    return wrapped
}

func (c *profiledClient) Call(ctx context.Context, endpoint string) error {
    startedAt := time.Now().UTC()
    err := c.inner.Call(ctx, endpoint)

    event := webpprof.HTTPCall{
        Meta: webpprof.Meta{
            StartedAt: startedAt,
            Duration:  time.Since(startedAt),
        },
        Method: "POST",
        URL:    endpoint,
    }
    if err != nil {
        event.Error = err.Error()
    }
    c.profiler.LogHTTPCallContext(ctx, event)
    return err
}

func Profile(client Client) Client {
    return webpprof.Profile(client, ProfilerAcme{})
}

func ProfileWith(profiler *webpprof.Profiler, client Client) Client {
    return webpprof.ProfileWith(profiler, client, ProfilerAcme{})
}

var _ webpprof.Integration[Client] = ProfilerAcme{}
var _ Client = (*profiledClient)(nil)
```

Lifecycle: start one webpprof runtime, build the real dependency, wrap it with `Profile` or `ProfileWith`, and inject the returned value. The wrapper delegates the operation and records it with a context-aware helper. `Scope.LoadOrStore` prevents duplicate installation. Shutdown closes only webpprof resources.

`Integration.Name()` must be stable and unique. Preserve the wrapped dependency's results, errors, and ownership. Keep third-party contracts inside the profiler package, not in the core.

Keep webpprof on loopback or a private network, set a token, and configure body capture and redaction for the data handled by the application.
