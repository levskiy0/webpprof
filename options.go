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

// Router is the minimal HTTP routing contract required to mount the profiler.
// Both http.ServeMux and routers exposing the same Handle method satisfy it.
type Router interface {
	Handle(string, http.Handler)
}

// Option configures a Profiler during construction.
type Option func(*config)

// RequestFilter decides whether an incoming request should be captured.
// Returning false skips the request and all entities correlated with it.
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
)

type config struct {
	basePath         string
	token            string
	unsafeNoAuth     bool
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
	storage          EntryStorage
	requestSample    float64
	requestLimit     int64
	disabledKinds    map[Kind]struct{}
	callsiteKinds    map[Kind]struct{}
	sidebarKinds     []Kind
	sourceLink       SourceLinkFunc
}

var defaultSidebarKinds = []Kind{
	KindSchedule,
	KindCallable,
	KindTask,
	KindRequest,
	KindMiddleware,
	KindQuery,
	KindCache,
	KindJob,
	KindEmail,
	KindLog,
	KindHTTPCall,
	KindException,
	KindEvent,
}

// WithStoragePath persists captured entries in an append-only journal. The
// journal is replayed when the profiler starts and compacted automatically.
// Leave path empty to keep the default in-memory-only behavior.
func WithStoragePath(storagePath string) Option {
	return func(c *config) {
		c.storage = nil
		c.storagePath = strings.TrimSpace(storagePath)
		c.storageKind = storageKindMemory
		if c.storagePath != "" {
			c.storageKind = storageKindJournal
		}
	}
}

// WithStorage uses an optional external storage implementation. The storage is
// replayed at startup, pruned with the in-memory retention limits, and closed
// with the profiler. When multiple storage options are supplied, the last one
// wins. Pass nil to restore in-memory-only behavior.
func WithStorage(storage EntryStorage) Option {
	return func(c *config) {
		c.storage = storage
		c.storagePath = ""
		c.storageKind = storageKindMemory
		if storage != nil {
			name := strings.TrimSpace(storage.Name())
			if name == "" {
				name = "custom"
			}
			c.storageKind = storageKind(name)
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
		sidebarKinds:     append([]Kind(nil), defaultSidebarKinds...),
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
// supported kinds are Query, Cache, Email, Job, HTTPCall, Schedule, Callable,
// and Task.
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

// WithSidebarKinds replaces the ordered entity sections shown in the viewer
// sidebar. Dashboard remains first and All Events remains last. Passing no
// kinds hides every entity-specific section without disabling capture.
func WithSidebarKinds(kinds ...Kind) Option {
	return func(c *config) {
		c.sidebarKinds = make([]Kind, 0, len(kinds))
		seen := make(map[Kind]struct{}, len(kinds))
		for _, kind := range kinds {
			if !supportsSidebar(kind) {
				continue
			}
			if _, duplicate := seen[kind]; duplicate {
				continue
			}
			seen[kind] = struct{}{}
			c.sidebarKinds = append(c.sidebarKinds, kind)
		}
	}
}

func supportsSidebar(kind Kind) bool {
	switch kind {
	case KindRequest, KindMiddleware, KindQuery, KindCache, KindJob, KindEmail,
		KindLog, KindHTTPCall, KindSchedule, KindCallable, KindTask, KindException, KindEvent:
		return true
	default:
		return false
	}
}

// WithBasePath changes the URL prefix used by the dashboard and JSON API.
// Empty paths and "/" leave the default /debug/webpprof prefix unchanged.
func WithBasePath(path string) Option {
	return func(c *config) {
		path = "/" + strings.Trim(path, "/")
		if path != "/" {
			c.basePath = path
		}
	}
}

// WithToken protects the dashboard and API with the supplied access token.
// Start requires either a non-empty token or WithUnsafeUnauthenticatedAccess.
func WithToken(token string) Option {
	return func(c *config) { c.token = token }
}

// WithUnsafeUnauthenticatedAccess exposes captured profiler data without a
// token. This should only be used on trusted local or otherwise isolated
// transports; remote access should use WithToken and additional network-level
// controls.
func WithUnsafeUnauthenticatedAccess() Option {
	return func(c *config) { c.unsafeNoAuth = true }
}

// WithRetention sets how long recorded entries remain available. Non-positive
// values leave the default retention unchanged.
func WithRetention(retention time.Duration) Option {
	return func(c *config) {
		if retention > 0 {
			c.retention = retention
		}
	}
}

// WithMaxEvents bounds the number of entries kept in memory. Non-positive
// values leave the default limit unchanged.
func WithMaxEvents(maxEvents int) Option {
	return func(c *config) {
		if maxEvents > 0 {
			c.maxEvents = maxEvents
		}
	}
}

// WithMaxBytes bounds the approximate encoded size of entries kept in memory.
// Non-positive values leave the default limit unchanged.
func WithMaxBytes(maxBytes int64) Option {
	return func(c *config) {
		if maxBytes > 0 {
			c.maxBytes = maxBytes
		}
	}
}

// WithBodyLimit limits captured HTTP request and response bodies. A zero limit
// disables body capture; negative values leave the default unchanged.
func WithBodyLimit(maxBytes int64) Option {
	return func(c *config) {
		if maxBytes >= 0 {
			c.bodyLimit = maxBytes
		}
	}
}

// WithStreamBuffer sets the per-subscriber live-event buffer size.
// Non-positive values leave the default size unchanged.
func WithStreamBuffer(size int) Option {
	return func(c *config) {
		if size > 0 {
			c.streamBuffer = size
		}
	}
}

// WithQueueStatsTimeout limits collection time for registered queue metrics.
// Non-positive values leave the default timeout unchanged.
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

// WithSecureCookie controls the Secure attribute of the dashboard session
// cookie. Enable it when the profiler is served over HTTPS.
func WithSecureCookie(isSecure bool) Option {
	return func(c *config) { c.secureCookie = isSecure }
}

// WithAllowedOrigins permits the listed browser origins to access profiler
// endpoints. Blank origins are ignored.
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

// WithRequestFilter appends a capture predicate. All configured predicates
// must return true for a request to be recorded; nil predicates are ignored.
func WithRequestFilter(filter RequestFilter) Option {
	return func(c *config) {
		if filter != nil {
			c.requestFilters = append(c.requestFilters, filter)
		}
	}
}

// WithExcludedRequests skips matching requests. Patterns may be paths, glob
// paths, prefix patterns ending in /*, or "METHOD path" pairs.
func WithExcludedRequests(patterns ...string) Option {
	return func(c *config) {
		c.excluded = append(c.excluded, parseRequestPatterns(patterns)...)
	}
}

// ExcludingRequests builds a reusable RequestFilter that rejects matching
// paths or "METHOD path" patterns.
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
