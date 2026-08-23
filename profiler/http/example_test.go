package http_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/levskiy0/webpprof"
	webpprofhttp "github.com/levskiy0/webpprof/profiler/http"
)

// ExampleMiddlewareWith demonstrates wrapping a standard HTTP handler.
func ExampleMiddlewareWith() {
	mux := http.NewServeMux()
	profiler := webpprof.New(mux, webpprof.WithUnsafeUnauthenticatedAccess())
	defer profiler.Close()

	handler := webpprofhttp.MiddlewareWith(profiler, http.HandlerFunc(
		func(response http.ResponseWriter, _ *http.Request) {
			_, _ = response.Write([]byte("ok"))
		},
	))

	request := httptest.NewRequest(http.MethodGet, "/users/42", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	fmt.Println(response.Code, response.Body.String())
	// Output: 200 ok
}
