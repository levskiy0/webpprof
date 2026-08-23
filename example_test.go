package webpprof_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/levskiy0/webpprof"
	webpprofhttp "github.com/levskiy0/webpprof/profiler/http"
)

// Example demonstrates mounting webpprof beside an application handler.
func Example() {
	mux := http.NewServeMux()
	profiler := webpprof.New(
		mux,
		webpprof.WithUnsafeUnauthenticatedAccess(),
		webpprof.WithExcludedRequests("GET /health"),
	)
	defer profiler.Close()

	application := http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusNoContent)
	})
	mux.Handle("/api/", webpprofhttp.MiddlewareWith(profiler, application))

	request := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)

	fmt.Println(response.Code, profiler.BasePath())
	// Output: 204 /debug/webpprof
}

// ExampleProfiler_LogQuery demonstrates manual SQL instrumentation.
func ExampleProfiler_LogQuery() {
	mux := http.NewServeMux()
	profiler := webpprof.New(mux, webpprof.WithUnsafeUnauthenticatedAccess())
	defer profiler.Close()

	startedAt := time.Now()
	profiler.LogQuery(webpprof.Query{
		Meta: webpprof.Meta{
			StartedAt: startedAt,
			Duration:  3 * time.Millisecond,
		},
		Driver: "postgres",
		SQL:    "select id, email from users where id = $1",
	})

	fmt.Println(profiler.Enabled())
	// Output: true
}

// ExampleExcludingRequests demonstrates reusable request capture rules.
func ExampleExcludingRequests() {
	filter := webpprof.ExcludingRequests("GET /health", "/assets/*")

	health := httptest.NewRequest(http.MethodGet, "/health", nil)
	postHealth := httptest.NewRequest(http.MethodPost, "/health", nil)
	asset := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)

	fmt.Println(filter(health), filter(postHealth), filter(asset))
	// Output: false true false
}
