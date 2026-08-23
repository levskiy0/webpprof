# Capture and storage configuration

webpprof uses bounded capture by default:

- at most 10,000 events;
- at most 64 MiB of retained data;
- 30-minute retention;
- at most 64 KiB per captured HTTP body.

The UI capacity indicator warns at 70% and becomes critical at 90%.

```go
sqliteStorage, err := webpprofsqlite.Open(context.Background(), "./var/webpprof/events.db")
if err != nil {
    return err
}

profiler := webpprof.New(
    mux,
    webpprof.WithToken(os.Getenv("WEBPPROF_TOKEN")),
    webpprof.WithRetention(2*time.Hour),
    webpprof.WithMaxEvents(25_000),
    webpprof.WithMaxBytes(128<<20),
    webpprof.WithBodyLimit(32<<10),
    webpprof.WithRequestSampleRate(0.25),
    webpprof.WithDisabledKinds(webpprof.KindEmail, webpprof.KindLog),
    webpprof.WithStorage(sqliteStorage),
)
```

## Selective request capture

Rules that need only the incoming `*http.Request` run before capture starts.
Rules that depend on response status, elapsed time, or request tags run after
the handler completes. Related operations are buffered with the request; when a
completed-request rule rejects it, webpprof discards the entire buffered tree.

```go
profiler := webpprof.New(
    mux,

    // Early rules.
    webpprof.WithRequestSampleRate(0.25),
    webpprof.WithNextRequests(20),
    webpprof.WithBrowserSession("developer-a"),

    // Completed-request rules.
    webpprof.WithHTTPStatusAtLeast(500),
    webpprof.WithMinRequestDuration(500*time.Millisecond),
    webpprof.WithRequestTags(map[string]string{"tenant": "acme"}),
)
```

Different options use **AND** semantics. Values passed to one option use
**OR** semantics, so the following accepts any listed response code:

```go
webpprof.WithHTTPStatusCodes(200, 301, 500, 502, 503)
```

Use `WithRequestFilter` for a custom early predicate, or
`WithRequestRetentionFilter` when the decision needs the completed request:

```go
webpprof.WithRequestFilter(func(r *http.Request) bool {
    return strings.HasPrefix(r.URL.Path, "/api/")
})

webpprof.WithRequestRetentionFilter(func(r webpprof.Request) bool {
    return r.Status >= 500 || r.Duration >= 500*time.Millisecond
})
```

## Browser sessions

`WithBrowserSession("developer-a")` accepts its marker from the
`X-Webpprof-Session` header or `webpprof_capture` cookie. Set the cookie before
reproducing an issue:

```js
document.cookie = "webpprof_capture=developer-a; Path=/; SameSite=Lax"
```

This marker selects capture traffic; it is not authentication and does not
replace `WithToken`. `WithNextRequests` counts candidates after exclusions,
early filters, browser-session matching, and sampling. A candidate still uses
one slot when a completed-request rule later rejects it.

## Local persistence

SQLite persistence is supplied by the independent
`github.com/levskiy0/webpprof/storage/sqlite` module:

```sh
go get github.com/levskiy0/webpprof/storage/sqlite@latest
```

Import it as
`webpprofsqlite "github.com/levskiy0/webpprof/storage/sqlite"`. Open the
owner-only database with `webpprofsqlite.Open`, then pass it to `WithStorage`.
webpprof restores it on restart and prunes it with the same retention, FIFO
event-count, and byte limits as the in-memory window. Clear and eviction also
delete persisted rows.

The active backend passed to `WithStorage` is owned by the profiler and closed
by `Profiler.Close`; do not close it separately. When several storage options
are supplied, only the last is active and the caller remains responsible for
closing any backend that was replaced during configuration.

`WithStoragePath` remains available for the append-only JSONL journal. It is
replayed on restart and compacted automatically. The last configured storage
option wins.

Both formats contain captured application data: keep them outside public
directories, do not commit them, and remove them after the investigation.

Without either storage option, all events stay in memory and disappear on
shutdown.

### Migrating SQLite configuration

`WithSQLiteStorage` was removed from the core module. Replace the old option:

```go
profiler := webpprof.New(
    mux,
    webpprof.WithSQLiteStorage("./var/webpprof/events.db"),
)
```

with the independent backend:

```go
sqliteStorage, err := webpprofsqlite.Open(
    context.Background(),
    "./var/webpprof/events.db",
)
if err != nil {
    return err
}

profiler := webpprof.New(
    mux,
    webpprof.WithStorage(sqliteStorage),
)
```

The database schema is unchanged, so an existing webpprof SQLite file can be
opened at the same path without conversion. The SQLite driver is now loaded
only by applications that import the optional backend.

## Querying captured events

The authenticated `GET /debug/webpprof/api/events` endpoint filters on the
server before applying the page limit. All supplied filters are AND conditions.
Repeated `tag` parameters are also AND conditions; use `tag=key` to require a
tag or `tag=key=value` to require its exact value.

| Parameter | Meaning |
| --- | --- |
| `kind` | Exact event kind. |
| `request_id` | Request ID, including directly and asynchronously correlated entries. |
| `tag` | Repeatable `key` or `key=value` selector. |
| `q` | Case-insensitive search over IDs, kind, process, instance, tags, and bounded redacted event data. |
| `method` | Case-insensitive exact method; matches request entries only. |
| `path_contains` | Case-insensitive path substring; matches request entries only. |
| `status` | Exact HTTP status from 100 through 599; matches request entries only. |
| `min_duration_ms` | Inclusive minimum duration in milliseconds. |
| `max_duration_ms` | Inclusive maximum duration in milliseconds. |
| `after` | Match cursors strictly greater than this value. |
| `before` | Match cursors strictly less than this value. |
| `limit` | Page size from 1 through 1,000; omitted or invalid values use 200. |

For example, this query returns slow `GET` requests for one tenant after a
known cursor:

```text
GET /debug/webpprof/api/events?kind=request&method=GET&status=500&min_duration_ms=250&after=1200&tag=tenant%3Dacme
```

The response contains `events`, `has_more`, capture `stats`, and `scanned`, the
number of cursor-eligible entries inspected to fill the filtered page. The Go
client exposes the same contract:

```go
page, err := profilerClient.ListEvents(ctx, client.ListEventsOptions{
    Kind:        webpprof.KindRequest,
    Method:      http.MethodGet,
    Status:      http.StatusInternalServerError,
    MinDuration: 250 * time.Millisecond,
    After:       cursor,
    Tags:        []string{"tenant=acme"},
    Limit:       100,
})
```

## Pagination, import, and export

The UI initially loads the newest 250 entries. **Load older events** requests
the next server page, preventing a large capture window from creating an
unbounded DOM.

Session JSON can be exported and imported from the header. Individual requests
can be copied as payload, headers, raw HTTP, or cURL, and downloaded as HAR.
Exports use the already redacted and size-limited capture; truncated content
cannot reproduce the complete original request.

See [Security](security.md) before enabling persistence or remote access.
