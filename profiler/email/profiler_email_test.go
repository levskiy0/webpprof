package email

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/levskiy0/webpprof"
)

type senderStub struct {
	err error
}

func (s senderStub) Send(context.Context, webpprof.Email) error {
	return s.err
}

func TestProfilerEmailRecordsAndReturnsOriginalError(t *testing.T) {
	mux := http.NewServeMux()
	profiler := webpprof.New(mux, webpprof.WithUnsafeUnauthenticatedAccess())
	t.Cleanup(func() { _ = profiler.Close() })
	want := errors.New("smtp unavailable")
	sender := ProfileWith(profiler, senderStub{err: want})
	capture := profiler.BeginRequest(webpprof.Request{Meta: webpprof.Meta{ID: "email-request"}, Method: http.MethodPost, Path: "/welcome"})
	ctx := webpprof.WithRequest(context.Background(), capture)
	if err := sender.Send(ctx, webpprof.Email{Transport: "smtp", Subject: "Welcome"}); !errors.Is(err, want) {
		t.Fatalf("error = %v", err)
	}
	capture.Finish(webpprof.RequestResult{Status: http.StatusBadGateway})
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/debug/webpprof/api/events?request_id=email-request&limit=10", nil))
	var payload struct {
		Events []webpprof.Entry `json:"events"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Events) != 2 || payload.Events[0].Kind != webpprof.KindEmail || payload.Events[1].Kind != webpprof.KindRequest {
		t.Fatalf("events = %+v", payload.Events)
	}
	if payload.Events[0].RequestID != "email-request" {
		t.Fatalf("email request id = %q", payload.Events[0].RequestID)
	}
	var event webpprof.Email
	if err := json.Unmarshal(payload.Events[0].Data, &event); err != nil {
		t.Fatal(err)
	}
	if event.Status != "failed" || event.Error != want.Error() {
		t.Fatalf("event = %+v", event)
	}
}
