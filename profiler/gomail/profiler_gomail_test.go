package gomail

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/levskiy0/webpprof"
	mail "github.com/wneessen/go-mail"
)

type clientStub struct {
	err      error
	messages []*mail.Msg
}

func (c *clientStub) DialAndSendWithContext(_ context.Context, messages ...*mail.Msg) error {
	c.messages = append(c.messages, messages...)
	return c.err
}

func TestProfileRecordsMessagesAndReturnsOriginalError(t *testing.T) {
	mux := http.NewServeMux()
	profiler := webpprof.New(mux, webpprof.WithUnsafeUnauthenticatedAccess())
	t.Cleanup(func() { _ = profiler.Close() })

	wantErr := errors.New("smtp unavailable")
	inner := &clientStub{err: wantErr}
	profiled := ProfileWith(profiler, inner)
	message := mail.NewMsg()
	if err := message.SetAddrHeader(mail.HeaderFrom, "Sender <sender@example.com>"); err != nil {
		t.Fatal(err)
	}
	if err := message.SetAddrHeader(mail.HeaderTo, "First <first@example.com>", "second@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := message.SetAddrHeader(mail.HeaderCc, "copy@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := message.SetAddrHeader(mail.HeaderBcc, "hidden@example.com"); err != nil {
		t.Fatal(err)
	}
	message.SetGenHeader(mail.HeaderSubject, "Welcome")

	if err := profiled.DialAndSendWithContext(context.Background(), message, nil); !errors.Is(err, wantErr) {
		t.Fatalf("send error = %v", err)
	}
	if len(inner.messages) != 2 {
		t.Fatalf("forwarded messages = %d", len(inner.messages))
	}

	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/debug/webpprof/api/events?kind=email&limit=10", nil))
	var payload struct {
		Events []webpprof.Entry `json:"events"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Events) != 2 {
		t.Fatalf("email events = %+v", payload.Events)
	}
	var event webpprof.Email
	if err := json.Unmarshal(payload.Events[0].Data, &event); err != nil {
		t.Fatal(err)
	}
	if event.Status != "failed" || event.Error != wantErr.Error() || event.Subject != "Welcome" {
		t.Fatalf("email event = %+v", event)
	}
	if event.From.Email != "sender@example.com" || len(event.To) != 2 || len(event.CC) != 1 || len(event.BCC) != 1 {
		t.Fatalf("email addresses = %+v", event)
	}
}

func TestProfileDoesNotDoubleWrapClient(t *testing.T) {
	profiler := webpprof.New(http.NewServeMux())
	t.Cleanup(func() { _ = profiler.Close() })
	inner := &clientStub{}
	first := ProfileWith(profiler, inner)
	if second := ProfileWith(profiler, first); second != first {
		t.Fatal("client was wrapped twice")
	}
	if ProfileWith(nil, inner) != inner || ProfileWith(profiler, nil) != nil {
		t.Fatal("nil profiler or client was not preserved")
	}
}
