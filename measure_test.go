package webpprof

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestProfilerMeasureRecordsEventAndParentsNestedEntries(t *testing.T) {
	profiler := newProfiler()
	t.Cleanup(func() { _ = profiler.Close() })

	fields := map[string]any{"component": "players"}
	measurement := profiler.Measure(
		WithTags(context.Background(), map[string]string{"tenant": "acme"}),
		Event{
			Kind:   "service",
			Name:   "load-player",
			Fields: fields,
		},
		func(ctx context.Context) error {
			profiler.LogCacheContext(ctx, Cache{Operation: "get", Key: "player:42", Hit: true})
			return nil
		},
	)
	fields["component"] = "changed-after-start"

	if measurement.Failed() {
		t.Fatalf("measurement failed: %v", measurement.Err)
	}
	if measurement.StartedAt.IsZero() || measurement.Duration < 0 {
		t.Fatalf("measurement timing = %+v", measurement)
	}

	eventEntry := onlyEntryOfKind(t, profiler, KindEvent)
	cacheEntry := onlyEntryOfKind(t, profiler, KindCache)
	var event Event
	if err := json.Unmarshal(eventEntry.Data, &event); err != nil {
		t.Fatalf("decode event: %v", err)
	}
	if event.Kind != "service" || event.Name != "load-player" || event.Status != "succeeded" {
		t.Fatalf("event = %+v", event)
	}
	if event.Fields["component"] != "players" {
		t.Fatalf("event fields were mutated through caller map: %#v", event.Fields)
	}
	if event.Tags["tenant"] != "acme" {
		t.Fatalf("event tags = %#v", event.Tags)
	}
	if cacheEntry.ParentID != eventEntry.ID {
		t.Fatalf("cache parent = %q, want event %q", cacheEntry.ParentID, eventEntry.ID)
	}
}

func TestEventSpanFinishResultUsesFirstResult(t *testing.T) {
	profiler := newProfiler()
	t.Cleanup(func() { _ = profiler.Close() })
	wantErr := errors.New("lookup failed")

	span := profiler.StartEvent(context.Background(), Event{
		Kind:    "client",
		Name:    "lookup",
		Summary: "initial summary",
		Fields:  map[string]any{"key": "player:42"},
	})
	first := span.FinishResult(EventResult{
		Status:  "unavailable",
		Summary: "lookup player:42",
		Fields:  map[string]any{"attempt": 2},
		Err:     wantErr,
	})
	second := span.Finish(nil)

	if !errors.Is(first.Err, wantErr) || !errors.Is(second.Err, wantErr) {
		t.Fatalf("finish errors = %v, %v", first.Err, second.Err)
	}
	entries := profiler.store.list(KindEvent, "", nil, 0, 10)
	if len(entries) != 1 {
		t.Fatalf("event count = %d, want 1", len(entries))
	}
	var event Event
	if err := json.Unmarshal(entries[0].Data, &event); err != nil {
		t.Fatalf("decode event: %v", err)
	}
	if event.Status != "unavailable" || event.Summary != "lookup player:42" || event.Error != wantErr.Error() {
		t.Fatalf("event = %+v", event)
	}
	if event.Fields["key"] != "player:42" || event.Fields["attempt"].(float64) != 2 {
		t.Fatalf("event fields = %#v", event.Fields)
	}
}

func TestMeasureValueReturnsValueAndFailure(t *testing.T) {
	profiler := newProfiler()
	t.Cleanup(func() { _ = profiler.Close() })
	wantErr := errors.New("partial result")

	value, measurement := MeasureValueWith(profiler, context.Background(), Event{
		Kind: "repository",
		Name: "find-player",
	}, func(context.Context) (string, error) {
		return "Ada", wantErr
	})

	if value != "Ada" || !errors.Is(measurement.Err, wantErr) || !measurement.Failed() {
		t.Fatalf("value, measurement = %q, %+v", value, measurement)
	}
	entry := onlyEntryOfKind(t, profiler, KindEvent)
	var event Event
	if err := json.Unmarshal(entry.Data, &event); err != nil {
		t.Fatalf("decode event: %v", err)
	}
	if event.Status != "failed" || event.Error != wantErr.Error() {
		t.Fatalf("event = %+v", event)
	}
}

func TestProfilerMeasureRecordsPanicAndRepanics(t *testing.T) {
	profiler := newProfiler()
	t.Cleanup(func() { _ = profiler.Close() })

	func() {
		defer func() {
			if recovered := recover(); recovered != "boom" {
				t.Fatalf("recovered = %#v", recovered)
			}
		}()
		profiler.Measure(context.Background(), Event{Kind: "service", Name: "panic"}, func(context.Context) error {
			panic("boom")
		})
	}()

	entry := onlyEntryOfKind(t, profiler, KindEvent)
	var event Event
	if err := json.Unmarshal(entry.Data, &event); err != nil {
		t.Fatalf("decode event: %v", err)
	}
	if event.Status != "panicked" || event.Error != "panic: boom" {
		t.Fatalf("panic event = %+v", event)
	}
	if event.Fields["panic_type"] != "string" || event.Fields["panic_stack"] == "" {
		t.Fatalf("panic fields = %#v", event.Fields)
	}
}

func TestProfilerMeasureHonorsWithoutRecording(t *testing.T) {
	profiler := newProfiler()
	t.Cleanup(func() { _ = profiler.Close() })
	called := false

	measurement := profiler.Measure(WithoutRecording(context.Background()), Event{
		Kind: "service",
		Name: "disabled",
	}, func(context.Context) error {
		called = true
		return nil
	})

	if !called || measurement.Failed() {
		t.Fatalf("called = %v, measurement = %+v", called, measurement)
	}
	if entries := profiler.store.list(KindEvent, "", nil, 0, 10); len(entries) != 0 {
		t.Fatalf("disabled measurement recorded %d events", len(entries))
	}
}

func TestNilProfilerMeasureStillReturnsApplicationTiming(t *testing.T) {
	var profiler *Profiler
	ctx := context.WithValue(context.Background(), measurementTestContextKey{}, "kept")

	measurement := profiler.Measure(ctx, Event{Kind: "service", Name: "disabled"}, func(got context.Context) error {
		if got.Value(measurementTestContextKey{}) != "kept" {
			t.Fatal("measurement replaced the application context")
		}
		return nil
	})

	if measurement.StartedAt.IsZero() || measurement.Duration < 0 || measurement.Failed() {
		t.Fatalf("measurement = %+v", measurement)
	}
}

type measurementTestContextKey struct{}

func onlyEntryOfKind(t *testing.T, profiler *Profiler, kind Kind) Entry {
	t.Helper()
	entries := profiler.store.list(kind, "", nil, 0, 10)
	if len(entries) != 1 {
		t.Fatalf("%s entry count = %d, want 1", kind, len(entries))
	}
	return entries[0]
}
