package echo

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/levskiy0/webpprof"
)

func TestMiddlewareRecordsEchoRoute(t *testing.T) {
	profilerMux := http.NewServeMux()
	profiler := webpprof.New(profilerMux)
	t.Cleanup(func() { _ = profiler.Close() })
	app := echo.New()
	app.Use(MiddlewareWith(profiler))
	app.GET("/players/:id", func(c echo.Context) error { return c.NoContent(http.StatusNoContent) })

	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/players/42", nil))
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
