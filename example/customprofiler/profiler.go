// Package customprofiler demonstrates how to adapt an application dependency
// to the generic webpprof integration contract.
package customprofiler

import (
	"context"
	"time"

	"github.com/levskiy0/webpprof"
)

// Client is the smallest part of the wrapped dependency used by the example.
// A real integration should include only the SDK methods it needs to intercept.
type Client interface {
	Lookup(context.Context, string) (string, error)
}

// Profiler implements webpprof.Integration for Client.
type Profiler struct{}

type profiledClient struct {
	inner    Client
	profiler *webpprof.Profiler
}

// New creates the custom profiler integration.
func New() Profiler {
	return Profiler{}
}

// Name returns a stable key used to isolate wrapped values in webpprof.Scope.
func (Profiler) Name() string {
	return "example-custom-client"
}

// Profile wraps client once for this profiler runtime.
func (Profiler) Profile(scope webpprof.Scope, client Client) Client {
	profiler := scope.Profiler()
	if profiler == nil || client == nil {
		return client
	}
	if wrapped, ok := client.(*profiledClient); ok && wrapped.profiler == profiler {
		return client
	}

	wrapped := Client(&profiledClient{inner: client, profiler: profiler})
	actual, _ := scope.LoadOrStore(client, wrapped)
	profiled, ok := actual.(Client)
	if !ok {
		return wrapped
	}
	return profiled
}

// Profile uses the package-level default webpprof runtime.
func Profile(client Client) Client {
	return webpprof.Profile(client, New())
}

// ProfileWith uses an explicit webpprof runtime.
func ProfileWith(profiler *webpprof.Profiler, client Client) Client {
	return webpprof.ProfileWith(profiler, client, New())
}

func (c *profiledClient) Lookup(ctx context.Context, key string) (string, error) {
	startedAt := time.Now().UTC()
	value, err := c.inner.Lookup(ctx, key)

	status := "found"
	if value == "" {
		status = "missing"
	}
	if err != nil {
		status = "failed"
	}

	event := webpprof.Event{
		Meta: webpprof.Meta{
			StartedAt: startedAt,
			Duration:  time.Since(startedAt),
		},
		Kind:    "custom-client",
		Name:    "lookup",
		Status:  status,
		Summary: "Lookup " + key,
		Fields: map[string]any{
			"key":   key,
			"found": value != "",
		},
	}
	if err != nil {
		event.Error = err.Error()
	}

	c.profiler.LogEventContext(ctx, event)
	return value, err
}

var _ webpprof.Integration[Client] = Profiler{}
var _ Client = (*profiledClient)(nil)
