# Changelog

All notable changes to webpprof are documented here. Releases follow semantic
versioning and are published from `v*` Git tags.

## Unreleased

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
