# Security

webpprof is a development and diagnostic tool. Captures may contain personal
data, authentication headers, request bodies, SQL, mail, arguments, logs, and
stack traces even after automatic redaction.

## Required deployment controls

- Bind a standalone profiler to loopback or a private administrative network.
- Set a strong `WithToken` value outside source control.
- Never expose an unprotected profiler to the public internet.
- Use `WithSecureCookie(true)` whenever the profiler is served through HTTPS.
- Put remote access behind private-network or reverse-proxy authentication as a
  second layer.
- Restrict `WithAllowedOrigins` to explicit trusted profiler origins required
  for WebSocket access.
- Keep persistent journals outside public directories and delete them after the
  investigation.

The built-in authentication compares tokens in constant time and throttles
failed login attempts per client. Session cookies are `HttpOnly` and
`SameSite=Strict`. Profiler routes receive a strict Content Security Policy and
no-store response headers.

## Captured payloads

Configure body and argument limits for the data handled by the application.
Automatic redaction reduces accidental exposure but is not a substitute for
safe capture rules. Avoid putting secrets in tags, error strings, event fields,
or custom summaries.

The MCP server omits bodies, values, arguments, and stacks by default. Enabling
`include_payloads` sends already captured data to the connected agent, so use it
only for safe development data. See [MCP security](mcp.md#remote-connections).

## Persistence

`WithSQLiteStorage` and `WithStoragePath` write owner-only local files
containing captured application data. Do not place them in a static/public
directory, commit them to version control, or retain them longer than the
investigation requires. See [Capture and storage
configuration](configuration.md#local-persistence).
