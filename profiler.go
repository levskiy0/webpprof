package webpprof

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

var defaultProfiler atomic.Pointer[Profiler]
var defaultProfilerMu sync.Mutex

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
}

func New(router Router, options ...Option) *Profiler {
	defaultProfilerMu.Lock()
	defer defaultProfilerMu.Unlock()
	if profiler := defaultProfiler.Load(); profiler != nil {
		return profiler
	}
	if router == nil {
		panic("webpprof: nil router")
	}
	profiler := newProfiler(options...)
	profiler.register(router)
	defaultProfiler.Store(profiler)
	return profiler
}

func newProfiler(options ...Option) *Profiler {
	c := defaultConfig()
	for _, option := range options {
		if option != nil {
			option(&c)
		}
	}
	return &Profiler{config: c, store: newEntryStore(c), startedAt: time.Now().UTC(), sessionToken: newID(), loginFailures: make(map[string]loginFailure)}
}

func NewIf(enabled bool, router Router, options ...Option) *Profiler {
	if !enabled {
		return nil
	}
	return New(router, options...)
}

func Default() *Profiler {
	return defaultProfiler.Load()
}

func Enabled() bool {
	return Default() != nil
}

func (p *Profiler) Enabled() bool {
	return p != nil
}

func (p *Profiler) BasePath() string {
	if p == nil {
		return ""
	}
	return p.config.basePath
}

func (p *Profiler) BodyLimit() int64 {
	if p == nil {
		return 0
	}
	return p.config.bodyLimit
}

func ShouldCaptureRequest(request *http.Request) bool {
	profiler := Default()
	return profiler != nil && profiler.ShouldCaptureRequest(request)
}

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
	if p.config.requestSample >= 1 {
		return true
	}
	if p.config.requestSample <= 0 {
		return false
	}
	var sample [8]byte
	if _, err := rand.Read(sample[:]); err != nil {
		return false
	}
	return float64(binary.BigEndian.Uint64(sample[:]))/float64(^uint64(0)) < p.config.requestSample
}

func NewID() string {
	return newID()
}

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

func (p *Profiler) LogRequest(request Request) {
	if p == nil {
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

func (p *Profiler) LogQuery(query Query) {
	if p == nil {
		return
	}
	p.prepareQuery(&query)
	p.record(KindQuery, query.Meta, query)
}
func (p *Profiler) LogEmail(email Email)          { p.record(KindEmail, email.Meta, email) }
func (p *Profiler) LogCache(cache Cache)          { p.record(KindCache, cache.Meta, cache) }
func (p *Profiler) LogJob(job Job)                { p.record(KindJob, job.Meta, job) }
func (p *Profiler) LogLog(log Log)                { p.record(KindLog, log.Meta, log) }
func (p *Profiler) LogHTTPCall(call HTTPCall)     { p.record(KindHTTPCall, call.Meta, call) }
func (p *Profiler) LogSchedule(schedule Schedule) { p.record(KindSchedule, schedule.Meta, schedule) }
func (p *Profiler) LogException(exception Exception) {
	p.record(KindException, exception.Meta, exception)
}
func (p *Profiler) LogEvent(event Event) { p.record(KindEvent, event.Meta, event) }

// LogMiddleware records a standalone or explicitly correlated middleware
// invocation.
func (p *Profiler) LogMiddleware(middleware Middleware) {
	p.record(KindMiddleware, middleware.Meta, middleware)
}

func LogRequest(request Request)       { withDefault(func(p *Profiler) { p.LogRequest(request) }) }
func LogQuery(query Query)             { withDefault(func(p *Profiler) { p.LogQuery(query) }) }
func LogEmail(email Email)             { withDefault(func(p *Profiler) { p.LogEmail(email) }) }
func LogCache(cache Cache)             { withDefault(func(p *Profiler) { p.LogCache(cache) }) }
func LogJob(job Job)                   { withDefault(func(p *Profiler) { p.LogJob(job) }) }
func LogLog(log Log)                   { withDefault(func(p *Profiler) { p.LogLog(log) }) }
func LogHTTPCall(call HTTPCall)        { withDefault(func(p *Profiler) { p.LogHTTPCall(call) }) }
func LogSchedule(schedule Schedule)    { withDefault(func(p *Profiler) { p.LogSchedule(schedule) }) }
func LogException(exception Exception) { withDefault(func(p *Profiler) { p.LogException(exception) }) }
func LogEvent(event Event)             { withDefault(func(p *Profiler) { p.LogEvent(event) }) }

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
