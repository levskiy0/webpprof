// Package email instruments application-defined email senders without tying
// webpprof to a particular mail transport.
package email

import (
	"context"
	"time"

	"github.com/levskiy0/webpprof"
)

// Sender is the minimal email transport contract wrapped by this package.
type Sender interface {
	// Send delivers email using ctx for cancellation and request correlation.
	Send(context.Context, webpprof.Email) error
}

// SenderFunc adapts a function to Sender.
type SenderFunc func(context.Context, webpprof.Email) error

// ProfilerEmail implements webpprof.Integration for Sender values.
type ProfilerEmail struct{}

type profiledEmailSender struct {
	inner    Sender
	profiler *webpprof.Profiler
}

// Send calls f with ctx and email.
func (f SenderFunc) Send(ctx context.Context, email webpprof.Email) error {
	return f(ctx, email)
}

// New creates an email sender integration.
func New() ProfilerEmail {
	return ProfilerEmail{}
}

// Name returns the integration cache namespace.
func (ProfilerEmail) Name() string {
	return "email"
}

// Profile wraps sender so each delivery attempt is recorded. An already wrapped
// sender for the same profiler is returned unchanged.
func (ProfilerEmail) Profile(scope webpprof.Scope, sender Sender) Sender {
	p := scope.Profiler()
	if p == nil || sender == nil {
		return sender
	}
	if wrapped, ok := sender.(*profiledEmailSender); ok && wrapped.profiler == p {
		return sender
	}
	return &profiledEmailSender{inner: sender, profiler: p}
}

// Profile wraps sender with the default profiler.
func Profile(sender Sender) Sender {
	return webpprof.Profile(sender, New())
}

// ProfileWith wraps sender with an explicit profiler.
func ProfileWith(profiler *webpprof.Profiler, sender Sender) Sender {
	return webpprof.ProfileWith(profiler, sender, New())
}

func (s *profiledEmailSender) Send(ctx context.Context, email webpprof.Email) error {
	if email.StartedAt.IsZero() {
		email.StartedAt = time.Now().UTC()
	}
	err := s.inner.Send(ctx, email)
	email.Duration = time.Since(email.StartedAt)
	email.Status = "sent"
	if err != nil {
		email.Status = "failed"
		email.Error = err.Error()
	}
	s.profiler.LogEmailContext(ctx, email)
	return err
}

var _ webpprof.Integration[Sender] = ProfilerEmail{}
var _ Sender = (*profiledEmailSender)(nil)
