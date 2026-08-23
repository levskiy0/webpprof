package nats

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/levskiy0/webpprof"
	natslibrary "github.com/nats-io/nats.go"
)

type clientStub struct{ handler natslibrary.MsgHandler }

func (*clientStub) Publish(string, []byte) error      { return nil }
func (*clientStub) PublishMsg(*natslibrary.Msg) error { return nil }
func (c *clientStub) Subscribe(_ string, handler natslibrary.MsgHandler) (*natslibrary.Subscription, error) {
	c.handler = handler
	return nil, nil
}
func (c *clientStub) QueueSubscribe(_, _ string, handler natslibrary.MsgHandler) (*natslibrary.Subscription, error) {
	c.handler = handler
	return nil, nil
}

func TestPublishContextCorrelatesAndDoesNotCapturePayload(t *testing.T) {
	mux := http.NewServeMux()
	profiler := webpprof.New(mux, webpprof.WithUnsafeUnauthenticatedAccess())
	t.Cleanup(func() { _ = profiler.Close() })
	client := ProfileWith(profiler, &clientStub{})
	capture := profiler.BeginRequest(webpprof.Request{Method: http.MethodPost, Path: "/players"})
	ctx := webpprof.WithRequest(context.Background(), capture)

	if err := client.PublishContext(ctx, "players.created", []byte("secret")); err != nil {
		t.Fatal(err)
	}
	capture.Finish(webpprof.RequestResult{Status: http.StatusCreated})
	job := readJob(t, mux)
	if job.Name != "players.created" || job.State != "dispatched" || job.Arguments[0].Value != "" || job.RequestID == "" {
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
	for _, entry := range payload.Events {
		if entry.Kind == webpprof.KindJob {
			var job webpprof.Job
			if err := json.Unmarshal(entry.Data, &job); err != nil {
				t.Fatal(err)
			}
			return job
		}
	}
	t.Fatal("job event not found")
	return webpprof.Job{}
}
