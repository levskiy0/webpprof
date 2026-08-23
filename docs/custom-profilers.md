# Writing a custom profiler

A profiler adapts one dependency to webpprof's generic integration contract:

```go
type Integration[T any] interface {
    Name() string
    Profile(Scope, T) T
}
```

Keep the intercepted interface and wrapper in the profiler package. The core
should contain only shared event entities.

For a runnable implementation, see
[`example/customprofiler/profiler.go`](../example/customprofiler/profiler.go)
and its installation in [`example/main.go`](../example/main.go).

## Example adapter

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

    wrapped := &profiledClient{inner: client, profiler: scope.Profiler()}
    actual, _ := scope.LoadOrStore(client, Client(wrapped))
    if profiled, ok := actual.(Client); ok {
        return profiled
    }
    return wrapped
}

func (c *profiledClient) Call(ctx context.Context, endpoint string) error {
    measurement := c.profiler.Measure(ctx, webpprof.Event{
        Kind:    "acme-client",
        Name:    "call",
        Summary: endpoint,
    }, func(ctx context.Context) error {
        return c.inner.Call(ctx, endpoint)
    })
    return measurement.Err
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

`Measure` is the short form for operations returning only an error. It records
start time, duration, success or failure, and a recovered panic (before
re-panicking). The callback receives a child context; pass it downstream so
SQL, cache, HTTP, and other profilers appear underneath the custom event.

For a result-returning API use `webpprof.MeasureValueWith`. When status or
fields depend on the returned value, use the manual lifecycle:

```go
span := c.profiler.StartEvent(ctx, webpprof.Event{
    Kind:   "acme-client",
    Name:   "lookup",
    Fields: map[string]any{"key": key},
})

value, err := c.inner.Lookup(span.Context(), key)
status := "found"
if value == "" {
    status = "missing"
}
if err != nil {
    status = "failed"
}
measurement := span.FinishResult(webpprof.EventResult{
    Status: status,
    Fields: map[string]any{"found": value != ""},
    Err:    err,
})
return value, measurement.Err
```

`Finish` and `FinishResult` record at most once, which makes cleanup paths safe.
They return a `webpprof.Measurement` containing `StartedAt`, `Duration`, `Err`,
and `Failed()` so the application can feed the same observation into its own
metrics. Finish the span before its request capture ends. Package-level
`StartEvent`, `Measure`, and `MeasureValue` use the default profiler; the method
and `MeasureValueWith` forms keep an explicit runtime.

## Lifecycle and invariants

1. Start one webpprof runtime.
2. Construct the real dependency.
3. Wrap it with `Profile` or `ProfileWith`.
4. Inject the returned value.
5. Delegate each operation and record it with a context-aware helper.
6. Let the application retain ownership and close the real dependency.

`Scope.LoadOrStore` prevents duplicate installation. `Integration.Name()` must
be stable and unique. Preserve all wrapped results, errors, and ownership
semantics. Keep third-party contracts inside the profiler module rather than
adding them to the core dependency graph.

Configure payload limits and redaction for data captured by the adapter. Keep
the profiler on loopback or a private network and require a token.
