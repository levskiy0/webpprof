package http

import (
	"bufio"
	"fmt"
	"io"
	"net"
	stdlibhttp "net/http"
	"net/url"
	"strings"

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
			Method:      r.Method,
			Path:        r.URL.Path,
			Query:       sanitizeQuery(r.URL.RawQuery),
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

func remoteIP(address string) string {
	host, _, err := net.SplitHostPort(address)
	if err == nil {
		return host
	}
	return address
}
