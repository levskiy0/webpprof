package webpprof

import (
	"net/http"
	"path"
	"strings"
	"time"
)

const (
	defaultBasePath         = "/debug/webpprof"
	defaultRetention        = 30 * time.Minute
	defaultMaxEvents        = 10_000
	defaultMaxBytes         = 64 << 20
	defaultBodyLimit        = 64 << 10
	defaultStreamBuffer     = 256
	defaultQueueTimeout     = 1500 * time.Millisecond
	defaultDashboardTimeout = 1500 * time.Millisecond
)

type Router interface {
	Handle(string, http.Handler)
}

type Option func(*config)

type RequestFilter func(*http.Request) bool

// RequestRetentionFilter decides whether a completed request and all of its
// related entities should be persisted.
type RequestRetentionFilter func(Request) bool

// SourceLinkFunc converts a captured Go source frame into an editor or source
// browser URL. Return an empty string when a frame should not be linked.
type SourceLinkFunc func(SourceFrame) string

type requestPattern struct {
	method string
	path   string
}

type storageKind string

const (
	storageKindMemory  storageKind = "memory"
	storageKindJournal storageKind = "disk"
	storageKindSQLite  storageKind = "sqlite"
)

type config struct {
	basePath         string
	token            string
	retention        time.Duration
	maxEvents        int
	maxBytes         int64
	bodyLimit        int64
	streamBuffer     int
	queueTimeout     time.Duration
	dashboardTimeout time.Duration
	dashboard        dashboardConfig
	secureCookie     bool
	allowedOrigins   map[string]struct{}
	requestFilters   []RequestFilter
	retentionFilters []RequestRetentionFilter
	excluded         []requestPattern
	storageKind      storageKind
	storagePath      string
	requestSample    float64
	requestLimit     int64
	disabledKinds    map[Kind]struct{}
	callsiteKinds    map[Kind]struct{}
	sourceLink       SourceLinkFunc
}

// WithStoragePath persists captured entries in an append-only journal. The
// journal is replayed when the profiler starts and compacted automatically.
// Leave path empty to keep the default in-memory-only behavior.
func WithStoragePath(storagePath string) Option {
	return func(c *config) {
		c.storagePath = strings.TrimSpace(storagePath)
		c.storageKind = storageKindMemory
		if c.storagePath != "" {
			c.storageKind = storageKindJournal
		}
	}
}

// WithSQLiteStorage persists captured entries in a SQLite database. The
// database is replayed when the profiler starts and is pruned with the same
// retention, event-count, and byte limits as the in-memory store. Leave path
// empty to keep the default in-memory-only behavior. When multiple storage
// options are supplied, the last one wins.
func WithSQLiteStorage(storagePath string) Option {
	return func(c *config) {
		c.storagePath = strings.TrimSpace(storagePath)
		c.storageKind = storageKindMemory
		if c.storagePath != "" {
			c.storageKind = storageKindSQLite
		}
	}
}

func defaultConfig() config {
	return config{
		basePath:         defaultBasePath,
		retention:        defaultRetention,
		maxEvents:        defaultMaxEvents,
		maxBytes:         defaultMaxBytes,
		bodyLimit:        defaultBodyLimit,
		streamBuffer:     defaultStreamBuffer,
		queueTimeout:     defaultQueueTimeout,
		dashboardTimeout: defaultDashboardTimeout,
		dashboard:        defaultDashboardConfig(),
		storageKind:      storageKindMemory,
		allowedOrigins:   make(map[string]struct{}),
		requestSample:    1,
		requestLimit:     -1,
		disabledKinds:    make(map[Kind]struct{}),
		callsiteKinds:    map[Kind]struct{}{KindQuery: {}},
	}
}

// WithQueryCallsite controls automatic Go stack capture for queries. It is
// enabled by default; disable it when the allocation overhead is undesirable.
// Deprecated: use WithCallsiteKinds to select all entity kinds whose callsites
// should be captured. This option remains available for backward compatibility.
func WithQueryCallsite(enabled bool) Option {
	return func(c *config) {
		if c.callsiteKinds == nil {
			c.callsiteKinds = make(map[Kind]struct{})
		}
		if enabled {
			c.callsiteKinds[KindQuery] = struct{}{}
			return
		}
		delete(c.callsiteKinds, KindQuery)
	}
}

// WithCallsiteKinds replaces the set of entity kinds whose Go callsites are
// captured automatically. Passing no kinds disables automatic capture. The
// supported kinds are Query, Cache, Email, Job, HTTPCall, and Schedule.
func WithCallsiteKinds(kinds ...Kind) Option {
	return func(c *config) {
		c.callsiteKinds = make(map[Kind]struct{}, len(kinds))
		for _, kind := range kinds {
			if supportsCallsite(kind) {
				c.callsiteKinds[kind] = struct{}{}
			}
		}
	}
}

// WithSourceLink makes captured Go frames clickable in the viewer.
func WithSourceLink(sourceLink SourceLinkFunc) Option {
	return func(c *config) { c.sourceLink = sourceLink }
}

// WithRequestSampleRate records approximately the given fraction of incoming
// HTTP requests. Values are clamped to the inclusive range 0..1.
func WithRequestSampleRate(rate float64) Option {
	return func(c *config) {
		c.requestSample = min(1, max(0, rate))
	}
}

// WithDisabledKinds prevents the listed entity kinds from being recorded.
func WithDisabledKinds(kinds ...Kind) Option {
	return func(c *config) {
		for _, kind := range kinds {
			if kind != "" {
				c.disabledKinds[kind] = struct{}{}
			}
		}
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

// WithDashboardTimeout limits how long one dashboard snapshot may spend
// collecting values from custom metric callbacks.
func WithDashboardTimeout(timeout time.Duration) Option {
	return func(c *config) {
		if timeout > 0 {
			c.dashboardTimeout = timeout
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
