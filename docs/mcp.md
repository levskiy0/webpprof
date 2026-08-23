# Debug Go applications with AI agents over MCP

`webpprof-mcp` lets an MCP-compatible coding agent inspect requests captured by
a running webpprof instance. It is a separate executable: it does not attach to
the Go process, read application memory, or embed MCP into the application.

```text
Codex / Claude / Cursor <-- MCP over stdio --> webpprof-mcp <-- HTTP --> Go application
```

## Install the binary

Installing `github.com/levskiy0/webpprof` as a library does not install the MCP
server. Install its independent Go module from any directory:

```sh
go install github.com/levskiy0/webpprof/cmd/webpprof-mcp@latest
webpprof-mcp --version
```

For a reproducible install, use `@v0.3.1`. The corresponding repository tag is
`cmd/webpprof-mcp/v0.3.1`. `go install` writes the executable
to `GOBIN`, or to `GOPATH/bin` when `GOBIN` is unset; that directory must be in
`PATH`.

The relative command below is only for contributors working inside a checkout:

```sh
go install ./cmd/webpprof-mcp
```

## Start the application profiler

Bind the profiler to loopback and share its token with the MCP process through
the environment:

```go
profiler, err := webpprof.Start(
    "127.0.0.1:6061",
    webpprof.WithToken(os.Getenv("WEBPPROF_TOKEN")),
)
if err != nil {
    return err
}
defer profiler.Shutdown(context.Background())
```

The default profiler URL is
`http://127.0.0.1:6061/debug/webpprof/`. Override it with `--url` or
`WEBPPROF_URL`. The token is intentionally available only through
`WEBPPROF_TOKEN`, which keeps it out of the process argument list.

## Configure Codex

```sh
codex mcp add webpprof \
  --env WEBPPROF_TOKEN="$WEBPPROF_TOKEN" \
  -- webpprof-mcp --url http://127.0.0.1:6061/debug/webpprof/
```

## Configure another MCP client

Clients such as Claude and Cursor accept the same stdio server through their
MCP JSON configuration:

```json
{
  "mcpServers": {
    "webpprof": {
      "command": "webpprof-mcp",
      "args": [
        "--url",
        "http://127.0.0.1:6061/debug/webpprof/"
      ],
      "env": {
        "WEBPPROF_TOKEN": "development-token"
      }
    }
  }
}
```

## Available tools

| Tool | Purpose |
| --- | --- |
| `webpprof_status` | Check connectivity, capacity, retention, storage, sampling, and the latest cursor. |
| `webpprof_list_requests` | List requests and filter by method, path, status, duration, tags, or cursor. |
| `webpprof_inspect_request` | Return automatic findings and the correlated request timeline. |
| `webpprof_search_events` | Search SQL, cache, logs, HTTP calls, exceptions, and other captured events. |
| `webpprof_wait_for_request` | Wait for the next matching request after an observed cursor. |

A typical agent workflow is:

1. Call `webpprof_status` and remember its cursor.
2. Reproduce the application problem.
3. Call `webpprof_wait_for_request` with the remembered cursor.
4. Pass the returned request ID to `webpprof_inspect_request`.
5. Search supporting events when the finding needs more evidence.

Captured bodies, values, arguments, and stacks remain omitted unless
`include_payloads` is explicitly enabled. Tools never replay requests, clear
events, or mutate application state.

## Remote connections

Loopback is required by default. A remote profiler requires both
`--allow-remote` and an HTTPS URL. Keep `WithToken` enabled, configure
`WithSecureCookie(true)`, and put the profiler behind private-network
authentication. See [Security](security.md).
