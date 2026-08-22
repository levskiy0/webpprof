# Local development example

Run from the repository root:

```sh
go run ./example
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
Restart `go run ./example` after changing Go or embedded UI files. Stop it with
`Ctrl+C`.

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
