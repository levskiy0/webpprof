package webpprof

import (
	"net/http"
	"strings"
	"time"
)

const (
	// CaptureSessionHeader can mark requests from one developer browser or
	// client session when WithBrowserSession is configured.
	CaptureSessionHeader = "X-Webpprof-Session"
	// CaptureSessionCookie is the browser cookie alternative to
	// CaptureSessionHeader.
	CaptureSessionCookie = "webpprof_capture"
)

// WithRequestRetentionFilter adds a predicate evaluated after the request has
// completed. Multiple retention filters are combined with AND.
func WithRequestRetentionFilter(filter RequestRetentionFilter) Option {
	return func(c *config) {
		if filter != nil {
			c.retentionFilters = append(c.retentionFilters, filter)
		}
	}
}

// WithHTTPStatusCodes retains requests whose final status equals one of codes.
func WithHTTPStatusCodes(codes ...int) Option {
	allowed := make(map[int]struct{}, len(codes))
	for _, code := range codes {
		allowed[code] = struct{}{}
	}
	return WithRequestRetentionFilter(func(request Request) bool {
		_, ok := allowed[request.Status]
		return ok
	})
}

// WithHTTPStatusAtLeast retains requests whose final status is at least status.
func WithHTTPStatusAtLeast(status int) Option {
	return WithRequestRetentionFilter(func(request Request) bool {
		return request.Status >= status
	})
}

// WithMinRequestDuration retains requests that took at least duration.
func WithMinRequestDuration(duration time.Duration) Option {
	return WithRequestRetentionFilter(func(request Request) bool {
		return request.Duration >= duration
	})
}

// WithRequestTags retains requests containing every configured tag/value pair.
func WithRequestTags(tags map[string]string) Option {
	expected := cloneTags(tags)
	return WithRequestRetentionFilter(func(request Request) bool {
		for key, value := range expected {
			if request.Tags[key] != value {
				return false
			}
		}
		return true
	})
}

// WithNextRequests limits capture to the next count requests that pass early
// request filters and sampling. A zero or negative count captures none.
func WithNextRequests(count int) Option {
	return func(c *config) {
		c.requestLimit = int64(max(0, count))
	}
}

// WithBrowserSession captures only requests marked with session in either the
// X-Webpprof-Session header or the webpprof_capture cookie.
func WithBrowserSession(session string) Option {
	session = strings.TrimSpace(session)
	return WithRequestFilter(func(request *http.Request) bool {
		if session == "" {
			return false
		}
		if request.Header.Get(CaptureSessionHeader) == session {
			return true
		}
		cookie, err := request.Cookie(CaptureSessionCookie)
		return err == nil && cookie.Value == session
	})
}

func (p *Profiler) shouldRetainRequest(request Request) bool {
	for _, filter := range p.config.retentionFilters {
		if !filter(request) {
			return false
		}
	}
	return true
}

func (p *Profiler) takeRequestCaptureSlot() bool {
	if p.config.requestLimit < 0 {
		return true
	}
	for {
		remaining := p.requestRemaining.Load()
		if remaining <= 0 {
			return false
		}
		if p.requestRemaining.CompareAndSwap(remaining, remaining-1) {
			return true
		}
	}
}
