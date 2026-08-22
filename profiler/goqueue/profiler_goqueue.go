package goqueue

import (
	"context"
	"time"

	"github.com/levskiy0/webpprof"
	queuecontract "github.com/levskiy0/go-queue/contract"
)

type ProfilerGoQueue struct {
	DefaultQueue string
}

type ProfilerJobs struct {
	Queue string
}

type Queue = queuecontract.Queue
type Job = queuecontract.Job
type Task = queuecontract.Task

type profiledQueue struct {
	inner        Queue
	profiler     *webpprof.Profiler
	defaultQueue string
}

type profiledQueueJob struct {
	inner      Job
	profiler   *webpprof.Profiler
	queue      string
	connection string
}

type profiledQueueTask struct {
	inner       Task
	profiler    *webpprof.Profiler
	jobs        []*profiledQueueJob
	arguments   [][]webpprof.Argument
	availableAt time.Time
}

func New(names ...string) ProfilerGoQueue {
	name := "default"
	if len(names) > 0 && names[0] != "" {
		name = names[0]
	}
	return ProfilerGoQueue{DefaultQueue: name}
}

func (ProfilerGoQueue) Name() string {
	return "go-queue"
}

func (d ProfilerGoQueue) Profile(scope webpprof.Scope, queue Queue) Queue {
	p := scope.Profiler()
	if p == nil || queue == nil {
		return queue
	}
	if wrapped, ok := queue.(*profiledQueue); ok && wrapped.profiler == p {
		return queue
	}
	if reader, ok := queue.(queuecontract.StatsReader); ok {
		p.RegisterQueueStats(&goQueueStatsSource{reader: reader}, "go-queue")
	}
	return &profiledQueue{inner: queue, profiler: p, defaultQueue: d.DefaultQueue}
}

func Profile(queue Queue, names ...string) Queue {
	return webpprof.Profile(queue, New(names...))
}

func ProfileWith(profiler *webpprof.Profiler, queue Queue, names ...string) Queue {
	return webpprof.ProfileWith(profiler, queue, New(names...))
}

type goQueueStatsSource struct {
	reader queuecontract.StatsReader
}

func (s *goQueueStatsSource) QueueStats(ctx context.Context) (webpprof.QueueStats, error) {
	stats, err := s.reader.Stats(ctx)
	result := webpprof.QueueStats{StartedAt: stats.StartedAt, WorkersActive: stats.WorkersActive, WorkersTotal: stats.WorkersTotal, Processed: stats.Processed, Succeeded: stats.Succeeded, Failed: stats.Failed, Pending: stats.Pending, Queues: make([]webpprof.QueueState, len(stats.Queues))}
	for index, queue := range stats.Queues {
		result.Queues[index] = webpprof.QueueState{Name: queue.Name, WorkersActive: queue.WorkersActive, WorkersTotal: queue.WorkersTotal, Processed: queue.Processed, Succeeded: queue.Succeeded, Failed: queue.Failed, Pending: queue.Pending}
	}
	return result, err
}

func (d ProfilerJobs) Name() string {
	return "go-queue-jobs"
}

func (d ProfilerJobs) Profile(scope webpprof.Scope, jobs []Job) []Job {
	p := scope.Profiler()
	if p == nil {
		return jobs
	}
	profiled := make([]Job, len(jobs))
	for index, job := range jobs {
		profiled[index] = profileQueueJob(p, job, d.Queue)
	}
	return profiled
}

func ProfileJobs(jobs []Job, queue string) []Job {
	return webpprof.Profile(jobs, ProfilerJobs{Queue: queue})
}

func ProfileJobsWith(profiler *webpprof.Profiler, jobs []Job, queue string) []Job {
	return webpprof.ProfileWith(profiler, jobs, ProfilerJobs{Queue: queue})
}

func (q *profiledQueue) Worker(args ...queuecontract.Args) queuecontract.Worker {
	return q.inner.Worker(args...)
}

func (q *profiledQueue) Register(jobs []Job) {
	profiled := make([]Job, len(jobs))
	for index, job := range jobs {
		if wrapped, ok := job.(*profiledQueueJob); ok && wrapped.profiler == q.profiler {
			profiled[index] = wrapped
		} else {
			profiled[index] = profileQueueJob(q.profiler, job, q.defaultQueue)
		}
	}
	q.inner.Register(profiled)
}

func (q *profiledQueue) GetJobs() []Job {
	return q.inner.GetJobs()
}

func (q *profiledQueue) Job(job Job, args []queuecontract.Arg) Task {
	tracked := q.profileJob(job, q.defaultQueue)
	return &profiledQueueTask{inner: q.inner.Job(tracked, args), profiler: q.profiler, jobs: []*profiledQueueJob{tracked}, arguments: [][]webpprof.Argument{queueArguments(args)}}
}

