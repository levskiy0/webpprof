// Package zap wraps a zapcore.Core and mirrors accepted structured log entries
// into webpprof while preserving the original core.
package zap

import (
	"time"

	"github.com/levskiy0/webpprof"
	"go.uber.org/zap/zapcore"
)

// ProfilerZap implements webpprof.Integration for zap cores.
type ProfilerZap struct{}

type profiledZapCore struct {
	inner    zapcore.Core
	profiler *webpprof.Profiler
	fields   []zapcore.Field
}

// New creates a zap core integration.
func New() ProfilerZap {
	return ProfilerZap{}
}

// Name returns the integration cache namespace.
func (ProfilerZap) Name() string {
	return "zap"
}

// Profile wraps core so accepted entries and structured fields are mirrored into
// webpprof. Existing wrappers for the same profiler are reused.
func (ProfilerZap) Profile(scope webpprof.Scope, core zapcore.Core) zapcore.Core {
	p := scope.Profiler()
	if p == nil || core == nil {
		return core
	}
	if wrapped, ok := core.(*profiledZapCore); ok && wrapped.profiler == p {
		return core
	}
	return &profiledZapCore{inner: core, profiler: p}
}

// Profile instruments core with the default profiler.
func Profile(core zapcore.Core) zapcore.Core {
	return webpprof.Profile(core, New())
}

// ProfileWith instruments core with an explicit profiler.
func ProfileWith(profiler *webpprof.Profiler, core zapcore.Core) zapcore.Core {
	return webpprof.ProfileWith(profiler, core, New())
}

func (c *profiledZapCore) Enabled(level zapcore.Level) bool {
	return c.inner.Enabled(level)
}

func (c *profiledZapCore) With(fields []zapcore.Field) zapcore.Core {
	return &profiledZapCore{inner: c.inner.With(fields), profiler: c.profiler, fields: append(append([]zapcore.Field(nil), c.fields...), fields...)}
}

func (c *profiledZapCore) Check(entry zapcore.Entry, checked *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if c.inner.Check(entry, nil) == nil {
		return checked
	}
	return checked.AddCore(entry, c)
}

func (c *profiledZapCore) Write(entry zapcore.Entry, fields []zapcore.Field) error {
	encoder := zapcore.NewMapObjectEncoder()
	for _, field := range c.fields {
		field.AddTo(encoder)
	}
	for _, field := range fields {
		field.AddTo(encoder)
	}
	startedAt := entry.Time
	if startedAt.IsZero() {
		startedAt = time.Now().UTC()
	} else {
		startedAt = startedAt.UTC()
	}
	c.profiler.LogLog(webpprof.Log{Meta: webpprof.Meta{StartedAt: startedAt}, Level: entry.Level.String(), Message: entry.Message, Fields: encoder.Fields, Stack: entry.Stack})
	return c.inner.Write(entry, fields)
}

var _ webpprof.Integration[zapcore.Core] = ProfilerZap{}
var _ zapcore.Core = (*profiledZapCore)(nil)

func (c *profiledZapCore) Sync() error {
	return c.inner.Sync()
}
