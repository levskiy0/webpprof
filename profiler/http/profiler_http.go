// Package http provides inbound net/http middleware and an outbound transport
// wrapper that correlate HTTP activity with webpprof request captures.
package http

import (
	stdlibhttp "net/http"
	"time"

	"github.com/levskiy0/webpprof"
)

type profiledRoundTripper struct {
	inner    stdlibhttp.RoundTripper
	profiler *webpprof.Profiler
}

// ProfilerHTTP implements webpprof.Integration for HTTP round trippers.
type ProfilerHTTP struct{}

// New creates an outbound HTTP transport integration.
func New() ProfilerHTTP {
	return ProfilerHTTP{}
}

// Name returns the integration cache namespace.
func (ProfilerHTTP) Name() string {
	return "http-client"
}

// Profile wraps transport so outbound requests are recorded. A nil transport
// is treated as http.DefaultTransport.
func (ProfilerHTTP) Profile(scope webpprof.Scope, transport stdlibhttp.RoundTripper) stdlibhttp.RoundTripper {
	p := scope.Profiler()
	if transport == nil {
		transport = stdlibhttp.DefaultTransport
	}
	if p == nil {
		return transport
	}
	if wrapped, ok := transport.(*profiledRoundTripper); ok && wrapped.profiler == p {
		return transport
	}
	return &profiledRoundTripper{inner: transport, profiler: p}
}

// ProfileTransport instruments transport with the default profiler.
func ProfileTransport(transport stdlibhttp.RoundTripper) stdlibhttp.RoundTripper {
	return webpprof.Profile(transport, New())
}

// ProfileTransportWith instruments transport with an explicit profiler.
func ProfileTransportWith(profiler *webpprof.Profiler, transport stdlibhttp.RoundTripper) stdlibhttp.RoundTripper {
	return webpprof.ProfileWith(profiler, transport, New())
}

func (t *profiledRoundTripper) RoundTrip(request *stdlibhttp.Request) (*stdlibhttp.Response, error) {
	startedAt := time.Now().UTC()
	response, err := t.inner.RoundTrip(request)
	event := webpprof.HTTPCall{Meta: webpprof.Meta{StartedAt: startedAt, Duration: time.Since(startedAt)}, Method: request.Method, URL: request.URL.Redacted()}
	if response != nil {
		event.Status = response.StatusCode
		event.ResponseSize = response.ContentLength
		event.Response = webpprof.HTTPMessage{Headers: response.Header.Clone(), ContentType: response.Header.Get("Content-Type"), Size: response.ContentLength}
	}
	if err != nil {
		event.Error = err.Error()
	}
	t.profiler.LogHTTPCallContext(request.Context(), event)
	return response, err
}

var _ webpprof.Integration[stdlibhttp.RoundTripper] = ProfilerHTTP{}
var _ stdlibhttp.RoundTripper = (*profiledRoundTripper)(nil)
