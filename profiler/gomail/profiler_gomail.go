// Package gomail records messages sent through github.com/wneessen/go-mail as
// webpprof email entries without capturing message bodies.
package gomail

import (
	"context"
	"net/mail"
	"time"

	"github.com/levskiy0/webpprof"
	gomail "github.com/wneessen/go-mail"
)

// Client is the subset of go-mail.Client required by this integration.
type Client interface {
	// DialAndSendWithContext connects and sends messages using ctx.
	DialAndSendWithContext(context.Context, ...*gomail.Msg) error
}

// ProfilerGoMail implements webpprof.Integration for go-mail clients.
type ProfilerGoMail struct{}

type profiledClient struct {
	inner    Client
	profiler *webpprof.Profiler
}

// New creates a go-mail integration.
func New() ProfilerGoMail {
	return ProfilerGoMail{}
}

// Name returns the integration cache namespace.
func (ProfilerGoMail) Name() string {
	return "go-mail"
}

// Profile wraps client so each send attempt is recorded. An existing wrapper
// for the same profiler is returned unchanged.
func (ProfilerGoMail) Profile(scope webpprof.Scope, client Client) Client {
	profiler := scope.Profiler()
	if profiler == nil || client == nil {
		return client
	}
	if wrapped, ok := client.(*profiledClient); ok && wrapped.profiler == profiler {
		return client
	}
	return &profiledClient{inner: client, profiler: profiler}
}

// Profile instruments client with the default profiler.
func Profile(client Client) Client {
	return webpprof.Profile(client, New())
}

// ProfileWith instruments client with an explicit profiler.
func ProfileWith(profiler *webpprof.Profiler, client Client) Client {
	return webpprof.ProfileWith(profiler, client, New())
}

func (c *profiledClient) DialAndSendWithContext(ctx context.Context, messages ...*gomail.Msg) error {
	startedAt := time.Now().UTC()
	err := c.inner.DialAndSendWithContext(ctx, messages...)
	duration := time.Since(startedAt)
	for _, message := range messages {
		event := webpprof.Email{Meta: webpprof.Meta{StartedAt: startedAt, Duration: duration}, Transport: "smtp", Status: "sent"}
		if message != nil {
			event.From = firstAddress(message.GetFrom())
			event.To = addresses(message.GetTo())
			event.CC = addresses(message.GetCc())
			event.BCC = addresses(message.GetBcc())
			if subjects := message.GetGenHeader(gomail.HeaderSubject); len(subjects) > 0 {
				event.Subject = subjects[0]
			}
		}
		if err != nil {
			event.Status = "failed"
			event.Error = err.Error()
		}
		c.profiler.LogEmailContext(ctx, event)
	}
	return err
}

func firstAddress(values []*mail.Address) webpprof.Address {
	if len(values) == 0 {
		return webpprof.Address{}
	}
	return webpprof.Address{Name: values[0].Name, Email: values[0].Address}
}

func addresses(values []*mail.Address) []webpprof.Address {
	result := make([]webpprof.Address, len(values))
	for index, address := range values {
		result[index] = webpprof.Address{Name: address.Name, Email: address.Address}
	}
	return result
}

var _ webpprof.Integration[Client] = ProfilerGoMail{}
var _ Client = (*profiledClient)(nil)
