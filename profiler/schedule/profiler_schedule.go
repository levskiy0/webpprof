package schedule

import (
	"context"
	"fmt"
	"time"

	"github.com/levskiy0/webpprof"
)

type Task func(context.Context)

type ProfilerSchedule struct {
	NameValue string
}

func New(name string) ProfilerSchedule {
	return ProfilerSchedule{NameValue: name}
}

func (d ProfilerSchedule) Name() string {
	return "schedule:" + d.NameValue
}

func (d ProfilerSchedule) Profile(scope webpprof.Scope, task Task) Task {
	p := scope.Profiler()
	if p == nil || task == nil {
		return task
	}
	return func(ctx context.Context) {
		startedAt := time.Now().UTC()
		defer func() {
			event := webpprof.Schedule{Meta: webpprof.Meta{StartedAt: startedAt, Duration: time.Since(startedAt)}, Name: d.NameValue, State: "succeeded"}
			if recovered := recover(); recovered != nil {
				event.State = "panicked"
				event.Panic = fmt.Sprint(recovered)
				p.LogScheduleContext(ctx, event)
				panic(recovered)
			}
			p.LogScheduleContext(ctx, event)
		}()
		task(ctx)
	}
}

func Profile(name string, task Task) Task {
	return webpprof.Profile(task, New(name))
}

func ProfileWith(profiler *webpprof.Profiler, name string, task Task) Task {
	return webpprof.ProfileWith(profiler, task, New(name))
}

var _ webpprof.Integration[Task] = ProfilerSchedule{}
