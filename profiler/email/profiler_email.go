package email

import (
	"context"
	"time"

	"github.com/levskiy0/webpprof"
)

type Sender interface {
	Send(context.Context, webpprof.Email) error
}

type SenderFunc func(context.Context, webpprof.Email) error

type ProfilerEmail struct{}

type profiledEmailSender struct {
	inner    Sender
	profiler *webpprof.Profiler
}

func (f SenderFunc) Send(ctx context.Context, email webpprof.Email) error {
	return f(ctx, email)
}

func New() ProfilerEmail {
	return ProfilerEmail{}
}

func (ProfilerEmail) Name() string {
	return "email"
}

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

func Profile(sender Sender) Sender {
	return webpprof.Profile(sender, New())
}

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
