// Package nats profiles core NATS publish and subscription handlers.
package nats

import (
	"context"
	"fmt"
	"time"

	"github.com/levskiy0/webpprof"
	"github.com/nats-io/nats.go"
)

// Config controls NATS metadata and optional bounded payload capture.
type Config struct {
	Connection     string
	CapturePayload bool
	PayloadLimit   int
}

// Client is the core NATS API wrapped by Profile. Retain the original
// *nats.Conn for lifecycle and APIs outside this focused interface.
type Client interface {
	// Publish sends payload to subject.
	Publish(string, []byte) error
	// PublishMsg sends a prepared NATS message.
	PublishMsg(*nats.Msg) error
	// Subscribe registers a handler for subject.
	Subscribe(string, nats.MsgHandler) (*nats.Subscription, error)
	// QueueSubscribe registers a queue-group handler for subject.
	QueueSubscribe(string, string, nats.MsgHandler) (*nats.Subscription, error)
}

// ProfiledClient adds a context-aware publish operation for request
// correlation while preserving the wrapped core NATS API.
type ProfiledClient interface {
	Client
	// PublishContext publishes while correlating the dispatch with ctx.
	PublishContext(context.Context, string, []byte) error
}

// Profile wraps client with the default profiler. It returns nil for a nil
// client and otherwise preserves core NATS operations.
func Profile(client Client, configs ...Config) ProfiledClient {
	return ProfileWith(webpprof.Default(), client, configs...)
}

// ProfileWith wraps client with p. When p is nil, operations still delegate to
// client but no entries are recorded.
func ProfileWith(p *webpprof.Profiler, client Client, configs ...Config) ProfiledClient {
	if client == nil {
		return nil
	}
	return &profiledClient{inner: client, profiler: p, config: firstConfig(configs)}
}

type profiledClient struct {
	inner    Client
	profiler *webpprof.Profiler
	config   Config
}

func (c *profiledClient) Publish(subject string, payload []byte) error {
	return c.PublishContext(context.Background(), subject, payload)
}

// PublishContext publishes while correlating the dispatch with ctx.
func (c *profiledClient) PublishContext(ctx context.Context, subject string, payload []byte) error {
	startedAt := time.Now().UTC()
	var callsite []webpprof.SourceFrame
	if c.profiler != nil {
		callsite = c.profiler.CaptureCallsite(webpprof.KindJob)
	}
	err := c.inner.Publish(subject, payload)
	c.record(ctx, subject, "", "dispatched", payload, startedAt, callsite, err)
	return err
}

func (c *profiledClient) PublishMsg(message *nats.Msg) error {
	startedAt := time.Now().UTC()
	var callsite []webpprof.SourceFrame
	if c.profiler != nil {
		callsite = c.profiler.CaptureCallsite(webpprof.KindJob)
	}
	err := c.inner.PublishMsg(message)
	if message == nil {
		c.record(context.Background(), "", "", "failed", nil, startedAt, callsite, err)
		return err
	}
	c.record(context.Background(), message.Subject, "", "dispatched", message.Data, startedAt, callsite, err)
	return err
}

func (c *profiledClient) Subscribe(subject string, handler nats.MsgHandler) (*nats.Subscription, error) {
	if c.profiler == nil {
		return c.inner.Subscribe(subject, handler)
	}
	return c.inner.Subscribe(subject, c.wrapHandler(subject, "", handler))
}

func (c *profiledClient) QueueSubscribe(subject, queue string, handler nats.MsgHandler) (*nats.Subscription, error) {
	if c.profiler == nil {
		return c.inner.QueueSubscribe(subject, queue, handler)
	}
	return c.inner.QueueSubscribe(subject, queue, c.wrapHandler(subject, queue, handler))
}

func (c *profiledClient) wrapHandler(subject, queue string, handler nats.MsgHandler) nats.MsgHandler {
	return func(message *nats.Msg) {
		startedAt := time.Now().UTC()
		var payload []byte
		actualSubject := subject
		if message != nil {
			payload = message.Data
			if message.Subject != "" {
				actualSubject = message.Subject
			}
		}
		defer func() {
			if recovered := recover(); recovered != nil {
				c.record(context.Background(), actualSubject, queue, "failed", payload, startedAt, nil, fmt.Errorf("panic: %v", recovered))
				panic(recovered)
			}
		}()
		handler(message)
		c.record(context.Background(), actualSubject, queue, "succeeded", payload, startedAt, nil, nil)
	}
}

func (c *profiledClient) record(ctx context.Context, subject, queue, state string, payload []byte, startedAt time.Time, callsite []webpprof.SourceFrame, err error) {
	if c.profiler == nil {
		return
	}
	if err != nil {
		state = "failed"
	}
	c.profiler.LogJobContext(ctx, webpprof.Job{
		Meta:       webpprof.Meta{StartedAt: startedAt, Duration: time.Since(startedAt), Tags: map[string]string{"messaging.system": "nats", "messaging.subject": subject}},
		Name:       subject,
		Queue:      queue,
		Connection: c.config.Connection,
		State:      state,
		Arguments:  payloadArgument(payload, c.config.CapturePayload, c.config.PayloadLimit),
		Callsite:   callsite,
		Error:      errorString(err),
	})
}

func payloadArgument(payload []byte, capture bool, limit int) []webpprof.Argument {
	if len(payload) == 0 {
		return nil
	}
	argument := webpprof.Argument{Name: "payload", Type: "bytes", Size: int64(len(payload))}
	if !capture {
		return []webpprof.Argument{argument}
	}
	if limit <= 0 {
		limit = 4096
	}
	if len(payload) > limit {
		payload = payload[:limit]
		argument.Truncated = true
	}
	argument.Value = string(payload)
	return []webpprof.Argument{argument}
}

func firstConfig(configs []Config) Config {
	if len(configs) == 0 {
		return Config{}
	}
	return configs[0]
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

var _ Client = (*profiledClient)(nil)
var _ ProfiledClient = (*profiledClient)(nil)
