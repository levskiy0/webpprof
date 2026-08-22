package customprofiler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/levskiy0/webpprof"
)

type clientStub struct {
	value string
	calls int
}

func (c *clientStub) Lookup(context.Context, string) (string, error) {
	c.calls++
	return c.value, nil
}

func TestProfileWithLogsRelatedEvent(t *testing.T) {
	mux := http.NewServeMux()
	profiler := webpprof.New(mux)
	t.Cleanup(func() { _ = profiler.Close() })

	inner := &clientStub{value: "Ada"}
	client := ProfileWith(profiler, inner)
	if actual := ProfileWith(profiler, client); actual != client {
		t.Fatal("profiling an already wrapped client returned another wrapper")
	}

	capture := profiler.BeginRequest(webpprof.Request{
		Meta:   webpprof.Meta{ID: "request-1"},
		Method: http.MethodGet,
		Path:   "/demo",
	})
	ctx := webpprof.WithRequest(context.Background(), capture)

	value, err := client.Lookup(ctx, "player:42")
	if err != nil || value != "Ada" || inner.calls != 1 {
		t.Fatalf("Lookup() = %q, %v; calls = %d", value, err, inner.calls)
	}
	capture.Finish(webpprof.RequestResult{Status: http.StatusOK})

	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/debug/webpprof/api/events?request_id=request-1", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("events status = %d", response.Code)
	}

	var payload struct {
		Events []webpprof.Entry `json:"events"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode events: %v", err)
	}

	for _, entry := range payload.Events {
		if entry.Kind != webpprof.KindEvent {
			continue
		}
		var event webpprof.Event
		if err := json.Unmarshal(entry.Data, &event); err != nil {
			t.Fatalf("decode custom event: %v", err)
		}
		if entry.RequestID != "request-1" || event.Kind != "custom-client" || event.Name != "lookup" || event.Status != "found" {
			t.Fatalf("custom event = %+v, entry = %+v", event, entry)
		}
		return
	}

	t.Fatal("related custom event was not recorded")
}
