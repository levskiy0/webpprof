package webpprof

import (
	"context"
	"fmt"
	"runtime/debug"
)

type recordingDisabledContextKey struct{}
type tagsContextKey struct{}
type parentEntryContextKey struct{}

type uncorrelatedContext struct {
	context.Context
}

func (ctx uncorrelatedContext) Value(key any) any {
	switch key.(type) {
	case requestContextKey, parentEntryContextKey:
		return nil
	default:
		return ctx.Context.Value(key)
	}
}

// WithoutCorrelation returns a context that preserves cancellation, deadlines,
// tags, recording state, and application values while removing webpprof request
// and parent-entry correlation. Execution-root integrations use it to avoid
// becoming children of the caller that invoked them.
func WithoutCorrelation(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return uncorrelatedContext{Context: ctx}
}

// WithParentEntry returns a context that makes entryID the default ParentID
// for profiler entities recorded downstream. An explicit Meta.ParentID always
// takes precedence.
func WithParentEntry(ctx context.Context, entryID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if entryID == "" {
		return ctx
	}
	return context.WithValue(ctx, parentEntryContextKey{}, entryID)
}

// ParentEntryIDFromContext returns the current profiler parent entry ID.
func ParentEntryIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	entryID, _ := ctx.Value(parentEntryContextKey{}).(string)
	return entryID
}

// WithTags returns a context carrying tags inherited by every profiler entity
// logged from it. When a request capture is present, the request receives the
// same tags. Values in tags replace values already present under the same key.
func WithTags(ctx context.Context, tags map[string]string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	merged := mergeTags(TagsFromContext(ctx), tags)
	if request := RequestFromContext(ctx); request != nil {
		request.AddTags(tags)
	}
	return context.WithValue(ctx, tagsContextKey{}, merged)
}

// TagsFromContext returns a copy of the profiler tags stored in ctx.
func TagsFromContext(ctx context.Context) map[string]string {
	if ctx == nil {
		return nil
	}
	tags, _ := ctx.Value(tagsContextKey{}).(map[string]string)
	return cloneTags(tags)
}

// WithoutRecording returns a child context that suppresses context-aware
// profiler integrations downstream.
func WithoutRecording(ctx context.Context) context.Context {
	return context.WithValue(ctx, recordingDisabledContextKey{}, true)
}

// RecordingEnabled reports whether context-aware profiler integrations should
// record work for ctx. A nil context is treated as enabled.
func RecordingEnabled(ctx context.Context) bool {
	if ctx == nil {
		return true
	}
	disabled, _ := ctx.Value(recordingDisabledContextKey{}).(bool)
	return !disabled
}

// LogQueryContext records a query with the default profiler and inherits tags,
// parent entry, and request correlation from ctx.
func LogQueryContext(ctx context.Context, query Query) {
	withDefault(func(p *Profiler) { p.LogQueryContext(ctx, query) })
}

// LogQueryContext records a query with this profiler and inherits tags, parent
// entry, and request correlation from ctx.
func (p *Profiler) LogQueryContext(ctx context.Context, query Query) {
	if p == nil || !RecordingEnabled(ctx) {
		return
	}
	p.prepareQuery(&query)
	inheritContextMeta(ctx, &query.Meta)
	if request := RequestFromContext(ctx); request != nil {
		query.RequestID = request.ID()
		if request.append(func(parent *Request) {
			query.Tags = mergeTags(parent.Tags, query.Tags)
			parent.Queries = append(parent.Queries, query)
		}) {
			return
		}
	}
	p.LogQuery(query)
}

// LogEmailContext records an email with the default profiler and correlation
// inherited from ctx.
func LogEmailContext(ctx context.Context, email Email) {
	withDefault(func(p *Profiler) { p.LogEmailContext(ctx, email) })
}

// LogEmailContext records an email with this profiler and correlation inherited
// from ctx.
func (p *Profiler) LogEmailContext(ctx context.Context, email Email) {
	if p == nil || !RecordingEnabled(ctx) {
		return
	}
	p.prepareCallsite(KindEmail, &email.Callsite)
	inheritContextMeta(ctx, &email.Meta)
	if request := RequestFromContext(ctx); request != nil {
		email.RequestID = request.ID()
		if request.append(func(parent *Request) {
			email.Tags = mergeTags(parent.Tags, email.Tags)
			parent.Emails = append(parent.Emails, email)
		}) {
			return
		}
	}
	p.LogEmail(email)
}

// LogCacheContext records a cache operation with the default profiler and
// correlation inherited from ctx.
func LogCacheContext(ctx context.Context, cache Cache) {
	withDefault(func(p *Profiler) { p.LogCacheContext(ctx, cache) })
}

