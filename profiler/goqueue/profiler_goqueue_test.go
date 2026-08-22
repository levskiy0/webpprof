package goqueue

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/levskiy0/webpprof"
	goqueue "github.com/levskiy0/go-queue"
	queuecontract "github.com/levskiy0/go-queue/contract"
)

type testJob struct{}

func (testJob) Signature() string {
	return "email:send"
}

func (testJob) Handle(...any) error {
	return nil
}

func TestProfileRecordsDispatchExecutionAndStats(t *testing.T) {
	mux := http.NewServeMux()
	profiler := webpprof.New(mux)
	t.Cleanup(func() { _ = profiler.Close() })
	connections := goqueue.NewConnections().Add("default", &goqueue.Connection{Driver: goqueue.DriverSync})
	queue := ProfileWith(profiler, goqueue.NewQueue(connections, slog.New(slog.NewTextHandler(io.Discard, nil)), false))
	job := testJob{}
	queue.Register(ProfileJobsWith(profiler, []queuecontract.Job{job}, "mail"))
	if err := queue.Job(job, []queuecontract.Arg{{Type: "uint64", Value: uint64(42)}}).OnQueue("mail").Dispatch(); err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/debug/webpprof/api/events?kind=job&limit=10", nil))
	var response struct {
		Events []webpprof.Entry `json:"events"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Events) != 2 {
		t.Fatalf("job events = %+v", response.Events)
	}
	stats := profiler.QueueStats(context.Background())
	if len(stats.Sources) != 1 || stats.Sources[0].Processed != 1 || len(stats.Sources[0].Queues) != 1 || stats.Sources[0].Queues[0].Name != "mail" {
		t.Fatalf("queue stats = %+v", stats)
	}
}
