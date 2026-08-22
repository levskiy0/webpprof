# Local development example

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

The example records synthetic related events and does not require a database,
Redis, SMTP, or a queue server. Its requests carry `app`, `tenant`, and
`scenario` tags, and its `security-headers` and `request-log` middleware are
profiled by name. Generate requests for both tenants, open **Tags** in the UI,
and select `tenant=acme` or `tenant=umbrella` to try the global live watcher.
The related **Schedules** entry includes a structured payload with a player ID,
tenant, refresh mode, and requested resources.
The related **Queries** entry also adds the entity-specific
`repository=players` tag on top of the tags inherited from the request context.
Open that query to inspect its automatically captured Go callsite, click the
first frame to open it in VS Code, inspect the representative SQLite EXPLAIN
plan, and copy a safe Go replay skeleton. SQL arguments are deliberately not
stored, so replay code contains a `TODO` for placeholder values.
Open **Timeline** to see the named middleware nested automatically and all
downstream entities positioned on a shared Gantt scale. The panel also shows
the critical path, bottleneck, and recorded-time breakdown.
Restart `go run ./example` after changing Go or embedded UI files. Stop it with
`Ctrl+C`.

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

Open `/demo?tenant=umbrella&diagnostics=1` or click **Diagnostics example** on
the example home page. The request records deterministic synthetic examples
for the five cross-entity backend analysis rules:

- 47 player queries with one SQL fingerprint, detected as a possible N+1;
- SQL work consuming about 82% of the recorded request timeline;
- three sequential same-host HTTP calls that could run concurrently;
- a cache miss immediately followed by 18 identical permission queries;
- an `auth` middleware invocation taking 430 ms.

These entries only carry captured durations: the example does not sleep or call
an external database, HTTP service, or queue. Open the generated request and
inspect its **Automatic findings** card. Findings are produced by the Go
analyzer, include a suggested action, and link to the supporting related entry.
The same request also demonstrates direct slow-query, slow-HTTP, failed-job,
and cache miss-rate findings retained from the original Diagnostics card.

## Custom dashboard example

[`main.go`](main.go) also configures the dashboard explicitly with
`webpprof.Dashboard(...)`. It demonstrates all three custom widget shapes:

- **Demo requests** — a metric counter without a graph.
- **Demo throughput** — a cumulative counter converted to requests per second,
  with a sparkline.
- **Demo outcomes** — a two-column group of current counters without charts.
- **Demo result history** — a two-series custom chart.

Generate several `/demo` and `/demo?fail=1` requests. Custom callbacks read
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
4. Measure the operation and log an existing webpprof entity with the operation
   context. This example emits `webpprof.Event` through `LogEventContext`.
5. Use `Scope.LoadOrStore` and a wrapper check to avoid installing the same
   profiler twice.

The integration is installed once in [`main.go`](main.go):

```go
// Wrap the real dependency in the composition root.
client := customprofiler.ProfileWith(profiler, demoClient{})

// Inject and use it through the original interface. The handler does not know
// that profiling is enabled; its context supplies request correlation.
value, err := client.Lookup(ctx, "player:42")
```

Open a successful or failed `/demo` request and select its **Events** related
tab to see the `custom-client / lookup` entry.
