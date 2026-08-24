package webpprof

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestMeasureTaskCreatesStandaloneExecutionRoot(t *testing.T) {
	profiler := newProfiler(WithCallsiteKinds(KindTask))
	t.Cleanup(func() { _ = profiler.Close() })
	capture := profiler.BeginRequest(Request{Meta: Meta{ID: "trigger-request"}, Method: http.MethodPost, Path: "/reports"})
	ctx := WithRequest(context.Background(), capture)
	ctx = WithParentEntry(ctx, "handler")
	ctx = WithTags(ctx, map[string]string{"tenant": "acme"})

	measurement := profiler.MeasureTask(ctx, Task{Name: "reports.generate"}, func(taskCtx context.Context) error {
		profiler.LogQueryContext(taskCtx, Query{Meta: Meta{ID: "task-query"}, SQL: "SELECT 1"})
		profiler.LogLogContext(taskCtx, Log{Meta: Meta{ID: "task-log"}, Level: "INFO", Message: "report generated"})
		return nil
	})
	if measurement.Failed() || measurement.Duration <= 0 {
		t.Fatalf("measurement = %+v", measurement)
	}

	var task Entry
	for _, entry := range profiler.store.list("", "", nil, 0, 20) {
		if entry.Kind == KindTask {
			task = entry
		}
	}
	if task.ID == "" || task.RequestID != "" || task.ParentID != "" || task.Tags["tenant"] != "acme" {
		t.Fatalf("task root = %+v", task)
	}
	var recorded Task
	if err := json.Unmarshal(task.Data, &recorded); err != nil {
		t.Fatal(err)
	}
	if len(recorded.Callsite) == 0 || !strings.HasSuffix(recorded.Callsite[0].File, "task_test.go") {
		t.Fatalf("task callsite = %+v", recorded.Callsite)
	}
	for _, id := range []string{"task-query", "task-log"} {
		entry, ok := profiler.store.get(id)
		if !ok || entry.ParentID != task.ID || entry.RequestID != "" {
			t.Fatalf("task child %q = %+v, ok=%v", id, entry, ok)
		}
	}
}

func TestTaskSpanRecordsResultErrorAndPanic(t *testing.T) {
	profiler := newProfiler()
	t.Cleanup(func() { _ = profiler.Close() })
	wantErr := errors.New("render failed")
	span := profiler.StartTask(context.Background(), Task{Name: "report.failed", Fields: map[string]any{"format": "pdf"}})
	first := span.FinishResult(TaskResult{Fields: map[string]any{"pages": 3}, Err: wantErr})
	second := span.Finish(nil)
	if !errors.Is(first.Err, wantErr) || !errors.Is(second.Err, wantErr) {
		t.Fatalf("task results = %+v %+v", first, second)
	}

	var recovered any
	func() {
		defer func() { recovered = recover() }()
		profiler.MeasureTask(context.Background(), Task{Name: "report.panicked"}, func(context.Context) error {
			panic("boom")
		})
	}()
	if recovered != "boom" {
		t.Fatalf("panic = %#v", recovered)
	}

	states := map[string]Task{}
	for _, entry := range profiler.store.list(KindTask, "", nil, 0, 20) {
		var task Task
		if err := json.Unmarshal(entry.Data, &task); err != nil {
			t.Fatal(err)
		}
		states[task.Name] = task
	}
	if states["report.failed"].State != "failed" || states["report.failed"].Error != wantErr.Error() || states["report.failed"].Fields["pages"] != float64(3) {
		t.Fatalf("failed task = %+v", states["report.failed"])
	}
	if states["report.panicked"].State != "panicked" || states["report.panicked"].Panic != "boom" {
		t.Fatalf("panicked task = %+v", states["report.panicked"])
	}
}

func TestMeasureTaskHonorsWithoutRecording(t *testing.T) {
	profiler := newProfiler()
	t.Cleanup(func() { _ = profiler.Close() })
	called := false
	profiler.MeasureTask(WithoutRecording(context.Background()), Task{Name: "disabled"}, func(context.Context) error {
		called = true
		return nil
	})
	if !called || len(profiler.store.list(KindTask, "", nil, 0, 10)) != 0 {
		t.Fatal("disabled task was not executed or was recorded")
	}
}

func TestTaskDurationUsesProvidedStartTime(t *testing.T) {
	profiler := newProfiler()
	t.Cleanup(func() { _ = profiler.Close() })
	startedAt := time.Now().Add(-time.Second)
	span := profiler.StartTask(context.Background(), Task{Meta: Meta{ID: "backdated-task", StartedAt: startedAt}, Name: "backdated"})
	measurement := span.Finish(nil)
	if measurement.Duration < time.Second {
		t.Fatalf("duration = %s", measurement.Duration)
	}
}
