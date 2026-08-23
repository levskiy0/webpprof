// Package fiber provides request profiling middleware for Fiber v3.
package fiber

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/levskiy0/webpprof"
	webpprofhttp "github.com/levskiy0/webpprof/profiler/http"
)

// Middleware profiles Fiber requests with the default profiler.
func Middleware() fiber.Handler { return MiddlewareWith(webpprof.Default()) }

// MiddlewareWith profiles Fiber requests with p and captures FullPath.
func MiddlewareWith(p *webpprof.Profiler) fiber.Handler {
	if p == nil {
		return func(c fiber.Ctx) error { return c.Next() }
	}
	return func(c fiber.Ctx) (err error) {
		ctx := c.Context()
		filterRequest := syntheticRequest(c)
		if !p.ShouldCaptureRequest(filterRequest) {
			c.SetContext(webpprof.WithoutRecording(ctx))
			return c.Next()
		}
		requestBody := bodyMessage(c.Request().Body(), c.GetReqHeaders(), p.BodyLimit())
		capture := p.BeginRequest(webpprof.Request{
			Meta:        webpprof.Meta{Tags: webpprof.TagsFromContext(ctx), StartedAt: time.Now().UTC()},
			Method:      c.Method(),
			Path:        c.Path(),
			Query:       webpprofhttp.SanitizeQuery(string(c.Request().URI().QueryString())),
			Scheme:      c.Protocol(),
			Protocol:    "HTTP/1.1",
			Host:        string(c.Request().Host()),
			RemoteIP:    c.IP(),
			RequestSize: int64(len(c.Request().Body())),
			Request:     requestBody,
		})
		ctx = webpprof.WithRequest(ctx, capture)
		c.SetContext(ctx)
		defer func() {
			capture.SetRoute(c.FullPath())
			if recovered := recover(); recovered != nil {
				p.LogExceptionContext(ctx, webpprof.PanicException(recovered))
				capture.Finish(webpprof.RequestResult{Status: http.StatusInternalServerError, Error: fmt.Sprint(recovered)})
				panic(recovered)
			}
			statusCode := c.Response().StatusCode()
			if statusCode == 0 || (statusCode == http.StatusOK && err != nil) {
				statusCode = fiberStatus(err)
			}
			responseBody := bodyMessage(c.Response().Body(), c.GetRespHeaders(), p.BodyLimit())
			capture.Finish(webpprof.RequestResult{
				Status:       statusCode,
				ResponseSize: int64(len(c.Response().Body())),
				Response:     responseBody,
				Error:        errorString(err),
			})
		}()
		err = c.Next()
		return err
	}
}

func syntheticRequest(c fiber.Ctx) *http.Request {
	parsed, err := url.Parse(c.OriginalURL())
	if err != nil {
		parsed = &url.URL{Path: c.Path()}
	}
	return &http.Request{Method: c.Method(), URL: parsed, Header: cloneHeaders(c.GetReqHeaders()), Host: string(c.Request().Host())}
}

func bodyMessage(body []byte, headers map[string][]string, limit int64) webpprof.HTTPMessage {
	recorder := webpprofhttp.NewBodyRecorder(limit)
	_, _ = recorder.Write(body)
	return recorder.Message(cloneHeaders(headers), int64(len(body)))
}

func cloneHeaders(headers map[string][]string) http.Header {
	cloned := make(http.Header, len(headers))
	for key, values := range headers {
		cloned[key] = append([]string(nil), values...)
	}
	return cloned
}

func fiberStatus(err error) int {
	if err == nil {
		return http.StatusOK
	}
	var fiberError *fiber.Error
	if errors.As(err, &fiberError) {
		return fiberError.Code
	}
	return http.StatusInternalServerError
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
