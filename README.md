# webpprof

[![Go Reference](https://pkg.go.dev/badge/github.com/levskiy0/webpprof.svg)](https://pkg.go.dev/github.com/levskiy0/webpprof)
[![CI](https://github.com/levskiy0/webpprof/actions/workflows/ci.yml/badge.svg)](https://github.com/levskiy0/webpprof/actions/workflows/ci.yml)

`webpprof` is a Telescope-like, in-memory application profiler for Go. It collects related application events in a searchable UI with live WebSocket updates. It is intended for development and short investigations, not durable storage.

## Install

```sh
go get github.com/levskiy0/webpprof
```

## Quick start

Initialize webpprof once and install profilers only when it is enabled:

```go
handler := http.Handler(applicationHandler)

if enabled {
    profiler, err := webpprof.Start(
        "127.0.0.1:6061",
        webpprof.WithToken(token),
        webpprof.WithExcludedRequests("GET /health", "GET *.js", "GET *.webp"),
    )
    if err != nil {
        return err
    }
    defer profiler.Shutdown(context.Background())

    handler = webpprofhttp.MiddlewareWith(profiler, handler)
    db = webpprofbun.Profile(db, webpprofbun.Config{
        Connection: "default",
        Driver:     databaseDriver,
        Database:   databaseName,
    })
    cache = webpprofgocache.Profile(cache, "redis")
    queue = webpprofgoqueue.Profile(queue, "default")
    logger = slog.New(webpprofslog.Profile(logger.Handler()))
    client.Transport = webpprofhttp.ProfileTransport(client.Transport)
    mailClient = webpprofgomail.Profile(mailClient)

    log.Printf("webpprof: %s", profiler.URL())
}
```

Open `profiler.URL()`, normally `http://127.0.0.1:6061/debug/webpprof/`, and enter the token.

When profiling is disabled, do not call `Start`, `New`, or any profiler wrapper. Package-level `Log*` functions are safe no-ops without an active profiler.

Use `webpprof.New(router, options...)` when the application owns the HTTP server. `Start` creates a private listener.

## What is recorded

| Entity | Manual API | Automatic profiler | Recorded data |
| --- | --- | --- | --- |
| HTTP request | `LogRequest` | `http`, `gin` | Method, real path, route, headers, bodies, status, sizes, duration, error |
| SQL query | `LogQueryContext`, `StartQuery` | `bun`, `sql`, `otel` | Connection, driver, database, operation, SQL, rows, duration, error |
| Cache | `LogCacheContext` | `gocache`, `goredis` | Store, operation, key, hit, TTL, duration, error |
| Job | `LogJobContext` | `goqueue` | Name, queue, connection, state, attempts, duration, error |
| Log | `LogLogContext` | `slog`, `zap` | Level, message, structured fields, stack |
| Mail | `LogEmailContext` | `email`, `gomail` | Transport, sender, recipients, subject, state, duration, error |
| Outgoing HTTP | `LogHTTPCallContext` | `http` | Method, URL, headers, status, size, duration, error |
| Schedule | `LogScheduleContext` | `schedule` | Name, planned time, state, duration, error or panic |
| Exception | `LogExceptionContext` | — | Type, message, stack |
| Custom event | `LogEventContext` | — | Kind, name, status, summary, fields, error |

Every entity embeds `webpprof.Meta`, which provides IDs, request correlation, timestamps, duration, process, instance, and tags.

Context-aware helpers attach the event to the current HTTP request:

```go
webpprof.LogCacheContext(ctx, webpprof.Cache{
    Meta:      webpprof.Meta{Duration: duration},
    Store:     "redis",
    Operation: "get",
    Key:       "player:42",
    Hit:       true,
})
```

Use the non-context variants for background work. `StartQuery` is available when timing must cover the actual operation:

```go
webpprof.LogJob(webpprof.Job{
    Name:  "SendWelcomeEmail",
    Queue: "mail",
    State: "succeeded",
})

span := webpprof.StartQuery(ctx, queryEvent)
_, err := db.ExecContext(ctx, query, args...)
span.Finish(err)
```

## Available profilers

Each profiler is a separate package. Importing the core does not pull every integration into the build.

| Package | Use | Integration point |
| --- | --- | --- |
| `profiler/http` | `Middleware`, `ProfileTransport` | `http.Handler`, `http.RoundTripper` |
| `profiler/gin` | `Middleware`, `MiddlewareWith` | `gin.HandlerFunc` with the Gin route template |
| `profiler/bun` | `Profile` | Bun `QueryHook` |
| `profiler/sql` | `ProfileConnector`, `ProfileDriver` | `database/sql/driver` before creating the pool |
| `profiler/gocache` | `Profile` | `github.com/levskiy0/go-cache` cache and lock contracts |
| `profiler/goredis` | `Profile` | go-redis command and pipeline hooks |
| `profiler/goqueue` | `Profile`, `ProfileJobs` | `github.com/levskiy0/go-queue` dispatch, execution, and queue statistics |
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
        return current.(Client)
    }

    wrapped := &profiledClient{
        inner:    client,
        profiler: scope.Profiler(),
    }
    actual, _ := scope.LoadOrStore(client, Client(wrapped))
    return actual.(Client)
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
