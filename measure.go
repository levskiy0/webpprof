package webpprof

import (
	"context"
	"fmt"
	"runtime/debug"
	"sync"
	"time"
)

// Measurement describes the observed result of one measured operation.
// It is returned even when profiling is disabled, so callers may also use the
// duration and failure state for their own metrics.
type Measurement struct {
	StartedAt time.Time
	Duration  time.Duration
	Err       error
}

// Failed reports whether the measured function returned an error.
func (m Measurement) Failed() bool {
	return m.Err != nil
}

// EventResult supplies values that are known only after an operation finishes.
// Empty values preserve those already set on the Event passed to StartEvent.
type EventResult struct {
	Status  string
	Summary string
	Fields  map[string]any
	Err     error
}

// EventSpan measures a custom application operation and emits one Event when
// it is finished. Finish and FinishResult are safe to call more than once; only
// the first call records the event.
type EventSpan struct {
	profiler     *Profiler
	ctx          context.Context
	operationCtx context.Context
	event        Event
	started      time.Time
	once         sync.Once
	result       Measurement
}

// StartEvent starts a custom event using the default profiler. The returned
// span still measures elapsed time when no profiler is active.
func StartEvent(ctx context.Context, event Event) *EventSpan {
	return startEvent(Default(), ctx, event)
}

// StartEvent starts a custom event using p. Pass span.Context() to nested work
// so its profiler entries use this event as their parent.
func (p *Profiler) StartEvent(ctx context.Context, event Event) *EventSpan {
	return startEvent(p, ctx, event)
}

func startEvent(profiler *Profiler, ctx context.Context, event Event) *EventSpan {
	if ctx == nil {
		ctx = context.Background()
	}

	started := time.Now()
	if event.StartedAt.IsZero() {
		event.StartedAt = started.UTC()
	} else {
		started = event.StartedAt
	}

	active := profiler != nil && RecordingEnabled(ctx)
	operationCtx := ctx
	if active {
		if event.ID == "" {
			event.ID = NewID()
		}
		event.Tags = cloneTags(event.Tags)
		event.Fields = cloneEventFields(event.Fields)
		operationCtx = WithParentEntry(ctx, event.ID)
	}
	return &EventSpan{
		profiler:     profiler,
		ctx:          ctx,
		operationCtx: operationCtx,
		event:        event,
		started:      started,
	}
}

// Context returns the operation context. Nested context-aware profilers inherit
// this event's ID as ParentID. When recording is disabled, it returns the
// original context without adding profiling metadata.
func (s *EventSpan) Context() context.Context {
	if s == nil {
		return context.Background()
	}
	return s.operationCtx
}

// Finish completes the operation. Errors are recorded on the Event and make
// its default status "failed"; successful events default to "succeeded".
func (s *EventSpan) Finish(err error) Measurement {
	return s.FinishResult(EventResult{Err: err})
}

// FinishResult completes the operation with fields or presentation values that
// were not known when it started.
func (s *EventSpan) FinishResult(result EventResult) Measurement {
	if s == nil {
		return Measurement{Err: result.Err}
	}

	s.once.Do(func() {
		s.result = Measurement{
			StartedAt: s.event.StartedAt,
			Duration:  time.Since(s.started),
			Err:       result.Err,
		}
		s.event.Duration = s.result.Duration
		applyEventResult(&s.event, result)
		if s.profiler != nil {
			s.profiler.LogEventContext(s.ctx, s.event)
		}
	})
	return s.result
}

// Measure runs fn as a custom event using the default profiler. The context
// passed to fn correlates nested profiler entries with the event.
func Measure(ctx context.Context, event Event, fn func(context.Context) error) Measurement {
	return measure(Default(), ctx, event, fn)
}

// Measure runs fn as a custom event using p.
func (p *Profiler) Measure(ctx context.Context, event Event, fn func(context.Context) error) Measurement {
	return measure(p, ctx, event, fn)
}

func measure(profiler *Profiler, ctx context.Context, event Event, fn func(context.Context) error) (measurement Measurement) {
	span := startEvent(profiler, ctx, event)
	defer func() {
		if recovered := recover(); recovered != nil {
			span.finishPanic(recovered)
			panic(recovered)
		}
	}()
	return span.Finish(fn(span.Context()))
}

// MeasureValue runs a value-returning function as a custom event using the
// default profiler.
func MeasureValue[T any](ctx context.Context, event Event, fn func(context.Context) (T, error)) (T, Measurement) {
	return MeasureValueWith(Default(), ctx, event, fn)
}

// MeasureValueWith runs a value-returning function as a custom event using an
// explicit profiler. It is a function rather than a method because Go methods
// cannot declare type parameters.
func MeasureValueWith[T any](profiler *Profiler, ctx context.Context, event Event, fn func(context.Context) (T, error)) (value T, measurement Measurement) {
	span := startEvent(profiler, ctx, event)
	defer func() {
		if recovered := recover(); recovered != nil {
			span.finishPanic(recovered)
			panic(recovered)
		}
	}()
	value, err := fn(span.Context())
	return value, span.Finish(err)
}

func (s *EventSpan) finishPanic(recovered any) {
	fields := map[string]any{
		"panic_type":  fmt.Sprintf("%T", recovered),
		"panic_stack": string(debug.Stack()),
	}
	s.FinishResult(EventResult{
		Status: "panicked",
		Fields: fields,
		Err:    fmt.Errorf("panic: %v", recovered),
	})
}

func applyEventResult(event *Event, result EventResult) {
	if result.Status != "" {
		event.Status = result.Status
	}
	if result.Summary != "" {
		event.Summary = result.Summary
	}
	if len(result.Fields) > 0 {
		if event.Fields == nil {
			event.Fields = make(map[string]any, len(result.Fields))
		}
		for key, value := range result.Fields {
			event.Fields[key] = value
		}
	}
	if result.Err != nil {
		event.Error = result.Err.Error()
	}
	if event.Status == "" {
		if event.Error != "" {
			event.Status = "failed"
		} else {
			event.Status = "succeeded"
		}
	}
}

func cloneEventFields(fields map[string]any) map[string]any {
	if len(fields) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(fields))
	for key, value := range fields {
		cloned[key] = value
	}
	return cloned
}
