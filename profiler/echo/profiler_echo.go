// Package echo provides request profiling middleware for Echo.
package echo

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/levskiy0/webpprof"
	webpprofhttp "github.com/levskiy0/webpprof/profiler/http"
)

// Middleware profiles Echo requests with the default profiler.
func Middleware() echo.MiddlewareFunc { return MiddlewareWith(webpprof.Default()) }

// MiddlewareWith profiles Echo requests with p and captures the matched route.
func MiddlewareWith(p *webpprof.Profiler) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		if p == nil {
			return next
		}
		return func(c echo.Context) (err error) {
			request := c.Request()
			if !p.ShouldCaptureRequest(request) {
				c.SetRequest(request.WithContext(webpprof.WithoutRecording(request.Context())))
				return next(c)
			}
			capture := p.BeginRequest(webpprof.Request{
				Meta:        webpprof.Meta{Tags: webpprof.TagsFromContext(request.Context()), StartedAt: time.Now().UTC()},
				Method:      request.Method,
				Path:        request.URL.Path,
				Query:       webpprofhttp.SanitizeQuery(request.URL.RawQuery),
				Scheme:      c.Scheme(),
				Protocol:    request.Proto,
				Host:        request.Host,
				RemoteIP:    c.RealIP(),
				RequestSize: request.ContentLength,
				Request:     webpprofhttp.SnapshotRequest(request, p.BodyLimit()),
			})
			request = request.WithContext(webpprof.WithRequest(request.Context(), capture))
			c.SetRequest(request)
			defer func() {
				capture.SetRoute(c.Path())
				if recovered := recover(); recovered != nil {
					p.LogExceptionContext(request.Context(), webpprof.PanicException(recovered))
					capture.Finish(webpprof.RequestResult{Status: http.StatusInternalServerError, Error: fmt.Sprint(recovered)})
					panic(recovered)
				}
				statusCode := c.Response().Status
				if statusCode == 0 {
					statusCode = echoStatus(err)
				}
				capture.Finish(webpprof.RequestResult{Status: statusCode, ResponseSize: c.Response().Size, Error: errorString(err)})
			}()
			err = next(c)
			return err
		}
	}
}

func echoStatus(err error) int {
	if err == nil {
		return http.StatusOK
	}
	var httpError *echo.HTTPError
	if errors.As(err, &httpError) {
		return httpError.Code
	}
	return http.StatusInternalServerError
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
