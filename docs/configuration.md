# Capture and storage configuration

webpprof uses bounded capture by default:

- at most 10,000 events;
- at most 64 MiB of retained data;
- 30-minute retention;
- at most 64 KiB per captured HTTP body.

The UI capacity indicator warns at 70% and becomes critical at 90%.

```go
profiler := webpprof.New(
    mux,
    webpprof.WithRetention(2*time.Hour),
    webpprof.WithMaxEvents(25_000),
    webpprof.WithMaxBytes(128<<20),
    webpprof.WithBodyLimit(32<<10),
    webpprof.WithRequestSampleRate(0.25),
    webpprof.WithDisabledKinds(webpprof.KindEmail, webpprof.KindLog),
    webpprof.WithSQLiteStorage("./var/webpprof/events.db"),
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

`WithSQLiteStorage` enables an owner-only SQLite database. webpprof restores it
on restart and prunes it with the same retention, FIFO event-count, and byte
limits as the in-memory window. Clear and eviction also delete persisted rows.

`WithStoragePath` remains available for the append-only JSONL journal. It is
replayed on restart and compacted automatically. When both storage options are
present, the last one wins.

Both formats contain captured application data: keep them outside public
directories, do not commit them, and remove them after the investigation.

Without either storage option, all events stay in memory and disappear on
shutdown.

## Pagination, import, and export

The UI initially loads the newest 250 entries. **Load older events** requests
the next server page, preventing a large capture window from creating an
unbounded DOM.

Session JSON can be exported and imported from the header. Individual requests
can be copied as payload, headers, raw HTTP, or cURL, and downloaded as HAR.
Exports use the already redacted and size-limited capture; truncated content
cannot reproduce the complete original request.

See [Security](security.md) before enabling persistence or remote access.
