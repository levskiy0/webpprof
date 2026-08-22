package webpprof

import (
	"context"
	"fmt"
	"runtime/debug"
)

type recordingDisabledContextKey struct{}
type tagsContextKey struct{}

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

func WithoutRecording(ctx context.Context) context.Context {
	return context.WithValue(ctx, recordingDisabledContextKey{}, true)
}

func RecordingEnabled(ctx context.Context) bool {
	if ctx == nil {
		return true
	}
	disabled, _ := ctx.Value(recordingDisabledContextKey{}).(bool)
	return !disabled
}

func LogQueryContext(ctx context.Context, query Query) {
	withDefault(func(p *Profiler) { p.LogQueryContext(ctx, query) })
}

func (p *Profiler) LogQueryContext(ctx context.Context, query Query) {
	if p == nil || !RecordingEnabled(ctx) {
		return
	}
	query.Tags = mergeTags(TagsFromContext(ctx), query.Tags)
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

func LogEmailContext(ctx context.Context, email Email) {
	withDefault(func(p *Profiler) { p.LogEmailContext(ctx, email) })
}

func (p *Profiler) LogEmailContext(ctx context.Context, email Email) {
	if p == nil || !RecordingEnabled(ctx) {
		return
	}
	email.Tags = mergeTags(TagsFromContext(ctx), email.Tags)
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

func LogCacheContext(ctx context.Context, cache Cache) {
	withDefault(func(p *Profiler) { p.LogCacheContext(ctx, cache) })
}

func (p *Profiler) LogCacheContext(ctx context.Context, cache Cache) {
	if p == nil || !RecordingEnabled(ctx) {
		return
	}
	cache.Tags = mergeTags(TagsFromContext(ctx), cache.Tags)
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

func LogJobContext(ctx context.Context, job Job) {
	withDefault(func(p *Profiler) { p.LogJobContext(ctx, job) })
}

func (p *Profiler) LogJobContext(ctx context.Context, job Job) {
	if p == nil || !RecordingEnabled(ctx) {
		return
	}
	job.Tags = mergeTags(TagsFromContext(ctx), job.Tags)
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

func LogLogContext(ctx context.Context, log Log) {
	withDefault(func(p *Profiler) { p.LogLogContext(ctx, log) })
}

func (p *Profiler) LogLogContext(ctx context.Context, log Log) {
	if p == nil || !RecordingEnabled(ctx) {
		return
	}
	log.Tags = mergeTags(TagsFromContext(ctx), log.Tags)
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

func LogHTTPCallContext(ctx context.Context, call HTTPCall) {
	withDefault(func(p *Profiler) { p.LogHTTPCallContext(ctx, call) })
}

func (p *Profiler) LogHTTPCallContext(ctx context.Context, call HTTPCall) {
	if p == nil || !RecordingEnabled(ctx) {
		return
	}
	call.Tags = mergeTags(TagsFromContext(ctx), call.Tags)
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

func LogScheduleContext(ctx context.Context, schedule Schedule) {
	withDefault(func(p *Profiler) { p.LogScheduleContext(ctx, schedule) })
}

func (p *Profiler) LogScheduleContext(ctx context.Context, schedule Schedule) {
	if p == nil || !RecordingEnabled(ctx) {
		return
	}
	schedule.Tags = mergeTags(TagsFromContext(ctx), schedule.Tags)
	if request := RequestFromContext(ctx); request != nil {
		schedule.RequestID = request.ID()
		if request.append(func(parent *Request) {
			schedule.Tags = mergeTags(parent.Tags, schedule.Tags)
			parent.Schedules = append(parent.Schedules, schedule)
		}) {
			return
		}
	}
	p.LogSchedule(schedule)
}

func LogExceptionContext(ctx context.Context, exception Exception) {
	withDefault(func(p *Profiler) { p.LogExceptionContext(ctx, exception) })
}

func (p *Profiler) LogExceptionContext(ctx context.Context, exception Exception) {
	if p == nil || !RecordingEnabled(ctx) {
		return
	}
	exception.Tags = mergeTags(TagsFromContext(ctx), exception.Tags)
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

func LogEventContext(ctx context.Context, event Event) {
	withDefault(func(p *Profiler) { p.LogEventContext(ctx, event) })
}

func (p *Profiler) LogEventContext(ctx context.Context, event Event) {
	if p == nil || !RecordingEnabled(ctx) {
		return
	}
	event.Tags = mergeTags(TagsFromContext(ctx), event.Tags)
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
	middleware.Tags = mergeTags(TagsFromContext(ctx), middleware.Tags)
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
