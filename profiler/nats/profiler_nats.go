// Package nats profiles core NATS publish and subscription handlers.
package nats

import (
	"context"
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
	Publish(string, []byte) error
	PublishMsg(*nats.Msg) error
	Subscribe(string, nats.MsgHandler) (*nats.Subscription, error)
	QueueSubscribe(string, string, nats.MsgHandler) (*nats.Subscription, error)
}

func Profile(client Client, configs ...Config) Client {
	return ProfileWith(webpprof.Default(), client, configs...)
}

func ProfileWith(p *webpprof.Profiler, client Client, configs ...Config) Client {
	if p == nil || client == nil {
		return client
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
	callsite := c.profiler.CaptureCallsite(webpprof.KindJob)
	err := c.inner.Publish(subject, payload)
	c.record(ctx, subject, "", "dispatched", payload, startedAt, callsite, err)
	return err
}

func (c *profiledClient) PublishMsg(message *nats.Msg) error {
	startedAt := time.Now().UTC()
	callsite := c.profiler.CaptureCallsite(webpprof.KindJob)
	err := c.inner.PublishMsg(message)
	if message == nil {
		c.record(context.Background(), "", "", "failed", nil, startedAt, callsite, err)
		return err
	}
	c.record(context.Background(), message.Subject, "", "dispatched", message.Data, startedAt, callsite, err)
	return err
}

func (c *profiledClient) Subscribe(subject string, handler nats.MsgHandler) (*nats.Subscription, error) {
	return c.inner.Subscribe(subject, c.wrapHandler(subject, "", handler))
}

func (c *profiledClient) QueueSubscribe(subject, queue string, handler nats.MsgHandler) (*nats.Subscription, error) {
	return c.inner.QueueSubscribe(subject, queue, c.wrapHandler(subject, queue, handler))
}

func (c *profiledClient) wrapHandler(subject, queue string, handler nats.MsgHandler) nats.MsgHandler {
	return func(message *nats.Msg) {
		startedAt := time.Now().UTC()
		var payload []byte
		if message != nil {
			payload = message.Data
			if message.Subject != "" {
				subject = message.Subject
			}
		}
		handler(message)
		c.record(context.Background(), subject, queue, "succeeded", payload, startedAt, nil, nil)
	}
}

func (c *profiledClient) record(ctx context.Context, subject, queue, state string, payload []byte, startedAt time.Time, callsite []webpprof.SourceFrame, err error) {
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
