package otel

import (
	"context"
	"strings"
	"sync"

	"github.com/levskiy0/webpprof"
	webpprofhttp "github.com/levskiy0/webpprof/profiler/http"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

type ProfilerOTel struct{}

type otelProfilerProcessor struct {
	profiler *webpprof.Profiler
	roots    sync.Map
}

func New() ProfilerOTel {
	return ProfilerOTel{}
}

func (ProfilerOTel) Name() string {
	return "otel"
}

func (ProfilerOTel) Profile(scope webpprof.Scope, provider *sdktrace.TracerProvider) *sdktrace.TracerProvider {
	p := scope.Profiler()
	if p == nil || provider == nil {
		return provider
	}
	if _, loaded := scope.LoadOrStore(provider, struct{}{}); !loaded {
		provider.RegisterSpanProcessor(NewSpanProcessor(p))
	}
	return provider
}

func Profile(provider *sdktrace.TracerProvider) *sdktrace.TracerProvider {
	return webpprof.Profile(provider, New())
}

func ProfileWith(profiler *webpprof.Profiler, provider *sdktrace.TracerProvider) *sdktrace.TracerProvider {
	return webpprof.ProfileWith(profiler, provider, New())
}

func SpanProcessor() sdktrace.SpanProcessor {
	return NewSpanProcessor(webpprof.Default())
}

func NewSpanProcessor(profiler *webpprof.Profiler) sdktrace.SpanProcessor {
	if profiler == nil {
		return nil
	}
	return &otelProfilerProcessor{profiler: profiler}
}

func (p *otelProfilerProcessor) OnStart(_ context.Context, span sdktrace.ReadWriteSpan) {
	attributes := otelAttributes(span.Attributes())
	if span.SpanKind() == trace.SpanKindServer && otelString(attributes, "http.request.method", "http.method") != "" {
		p.roots.Store(span.SpanContext().TraceID(), span.SpanContext().SpanID())
	}
}

func (p *otelProfilerProcessor) OnEnd(span sdktrace.ReadOnlySpan) {
	attributes := otelAttributes(span.Attributes())
	meta := p.meta(span)
	switch {
	case span.SpanKind() == trace.SpanKindServer && otelString(attributes, "http.request.method", "http.method") != "":
		p.recordRequest(span, meta, attributes)
	case span.SpanKind() == trace.SpanKindClient && otelString(attributes, "http.request.method", "http.method") != "":
		p.recordHTTPCall(span, meta, attributes)
	case otelString(attributes, "messaging.system", "messaging.system.name") != "":
		p.recordJob(span, meta, attributes)
	case otelCacheSystem(attributes) != "":
		p.recordCache(span, meta, attributes)
	case otelString(attributes, "db.system.name", "db.system") != "":
		p.recordQuery(span, meta, attributes)
	case otelString(attributes, "webpprof.kind") != "":
		p.recordEvent(span, meta, attributes)
	}
	if span.SpanKind() == trace.SpanKindServer {
		p.roots.Delete(span.SpanContext().TraceID())
	}
}

func (p *otelProfilerProcessor) Shutdown(context.Context) error {
	return nil
}

func (p *otelProfilerProcessor) ForceFlush(context.Context) error {
	return nil
}

func (p *otelProfilerProcessor) meta(span sdktrace.ReadOnlySpan) webpprof.Meta {
	spanContext := span.SpanContext()
	meta := webpprof.Meta{ID: spanContext.SpanID().String(), OriginRequestID: spanContext.TraceID().String(), StartedAt: span.StartTime().UTC(), Duration: span.EndTime().Sub(span.StartTime())}
	if parent := span.Parent(); parent.IsValid() {
		meta.ParentID = parent.SpanID().String()
	}
	if root, ok := p.roots.Load(spanContext.TraceID()); ok {
		meta.RequestID = root.(trace.SpanID).String()
	}
	return meta
}

func (p *otelProfilerProcessor) recordRequest(span sdktrace.ReadOnlySpan, meta webpprof.Meta, attributes map[string]any) {
	path := otelString(attributes, "url.path", "http.target")
	query := ""
	if index := strings.IndexByte(path, '?'); index >= 0 {
		query = webpprofhttp.SanitizeQuery(path[index+1:])
		path = path[:index]
	}
	event := webpprof.Request{Meta: meta, Method: otelString(attributes, "http.request.method", "http.method"), Path: path, Route: otelString(attributes, "http.route"), Query: query, Protocol: otelString(attributes, "network.protocol.version", "http.flavor"), Host: otelString(attributes, "server.address", "http.host"), Status: otelInt(attributes, "http.response.status_code", "http.status_code"), Error: otelSpanError(span, attributes)}
	if event.Path == "" {
		event.Path = span.Name()
	}
	p.profiler.LogRequest(event)
}

func (p *otelProfilerProcessor) recordHTTPCall(span sdktrace.ReadOnlySpan, meta webpprof.Meta, attributes map[string]any) {
	event := webpprof.HTTPCall{Meta: meta, Method: otelString(attributes, "http.request.method", "http.method"), URL: otelString(attributes, "url.full", "http.url"), Status: otelInt(attributes, "http.response.status_code", "http.status_code"), Error: otelSpanError(span, attributes)}
	p.profiler.LogHTTPCall(event)
}

func (p *otelProfilerProcessor) recordQuery(span sdktrace.ReadOnlySpan, meta webpprof.Meta, attributes map[string]any) {
	event := webpprof.Query{Meta: meta, Connection: otelString(attributes, "server.address", "net.peer.name"), Driver: otelString(attributes, "db.system.name", "db.system"), Database: otelString(attributes, "db.namespace", "db.name"), Operation: otelString(attributes, "db.operation.name", "db.operation"), SQL: compactSQL(otelString(attributes, "db.query.text", "db.statement")), Error: otelSpanError(span, attributes)}
	if event.Operation == "" {
		event.Operation = span.Name()
	}
	if event.SQL == "" {
		event.SQL = compactSQL(span.Name())
	}
	p.profiler.LogQuery(event)
}

func (p *otelProfilerProcessor) recordCache(span sdktrace.ReadOnlySpan, meta webpprof.Meta, attributes map[string]any) {
	event := webpprof.Cache{Meta: meta, Store: otelCacheSystem(attributes), Operation: otelString(attributes, "db.operation.name", "db.operation"), Key: otelString(attributes, "db.collection.name"), Hit: span.Status().Code != codes.Error, Error: otelSpanError(span, attributes)}
	if event.Operation == "" {
		event.Operation = span.Name()
	}
	p.profiler.LogCache(event)
}

func (p *otelProfilerProcessor) recordJob(span sdktrace.ReadOnlySpan, meta webpprof.Meta, attributes map[string]any) {
	state := "succeeded"
	if span.SpanKind() == trace.SpanKindProducer {
		state = "dispatched"
	}
	err := otelSpanError(span, attributes)
	if err != "" {
		state = "failed"
	}
	event := webpprof.Job{Meta: meta, Name: span.Name(), Queue: otelString(attributes, "messaging.destination.name", "messaging.destination"), Connection: otelString(attributes, "messaging.system", "messaging.system.name"), State: state, Error: err}
	p.profiler.LogJob(event)
}

func (p *otelProfilerProcessor) recordEvent(span sdktrace.ReadOnlySpan, meta webpprof.Meta, attributes map[string]any) {
	event := webpprof.Event{Meta: meta, Kind: otelString(attributes, "webpprof.kind"), Name: span.Name(), Status: span.Status().Code.String(), Fields: attributes, Error: otelSpanError(span, attributes)}
	p.profiler.LogEvent(event)
}

func otelAttributes(values []attribute.KeyValue) map[string]any {
	attributes := make(map[string]any, len(values))
	for _, value := range values {
		attributes[string(value.Key)] = value.Value.AsInterface()
	}
	return attributes
}

func otelString(attributes map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := attributes[key].(string); ok {
			return value
		}
	}
	return ""
}

func otelInt(attributes map[string]any, keys ...string) int {
	for _, key := range keys {
		switch value := attributes[key].(type) {
		case int:
			return value
		case int64:
			return int(value)
		}
	}
	return 0
}

func otelCacheSystem(attributes map[string]any) string {
	system := strings.ToLower(otelString(attributes, "db.system.name", "db.system"))
	switch system {
	case "redis", "memcached", "valkey":
		return system
	default:
		return ""
	}
}

func otelSpanError(span sdktrace.ReadOnlySpan, attributes map[string]any) string {
	status := span.Status()
	if status.Code == codes.Error && status.Description != "" {
		return status.Description
	}
	return otelString(attributes, "error.type")
}

func compactSQL(query string) string {
	query = strings.Join(strings.Fields(query), " ")
	if len(query) <= 8000 {
		return query
	}
	return query[:8000] + "…"
}

var _ webpprof.Integration[*sdktrace.TracerProvider] = ProfilerOTel{}
var _ sdktrace.SpanProcessor = (*otelProfilerProcessor)(nil)
