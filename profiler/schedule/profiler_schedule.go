// Package schedule wraps context-aware scheduled tasks and records their
// duration, success, and panics.
package schedule

import (
	"context"
	"fmt"
	"time"

	"github.com/levskiy0/webpprof"
)

// Task is a context-aware scheduled function supported by this integration.
type Task func(context.Context)

// ProfilerSchedule implements webpprof.Integration for one named Task.
type ProfilerSchedule struct {
	NameValue string
}

// New creates a schedule integration using name in captured entries.
func New(name string) ProfilerSchedule {
	return ProfilerSchedule{NameValue: name}
}

// Name returns the integration cache namespace, including the task name.
func (d ProfilerSchedule) Name() string {
	return "schedule:" + d.NameValue
}

// Profile wraps task so executions and panics are recorded. Panics are rethrown
// after recording.
func (d ProfilerSchedule) Profile(scope webpprof.Scope, task Task) Task {
	p := scope.Profiler()
	if p == nil || task == nil {
		return task
	}
	return func(ctx context.Context) {
		if !webpprof.RecordingEnabled(ctx) {
			task(ctx)
			return
		}
		rootCtx := webpprof.WithoutCorrelation(ctx)
		invocationID := webpprof.NewID()
		taskCtx := webpprof.WithParentEntry(rootCtx, invocationID)
		startedAt := time.Now().UTC()
		defer func() {
			event := webpprof.Schedule{Meta: webpprof.Meta{ID: invocationID, StartedAt: startedAt, Duration: time.Since(startedAt)}, Name: d.NameValue, State: "succeeded"}
			if recovered := recover(); recovered != nil {
				event.State = "panicked"
				event.Panic = fmt.Sprint(recovered)
				p.LogScheduleContext(rootCtx, event)
				panic(recovered)
			}
			p.LogScheduleContext(rootCtx, event)
		}()
		task(taskCtx)
	}
}

// Profile instruments task with the default profiler.
func Profile(name string, task Task) Task {
	return webpprof.Profile(task, New(name))
}

// ProfileWith instruments task with an explicit profiler.
func ProfileWith(profiler *webpprof.Profiler, name string, task Task) Task {
	return webpprof.ProfileWith(profiler, task, New(name))
}

var _ webpprof.Integration[Task] = ProfilerSchedule{}
