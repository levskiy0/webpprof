# Profiler integrations

Create each real dependency once, wrap or configure it in the composition root,
and inject the resulting value. The application retains ownership and closes
the original dependency as usual.

Prefer `...With(profiler, ...)` when keeping an explicit `*webpprof.Profiler`.
Short `Profile(...)` forms use `webpprof.Default()`.

## Core and standard-library profilers

These packages are part of the root module:

```sh
go get github.com/levskiy0/webpprof@latest
```

| Package | Connect it | Recorded data |
| --- | --- | --- |
| `profiler/http` | Wrap an `http.Handler`, named middleware, or `http.RoundTripper`. | Incoming requests, middleware, outgoing HTTP calls |
| `profiler/sql` | Wrap a `driver.Connector` or `driver.Driver` before `sql.OpenDB`. | Queries, callsites, optional plain EXPLAIN |
| `profiler/slog` | Wrap `slog.Handler` before constructing the logger. | Level, message, fields, request correlation |
| `profiler/email` | Wrap the dependency-neutral `Sender`. | Mail state, addresses, subject, duration, error |
| `profiler/schedule` | Wrap a named `func(context.Context)`. | Planned time, state, duration, error, panic |

```go
handler = webpprofhttp.MiddlewareWith(profiler, handler)
httpClient.Transport = webpprofhttp.ProfileTransportWith(profiler, httpClient.Transport)
logger = slog.New(webpprofslog.ProfileWith(profiler, logger.Handler()))
sender = webpprofemail.ProfileWith(profiler, sender)
cleanup = webpprofschedule.ProfileWith(profiler, "expired-sessions", cleanup)
```

Wrap `database/sql` before creating the pool:

```go
connector, err := driverWithConnector.OpenConnector(dsn)
if err != nil {
    return err
}
connector = webpprofsql.ProfileConnectorWith(profiler, connector, webpprofsql.Config{
    Connection: "primary",
    Driver:     "postgresql",
    Database:   "app",
})
db := sql.OpenDB(connector)
```

## Optional profiler modules

Every row is an independent Go module. Installing one integration does not add
SDKs from the other rows.

| Module | Install | Connect it |
| --- | --- | --- |
| `profiler/pgx` | `go get github.com/levskiy0/webpprof/profiler/pgx@latest` | Profile a copied `pgx.ConnConfig` or `pgxpool.Config` before connecting. |
| `profiler/gorm` | `go get github.com/levskiy0/webpprof/profiler/gorm@latest` | Install the GORM callback plugin. |
| `profiler/bun` | `go get github.com/levskiy0/webpprof/profiler/bun@latest` | Add the Bun query hook. |
| `profiler/gin` | `go get github.com/levskiy0/webpprof/profiler/gin@latest` | Register request or named middleware. |
| `profiler/chi` | `go get github.com/levskiy0/webpprof/profiler/chi@latest` | Register with `router.Use`; captures route patterns. |
| `profiler/echo` | `go get github.com/levskiy0/webpprof/profiler/echo@latest` | Register with `app.Use`; captures route patterns. |
| `profiler/fiber` | `go get github.com/levskiy0/webpprof/profiler/fiber@latest` | Register with `app.Use`; captures route patterns. |
| `profiler/grpc` | `go get github.com/levskiy0/webpprof/profiler/grpc@latest` | Add unary and stream interceptors to clients or servers. |
| `profiler/asynq` | `go get github.com/levskiy0/webpprof/profiler/asynq@latest` | Wrap enqueue calls and install worker middleware. |
| `profiler/nats` | `go get github.com/levskiy0/webpprof/profiler/nats@latest` | Wrap core publish and subscribe operations. |
| `profiler/kafka` | `go get github.com/levskiy0/webpprof/profiler/kafka@latest` | Wrap kafka-go writer and reader methods. |
| `profiler/gocache` | `go get github.com/levskiy0/webpprof/profiler/gocache@latest` | Wrap `github.com/levskiy0/go-cache` cache and locks. |
| `profiler/goredis` | `go get github.com/levskiy0/webpprof/profiler/goredis@latest` | Install go-redis command and pipeline hooks. |
| `profiler/goqueue` | `go get github.com/levskiy0/webpprof/profiler/goqueue@latest` | Wrap dispatch and execution; expose queue statistics. |
| `profiler/gomail` | `go get github.com/levskiy0/webpprof/profiler/gomail@latest` | Wrap a `github.com/wneessen/go-mail` client. |
| `profiler/zap` | `go get github.com/levskiy0/webpprof/profiler/zap@latest` | Wrap `zapcore.Core` before creating the logger. |
| `profiler/zerolog` | `go get github.com/levskiy0/webpprof/profiler/zerolog@latest` | Keep the returned logger and use `.Ctx(ctx)`. |
| `profiler/otel` | `go get github.com/levskiy0/webpprof/profiler/otel@latest` | Add the span processor to an OTel provider. |

## SQL: pgx, GORM, and Bun

