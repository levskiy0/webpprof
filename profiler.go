package webpprof

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

var defaultProfiler atomic.Pointer[Profiler]
var defaultProfilerMu sync.Mutex

// Profiler records bounded diagnostic entries and serves the dashboard and API.
// A process has at most one active default Profiler; call Close or Shutdown to
// release it before constructing another.
type Profiler struct {
	config            config
	store             *entryStore
	startedAt         time.Time
	sessionToken      string
	closeOnce         sync.Once
	integrationValues sync.Map
	queueStatsMu      sync.RWMutex
	queueStats        map[string]QueueStatsSource
	serverMu          sync.Mutex
	server            *http.Server
	serverAddr        string
	loginMu           sync.Mutex
	loginFailures     map[string]loginFailure
	requestRemaining  atomic.Int64
}

// New creates the process-wide profiler and mounts its handlers on router. It
// returns the existing default profiler when already initialized and panics if
// router is nil on first initialization.
func New(router Router, options ...Option) *Profiler {
	defaultProfilerMu.Lock()
	defer defaultProfilerMu.Unlock()
	if profiler := defaultProfiler.Load(); profiler != nil {
		return profiler
	}
	if router == nil {
		panic("webpprof: nil router")
	}
	configuration := configFromOptions(options...)
	profiler := newProfilerWithConfig(configuration)
	profiler.register(router)
	defaultProfiler.Store(profiler)
	return profiler
}

func newProfiler(options ...Option) *Profiler {
	return newProfilerWithConfig(configFromOptions(options...))
}

func configFromOptions(options ...Option) config {
	c := defaultConfig()
	for _, option := range options {
		if option != nil {
			option(&c)
		}
	}
	return c
}

func newProfilerWithConfig(c config) *Profiler {
	profiler := &Profiler{config: c, store: newEntryStore(c), startedAt: time.Now().UTC(), sessionToken: newID(), loginFailures: make(map[string]loginFailure)}
	profiler.requestRemaining.Store(c.requestLimit)
	return profiler
}

func validateAuthentication(c config) error {
	if c.token == "" && !c.unsafeNoAuth {
		return errors.New("webpprof: authentication is required; configure WithToken or explicitly opt in with WithUnsafeUnauthenticatedAccess")
	}
	return nil
}

// NewIf calls New only when enabled. It is useful for environment-controlled
// setup and returns nil when disabled.
func NewIf(enabled bool, router Router, options ...Option) *Profiler {
	if !enabled {
		return nil
	}
	return New(router, options...)
}

// Default returns the active process-wide profiler, or nil when profiling is
// not initialized.
func Default() *Profiler {
	return defaultProfiler.Load()
}

// Enabled reports whether a default profiler is active.
func Enabled() bool {
	return Default() != nil
}

// Enabled reports whether the receiver is non-nil.
func (p *Profiler) Enabled() bool {
	return p != nil
}

// BasePath returns the URL prefix under which dashboard handlers are mounted.
func (p *Profiler) BasePath() string {
	if p == nil {
		return ""
	}
	return p.config.basePath
}

// BodyLimit returns the maximum number of request or response body bytes
// captured by HTTP integrations.
func (p *Profiler) BodyLimit() int64 {
	if p == nil {
		return 0
	}
	return p.config.bodyLimit
}

// ShouldCaptureRequest applies the default profiler's exclusions, filters,
// sampling rate, and optional request limit.
func ShouldCaptureRequest(request *http.Request) bool {
	profiler := Default()
	return profiler != nil && profiler.ShouldCaptureRequest(request)
}

// ShouldCaptureRequest applies this profiler's exclusions, filters, sampling
// rate, and optional request limit. A successful call consumes one configured
// request-limit slot.
func (p *Profiler) ShouldCaptureRequest(request *http.Request) bool {
	if p == nil || request == nil {
		return false
	}
	for _, excluded := range p.config.excluded {
		if excluded.matches(request) {
			return false
		}
	}
	for _, filter := range p.config.requestFilters {
		if !filter(request) {
			return false
		}
	}
	if p.config.requestSample <= 0 {
		return false
	}
	if p.config.requestSample < 1 {
		var sample [8]byte
		if _, err := rand.Read(sample[:]); err != nil {
			return false
		}
		if float64(binary.BigEndian.Uint64(sample[:]))/float64(^uint64(0)) >= p.config.requestSample {
			return false
		}
	}
	return p.takeRequestCaptureSlot()
}

// NewID returns a random 128-bit lowercase hexadecimal identifier. It falls
// back to a UTC timestamp only when the system random source fails.
func NewID() string {
	return newID()
}

