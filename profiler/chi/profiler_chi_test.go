package chi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	chilibrary "github.com/go-chi/chi/v5"
	"github.com/levskiy0/webpprof"
)

func TestMiddlewareRecordsRoutePattern(t *testing.T) {
	profilerMux := http.NewServeMux()
	profiler := webpprof.New(profilerMux, webpprof.WithUnsafeUnauthenticatedAccess())
	t.Cleanup(func() { _ = profiler.Close() })
	router := chilibrary.NewRouter()
	router.Use(MiddlewareWith(profiler))
	router.Get("/players/{playerID}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/players/42", nil))
	request := readRequest(t, profilerMux)
	if request.Route != "/players/{playerID}" || request.Status != http.StatusNoContent {
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