```go
// Native pgx/pgxpool. Existing tracers are preserved through multitracer.
poolConfig, err := pgxpool.ParseConfig(databaseURL)
if err != nil {
    return err
}
poolConfig = webpprofpgx.ProfilePoolConfigWith(profiler, poolConfig, webpprofpgx.Config{
    Connection: "primary",
    Database:   "app",
})
pool, err := pgxpool.NewWithConfig(ctx, poolConfig)

// GORM callbacks cover create, query, update, delete, row, and raw operations.
if err := webpprofgorm.ProfileWith(profiler, gormDB, webpprofgorm.Config{
    Connection: "primary",
    Database:   "app",
}); err != nil {
    return err
}

// Bun uses a query hook. Do not also wrap its underlying database/sql driver.
bunDB = webpprofbun.ProfileWith(profiler, bunDB, webpprofbun.Config{
    Connection: "primary",
    Driver:     "postgresql",
    Database:   "app",
})
```

Pass the operation context to pgx and Bun. Use `gormDB.WithContext(ctx)` for
GORM. See [SQL profiling](sql-profiling.md) for callsites and EXPLAIN.

## HTTP frameworks

```go
ginRouter.Use(webpprofgin.MiddlewareWith(profiler))

// Chi middleware must be inside the router so RoutePattern is available.
chiRouter.Use(webpprofchi.MiddlewareWith(profiler))

echoApp.Use(webpprofecho.MiddlewareWith(profiler))
fiberApp.Use(webpproffiber.MiddlewareWith(profiler))
```

Framework middleware stores request capture in the operation context. Pass it
to database, cache, queue, logger, mail, and HTTP client calls. Do not stack a
framework request profiler and generic `net/http` request middleware around the
same handler.

## gRPC clients and servers

```go
server := grpc.NewServer(
    grpc.ChainUnaryInterceptor(webpprofgrpc.UnaryServerInterceptorWith(profiler)),
    grpc.ChainStreamInterceptor(webpprofgrpc.StreamServerInterceptorWith(profiler)),
)

connection, err := grpc.NewClient(target,
    grpc.WithChainUnaryInterceptor(webpprofgrpc.UnaryClientInterceptorWith(profiler)),
    grpc.WithChainStreamInterceptor(webpprofgrpc.StreamClientInterceptorWith(profiler)),
)
```

Server RPCs become request entries with `Method=GRPC` and the full method as the
route. Client RPCs become outgoing calls. Unary and streaming contexts
correlate downstream events like HTTP request contexts.

## Asynq

```go
rawClient := asynq.NewClient(redisOptions)
defer rawClient.Close()

queue := webpprofasynq.ProfileWith(profiler, rawClient, webpprofasynq.Config{
    Connection: "redis",
})
_, err := queue.EnqueueContext(ctx, asynq.NewTask("email:deliver", payload))

mux := asynq.NewServeMux()
mux.Use(webpprofasynq.MiddlewareWith(profiler, webpprofasynq.Config{
    Connection: "redis",
}))
mux.HandleFunc("email:deliver", handleEmail)
```

Enqueue and worker execution are separate job entries. Payload contents are not
stored by default, only their byte size. Enable `CapturePayload` with a bounded
`PayloadLimit` only for safe development data.

## NATS and kafka-go

```go
natsClient := webpprofnats.ProfileWith(profiler, rawNATS, webpprofnats.Config{
    Connection: "events",
})
err := natsClient.PublishContext(ctx, "players.created", payload)
_, err = natsClient.QueueSubscribe("players.created", "indexers", handleMessage)

writer := webpprofkafka.ProfileWriterWith(profiler, rawWriter, webpprofkafka.Config{
    Connection: "kafka",
    Topic:      "players",
})
err = writer.WriteMessages(ctx, kafka.Message{Key: playerID, Value: payload})

reader := webpprofkafka.ProfileReaderWith(profiler, rawReader, webpprofkafka.Config{
    Connection: "kafka",
    Topic:      "players",
    GroupID:    "search-index",
})
message, err := reader.ReadMessage(ctx)
```

The application retains the raw NATS connection for `Drain`, `Close`, and APIs
outside the focused wrapper. Message bodies are opt-in and bounded with
`CapturePayload`/`PayloadLimit` for NATS and `CaptureValue`/`ValueLimit` for
Kafka.

## Cache, queue, mail, logging, and OTel

```go
cache = webpprofgocache.ProfileWith(profiler, cache, "default")
webpprofgoredis.ProfileWith(profiler, redisClient, "redis")
queue = webpprofgoqueue.ProfileWith(profiler, queue, "default")
mailClient = webpprofgomail.ProfileWith(profiler, mailClient)

zapLogger := zap.New(webpprofzap.ProfileWith(profiler, zapCore))
zeroLogger = webpprofzerolog.ProfileWith(profiler, zeroLogger)
zeroLogger.Info().Ctx(ctx).Msg("player loaded")

provider = webpprofotel.ProfileWith(profiler, provider)
```

## Avoid duplicate instrumentation

Use one profiler for each operation path. Combining Bun, GORM, pgx,
`database/sql`, or OpenTelemetry database instrumentation on the same query
records duplicates. The same applies to generic and framework-specific HTTP
request middleware.

See [Request correlation](correlation.md) for context propagation and
[Custom profilers](custom-profilers.md) for unsupported dependencies.
