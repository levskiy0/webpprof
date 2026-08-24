package webpprof

import (
	"context"
	"fmt"
	"runtime/debug"
	"sync"
	"time"
)

// TaskResult supplies state and fields known only when a Task finishes.
type TaskResult struct {
	State  string
	Fields map[string]any
	Err    error
}

// TaskSpan measures one long-running application task. Finish and FinishResult
// are idempotent; only the first call records the Task.
type TaskSpan struct {
	profiler *Profiler
	rootCtx  context.Context
	taskCtx  context.Context
	task     Task
	started  time.Time
	once     sync.Once
	result   Measurement
}

// StartTask starts a Task using the default profiler.
func StartTask(ctx context.Context, task Task) *TaskSpan {
	return startTask(Default(), ctx, task)
}

// StartTask starts a standalone Task using p. Pass span.Context() to nested
// work so profiler entries use the Task ID as ParentID.
func (p *Profiler) StartTask(ctx context.Context, task Task) *TaskSpan {
	return startTask(p, ctx, task)
}

func startTask(profiler *Profiler, ctx context.Context, task Task) *TaskSpan {
	if ctx == nil {
		ctx = context.Background()
	}
	rootCtx := WithoutCorrelation(ctx)
	started := time.Now()
	if task.StartedAt.IsZero() {
		task.StartedAt = started.UTC()
	} else {
		started = task.StartedAt
	}
	taskCtx := rootCtx
	if profiler != nil && RecordingEnabled(ctx) {
		if task.ID == "" {
			task.ID = NewID()
		}
		task.Tags = mergeTags(TagsFromContext(rootCtx), task.Tags)
		task.Fields = cloneTaskFields(task.Fields)
		profiler.prepareCallsite(KindTask, &task.Callsite)
		taskCtx = WithParentEntry(rootCtx, task.ID)
	}
	return &TaskSpan{profiler: profiler, rootCtx: rootCtx, taskCtx: taskCtx, task: task, started: started}
}

// Context returns the Task execution context for nested profiler operations.
func (s *TaskSpan) Context() context.Context {
	if s == nil {
		return context.Background()
	}
	return s.taskCtx
}

// Finish completes the Task and records a returned error.
func (s *TaskSpan) Finish(err error) Measurement {
	return s.FinishResult(TaskResult{Err: err})
}

// FinishResult completes the Task with state or fields known at completion.
func (s *TaskSpan) FinishResult(result TaskResult) Measurement {
	if s == nil {
		return Measurement{Err: result.Err}
	}
	s.once.Do(func() {
		s.result = Measurement{StartedAt: s.task.StartedAt, Duration: time.Since(s.started), Err: result.Err}
		s.task.Duration = s.result.Duration
		if result.State != "" {
			s.task.State = result.State
		}
		if len(result.Fields) > 0 {
			if s.task.Fields == nil {
				s.task.Fields = make(map[string]any, len(result.Fields))
			}
			for key, value := range result.Fields {
				s.task.Fields[key] = value
			}
		}
		if result.Err != nil {
			s.task.Error = result.Err.Error()
		}
		if s.task.State == "" {
			if s.task.Error != "" {
				s.task.State = "failed"
			} else {
				s.task.State = "succeeded"
			}
		}
		if s.profiler != nil && RecordingEnabled(s.rootCtx) {
			s.profiler.LogTaskContext(s.rootCtx, s.task)
		}
	})
	return s.result
}

// MeasureTask runs fn as a standalone Task using the default profiler.
func MeasureTask(ctx context.Context, task Task, fn func(context.Context) error) Measurement {
	return measureTask(Default(), ctx, task, fn)
}

// MeasureTask runs fn as a standalone Task using p.
func (p *Profiler) MeasureTask(ctx context.Context, task Task, fn func(context.Context) error) Measurement {
	return measureTask(p, ctx, task, fn)
}

func measureTask(profiler *Profiler, ctx context.Context, task Task, fn func(context.Context) error) (measurement Measurement) {
	span := startTask(profiler, ctx, task)
	defer func() {
		if recovered := recover(); recovered != nil {
			span.finishPanic(recovered)
			panic(recovered)
		}
	}()
	return span.Finish(fn(span.Context()))
}

func (s *TaskSpan) finishPanic(recovered any) {
	if s == nil {
		return
	}
	s.task.Panic = fmt.Sprint(recovered)
	s.FinishResult(TaskResult{
		State:  "panicked",
		Fields: map[string]any{"panic_type": fmt.Sprintf("%T", recovered), "panic_stack": string(debug.Stack())},
		Err:    fmt.Errorf("panic: %v", recovered),
	})
}

func cloneTaskFields(fields map[string]any) map[string]any {
	if len(fields) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(fields))
	for key, value := range fields {
		cloned[key] = value
	}
	return cloned
}
