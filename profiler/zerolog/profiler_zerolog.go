// Package zerolog provides a zerolog hook that mirrors log messages into
// webpprof without adding zerolog to the core module.
package zerolog

import (
	"context"

	"github.com/levskiy0/webpprof"
	"github.com/rs/zerolog"
)

// Profile attaches a webpprof hook to logger using the default profiler.
func Profile(logger zerolog.Logger) zerolog.Logger {
	return ProfileWith(webpprof.Default(), logger)
}

// ProfileWith attaches a webpprof hook to logger using p. The returned logger
// retains all existing writers, levels, context fields, and hooks.
func ProfileWith(p *webpprof.Profiler, logger zerolog.Logger) zerolog.Logger {
	if p == nil {
		return logger
	}
	return logger.Hook(Hook{Profiler: p})
}

// Hook records zerolog events after zerolog accepts their level.
type Hook struct {
	Profiler *webpprof.Profiler
}

// Run implements zerolog.Hook.
func (h Hook) Run(event *zerolog.Event, level zerolog.Level, message string) {
	if h.Profiler == nil {
		return
	}
	ctx := context.Background()
	if event != nil && event.GetCtx() != nil {
		ctx = event.GetCtx()
	}
	h.Profiler.LogLogContext(ctx, webpprof.Log{
		Level:   level.String(),
		Message: message,
	})
}

var _ zerolog.Hook = Hook{}