// Close immediately stops an owned server, closes storage, and clears the
// default profiler. It is safe to call repeatedly; a nil receiver is a no-op.
func (p *Profiler) Close() error {
	if p == nil {
		return nil
	}
	defaultProfilerMu.Lock()
	defer defaultProfilerMu.Unlock()
	p.serverMu.Lock()
	server := p.server
	p.server = nil
	p.serverAddr = ""
	p.serverMu.Unlock()
	var serverErr error
	if server != nil {
		serverErr = server.Close()
	}
	p.closeOnce.Do(func() {
		p.store.close()
	})
	defaultProfiler.CompareAndSwap(p, nil)
	return serverErr
}

// LogRequest records a completed request and stores its related entities as
// individually correlated entries. Retention filters may discard the request.
func (p *Profiler) LogRequest(request Request) {
	if p == nil {
		return
	}
	if !p.shouldRetainRequest(request) {
		return
	}
	if request.ID == "" {
		request.ID = newID()
	}
	requestID := request.ID
	for index := range request.Queries {
		attachRequest(&request.Queries[index].Meta, requestID, request.Tags)
		p.LogQuery(request.Queries[index])
	}
	for index := range request.Emails {
		attachRequest(&request.Emails[index].Meta, requestID, request.Tags)
		p.LogEmail(request.Emails[index])
	}
	for index := range request.Cache {
		attachRequest(&request.Cache[index].Meta, requestID, request.Tags)
		p.LogCache(request.Cache[index])
	}
	for index := range request.Jobs {
		attachRequest(&request.Jobs[index].Meta, requestID, request.Tags)
		p.LogJob(request.Jobs[index])
	}
	for index := range request.Logs {
		attachRequest(&request.Logs[index].Meta, requestID, request.Tags)
		p.LogLog(request.Logs[index])
	}
	for index := range request.HTTPCalls {
		attachRequest(&request.HTTPCalls[index].Meta, requestID, request.Tags)
		p.LogHTTPCall(request.HTTPCalls[index])
	}
	for index := range request.Schedules {
		attachRequest(&request.Schedules[index].Meta, requestID, request.Tags)
		p.LogSchedule(request.Schedules[index])
	}
	for index := range request.Exceptions {
		attachRequest(&request.Exceptions[index].Meta, requestID, request.Tags)
		p.LogException(request.Exceptions[index])
	}
	for index := range request.Events {
		attachRequest(&request.Events[index].Meta, requestID, request.Tags)
		p.LogEvent(request.Events[index])
	}
	for index := range request.Middlewares {
		attachRequest(&request.Middlewares[index].Meta, requestID, request.Tags)
		p.LogMiddleware(request.Middlewares[index])
	}
	request.Queries = nil
	request.Emails = nil
	request.Cache = nil
	request.Jobs = nil
	request.Logs = nil
	request.HTTPCalls = nil
	request.Schedules = nil
	request.Exceptions = nil
	request.Events = nil
	request.Middlewares = nil
	request.RequestID = requestID
	p.record(KindRequest, request.Meta, request)
}

// LogQuery records a database query and captures a callsite when configured.
func (p *Profiler) LogQuery(query Query) {
	if p == nil {
		return
	}
	p.prepareQuery(&query)
	p.record(KindQuery, query.Meta, query)
}

// LogEmail records an outgoing email and captures a callsite when configured.
func (p *Profiler) LogEmail(email Email) {
	if p == nil {
		return
	}
	p.prepareCallsite(KindEmail, &email.Callsite)
	p.record(KindEmail, email.Meta, email)
}

// LogCache records a cache operation and captures a callsite when configured.
func (p *Profiler) LogCache(cache Cache) {
	if p == nil {
		return
	}
	p.prepareCallsite(KindCache, &cache.Callsite)
	p.record(KindCache, cache.Meta, cache)
}

// LogJob records a background job and captures a callsite when configured.
func (p *Profiler) LogJob(job Job) {
	if p == nil {
		return
	}
	p.prepareCallsite(KindJob, &job.Callsite)
	p.record(KindJob, job.Meta, job)
}

// LogLog records a structured application log.
func (p *Profiler) LogLog(log Log) { p.record(KindLog, log.Meta, log) }

// LogHTTPCall records an outbound HTTP call and captures a callsite when
// configured.
func (p *Profiler) LogHTTPCall(call HTTPCall) {
	if p == nil {
		return
	}
	p.prepareCallsite(KindHTTPCall, &call.Callsite)
	p.record(KindHTTPCall, call.Meta, call)
}

// LogSchedule records a scheduled task and captures a callsite when configured.
func (p *Profiler) LogSchedule(schedule Schedule) {
	if p == nil {
		return
	}
	p.prepareCallsite(KindSchedule, &schedule.Callsite)
	p.record(KindSchedule, schedule.Meta, schedule)
}

