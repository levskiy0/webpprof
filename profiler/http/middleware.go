package http

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	stdlibhttp "net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/levskiy0/webpprof"
)

type responseObserver struct {
	stdlibhttp.ResponseWriter
	status      int
	size        int64
	wroteHeader bool
	body        *BodyRecorder
}

type middlewareTimingContextKey struct{}

type middlewareInterval struct {
	start time.Time
	end   time.Time
}

type middlewareTiming struct {
	mu         sync.Mutex
	downstream []middlewareInterval
}

func (t *middlewareTiming) recordDownstream(start, end time.Time) {
	if t == nil || !end.After(start) {
		return
	}
	t.mu.Lock()
	t.downstream = append(t.downstream, middlewareInterval{start: start, end: end})
	t.mu.Unlock()
}

func (t *middlewareTiming) workSpans(start, end time.Time) ([]webpprof.MiddlewareWorkSpan, time.Duration) {
	if t == nil || !end.After(start) {
		return nil, 0
	}
	t.mu.Lock()
	intervals := append([]middlewareInterval(nil), t.downstream...)
	t.mu.Unlock()
	for index := range intervals {
		if intervals[index].start.Before(start) {
			intervals[index].start = start
		}
		if intervals[index].end.After(end) {
			intervals[index].end = end
		}
	}
	sort.Slice(intervals, func(i, j int) bool { return intervals[i].start.Before(intervals[j].start) })
	merged := make([]middlewareInterval, 0, len(intervals))
	var current middlewareInterval
	for _, interval := range intervals {
		if !interval.end.After(interval.start) {
			continue
		}
		if current.end.IsZero() {
			current = interval
			continue
		}
		if !interval.start.After(current.end) {
			if interval.end.After(current.end) {
				current.end = interval.end
			}
			continue
		}
		merged = append(merged, current)
		current = interval
	}
	if !current.end.IsZero() {
		merged = append(merged, current)
	}
	spans := make([]webpprof.MiddlewareWorkSpan, 0, len(merged)+1)
	workDuration := time.Duration(0)
	cursor := start
	for _, interval := range merged {
		if interval.start.After(cursor) {
			duration := interval.start.Sub(cursor)
			spans = append(spans, webpprof.MiddlewareWorkSpan{Offset: cursor.Sub(start), Duration: duration})
			workDuration += duration
		}
		if interval.end.After(cursor) {
			cursor = interval.end
		}
	}
	if end.After(cursor) {
		duration := end.Sub(cursor)
		spans = append(spans, webpprof.MiddlewareWorkSpan{Offset: cursor.Sub(start), Duration: duration})
		workDuration += duration
	}
	return spans, workDuration
}

// Middleware records inbound requests handled by next using the default
// profiler. It panics when next is nil.
func Middleware(next stdlibhttp.Handler) stdlibhttp.Handler {
	return MiddlewareWith(webpprof.Default(), next)
}

// MiddlewareWith records inbound requests handled by next using p. A nil
// profiler returns next unchanged; a nil handler causes a panic.
func MiddlewareWith(p *webpprof.Profiler, next stdlibhttp.Handler) stdlibhttp.Handler {
	if next == nil {
		panic("webpprof: nil HTTP handler")
	}
	if p == nil {
		return next
	}
	return stdlibhttp.HandlerFunc(func(w stdlibhttp.ResponseWriter, r *stdlibhttp.Request) {
		if !p.ShouldCaptureRequest(r) {
			next.ServeHTTP(w, r.WithContext(webpprof.WithoutRecording(r.Context())))
			return
		}
		requestMessage := SnapshotRequest(r, p.BodyLimit())
		capture := p.BeginRequest(webpprof.Request{
			Meta:        webpprof.Meta{Tags: webpprof.TagsFromContext(r.Context())},
			Method:      r.Method,
			Path:        r.URL.Path,
			Query:       sanitizeQuery(r.URL.RawQuery),
			Scheme:      RequestScheme(r),
			Protocol:    r.Proto,
			Host:        r.Host,
			RemoteIP:    remoteIP(r.RemoteAddr),
			RequestSize: r.ContentLength,
			Request:     requestMessage,
		})
		observer := &responseObserver{ResponseWriter: w, status: stdlibhttp.StatusOK, body: NewBodyRecorder(p.BodyLimit())}
		r = r.WithContext(webpprof.WithRequest(r.Context(), capture))
		defer func() {
			if recovered := recover(); recovered != nil {
				p.LogExceptionContext(r.Context(), webpprof.PanicException(recovered))
				capture.Finish(webpprof.RequestResult{Status: stdlibhttp.StatusInternalServerError, ResponseSize: observer.size, Response: observer.body.Message(observer.Header(), observer.size), Error: fmt.Sprint(recovered)})
				panic(recovered)
			}
			capture.Finish(webpprof.RequestResult{
				Status:       observer.status,
				ResponseSize: observer.size,
				Response:     observer.body.Message(observer.Header(), observer.size),
			})
		}()
		next.ServeHTTP(observer, r)
	})
}

// ProfileMiddleware wraps a standard HTTP middleware and records each
// invocation by name. The complete invocation span includes downstream work;
// Middleware.WorkDuration excludes time delegated to the downstream handler.
func ProfileMiddleware(name string, middleware func(stdlibhttp.Handler) stdlibhttp.Handler) func(stdlibhttp.Handler) stdlibhttp.Handler {
	return ProfileMiddlewareWith(webpprof.Default(), name, middleware)
}

