package gin

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/levskiy0/webpprof"
	webpprofhttp "github.com/levskiy0/webpprof/profiler/http"
	"github.com/gin-gonic/gin"
)

type ginResponseObserver struct {
	gin.ResponseWriter
	body *webpprofhttp.BodyRecorder
}

type ProfilerGin struct {
	profiler *webpprof.Profiler
}

func New(profiler *webpprof.Profiler) ProfilerGin {
	return ProfilerGin{profiler: profiler}
}

func Middleware() gin.HandlerFunc {
	return New(webpprof.Default()).Middleware()
}

func MiddlewareWith(p *webpprof.Profiler) gin.HandlerFunc {
	return New(p).Middleware()
}

func (p ProfilerGin) Middleware() gin.HandlerFunc {
	profiler := p.profiler
	if profiler == nil {
		return func(c *gin.Context) { c.Next() }
	}
	return func(c *gin.Context) {
		if !profiler.ShouldCaptureRequest(c.Request) {
			c.Request = c.Request.WithContext(webpprof.WithoutRecording(c.Request.Context()))
			c.Next()
			return
		}
		requestMessage := webpprofhttp.SnapshotRequest(c.Request, profiler.BodyLimit())
		observer := &ginResponseObserver{ResponseWriter: c.Writer, body: webpprofhttp.NewBodyRecorder(profiler.BodyLimit())}
		c.Writer = observer
		capture := profiler.BeginRequest(webpprof.Request{Method: c.Request.Method, Path: sanitizeGinPath(c.FullPath(), c.Request.URL.Path), Route: c.FullPath(), Query: webpprofhttp.SanitizeQuery(c.Request.URL.RawQuery), Protocol: c.Request.Proto, Host: c.Request.Host, RemoteIP: c.ClientIP(), RequestSize: c.Request.ContentLength, Request: requestMessage})
		c.Request = c.Request.WithContext(webpprof.WithRequest(c.Request.Context(), capture))
		defer func() {
			errorMessage := ""
			if len(c.Errors) > 0 {
				errorMessage = c.Errors.String()
			}
			if recovered := recover(); recovered != nil {
				errorMessage = fmt.Sprint(recovered)
				capture.Finish(webpprof.RequestResult{Status: http.StatusInternalServerError, ResponseSize: int64(c.Writer.Size()), Response: observer.body.Message(c.Writer.Header(), int64(c.Writer.Size())), Error: errorMessage})
				panic(recovered)
			}
			capture.Finish(webpprof.RequestResult{Status: c.Writer.Status(), ResponseSize: int64(c.Writer.Size()), Response: observer.body.Message(c.Writer.Header(), int64(c.Writer.Size())), Error: errorMessage})
		}()
		c.Next()
	}
}

func (w *ginResponseObserver) Write(data []byte) (int, error) {
	_, _ = w.body.Write(data)
	return w.ResponseWriter.Write(data)
}

func (w *ginResponseObserver) WriteString(data string) (int, error) {
	_, _ = w.body.Write([]byte(data))
	return w.ResponseWriter.WriteString(data)
}

func sanitizeGinPath(route, raw string) string {
	if route == "" {
		return raw
	}
	actualParts := strings.Split(raw, "/")
	routeParts := strings.Split(route, "/")
	if len(actualParts) != len(routeParts) {
		return raw
	}
	for index, part := range routeParts {
		if len(part) > 1 && (part[0] == ':' || part[0] == '*') && webpprof.IsSensitiveKey(part[1:]) {
			actualParts[index] = "[REDACTED]"
		}
	}
	return strings.Join(actualParts, "/")
}