// LogCacheContext records a cache operation with this profiler and correlation
// inherited from ctx.
func (p *Profiler) LogCacheContext(ctx context.Context, cache Cache) {
	if p == nil || !RecordingEnabled(ctx) {
		return
	}
	p.prepareCallsite(KindCache, &cache.Callsite)
	inheritContextMeta(ctx, &cache.Meta)
	if request := RequestFromContext(ctx); request != nil {
		cache.RequestID = request.ID()
		if request.append(func(parent *Request) {
			cache.Tags = mergeTags(parent.Tags, cache.Tags)
			parent.Cache = append(parent.Cache, cache)
		}) {
			return
		}
	}
	p.LogCache(cache)
}

// LogJobContext records a job with the default profiler and correlation
// inherited from ctx.
func LogJobContext(ctx context.Context, job Job) {
	withDefault(func(p *Profiler) { p.LogJobContext(ctx, job) })
}

// LogJobContext records a job with this profiler and correlation inherited from
// ctx.
func (p *Profiler) LogJobContext(ctx context.Context, job Job) {
	if p == nil || !RecordingEnabled(ctx) {
		return
	}
	p.prepareCallsite(KindJob, &job.Callsite)
	inheritContextMeta(ctx, &job.Meta)
	if request := RequestFromContext(ctx); request != nil {
		job.RequestID = request.ID()
		if request.append(func(parent *Request) {
			job.Tags = mergeTags(parent.Tags, job.Tags)
			parent.Jobs = append(parent.Jobs, job)
		}) {
			return
		}
	}
	p.LogJob(job)
}

// LogLogContext records a structured log with the default profiler and
// correlation inherited from ctx.
func LogLogContext(ctx context.Context, log Log) {
	withDefault(func(p *Profiler) { p.LogLogContext(ctx, log) })
}

// LogLogContext records a structured log with this profiler and correlation
// inherited from ctx.
func (p *Profiler) LogLogContext(ctx context.Context, log Log) {
	if p == nil || !RecordingEnabled(ctx) {
		return
	}
	inheritContextMeta(ctx, &log.Meta)
	if request := RequestFromContext(ctx); request != nil {
		log.RequestID = request.ID()
		if request.append(func(parent *Request) {
			log.Tags = mergeTags(parent.Tags, log.Tags)
			parent.Logs = append(parent.Logs, log)
		}) {
			return
		}
	}
	p.LogLog(log)
}

// LogHTTPCallContext records an outbound HTTP call with the default profiler and
// correlation inherited from ctx.
func LogHTTPCallContext(ctx context.Context, call HTTPCall) {
	withDefault(func(p *Profiler) { p.LogHTTPCallContext(ctx, call) })
}

// LogHTTPCallContext records an outbound HTTP call with this profiler and
// correlation inherited from ctx.
func (p *Profiler) LogHTTPCallContext(ctx context.Context, call HTTPCall) {
	if p == nil || !RecordingEnabled(ctx) {
		return
	}
	p.prepareCallsite(KindHTTPCall, &call.Callsite)
	inheritContextMeta(ctx, &call.Meta)
	if request := RequestFromContext(ctx); request != nil {
		call.RequestID = request.ID()
		if request.append(func(parent *Request) {
			call.Tags = mergeTags(parent.Tags, call.Tags)
			parent.HTTPCalls = append(parent.HTTPCalls, call)
		}) {
			return
		}
	}
	p.LogHTTPCall(call)
}

// LogScheduleContext records a scheduled task with the default profiler. The
// Schedule remains an execution root while inheriting tags.
func LogScheduleContext(ctx context.Context, schedule Schedule) {
	withDefault(func(p *Profiler) { p.LogScheduleContext(ctx, schedule) })
}

// LogScheduleContext records a scheduled task with this profiler. Request and
// parent correlation are removed because Schedule is a root entity.
func (p *Profiler) LogScheduleContext(ctx context.Context, schedule Schedule) {
	if p == nil || !RecordingEnabled(ctx) {
		return
	}
	rootCtx := WithoutCorrelation(ctx)
	p.prepareCallsite(KindSchedule, &schedule.Callsite)
	inheritContextMeta(rootCtx, &schedule.Meta)
	p.LogSchedule(schedule)
}

// LogCallableContext records an explicitly invoked command with the default
// profiler. The Callable remains an execution root while inheriting tags.
func LogCallableContext(ctx context.Context, callable Callable) {
	withDefault(func(p *Profiler) { p.LogCallableContext(ctx, callable) })
}

