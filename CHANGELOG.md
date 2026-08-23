# Changelog

All notable changes to webpprof are documented here. Releases follow semantic
versioning and are published from `v*` Git tags.

## Unreleased

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
