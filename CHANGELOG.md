# Changelog

All notable changes to webpprof are documented here. Releases follow semantic
versioning and are published from `v*` Git tags.

## Unreleased

## 0.5.0 - 2026-08-24

### Added

- Added the `debug-go-with-webpprof` agent skill for evidence-driven Go API,
  request, SQL, cache, and data-lifecycle diagnostics through the read-only MCP
  server.

### Changed

- Add opt-in, bounded plain EXPLAIN plans to the Bun, GORM, and native pgx
  database profilers, matching the existing `database/sql` capability.
- Publish nested profiler, storage, and MCP command modules through
  module-scoped Git tags and `proxy.golang.org` without creating separate
  GitHub Release pages; only root `vX.Y.Z` tags create product releases.
- Treat every schedule invocation as a standalone execution scope: SQL, logs,
  HTTP calls, cache operations, and other context-aware work now use the
  Schedule entry as their parent. The UI, events API, Go client, and MCP server
  can inspect that complete execution tree, and the bundled example exercises
  the behavior with a real SQLite query and structured log. Schedule execution
  pages also expose automatic findings for N+1 queries, SQL share, slow or
  failed operations, cache behavior, and sequential HTTP calls.
- Add Callable as a third standalone execution root for custom commands that
  are neither HTTP requests nor scheduled tasks. The core contract, wrapper,
  analyzer, UI, HTTP/Go/MCP inspection APIs, example, and tests all expose its
  complete `ParentID` execution tree.
- Add Task as a standalone execution root for measured application work such
  as report generation. `StartTask` and `MeasureTask` time the operation,
  parent nested queries, logs, HTTP calls, and other context-aware entries,
  preserve errors and panics, and expose the complete scope to the analyzer,
  UI, HTTP/Go client, MCP, example, and tests.
- Place Schedules, Callables, and Tasks before Requests in the default sidebar and add
  `WithSidebarKinds` for independently ordering or hiding entity sections
  without changing capture behavior.
- Expand automatic analysis to failed core operations, slow measured events,
  execution bottlenecks, and conservative normalized EXPLAIN concerns. N+1
  detection now groups read queries by database and execution locality.

### Fixed

- Include measured custom Events in Timeline critical-path and bottleneck
  calculation, so a long programmatic Task step is not hidden by a tiny nested
  SQL operation.
- Measure `database/sql` queries until rows reach EOF, fail, or are closed, so
  recorded duration and row-stream errors cover the complete query lifecycle.

## 0.4.2 - 2026-08-24

### Added

- Added executable pkg.go.dev examples for the core package and the `net/http`
  integration.
- Added a repository-wide GoDoc coverage check to `make check` for every public
  library package across all Go modules.

### Changed

- Documented the exported core API and integration adapters, including package
  overviews, configuration options, DTOs, integration contracts, and methods.
- Reused each entity section's original table columns and row presentation in
  request-related tabs, with consistent badge counters across all tabs.
- Marked the project as early-stage and pre-v1 near the top of the README.

### Fixed

- Kept request-related entity rows white while preserving their hover state and
  aligned the final table-header column with the row action control.

## 0.4.1 - 2026-08-24

### Added

- Added an end-to-end SQLite steady-state eviction benchmark in the optional
  `storage/sqlite` module.

### Fixed

- Kept MCP search results matched by server-side entry metadata such as ID,
  process, instance, kind, request correlation, or tags instead of rechecking
  only the event payload locally.

## 0.4.0 - 2026-08-24

### Added

- Added reproducible end-to-end overhead and steady-state eviction benchmarks,
  with reference results documented in the README.
- Added server-side HTTP and MCP filters for request fields, text, tags,
  cursors, and minimum or maximum duration.
- Added `govulncheck` gates for every Go module and regression coverage for the
  schedule and go-mail integrations.

### Changed

- Replaced the event-order slice with a circular deque and moved persistence
  I/O outside the shared store mutex while preserving write order.
- Moved SQLite persistence and its driver dependency to the independent
  `storage/sqlite` module behind the core `EntryStorage` interface.
- Made unauthenticated access to captured data an explicit unsafe opt-in through
  `WithUnsafeUnauthenticatedAccess`; standalone servers require a token or the
  unsafe option.

### Removed

- Removed the core `WithSQLiteStorage` option. Install the optional
  `storage/sqlite` module and use `sqlite.Open` with `WithStorage`; existing
  database files remain compatible.

### Fixed

- Raised the `profiler/gorm` `golang.org/x/text` constraint to `v0.39.0` so its
  reachable dependency graph is clear of GO-2026-5970.

## 0.3.2 - 2026-08-23

### Fixed

- Preserved JSON container shapes while redacting sensitive values so captured
  `Cookie` and `Authorization` headers remain decodable as header arrays.
- Allowed request summaries, including MCP inspection, to read legacy entries
  whose sensitive headers were stored as scalar redaction placeholders.

## 0.3.1 - 2026-08-23

### Changed

- Reworked profiler event indexes and the dashboard's slowest-operations widget
  as compact, accessible tables aligned with Laravel Telescope's presentation.
- Refined sidebar typography, list density, event viewport sizing, badges, and
  action alignment while removing inline tags from event index rows.
- Rendered captured request and response headers as compact highlighted JSON,
  collapsing single-value header arrays to one line.

## 0.3.0 - 2026-08-23

### Added

- Added bounded SQLite profiler storage through `WithSQLiteStorage` with
  replay, FIFO eviction, clear, and monotonic cursor persistence.
- Added `StartEvent`, `Measure`, and value-returning measurement helpers for
  custom profilers and application-level operations.
- Added a realistic `net/http`, `database/sql`, SQLite, and `log/slog` example
  with automatic correlation, EXPLAIN plans, custom measurements, diagnostics,
  and dashboard widgets.
- Added focused documentation for configuration, correlation, dashboard,
  entities, integrations, MCP, security, SQL profiling, and custom profilers.

### Changed

- Expanded SQL EXPLAIN support to safe plans for `INSERT`, `UPDATE`, `DELETE`,
  and `WITH` statements without executing their mutations.
- Improved the viewer's event loading, request details, timeline, tables, and
  reusable tab presentation for larger profiling sessions.
- Updated the core, MCP command, and all optional integration modules to use a
  consistent `v0.3.0` core dependency.

### Fixed

- Included entry tags in byte-limit accounting so `WithMaxBytes` remains a
  real bound for tagged events.
- Kept error logs and panic diagnostics nested beneath measured handler events
  in the development example.

## 0.2.1 - 2026-08-23

### Changed

- Published the core library, MCP command, and every optional profiler module
  with matching `v0.2.1` module tags.
- Aligned all nested Go modules and local workspace tooling on core `v0.2.1`.

## 0.2.0 - 2026-08-23

### Added

- Request-related middleware, cache, mail, jobs, logs, HTTP calls, schedules,
  exceptions, and custom events.
- Global tag watcher plus entity-specific, status, duration, and time filters
  persisted in the URL.
- Request waterfall, automatic diagnostics, session JSON import/export, and HAR
  download.
- Optional owner-only disk journal, server pagination, and storage capacity
  indicators.
- Request sampling, disabled-kind capture controls, login throttling, and
  hardened browser response headers.
- Read-only `webpprof-mcp` stdio server for agent-driven request listing,
  inspection, event search, automatic findings, and waiting for new requests.
- Runnable custom-profiler example and expanded integration contracts.

### Changed

- Dashboard and table typography is larger and table actions use one consistent
  arrow treatment.
- The development example listens on `127.0.0.1:3030` by default and supports
  the `WEBPPROF_ADDR` override.
