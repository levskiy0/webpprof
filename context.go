package webpprof

import "context"

type recordingDisabledContextKey struct{}

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
	if request := RequestFromContext(ctx); request != nil {
		query.RequestID = request.ID()
		if request.append(func(parent *Request) { parent.Queries = append(parent.Queries, query) }) {
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
	if request := RequestFromContext(ctx); request != nil {
		email.RequestID = request.ID()
		if request.append(func(parent *Request) { parent.Emails = append(parent.Emails, email) }) {
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
	if request := RequestFromContext(ctx); request != nil {
		cache.RequestID = request.ID()
		if request.append(func(parent *Request) { parent.Cache = append(parent.Cache, cache) }) {
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
	if request := RequestFromContext(ctx); request != nil {
		job.RequestID = request.ID()
		if request.append(func(parent *Request) { parent.Jobs = append(parent.Jobs, job) }) {
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
	if request := RequestFromContext(ctx); request != nil {
		log.RequestID = request.ID()
		if request.append(func(parent *Request) { parent.Logs = append(parent.Logs, log) }) {
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
	if request := RequestFromContext(ctx); request != nil {
		call.RequestID = request.ID()
		if request.append(func(parent *Request) { parent.HTTPCalls = append(parent.HTTPCalls, call) }) {
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
	if request := RequestFromContext(ctx); request != nil {
		schedule.RequestID = request.ID()
		if request.append(func(parent *Request) { parent.Schedules = append(parent.Schedules, schedule) }) {
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
	if request := RequestFromContext(ctx); request != nil {
		exception.RequestID = request.ID()
		if request.append(func(parent *Request) { parent.Exceptions = append(parent.Exceptions, exception) }) {
			return
		}
	}
	p.LogException(exception)
}

func LogEventContext(ctx context.Context, event Event) {
	withDefault(func(p *Profiler) { p.LogEventContext(ctx, event) })
}

func (p *Profiler) LogEventContext(ctx context.Context, event Event) {
	if p == nil || !RecordingEnabled(ctx) {
		return
	}
	if request := RequestFromContext(ctx); request != nil {
		event.RequestID = request.ID()
		if request.append(func(parent *Request) { parent.Events = append(parent.Events, event) }) {
			return
		}
	}
	p.LogEvent(event)
}
