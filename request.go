package webpprof

import (
	"context"
	"sync"
	"time"
)

type requestContextKey struct{}

// RequestCapture buffers entities produced during one inbound request and logs
// the completed request exactly once.
type RequestCapture struct {
	mu         sync.Mutex
	request    Request
	profiler   *Profiler
	isFinished bool
}

// BeginRequest starts a request capture using the default profiler at Finish
// time. Missing IDs and start times are initialized automatically.
func BeginRequest(request Request) *RequestCapture {
	if request.ID == "" {
		request.ID = newID()
	}
	if request.StartedAt.IsZero() {
		request.StartedAt = time.Now().UTC()
	}
	request.Tags = cloneTags(request.Tags)
	return &RequestCapture{request: request}
}

// BeginRequest starts a request capture bound to this profiler.
func (p *Profiler) BeginRequest(request Request) *RequestCapture {
	capture := BeginRequest(request)
	capture.profiler = p
	return capture
}

// WithRequest associates capture with ctx so context-aware integrations append
// related entities to the same request.
func WithRequest(ctx context.Context, capture *RequestCapture) context.Context {
	if capture == nil {
		return ctx
	}
	return context.WithValue(ctx, requestContextKey{}, capture)
}

// RequestFromContext returns the request capture associated with ctx, if any.
func RequestFromContext(ctx context.Context) *RequestCapture {
	if ctx == nil {
		return nil
	}
	capture, _ := ctx.Value(requestContextKey{}).(*RequestCapture)
	return capture
}

// ID returns the stable request identifier assigned to this capture.
func (c *RequestCapture) ID() string {
	if c == nil {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.request.ID
}

// LogQuery buffers a query under this request until Finish.
func (c *RequestCapture) LogQuery(query Query) {
	c.append(func(request *Request) {
		query.Tags = mergeTags(request.Tags, query.Tags)
		request.Queries = append(request.Queries, query)
	})
}

// LogEmail buffers an email under this request until Finish.
func (c *RequestCapture) LogEmail(email Email) {
	c.append(func(request *Request) {
		email.Tags = mergeTags(request.Tags, email.Tags)
		request.Emails = append(request.Emails, email)
	})
}

// LogCache buffers a cache operation under this request until Finish.
func (c *RequestCapture) LogCache(cache Cache) {
	c.append(func(request *Request) {
		cache.Tags = mergeTags(request.Tags, cache.Tags)
		request.Cache = append(request.Cache, cache)
	})
}

// LogJob buffers a job under this request until Finish.
func (c *RequestCapture) LogJob(job Job) {
	c.append(func(request *Request) {
		job.Tags = mergeTags(request.Tags, job.Tags)
		request.Jobs = append(request.Jobs, job)
	})
}

// LogLog buffers a structured log under this request until Finish.
func (c *RequestCapture) LogLog(log Log) {
	c.append(func(request *Request) {
		log.Tags = mergeTags(request.Tags, log.Tags)
		request.Logs = append(request.Logs, log)
	})
}

// LogHTTPCall buffers an outbound HTTP call under this request until Finish.
func (c *RequestCapture) LogHTTPCall(call HTTPCall) {
	c.append(func(request *Request) {
		call.Tags = mergeTags(request.Tags, call.Tags)
		request.HTTPCalls = append(request.HTTPCalls, call)
	})
}

// LogSchedule buffers a scheduled task under this request until Finish.
func (c *RequestCapture) LogSchedule(schedule Schedule) {
	c.append(func(request *Request) {
		schedule.Tags = mergeTags(request.Tags, schedule.Tags)
		request.Schedules = append(request.Schedules, schedule)
	})
}

// LogException buffers an exception under this request until Finish.
func (c *RequestCapture) LogException(exception Exception) {
	c.append(func(request *Request) {
		exception.Tags = mergeTags(request.Tags, exception.Tags)
		request.Exceptions = append(request.Exceptions, exception)
	})
}

// LogEvent buffers a custom event under this request until Finish.
func (c *RequestCapture) LogEvent(event Event) {
	c.append(func(request *Request) {
		event.Tags = mergeTags(request.Tags, event.Tags)
		request.Events = append(request.Events, event)
	})
}

// LogMiddleware buffers middleware under this request capture.
func (c *RequestCapture) LogMiddleware(middleware Middleware) {
	c.append(func(request *Request) {
		middleware.Tags = mergeTags(request.Tags, middleware.Tags)
		request.Middlewares = append(request.Middlewares, middleware)
	})
}

// AddTags adds or replaces tags on the captured request.
func (c *RequestCapture) AddTags(tags map[string]string) {
	if len(tags) == 0 {
		return
	}
	c.append(func(request *Request) { request.Tags = mergeTags(request.Tags, tags) })
}

// SetRoute updates the matched route pattern before the request is finished.
// Framework middleware can call it after routing has selected a handler.
func (c *RequestCapture) SetRoute(route string) {
	if c == nil || route == "" {
		return
	}
	c.append(func(request *Request) { request.Route = route })
}

// Finish records the request and its buffered entities. Only the first call has
// an effect; later calls are ignored.
func (c *RequestCapture) Finish(result RequestResult) {
	if c == nil {
		return
	}
	c.mu.Lock()
	if c.isFinished {
		c.mu.Unlock()
		return
	}
	c.isFinished = true
	c.request.Duration = time.Since(c.request.StartedAt)
	c.request.Status = result.Status
	c.request.ResponseSize = result.ResponseSize
	c.request.Response = result.Response
	c.request.Error = result.Error
	request := c.request
	c.mu.Unlock()
	if c.profiler != nil {
		c.profiler.LogRequest(request)
		return
	}
	LogRequest(request)
}

func (c *RequestCapture) append(add func(*Request)) bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.isFinished {
		add(&c.request)
		return true
	}
	return false
}