// LogCallableContext records an explicitly invoked command with this profiler.
// Request and parent correlation are removed because Callable is a root entity.
func (p *Profiler) LogCallableContext(ctx context.Context, callable Callable) {
	if p == nil || !RecordingEnabled(ctx) {
		return
	}
	rootCtx := WithoutCorrelation(ctx)
	p.prepareCallsite(KindCallable, &callable.Callsite)
	inheritContextMeta(rootCtx, &callable.Meta)
	p.LogCallable(callable)
}

// LogTaskContext records a measured task with the default profiler. The Task
// remains an execution root while inheriting tags.
func LogTaskContext(ctx context.Context, task Task) {
	withDefault(func(p *Profiler) { p.LogTaskContext(ctx, task) })
}

// LogTaskContext records a measured task with this profiler. Request and
// parent correlation are removed because Task is a root entity.
func (p *Profiler) LogTaskContext(ctx context.Context, task Task) {
	if p == nil || !RecordingEnabled(ctx) {
		return
	}
	rootCtx := WithoutCorrelation(ctx)
	p.prepareCallsite(KindTask, &task.Callsite)
	inheritContextMeta(rootCtx, &task.Meta)
	p.LogTask(task)
}

// LogExceptionContext records an exception with the default profiler and
// correlation inherited from ctx.
func LogExceptionContext(ctx context.Context, exception Exception) {
	withDefault(func(p *Profiler) { p.LogExceptionContext(ctx, exception) })
}

// LogExceptionContext records an exception with this profiler and correlation
// inherited from ctx.
func (p *Profiler) LogExceptionContext(ctx context.Context, exception Exception) {
	if p == nil || !RecordingEnabled(ctx) {
		return
	}
	inheritContextMeta(ctx, &exception.Meta)
	if request := RequestFromContext(ctx); request != nil {
		exception.RequestID = request.ID()
		if request.append(func(parent *Request) {
			exception.Tags = mergeTags(parent.Tags, exception.Tags)
			parent.Exceptions = append(parent.Exceptions, exception)
		}) {
			return
		}
	}
	p.LogException(exception)
}

// PanicException converts a recovered panic value into an exception event.
// Call it from the deferred function that recovered the panic so the stack
// still describes the failing goroutine.
func PanicException(recovered any) Exception {
	return Exception{
		Type:    fmt.Sprintf("%T", recovered),
		Message: fmt.Sprint(recovered),
		Stack:   string(debug.Stack()),
	}
}

// LogEventContext records a custom event with the default profiler and
// correlation inherited from ctx.
func LogEventContext(ctx context.Context, event Event) {
	withDefault(func(p *Profiler) { p.LogEventContext(ctx, event) })
}

// LogEventContext records a custom event with this profiler and correlation
// inherited from ctx.
func (p *Profiler) LogEventContext(ctx context.Context, event Event) {
	if p == nil || !RecordingEnabled(ctx) {
		return
	}
	inheritContextMeta(ctx, &event.Meta)
	if request := RequestFromContext(ctx); request != nil {
		event.RequestID = request.ID()
		if request.append(func(parent *Request) {
			event.Tags = mergeTags(parent.Tags, event.Tags)
			parent.Events = append(parent.Events, event)
		}) {
			return
		}
	}
	p.LogEvent(event)
}

// LogMiddlewareContext records middleware using the default profiler and
// correlates it with the request capture in ctx.
func LogMiddlewareContext(ctx context.Context, middleware Middleware) {
	withDefault(func(p *Profiler) { p.LogMiddlewareContext(ctx, middleware) })
}

// LogMiddlewareContext records middleware with inherited context tags and
// request correlation.
func (p *Profiler) LogMiddlewareContext(ctx context.Context, middleware Middleware) {
	if p == nil || !RecordingEnabled(ctx) {
		return
	}
	inheritContextMeta(ctx, &middleware.Meta)
	if request := RequestFromContext(ctx); request != nil {
		middleware.RequestID = request.ID()
		if request.append(func(parent *Request) {
			middleware.Tags = mergeTags(parent.Tags, middleware.Tags)
			parent.Middlewares = append(parent.Middlewares, middleware)
		}) {
			return
		}
	}
	p.LogMiddleware(middleware)
}

func mergeTags(base, override map[string]string) map[string]string {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	merged := make(map[string]string, len(base)+len(override))
	for key, value := range base {
		merged[key] = value
	}
	for key, value := range override {
		merged[key] = value
	}
	return merged
}

func inheritContextMeta(ctx context.Context, meta *Meta) {
	meta.Tags = mergeTags(TagsFromContext(ctx), meta.Tags)
	if meta.ParentID == "" {
		meta.ParentID = ParentEntryIDFromContext(ctx)
	}
}
