package fiber

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	fiberlibrary "github.com/gofiber/fiber/v3"
	"github.com/levskiy0/webpprof"
)

func TestMiddlewareRecordsFiberRoute(t *testing.T) {
	profilerMux := http.NewServeMux()
	profiler := webpprof.New(profilerMux)
	t.Cleanup(func() { _ = profiler.Close() })
	app := fiberlibrary.New()
	app.Use(MiddlewareWith(profiler))
	app.Get("/players/:id", func(c fiberlibrary.Ctx) error { return c.SendStatus(http.StatusNoContent) })

	response, err := app.Test(httptest.NewRequest(http.MethodGet, "/players/42", nil))
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	request := readRequest(t, profilerMux)
	if request.Route != "/players/:id" || request.Status != http.StatusNoContent {
		t.Fatalf("request = %#v", request)
	}
}

func readRequest(t *testing.T, handler http.Handler) webpprof.Request {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/debug/webpprof/api/events?kind=request&limit=10", nil))
	var payload struct {
		Events []webpprof.Entry `json:"events"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Events) != 1 {
		t.Fatalf("events = %#v", payload.Events)
	}
	var request webpprof.Request
	if err := json.Unmarshal(payload.Events[0].Data, &request); err != nil {
		t.Fatal(err)
	}
	return request
}
