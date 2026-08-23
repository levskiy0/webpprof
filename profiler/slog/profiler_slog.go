// Package slog wraps a log/slog Handler and mirrors accepted records into
// webpprof while preserving the original handler chain.
package slog

import (
	"context"
	stdlibslog "log/slog"

	"github.com/levskiy0/webpprof"
)

type profiledSlogHandler struct {
	inner    stdlibslog.Handler
	profiler *webpprof.Profiler
	attrs    []stdlibslog.Attr
	groups   []string
}

// ProfilerSlog implements webpprof.Integration for slog handlers.
type ProfilerSlog struct{}

// New creates a slog handler integration.
func New() ProfilerSlog {
	return ProfilerSlog{}
}

// Name returns the integration cache namespace.
func (ProfilerSlog) Name() string {
	return "slog"
}

// Profile wraps handler so accepted records, attributes, and groups are mirrored
// into webpprof. Existing wrappers for the same profiler are reused.
func (ProfilerSlog) Profile(scope webpprof.Scope, handler stdlibslog.Handler) stdlibslog.Handler {
	p := scope.Profiler()
	if p == nil || handler == nil {
		return handler
	}
	if wrapped, ok := handler.(*profiledSlogHandler); ok && wrapped.profiler == p {
		return handler
	}
	return &profiledSlogHandler{inner: handler, profiler: p}
}

// Profile instruments handler with the default profiler.
func Profile(handler stdlibslog.Handler) stdlibslog.Handler {
	return webpprof.Profile(handler, New())
}

// ProfileWith instruments handler with an explicit profiler.
func ProfileWith(profiler *webpprof.Profiler, handler stdlibslog.Handler) stdlibslog.Handler {
	return webpprof.ProfileWith(profiler, handler, New())
}

func (h *profiledSlogHandler) Enabled(ctx context.Context, level stdlibslog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *profiledSlogHandler) Handle(ctx context.Context, record stdlibslog.Record) error {
	fields := make(map[string]any, len(h.attrs)+record.NumAttrs())
	for _, attr := range h.attrs {
		addSlogAttr(fields, h.groups, attr)
	}
	record.Attrs(func(attr stdlibslog.Attr) bool {
		addSlogAttr(fields, h.groups, attr)
		return true
	})
	h.profiler.LogLogContext(ctx, webpprof.Log{Meta: webpprof.Meta{StartedAt: record.Time}, Level: record.Level.String(), Message: record.Message, Fields: fields})
	return h.inner.Handle(ctx, record)
}

func (h *profiledSlogHandler) WithAttrs(attrs []stdlibslog.Attr) stdlibslog.Handler {
	next := *h
	next.inner = h.inner.WithAttrs(attrs)
	next.attrs = append(append([]stdlibslog.Attr(nil), h.attrs...), attrs...)
	return &next
}

func (h *profiledSlogHandler) WithGroup(name string) stdlibslog.Handler {
	next := *h
	next.inner = h.inner.WithGroup(name)
	next.groups = append(append([]string(nil), h.groups...), name)
	return &next
}

func addSlogAttr(fields map[string]any, groups []string, attr stdlibslog.Attr) {
	attr.Value = attr.Value.Resolve()
	if attr.Equal(stdlibslog.Attr{}) {
		return
	}
	target := fields
	for _, group := range groups {
		nested, ok := target[group].(map[string]any)
		if !ok {
			nested = make(map[string]any)
			target[group] = nested
		}
		target = nested
	}
	if attr.Value.Kind() == stdlibslog.KindGroup {
		nested := make(map[string]any)
		for _, child := range attr.Value.Group() {
			addSlogAttr(nested, nil, child)
		}
		target[attr.Key] = nested
		return
	}
	value := attr.Value.Any()
	if err, ok := value.(error); ok {
		value = err.Error()
	}
	target[attr.Key] = value
}

var _ webpprof.Integration[stdlibslog.Handler] = ProfilerSlog{}
var _ stdlibslog.Handler = (*profiledSlogHandler)(nil)
