package http

import (
	"bufio"
	"fmt"
	"io"
	"net"
	stdlibhttp "net/http"
	"net/url"
	"strings"
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

func Middleware(next stdlibhttp.Handler) stdlibhttp.Handler {
	return MiddlewareWith(webpprof.Default(), next)
}

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
// invocation by name. The recorded duration is inclusive of downstream work.
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
		wrapped := middleware(next)
		if wrapped == nil {
			panic("webpprof: HTTP middleware returned a nil handler")
		}
		return stdlibhttp.HandlerFunc(func(w stdlibhttp.ResponseWriter, r *stdlibhttp.Request) {
			invocation := webpprof.Middleware{
				Meta: webpprof.Meta{
					ID:        webpprof.NewID(),
					ParentID:  webpprof.ParentEntryIDFromContext(r.Context()),
					StartedAt: time.Now().UTC(),
				},
				Name:  name,
				State: "completed",
			}
			defer func() {
				invocation.Duration = time.Since(invocation.StartedAt)
				if recovered := recover(); recovered != nil {
					invocation.State = "panicked"
					invocation.Error = fmt.Sprint(recovered)
					p.LogMiddlewareContext(r.Context(), invocation)
					panic(recovered)
				}
				p.LogMiddlewareContext(r.Context(), invocation)
			}()
			ctx := webpprof.WithParentEntry(r.Context(), invocation.ID)
			wrapped.ServeHTTP(w, r.WithContext(ctx))
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
