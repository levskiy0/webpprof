// Package asynq profiles Asynq enqueue and worker execution operations while
// keeping Asynq and Redis dependencies outside the webpprof core module.
package asynq

import (
	"context"
	"time"

	"github.com/hibiken/asynq"
	"github.com/levskiy0/webpprof"
)

// Config controls Asynq event metadata. Payload capture is disabled by
// default because queue payloads commonly contain credentials or personal data.
type Config struct {
	Connection     string
	CapturePayload bool
	PayloadLimit   int
}

// Client is the subset of asynq.Client wrapped by Profile. Keep the original
// *asynq.Client to call Close; ownership is not transferred to this wrapper.
type Client interface {
	Enqueue(*asynq.Task, ...asynq.Option) (*asynq.TaskInfo, error)
	EnqueueContext(context.Context, *asynq.Task, ...asynq.Option) (*asynq.TaskInfo, error)
}

// Profile wraps client with the default profiler.
func Profile(client Client, configs ...Config) Client {
	return ProfileWith(webpprof.Default(), client, configs...)
}

// ProfileWith wraps client with p.
func ProfileWith(p *webpprof.Profiler, client Client, configs ...Config) Client {
	if p == nil || client == nil {
		return client
	}
	return &profiledClient{inner: client, profiler: p, config: firstConfig(configs)}
}

type profiledClient struct {
	inner    Client
	profiler *webpprof.Profiler
	config   Config
}

func (c *profiledClient) Enqueue(task *asynq.Task, options ...asynq.Option) (*asynq.TaskInfo, error) {
	return c.EnqueueContext(context.Background(), task, options...)
}

func (c *profiledClient) EnqueueContext(ctx context.Context, task *asynq.Task, options ...asynq.Option) (*asynq.TaskInfo, error) {
	startedAt := time.Now().UTC()
	callsite := c.profiler.CaptureCallsite(webpprof.KindJob)
	info, err := c.inner.EnqueueContext(ctx, task, options...)
	job := webpprof.Job{
		Meta:       webpprof.Meta{StartedAt: startedAt, Duration: time.Since(startedAt)},
		Name:       taskName(task),
		Connection: c.config.Connection,
		State:      "dispatched",
		Arguments:  payloadArgument(task, c.config),
		Callsite:   callsite,
		Error:      errorString(err),
	}
	if info != nil {
		job.Queue = info.Queue
		job.MaxAttempts = info.MaxRetry + 1
		job.AvailableAt = info.NextProcessAt
		job.Tags = map[string]string{"asynq.task_id": info.ID}
	}
	if err != nil {
		job.State = "failed"
	}
	c.profiler.LogJobContext(ctx, job)
	return info, err
}

// Middleware profiles worker execution. Register it with ServeMux.Use.
func Middleware(configs ...Config) asynq.MiddlewareFunc {
	return MiddlewareWith(webpprof.Default(), configs...)
}

// MiddlewareWith profiles worker execution with p.
func MiddlewareWith(p *webpprof.Profiler, configs ...Config) asynq.MiddlewareFunc {
	config := firstConfig(configs)
	return func(next asynq.Handler) asynq.Handler {
		if p == nil {
			return next
		}
		return asynq.HandlerFunc(func(ctx context.Context, task *asynq.Task) error {
			startedAt := time.Now().UTC()
			callsite := p.CaptureCallsite(webpprof.KindJob)
			err := next.ProcessTask(ctx, task)
			queue, _ := asynq.GetQueueName(ctx)
			attempt, _ := asynq.GetRetryCount(ctx)
			maxRetry, _ := asynq.GetMaxRetry(ctx)
			taskID, _ := asynq.GetTaskID(ctx)
			state := "succeeded"
			if err != nil {
				state = "failed"
			}
			job := webpprof.Job{
				Meta:        webpprof.Meta{StartedAt: startedAt, Duration: time.Since(startedAt)},
				Name:        taskName(task),
				Queue:       queue,
				Connection:  config.Connection,
				State:       state,
				Attempt:     attempt + 1,
				MaxAttempts: maxRetry + 1,
				Arguments:   payloadArgument(task, config),
				Callsite:    callsite,
				Error:       errorString(err),
			}
			if taskID != "" {
				job.Tags = map[string]string{"asynq.task_id": taskID}
			}
			p.LogJobContext(ctx, job)
			return err
		})
	}
}

func payloadArgument(task *asynq.Task, config Config) []webpprof.Argument {
	if task == nil || len(task.Payload()) == 0 {
		return nil
	}
	payload := task.Payload()
	argument := webpprof.Argument{Name: "payload", Type: "bytes", Size: int64(len(payload))}
	if !config.CapturePayload {
		return []webpprof.Argument{argument}
	}
	limit := config.PayloadLimit
	if limit <= 0 {
		limit = 4096
	}
	if len(payload) > limit {
		payload = payload[:limit]
		argument.Truncated = true
	}
	argument.Value = string(payload)
	return []webpprof.Argument{argument}
}

func taskName(task *asynq.Task) string {
	if task == nil {
		return ""
	}
	return task.Type()
}

func firstConfig(configs []Config) Config {
	if len(configs) == 0 {
		return Config{}
	}
	return configs[0]
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

var _ Client = (*profiledClient)(nil)
