// Package callable wraps context-aware custom commands and records each call as
// an independent execution root.
package callable

import (
	"context"
	"fmt"
	"time"

	"github.com/levskiy0/webpprof"
)

// Command is a context-aware custom command supported by this integration.
type Command func(context.Context) error

// ProfilerCallable implements webpprof.Integration for one named Command.
type ProfilerCallable struct {
	NameValue string
}

// New creates a callable integration using name in captured entries.
func New(name string) ProfilerCallable {
	return ProfilerCallable{NameValue: name}
}

// Name returns the integration cache namespace, including the command name.
func (c ProfilerCallable) Name() string {
	return "callable:" + c.NameValue
}

// Profile wraps command so calls, returned errors, and panics are recorded.
// Each call is an independent root; nested context-aware profiler operations
// use its Callable entry as their parent.
func (c ProfilerCallable) Profile(scope webpprof.Scope, command Command) Command {
	p := scope.Profiler()
	if p == nil || command == nil {
		return command
	}
	return func(ctx context.Context) (commandErr error) {
		if !webpprof.RecordingEnabled(ctx) {
			return command(ctx)
		}
		rootCtx := webpprof.WithoutCorrelation(ctx)
		invocationID := webpprof.NewID()
		commandCtx := webpprof.WithParentEntry(rootCtx, invocationID)
		startedAt := time.Now().UTC()
		defer func() {
			callable := webpprof.Callable{
				Meta:  webpprof.Meta{ID: invocationID, StartedAt: startedAt, Duration: time.Since(startedAt)},
				Name:  c.NameValue,
				State: "succeeded",
			}
			if recovered := recover(); recovered != nil {
				callable.State = "panicked"
				callable.Panic = fmt.Sprint(recovered)
				p.LogCallableContext(rootCtx, callable)
				panic(recovered)
			}
			if commandErr != nil {
				callable.State = "failed"
				callable.Error = commandErr.Error()
			}
			p.LogCallableContext(rootCtx, callable)
		}()
		return command(commandCtx)
	}
}

// Profile instruments command with the default profiler.
func Profile(name string, command Command) Command {
	return webpprof.Profile(command, New(name))
}

// ProfileWith instruments command with an explicit profiler.
func ProfileWith(profiler *webpprof.Profiler, name string, command Command) Command {
	return webpprof.ProfileWith(profiler, command, New(name))
}

var _ webpprof.Integration[Command] = ProfilerCallable{}
