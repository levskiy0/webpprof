package asynq

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	asynqlib "github.com/hibiken/asynq"
	"github.com/levskiy0/webpprof"
)

type clientStub struct {
	info *asynqlib.TaskInfo
	err  error
}

func (c clientStub) Enqueue(task *asynqlib.Task, options ...asynqlib.Option) (*asynqlib.TaskInfo, error) {
	return c.EnqueueContext(context.Background(), task, options...)
}

func (c clientStub) EnqueueContext(context.Context, *asynqlib.Task, ...asynqlib.Option) (*asynqlib.TaskInfo, error) {
	return c.info, c.err
}

func TestProfileRecordsDispatchWithoutPayloadByDefault(t *testing.T) {
	mux := http.NewServeMux()
	profiler := webpprof.New(mux)
	t.Cleanup(func() { _ = profiler.Close() })
	client := ProfileWith(profiler, clientStub{info: &asynqlib.TaskInfo{ID: "task-42", Queue: "critical", MaxRetry: 3}})

	_, err := client.EnqueueContext(context.Background(), asynqlib.NewTask("email:deliver", []byte(`{"secret":"value"}`)))
	if err != nil {
		t.Fatal(err)
	}
	job := readJob(t, mux)
	if job.Name != "email:deliver" || job.Queue != "critical" || job.State != "dispatched" || job.MaxAttempts != 4 {
		t.Fatalf("job = %#v", job)
	}
	if len(job.Arguments) != 1 || job.Arguments[0].Value != "" || job.Arguments[0].Size == 0 {
		t.Fatalf("arguments = %#v", job.Arguments)
	}
}

func TestMiddlewareRecordsFailure(t *testing.T) {
	mux := http.NewServeMux()
	profiler := webpprof.New(mux)
	t.Cleanup(func() { _ = profiler.Close() })
	want := errors.New("delivery failed")
	handler := MiddlewareWith(profiler)(asynqlib.HandlerFunc(func(context.Context, *asynqlib.Task) error { return want }))

	if err := handler.ProcessTask(context.Background(), asynqlib.NewTask("email:deliver", nil)); !errors.Is(err, want) {
		t.Fatalf("error = %v", err)
	}
	job := readJob(t, mux)
	if job.State != "failed" || job.Error != want.Error() {
		t.Fatalf("job = %#v", job)
	}
}

func readJob(t *testing.T, handler http.Handler) webpprof.Job {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/debug/webpprof/api/events?kind=job&limit=10", nil))
	var payload struct {
		Events []webpprof.Entry `json:"events"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Events) != 1 {
		t.Fatalf("events = %#v", payload.Events)
	}
	var job webpprof.Job
	if err := json.Unmarshal(payload.Events[0].Data, &job); err != nil {
		t.Fatal(err)
	}
	return job
}
