// Package chi augments the standard net/http profiler with Chi route patterns.
package chi

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/levskiy0/webpprof"
	webpprofhttp "github.com/levskiy0/webpprof/profiler/http"
)

// Middleware profiles requests with the default profiler and records the
// matched Chi route pattern after the handler returns.
func Middleware(next http.Handler) http.Handler {
	return MiddlewareWith(webpprof.Default())(next)
}

// MiddlewareWith profiles requests with p and records the Chi route pattern.
func MiddlewareWith(p *webpprof.Profiler) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if next == nil {
			panic("webpprof: nil Chi handler")
		}
		if p == nil {
			return next
		}
		routed := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
			if capture := webpprof.RequestFromContext(r.Context()); capture != nil {
				capture.SetRoute(chi.RouteContext(r.Context()).RoutePattern())
			}
		})
		return webpprofhttp.MiddlewareWith(p, routed)
	}
}
