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
	profiler := webpprof.New(mux)
	t.Cleanup(func() { _ = profiler.Close() })
	want := errors.New("smtp unavailable")
	sender := ProfileWith(profiler, senderStub{err: want})
	if err := sender.Send(context.Background(), webpprof.Email{Transport: "smtp", Subject: "Welcome"}); !errors.Is(err, want) {
		t.Fatalf("error = %v", err)
	}
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/debug/webpprof/api/events?kind=email&limit=10", nil))
	var payload struct {
		Events []webpprof.Entry `json:"events"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Events) != 1 {
		t.Fatalf("events = %+v", payload.Events)
	}
	var event webpprof.Email
	if err := json.Unmarshal(payload.Events[0].Data, &event); err != nil {
		t.Fatal(err)
	}
	if event.Status != "failed" || event.Error != want.Error() {
		t.Fatalf("event = %+v", event)
	}
}
