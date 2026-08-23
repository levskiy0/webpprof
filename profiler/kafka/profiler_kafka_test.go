package kafka

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/levskiy0/webpprof"
	kafkalibrary "github.com/segmentio/kafka-go"
)

type writerStub struct{}

func (writerStub) WriteMessages(context.Context, ...kafkalibrary.Message) error { return nil }

func TestWriterRecordsOneDispatchPerMessage(t *testing.T) {
	mux := http.NewServeMux()
	profiler := webpprof.New(mux, webpprof.WithUnsafeUnauthenticatedAccess())
	t.Cleanup(func() { _ = profiler.Close() })
	writer := ProfileWriterWith(profiler, writerStub{}, Config{Topic: "players"})

	if err := writer.WriteMessages(context.Background(), kafkalibrary.Message{Key: []byte("42"), Value: []byte("secret")}); err != nil {
		t.Fatal(err)
	}
	job := readJob(t, mux)
	if job.Name != "players" || job.State != "dispatched" || len(job.Arguments) != 2 || job.Arguments[1].Value != "" {
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