// LogCallable records an explicitly invoked custom command and captures a
// callsite when configured.
func (p *Profiler) LogCallable(callable Callable) {
	if p == nil {
		return
	}
	p.prepareCallsite(KindCallable, &callable.Callsite)
	p.record(KindCallable, callable.Meta, callable)
}

// LogTask records a measured application task and captures a callsite when configured.
func (p *Profiler) LogTask(task Task) {
	if p == nil {
		return
	}
	p.prepareCallsite(KindTask, &task.Callsite)
	p.record(KindTask, task.Meta, task)
}

// LogException records an application error or recovered panic.
func (p *Profiler) LogException(exception Exception) {
	p.record(KindException, exception.Meta, exception)
}

// LogEvent records a custom application event.
func (p *Profiler) LogEvent(event Event) { p.record(KindEvent, event.Meta, event) }

// LogMiddleware records a standalone or explicitly correlated middleware
// invocation.
func (p *Profiler) LogMiddleware(middleware Middleware) {
	p.record(KindMiddleware, middleware.Meta, middleware)
}

// LogRequest records a completed request with the default profiler.
func LogRequest(request Request) { withDefault(func(p *Profiler) { p.LogRequest(request) }) }

// LogQuery records a database query with the default profiler.
func LogQuery(query Query) { withDefault(func(p *Profiler) { p.LogQuery(query) }) }

// LogEmail records an outgoing email with the default profiler.
func LogEmail(email Email) { withDefault(func(p *Profiler) { p.LogEmail(email) }) }

// LogCache records a cache operation with the default profiler.
func LogCache(cache Cache) { withDefault(func(p *Profiler) { p.LogCache(cache) }) }

// LogJob records a background job with the default profiler.
func LogJob(job Job) { withDefault(func(p *Profiler) { p.LogJob(job) }) }

// LogLog records a structured log with the default profiler.
func LogLog(log Log) { withDefault(func(p *Profiler) { p.LogLog(log) }) }

// LogHTTPCall records an outbound HTTP call with the default profiler.
func LogHTTPCall(call HTTPCall) { withDefault(func(p *Profiler) { p.LogHTTPCall(call) }) }

// LogSchedule records a scheduled task with the default profiler.
func LogSchedule(schedule Schedule) {
	withDefault(func(p *Profiler) { p.LogSchedule(schedule) })
}

// LogCallable records an explicitly invoked custom command with the default profiler.
func LogCallable(callable Callable) {
	withDefault(func(p *Profiler) { p.LogCallable(callable) })
}

// LogTask records a measured application task with the default profiler.
func LogTask(task Task) { withDefault(func(p *Profiler) { p.LogTask(task) }) }

// LogException records an application exception with the default profiler.
func LogException(exception Exception) {
	withDefault(func(p *Profiler) { p.LogException(exception) })
}

// LogEvent records a custom event with the default profiler.
func LogEvent(event Event) { withDefault(func(p *Profiler) { p.LogEvent(event) }) }

// LogMiddleware records middleware using the default profiler.
func LogMiddleware(middleware Middleware) {
	withDefault(func(p *Profiler) { p.LogMiddleware(middleware) })
}

func (p *Profiler) record(kind Kind, meta Meta, value any) {
	if p == nil || p.store == nil {
		return
	}
	if _, disabled := p.config.disabledKinds[kind]; disabled {
		return
	}
	if meta.ID == "" {
		meta.ID = newID()
	}
	if meta.StartedAt.IsZero() {
		meta.StartedAt = time.Now().UTC()
	}
	data, err := marshalRedacted(value)
	if err != nil {
		return
	}
	p.store.put(Entry{ID: meta.ID, Kind: kind, RequestID: meta.RequestID, ParentID: meta.ParentID, OriginRequestID: meta.OriginRequestID, Process: meta.Process, Instance: meta.Instance, StartedAt: meta.StartedAt, RecordedAt: time.Now().UTC(), DurationNS: int64(meta.Duration), Tags: meta.Tags, Data: data})
}

func withDefault(log func(*Profiler)) {
	if profiler := defaultProfiler.Load(); profiler != nil {
		log(profiler)
	}
}

func attachRequest(meta *Meta, requestID string, tags map[string]string) {
	if meta.RequestID == "" {
		meta.RequestID = requestID
	}
	meta.Tags = mergeTags(tags, meta.Tags)
}

func newID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err == nil {
		return hex.EncodeToString(value[:])
	}
	return time.Now().UTC().Format("20060102150405.000000000")
}
