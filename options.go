package webpprof

import (
	"net/http"
	"path"
	"strings"
	"time"
)

const (
	defaultBasePath     = "/debug/webpprof"
	defaultRetention    = 30 * time.Minute
	defaultMaxEvents    = 10_000
	defaultMaxBytes     = 64 << 20
	defaultBodyLimit    = 64 << 10
	defaultStreamBuffer = 256
	defaultQueueTimeout = 1500 * time.Millisecond
)

type Router interface {
	Handle(string, http.Handler)
}

type Option func(*config)

type RequestFilter func(*http.Request) bool

type requestPattern struct {
	method string
	path   string
}

type config struct {
	basePath       string
	token          string
	retention      time.Duration
	maxEvents      int
	maxBytes       int64
	bodyLimit      int64
	streamBuffer   int
	queueTimeout   time.Duration
	secureCookie   bool
	allowedOrigins map[string]struct{}
	requestFilters []RequestFilter
	excluded       []requestPattern
}

func defaultConfig() config {
	return config{
		basePath:       defaultBasePath,
		retention:      defaultRetention,
		maxEvents:      defaultMaxEvents,
		maxBytes:       defaultMaxBytes,
		bodyLimit:      defaultBodyLimit,
		streamBuffer:   defaultStreamBuffer,
		queueTimeout:   defaultQueueTimeout,
		allowedOrigins: make(map[string]struct{}),
	}
}

func WithBasePath(path string) Option {
	return func(c *config) {
		path = "/" + strings.Trim(path, "/")
		if path != "/" {
			c.basePath = path
		}
	}
}

func WithToken(token string) Option {
	return func(c *config) { c.token = token }
}

func WithRetention(retention time.Duration) Option {
	return func(c *config) {
		if retention > 0 {
			c.retention = retention
		}
	}
}

func WithMaxEvents(maxEvents int) Option {
	return func(c *config) {
		if maxEvents > 0 {
			c.maxEvents = maxEvents
		}
	}
}

func WithMaxBytes(maxBytes int64) Option {
	return func(c *config) {
		if maxBytes > 0 {
			c.maxBytes = maxBytes
		}
	}
}

func WithBodyLimit(maxBytes int64) Option {
	return func(c *config) {
		if maxBytes >= 0 {
			c.bodyLimit = maxBytes
		}
	}
}

func WithStreamBuffer(size int) Option {
	return func(c *config) {
		if size > 0 {
			c.streamBuffer = size
		}
	}
}

func WithQueueStatsTimeout(timeout time.Duration) Option {
	return func(c *config) {
		if timeout > 0 {
			c.queueTimeout = timeout
		}
	}
}

func WithSecureCookie(isSecure bool) Option {
	return func(c *config) { c.secureCookie = isSecure }
}

func WithAllowedOrigins(origins ...string) Option {
	return func(c *config) {
		for _, origin := range origins {
			origin = strings.TrimSpace(origin)
			if origin != "" {
				c.allowedOrigins[origin] = struct{}{}
			}
		}
	}
}

func WithRequestFilter(filter RequestFilter) Option {
	return func(c *config) {
		if filter != nil {
			c.requestFilters = append(c.requestFilters, filter)
		}
	}
}

func WithExcludedRequests(patterns ...string) Option {
	return func(c *config) {
		c.excluded = append(c.excluded, parseRequestPatterns(patterns)...)
	}
}

func ExcludingRequests(patterns ...string) RequestFilter {
	excluded := parseRequestPatterns(patterns)
	return func(request *http.Request) bool {
		for _, pattern := range excluded {
			if pattern.matches(request) {
				return false
			}
		}
		return true
	}
}

func parseRequestPatterns(patterns []string) []requestPattern {
	parsed := make([]requestPattern, 0, len(patterns))
	for _, pattern := range patterns {
		fields := strings.Fields(pattern)
		switch len(fields) {
		case 1:
			parsed = append(parsed, requestPattern{path: fields[0]})
		case 2:
			parsed = append(parsed, requestPattern{method: strings.ToUpper(fields[0]), path: fields[1]})
		}
	}
	return parsed
}

func (p requestPattern) matches(request *http.Request) bool {
	if p.path == "" || p.method != "" && p.method != request.Method {
		return false
	}
	if strings.HasSuffix(p.path, "/*") {
		prefix := strings.TrimSuffix(p.path, "/*")
		return request.URL.Path == prefix || strings.HasPrefix(request.URL.Path, prefix+"/")
	}
	if strings.HasPrefix(p.path, "*.") {
		return strings.HasSuffix(strings.ToLower(request.URL.Path), strings.ToLower(strings.TrimPrefix(p.path, "*")))
	}
	matched, err := path.Match(p.path, request.URL.Path)
	return err == nil && matched
}