func (q *profiledQueue) Chain(jobs []queuecontract.Jobs) Task {
	tracked := make([]queuecontract.Jobs, len(jobs))
	trackedJobs := make([]*profiledQueueJob, len(jobs))
	arguments := make([][]webpprof.Argument, len(jobs))
	for index, item := range jobs {
		job := q.profileJob(item.Job, q.defaultQueue)
		trackedJobs[index] = job
		arguments[index] = queueArguments(item.Args)
		tracked[index] = queuecontract.Jobs{Job: job, Args: item.Args}
	}
	return &profiledQueueTask{inner: q.inner.Chain(tracked), profiler: q.profiler, jobs: trackedJobs, arguments: arguments}
}

func (q *profiledQueue) profileJob(job Job, queue string) *profiledQueueJob {
	profiled := profileQueueJob(q.profiler, job, queue)
	return profiled.(*profiledQueueJob)
}

func profileQueueJob(p *webpprof.Profiler, job Job, queue string) Job {
	if wrapped, ok := job.(*profiledQueueJob); ok && wrapped.profiler == p {
		if queue == "" || wrapped.queue == queue {
			return wrapped
		}
		return &profiledQueueJob{inner: wrapped.inner, profiler: p, queue: queue, connection: wrapped.connection}
	}
	return &profiledQueueJob{inner: job, profiler: p, queue: queue}
}

func (j *profiledQueueJob) Signature() string {
	return j.inner.Signature()
}

func (j *profiledQueueJob) Handle(args ...any) error {
	startedAt := time.Now().UTC()
	err := j.inner.Handle(args...)
	event := webpprof.Job{Meta: webpprof.Meta{StartedAt: startedAt, Duration: time.Since(startedAt)}, Name: j.Signature(), Queue: j.queue, Connection: j.connection, State: "succeeded"}
	if err != nil {
		event.State = "failed"
		event.Error = err.Error()
	}
	j.profiler.LogJobContext(context.Background(), event)
	return err
}

func (j *profiledQueueJob) NoRetry(err error) bool {
	if job, ok := j.inner.(queuecontract.JobWithNoRetry); ok {
		return job.NoRetry(err)
	}
	return false
}

func (t *profiledQueueTask) Dispatch() error {
	return t.dispatch("dispatched", t.inner.Dispatch)
}

func (t *profiledQueueTask) DispatchSync() error {
	return t.dispatch("dispatched_sync", t.inner.DispatchSync)
}

func (t *profiledQueueTask) Delay(at time.Time) Task {
	t.availableAt = at
	t.inner = t.inner.Delay(at)
	return t
}

func (t *profiledQueueTask) OnConnection(connection string) Task {
	t.inner = t.inner.OnConnection(connection)
	for _, job := range t.jobs {
		job.connection = connection
	}
	return t
}

func (t *profiledQueueTask) OnQueue(queue string) Task {
	t.inner = t.inner.OnQueue(queue)
	for _, job := range t.jobs {
		job.queue = queue
	}
	return t
}

func (t *profiledQueueTask) Retries(count int) Task {
	t.inner = t.inner.Retries(count)
	return t
}

func (t *profiledQueueTask) RetryAfter(initial time.Duration) Task {
	t.inner = t.inner.RetryAfter(initial)
	return t
}

func (t *profiledQueueTask) dispatch(state string, dispatch func() error) error {
	startedAt := time.Now().UTC()
	err := dispatch()
	for index, job := range t.jobs {
		event := webpprof.Job{Meta: webpprof.Meta{StartedAt: startedAt, Duration: time.Since(startedAt)}, Name: job.Signature(), Queue: job.queue, Connection: job.connection, State: state, AvailableAt: t.availableAt, Arguments: t.arguments[index]}
		if err != nil {
			event.State = "dispatch_failed"
			event.Error = err.Error()
		}
		t.profiler.LogJob(event)
	}
	return err
}

func queueArguments(args []queuecontract.Arg) []webpprof.Argument {
	arguments := make([]webpprof.Argument, len(args))
	for index, arg := range args {
		arguments[index] = webpprof.Argument{Type: arg.Type}
	}
	return arguments
}

var _ webpprof.Integration[Queue] = ProfilerGoQueue{}
var _ webpprof.Integration[[]Job] = ProfilerJobs{}
var _ Queue = (*profiledQueue)(nil)
var _ Job = (*profiledQueueJob)(nil)
var _ Task = (*profiledQueueTask)(nil)