// ProfileMiddlewareWith is ProfileMiddleware using an explicit profiler.
// Place MiddlewareWith outside the returned middleware chain so the invocation
// can be correlated with the captured request.
func ProfileMiddlewareWith(p *webpprof.Profiler, name string, middleware func(stdlibhttp.Handler) stdlibhttp.Handler) func(stdlibhttp.Handler) stdlibhttp.Handler {
	if middleware == nil {
		panic("webpprof: nil HTTP middleware")
	}
	if p == nil {
		return middleware
	}
	return func(next stdlibhttp.Handler) stdlibhttp.Handler {
		timedNext := stdlibhttp.HandlerFunc(func(w stdlibhttp.ResponseWriter, r *stdlibhttp.Request) {
			startedAt := time.Now()
			timing, _ := r.Context().Value(middlewareTimingContextKey{}).(*middlewareTiming)
			if timing == nil {
				next.ServeHTTP(w, r)
				return
			}
			defer func() { timing.recordDownstream(startedAt, time.Now()) }()
			next.ServeHTTP(w, r)
		})
		wrapped := middleware(timedNext)
		if wrapped == nil {
			panic("webpprof: HTTP middleware returned a nil handler")
		}
		return stdlibhttp.HandlerFunc(func(w stdlibhttp.ResponseWriter, r *stdlibhttp.Request) {
			timing := &middlewareTiming{}
			invocation := webpprof.Middleware{
				Meta: webpprof.Meta{
					ID:       webpprof.NewID(),
					ParentID: webpprof.ParentEntryIDFromContext(r.Context()),
				},
				Name:  name,
				State: "completed",
			}
			ctx := webpprof.WithParentEntry(r.Context(), invocation.ID)
			ctx = context.WithValue(ctx, middlewareTimingContextKey{}, timing)
			profiledRequest := r.WithContext(ctx)
			startedAt := time.Now()
			invocation.StartedAt = startedAt.UTC()
			defer func() {
				finishedAt := time.Now()
				invocation.Duration = finishedAt.Sub(startedAt)
				workSpans, workDuration := timing.workSpans(startedAt, finishedAt)
				invocation.WorkSpans = workSpans
				invocation.WorkDuration = &workDuration
				if recovered := recover(); recovered != nil {
					invocation.State = "panicked"
					invocation.Error = fmt.Sprint(recovered)
					p.LogMiddlewareContext(r.Context(), invocation)
					panic(recovered)
				}
				p.LogMiddlewareContext(r.Context(), invocation)
			}()
			wrapped.ServeHTTP(w, profiledRequest)
		})
	}
}

func (w *responseObserver) WriteHeader(status int) {
	if status >= 100 && status < 200 {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *responseObserver) Write(data []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(stdlibhttp.StatusOK)
	}
	_, _ = w.body.Write(data)
	written, err := w.ResponseWriter.Write(data)
	w.size += int64(written)
	return written, err
}

func (w *responseObserver) ReadFrom(reader io.Reader) (int64, error) {
	if !w.wroteHeader {
		w.WriteHeader(stdlibhttp.StatusOK)
	}
	return io.Copy(struct{ io.Writer }{w}, reader)
}

func (w *responseObserver) Flush() {
	if !w.wroteHeader {
		w.WriteHeader(stdlibhttp.StatusOK)
	}
	_ = stdlibhttp.NewResponseController(w.ResponseWriter).Flush()
}

func (w *responseObserver) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return stdlibhttp.NewResponseController(w.ResponseWriter).Hijack()
}

func (w *responseObserver) Push(target string, options *stdlibhttp.PushOptions) error {
	pusher, ok := w.ResponseWriter.(stdlibhttp.Pusher)
	if !ok {
		return stdlibhttp.ErrNotSupported
	}
	return pusher.Push(target, options)
}

func (w *responseObserver) Unwrap() stdlibhttp.ResponseWriter {
	return w.ResponseWriter
}

func sanitizeQuery(raw string) string {
	values, err := url.ParseQuery(raw)
	if err != nil {
		return "[REDACTED]"
	}
	for key := range values {
		if webpprof.IsSensitiveKey(key) {
			values[key] = []string{"[REDACTED]"}
		}
	}
	return strings.ReplaceAll(values.Encode(), url.QueryEscape("[REDACTED]"), "[REDACTED]")
}

// SanitizeQuery parses a raw query string and redacts values whose keys match
// webpprof's sensitive-key policy. Invalid encodings return "[REDACTED]".
func SanitizeQuery(raw string) string {
	return sanitizeQuery(raw)
}

// RequestScheme returns the original HTTP scheme when it can be determined.
// It accepts a valid X-Forwarded-Proto value for applications behind a reverse
// proxy and otherwise falls back to the request TLS state.
func RequestScheme(request *stdlibhttp.Request) string {
	if request == nil {
		return "http"
	}
	for _, candidate := range []string{request.URL.Scheme, strings.Split(request.Header.Get("X-Forwarded-Proto"), ",")[0]} {
		scheme := strings.ToLower(strings.TrimSpace(candidate))
		if scheme == "http" || scheme == "https" {
			return scheme
		}
	}
	if request.TLS != nil {
		return "https"
	}
	return "http"
}

func remoteIP(address string) string {
	host, _, err := net.SplitHostPort(address)
	if err == nil {
		return host
	}
	return address
}
