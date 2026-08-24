// Package webpprof provides a Telescope-like request profiler and debug toolbar
// for Go web applications.
//
// It records bounded, redacted diagnostic entries for inbound requests, SQL,
// cache operations, jobs, logs, email, outbound HTTP calls, schedules, callables,
// measured tasks, middleware, exceptions, and custom events. Related work can be correlated
// through context.Context and inspected in the embedded dashboard.
//
// # Getting started
//
// Use [New] to mount the profiler on an existing HTTP router, or [Start] to run
// it on a dedicated address. HTTP framework and dependency adapters live under
// github.com/levskiy0/webpprof/profiler.
//
// # Capture lifecycle
//
// [BeginRequest] and [RequestCapture] support manual request instrumentation.
// Context-aware Log methods automatically inherit request correlation, parent
// entry IDs, and tags. Storage is bounded by retention, event count, and byte
// limits.
//
// # Security
//
// The dashboard contains application data. Dedicated servers created by
// [Start] require [WithToken] unless the caller explicitly opts into
// [WithUnsafeUnauthenticatedAccess]. Captured JSON is redacted using the
// built-in sensitive-key policy, but callers should still avoid recording
// secrets in opaque strings.
package webpprof
