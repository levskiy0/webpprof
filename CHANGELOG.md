# Changelog

All notable changes to webpprof are documented here. Releases follow semantic
versioning and are published from `v*` Git tags.

## Unreleased

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
