# Local development example

## 30-second tour

1. Run `go run ./example` from the repository root.
2. Open [http://127.0.0.1:3030/](http://127.0.0.1:3030/).
3. Click **Load Ada**; its JSON response appears at the bottom of the page.
4. Click **Open webpprof**, select the new Request, then inspect **Timeline**
   and **Queries**. The request, measured handler, SQLite query, EXPLAIN plan,
   middleware, tags, and slog record are already correlated.

Run from the repository root:

```sh
go run ./example
```

If port 3030 is busy, select another address without editing the example:

```sh
WEBPPROF_ADDR=127.0.0.1:3031 go run ./example
```

Open [http://127.0.0.1:3030/](http://127.0.0.1:3030/), generate one or more
requests, then open
[http://127.0.0.1:3030/debug/webpprof/](http://127.0.0.1:3030/debug/webpprof/).

## Where the profiler is connected

The integration is intentionally visible in one short composition-root block
in [`main.go`](main.go). Optional retention, dashboard, and storage tuning lives
separately in [`profiler_config.go`](profiler_config.go), so it cannot hide the
actual integration. The application starts with ordinary standard-library
dependencies, wraps each boundary once, and then runs its normal handlers:

```go
// Ordinary application dependencies.
baseLogHandler := slog.NewJSONHandler(os.Stdout, nil)
baseSQLiteDriver := driver.Driver(&modernsqlite.Driver{})

profilerStore, err := webpprofsqlite.Open(context.Background(), "./var/webpprof/events.db")
if err != nil {
    return err
}

// Connect webpprof.
profiler := webpprof.New(
    mux,
	webpprof.WithUnsafeUnauthenticatedAccess(),
    webpprof.WithMaxEvents(25_000),
	webpprof.WithStorage(profilerStore),
)
defer profiler.Close()

logHandler := webpprofslog.ProfileWith(profiler, baseLogHandler)
sqliteDriver := webpprofsql.ProfileDriverWith(
    profiler,
    baseSQLiteDriver,
    webpprofsql.Config{Driver: "sqlite", Explain: true},
)

// Build the ordinary application with the decorated dependencies.
logger := slog.New(logHandler)
database, err := openPlayerDatabase(
    context.Background(),
    sqliteDriver,
    "./var/webpprof/example.db",
)
if err != nil {
    return err
}
defer database.Close()
app := &application{
    profiler: profiler,
    players:  &playerRepository{database: database},
    logger:   logger,
    metrics:  &demoMetrics{},
}

// One outer HTTP wrapper creates request correlation for everything above.
handler := webpprofhttp.MiddlewareWith(profiler, app.routes())
mux.Handle("/", handler)
```

The real code also names two standard HTTP middleware and adds tags, but the
integration model is exactly the same. There is no global instrumentation and
no hidden source rewriting: the dependencies are decorated once where the
application is assembled.

After those three wrappers, webpprof does this automatically:

| Application code | What appears in webpprof |
| --- | --- |
| `http.Handler` serves a request | request method, route, status, duration, headers, bounded bodies, and panic/error data |
| repository calls `QueryContext` or `ExecContext` | correlated Query, duration, rows/error, callsite, and optional EXPLAIN |
| handler calls `logger.InfoContext` | correlated Log with level, message, and structured fields |
| named middleware runs | nested Middleware timing in the request timeline |
| request context flows downstream | request ID, parent operation, and tags propagate to supported profilers |
| retention limit is reached | the oldest stored entries are evicted first |

The player handlers additionally demonstrate the optional core measurement
helper. One small wrapper creates a named service Event, forwards the child
context, updates the application's own counters from the returned measurement,
and handles the returned error:

```go
func (a *application) serveMeasured(
    name string,
    handler func(context.Context) error,
    w http.ResponseWriter,
    r *http.Request,
) {
    measurement := a.profiler.Measure(r.Context(), webpprof.Event{
        Kind: "handler",
        Name: name,
    }, func(ctx context.Context) error {
        err := handler(ctx)
        if err != nil {
            // The child context nests this Log under the handler Event.
            a.handleError(w, r.WithContext(ctx), err)
        }
        return err
    })

    a.metrics.record(measurement.Failed(), measurement.Duration)
}
```

Each handler uses the callback context for SQL and slog calls, so those entries
are nested under `players.list`, `players.get`, or
`players.increment-views` in Timeline. The repository still contains no manual
profiler calls. HTTP-facing code is in [`application.go`](application.go), its
middleware is in [`middleware.go`](middleware.go), and the embedded page is
[`home.html`](home.html). The custom integration and synthetic diagnostic
entities remain isolated in [`manual_examples.go`](manual_examples.go) and
`/api/manual/*`.

This is a real standard-library application, not a page that only emits fake
profiler entities:

- `net/http` serves routes using Go 1.22 method and path patterns;
- `database/sql` reads and updates a pure-Go SQLite database;
- `log/slog` writes JSON logs to stdout through the webpprof slog handler;
- `profiler/sql` records the actual SQL duration, error, callsite, and SQLite
  `EXPLAIN QUERY PLAN` result;
- the optional `storage/sqlite` module keeps captured profiler events across
  restarts.

The only non-webpprof dependency is `modernc.org/sqlite`, the pure-Go SQLite
driver used by both the application and the optional profiler storage backend. No Gin,
ORM, logging framework, Redis, SMTP, queue, external database, or CGO toolchain
is required.

The two databases are created under the ignored `./var/webpprof/` directory:

| File | Purpose |
| --- | --- |
| `example.db` | application players and view counters |
| `events.db` | bounded webpprof event storage |

Override them when needed:

```sh
WEBPPROF_EXAMPLE_DB=/tmp/players.db \
WEBPPROF_STORAGE=/tmp/webpprof-events.db \
WEBPPROF_ADDR=127.0.0.1:3031 \
go run ./example
```

## Routes

| Method and path | What it demonstrates |
| --- | --- |
| `GET /api/players` | real multi-row SQLite query and structured slog record |
| `GET /api/players/42` | automatic parameterized SQL, EXPLAIN, request correlation, and slog |
| `POST /api/players/42/views` | transaction containing UPDATE and SELECT |
| `GET /api/failure` | real SQLite error, safe HTTP response, and error-level log |
| `GET /api/manual/custom-profiler` | isolated hand-written custom profiler Event |
| `GET /api/manual/diagnostics` | isolated deterministic automatic-finding examples |
| `GET /panic` | panic capture and recovery |

Requests carry `app`, `tenant`, and `scenario` tags. The
`security-headers` and `request-log` standard HTTP middleware are profiled by
name. Generate requests for both tenants, open **Tags**, and select
`tenant=acme` or `tenant=umbrella` to try the global watcher.

The example enables query callsites and source links. Open a Query entry to see
the real SQLite plan and click a frame to open it in VS Code. SQL arguments are
not stored, so generated replay code leaves placeholder values as a `TODO`.
EXPLAIN is captured when a query is recorded; older entries already present in
`events.db` are not retroactively analyzed. Generate a new player request after
restarting the current example binary when checking this tab.
Open **Timeline** to see the named middleware nested automatically and all
downstream entities positioned on a shared Gantt scale. The panel also shows
the critical path, bottleneck, and recorded-time breakdown.
Restart `go run ./example` after changing Go or embedded UI files. Stop it with
`Ctrl+C`.

### SQLite profiler storage

The example uses the independent SQLite storage module available to
applications:

```go
profilerStore, err := webpprofsqlite.Open(context.Background(), "./var/webpprof/events.db")
if err != nil {
    return err
}
profiler := webpprof.New(
    mux,
	webpprof.WithUnsafeUnauthenticatedAccess(),
    webpprof.WithRetention(2*time.Hour),
    webpprof.WithMaxEvents(25_000),
    webpprof.WithMaxBytes(128<<20),
    webpprof.WithBodyLimit(32<<10),
	webpprof.WithStorage(profilerStore),
)
```

SQLite storage remains bounded by retention, event-count, and byte limits.
FIFO eviction and Clear delete the corresponding SQLite rows, and the cursor
continues monotonically after restart. `WithStoragePath` remains available for
the existing append-only JSONL journal; the last configured storage option
wins.

### Selective capture examples

Add any of these options to `webpprof.New` in `main.go` while investigating a
specific problem:

```go
// Keep only exact response codes (values inside one option are OR conditions).
webpprof.WithHTTPStatusCodes(200, 301, 500, 502, 503),

// Keep only server errors.
webpprof.WithHTTPStatusAtLeast(500),

// Keep only slow requests.
webpprof.WithMinRequestDuration(500*time.Millisecond),

// Keep requests carrying this profiler tag.
webpprof.WithRequestTags(map[string]string{"tenant": "acme"}),

// Capture only the next 20 eligible requests.
webpprof.WithNextRequests(20),

// Capture one developer browser session, marked by a cookie or header.
webpprof.WithBrowserSession("developer-a"),
```

Separate options are AND conditions. For example, combining status, duration,
and tag rules retains only slow `5xx` requests for `tenant=acme`. To mark this
browser, run the following in its developer console and reload the demo:

```js
document.cookie = "webpprof_capture=developer-a; Path=/; SameSite=Lax"
```

### Diagnostics scenario

Open `/api/manual/diagnostics?tenant=umbrella` or click **Synthetic findings** on
the example home page. The request records deterministic synthetic examples
for the five cross-entity backend analysis rules:

- 47 player queries with one SQL fingerprint, detected as a possible N+1;
- SQL work covering about 73% of the recorded request timeline after
  overlapping query intervals are counted once;
- three sequential same-host HTTP calls that could run concurrently;
- a cache miss immediately followed by 18 identical permission queries;
- an `auth` middleware invocation taking 430 ms.

These diagnostic entries only carry captured durations; they are kept separate
from the normal routes, whose SQLite timings are real. Open the generated
request and inspect its **Automatic findings** tab. Findings are produced by the
Go analyzer, include a suggested action, and link to the supporting entry.

## Custom dashboard example

[`profiler_config.go`](profiler_config.go) configures the dashboard explicitly with
`webpprof.Dashboard(...)`. It demonstrates all three custom widget shapes:

- **Demo requests** — a metric counter without a graph.
- **Demo throughput** — a cumulative counter converted to requests per second,
  with a sparkline.
- **Demo outcomes** — a two-column group of current counters without charts.
- **Demo result history** — a two-series custom chart.

Generate several API requests, including `/api/failure`. Custom callbacks read
atomic counters from the demo application every two seconds, so the cards and
chart update live. The final `WithSlowestOperations()` option shows how a
built-in full-width dashboard panel is mixed with custom widgets.

## Custom profiler example

[`customprofiler/profiler.go`](customprofiler/profiler.go) contains a complete
custom integration for a small `Client` interface. It demonstrates the same
pattern used by the built-in profilers:

1. Define the minimum dependency interface that must be intercepted.
2. Implement `webpprof.Integration[Client]` with a stable `Name`.
3. Return a wrapper that delegates to the original client.
4. Start a `webpprof.Event` span, pass `span.Context()` to the dependency, and
   finish it with the returned status, fields, and error. The helper supplies
   timing, correlation, nesting, error handling, and single-finish semantics.
5. Use `Scope.LoadOrStore` and a wrapper check to avoid installing the same
   profiler twice.

The integration is installed once in [`main.go`](main.go) and is used only by
the explicitly manual route:

```go
// Wrap the real dependency in the composition root.
client := customprofiler.ProfileWith(profiler, demoClient{})

// Inject and use it through the original interface. The handler does not know
// that profiling is enabled; its context supplies request correlation.
value, err := client.Lookup(ctx, "feature:developer-plan")
```

Open `/api/manual/custom-profiler` and select its **Events** related tab to see
the `custom-client / lookup` entry nested under the `demo.custom-profiler`
handler measurement. Normal `/api/players/*` requests emit only their named
handler measurement; their HTTP, SQL, slog, and middleware records are automatic.
See [the custom profiler guide](../docs/custom-profilers.md) for the shorter
`Measure`/`MeasureValueWith` forms and the complete helper contract.
