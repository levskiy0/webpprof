package webpprof

import (
	"encoding/json"
	"time"
)

// Kind identifies the schema stored in an Entry.
type Kind string

const (
	// KindRequest identifies an inbound HTTP request.
	KindRequest Kind = "request"
	// KindQuery identifies a database query.
	KindQuery Kind = "query"
	// KindEmail identifies an outgoing email.
	KindEmail Kind = "email"
	// KindCache identifies a cache operation.
	KindCache Kind = "cache"
	// KindJob identifies a queued job.
	KindJob Kind = "job"
	// KindLog identifies a structured application log.
	KindLog Kind = "log"
	// KindHTTPCall identifies an outbound HTTP request.
	KindHTTPCall Kind = "http_call"
	// KindSchedule identifies a scheduled task execution.
	KindSchedule Kind = "schedule"
	// KindException identifies a captured error or panic.
	KindException Kind = "exception"
	// KindEvent identifies a custom application event.
	KindEvent Kind = "event"
	// KindMiddleware identifies one inbound middleware invocation.
	KindMiddleware Kind = "middleware"
)

// Meta contains correlation, timing, process, and tag data shared by all
// profiler entities.
type Meta struct {
	ID              string            `json:"id,omitempty"`
	RequestID       string            `json:"request_id,omitempty"`
	ParentID        string            `json:"parent_id,omitempty"`
	OriginRequestID string            `json:"origin_request_id,omitempty"`
	Process         string            `json:"process,omitempty"`
	Instance        string            `json:"instance,omitempty"`
	StartedAt       time.Time         `json:"started_at,omitempty"`
	Duration        time.Duration     `json:"duration_ns,omitempty"`
	Tags            map[string]string `json:"tags,omitempty"`
}

// HTTPMessage is a size-aware snapshot of HTTP headers and an optional body.
// Truncated reports whether Body was shortened by the configured body limit.
type HTTPMessage struct {
	Headers     map[string][]string `json:"headers,omitempty"`
	ContentType string              `json:"content_type,omitempty"`
	Body        string              `json:"body,omitempty"`
	Size        int64               `json:"size,omitempty"`
	Truncated   bool                `json:"truncated,omitempty"`
}

// Request describes an inbound HTTP exchange and may temporarily contain
// related entities before LogRequest stores them as individually linked entries.
type Request struct {
	Meta
	Method       string       `json:"method"`
	Path         string       `json:"path"`
	Route        string       `json:"route,omitempty"`
	Query        string       `json:"query,omitempty"`
	Scheme       string       `json:"scheme,omitempty"`
	Protocol     string       `json:"protocol,omitempty"`
	Host         string       `json:"host,omitempty"`
	RemoteIP     string       `json:"remote_ip,omitempty"`
	Status       int          `json:"status"`
	RequestSize  int64        `json:"request_size,omitempty"`
	ResponseSize int64        `json:"response_size,omitempty"`
	Request      HTTPMessage  `json:"request,omitempty"`
	Response     HTTPMessage  `json:"response,omitempty"`
	Error        string       `json:"error,omitempty"`
	Queries      []Query      `json:"queries,omitempty"`
	Emails       []Email      `json:"emails,omitempty"`
	Cache        []Cache      `json:"cache,omitempty"`
	Jobs         []Job        `json:"jobs,omitempty"`
	Logs         []Log        `json:"logs,omitempty"`
	HTTPCalls    []HTTPCall   `json:"http_calls,omitempty"`
	Schedules    []Schedule   `json:"schedules,omitempty"`
	Exceptions   []Exception  `json:"exceptions,omitempty"`
	Events       []Event      `json:"events,omitempty"`
	Middlewares  []Middleware `json:"middlewares,omitempty"`
}

// RequestResult supplies response metadata when a RequestCapture is finished.
type RequestResult struct {
	Status       int
	ResponseSize int64
	Response     HTTPMessage
	Error        string
}

// Query describes a database operation, including its SQL, timing, result, and
// optional source callsite or EXPLAIN plan.
type Query struct {
	Meta
	Connection   string        `json:"connection,omitempty"`
	Driver       string        `json:"driver,omitempty"`
	Database     string        `json:"database,omitempty"`
	Operation    string        `json:"operation,omitempty"`
	SQL          string        `json:"sql"`
	RowsAffected *int64        `json:"rows_affected,omitempty"`
	Callsite     []SourceFrame `json:"callsite,omitempty"`
	Plan         *QueryPlan    `json:"plan,omitempty"`
	Error        string        `json:"error,omitempty"`
}

// SourceFrame identifies one Go frame that led to a profiled operation. URL is
// optional and can point to an editor deep link such as vscode://file/....
type SourceFrame struct {
	Function string `json:"function,omitempty"`
	File     string `json:"file"`
	Line     int    `json:"line"`
	URL      string `json:"url,omitempty"`
}

// QueryPlan contains a non-executing SQL EXPLAIN result. Duration measures the
// plan lookup itself and is intentionally separate from Query.Duration.
type QueryPlan struct {
	Command  string        `json:"command,omitempty"`
	Format   string        `json:"format,omitempty"`
	Text     string        `json:"text,omitempty"`
	Duration time.Duration `json:"duration_ns,omitempty"`
	Error    string        `json:"error,omitempty"`
}

// Address identifies one email sender or recipient.
type Address struct {
	Name  string `json:"name,omitempty"`
	Email string `json:"email"`
}

// Email describes an outgoing email delivery attempt.
type Email struct {
	Meta
	Transport string        `json:"transport,omitempty"`
	From      Address       `json:"from"`
	To        []Address     `json:"to,omitempty"`
	CC        []Address     `json:"cc,omitempty"`
	BCC       []Address     `json:"bcc,omitempty"`
	Subject   string        `json:"subject,omitempty"`
	Text      string        `json:"text,omitempty"`
	HTML      string        `json:"html,omitempty"`
	Status    string        `json:"status,omitempty"`
	Callsite  []SourceFrame `json:"callsite,omitempty"`
	Error     string        `json:"error,omitempty"`
}

// Cache describes a cache read, write, invalidation, or lock operation.
type Cache struct {
	Meta
	Store     string        `json:"store,omitempty"`
	Operation string        `json:"operation,omitempty"`
	Key       string        `json:"key,omitempty"`
	Hit       bool          `json:"hit"`
	TTL       time.Duration `json:"ttl_ns,omitempty"`
	Size      int64         `json:"size,omitempty"`
	Value     string        `json:"value,omitempty"`
	Truncated bool          `json:"truncated,omitempty"`
	Callsite  []SourceFrame `json:"callsite,omitempty"`
	Error     string        `json:"error,omitempty"`
}

// Argument is a redacted, size-aware representation of a job argument.
type Argument struct {
	Name      string `json:"name,omitempty"`
	Type      string `json:"type,omitempty"`
	Value     string `json:"value,omitempty"`
	Size      int64  `json:"size,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

// Job describes the enqueueing or execution state of a background job.
type Job struct {
	Meta
	Name        string        `json:"name"`
	Queue       string        `json:"queue,omitempty"`
	Connection  string        `json:"connection,omitempty"`
	State       string        `json:"state,omitempty"`
	Attempt     int           `json:"attempt,omitempty"`
	MaxAttempts int           `json:"max_attempts,omitempty"`
	AvailableAt time.Time     `json:"available_at,omitempty"`
	Wait        time.Duration `json:"wait_ns,omitempty"`
	Arguments   []Argument    `json:"arguments,omitempty"`
	Callsite    []SourceFrame `json:"callsite,omitempty"`
	Error       string        `json:"error,omitempty"`
}

// Log describes one structured application log record.
type Log struct {
	Meta
	Level   string         `json:"level,omitempty"`
	Message string         `json:"message"`
	Fields  map[string]any `json:"fields,omitempty"`
	Stack   string         `json:"stack,omitempty"`
}

// HTTPCall describes an outbound HTTP exchange.
type HTTPCall struct {
	Meta
	Method       string        `json:"method"`
	URL          string        `json:"url"`
	Status       int           `json:"status,omitempty"`
	Request      HTTPMessage   `json:"request,omitempty"`
	Response     HTTPMessage   `json:"response,omitempty"`
	ResponseSize int64         `json:"response_size,omitempty"`
	Callsite     []SourceFrame `json:"callsite,omitempty"`
	Error        string        `json:"error,omitempty"`
}

// Schedule describes one scheduled task invocation and its outcome.
type Schedule struct {
	Meta
	Name      string        `json:"name"`
	State     string        `json:"state,omitempty"`
	PlannedAt time.Time     `json:"planned_at,omitempty"`
	Payload   any           `json:"payload,omitempty"`
	Callsite  []SourceFrame `json:"callsite,omitempty"`
	Error     string        `json:"error,omitempty"`
	Panic     string        `json:"panic,omitempty"`
}

// Exception describes a captured error or recovered panic with an optional stack.
type Exception struct {
	Meta
	Type    string `json:"type,omitempty"`
	Message string `json:"message"`
	Stack   string `json:"stack,omitempty"`
}

// Event describes a custom domain or application event.
type Event struct {
	Meta
	Kind    string         `json:"kind"`
	Name    string         `json:"name"`
	Status  string         `json:"status,omitempty"`
	Summary string         `json:"summary,omitempty"`
	Fields  map[string]any `json:"fields,omitempty"`
	Error   string         `json:"error,omitempty"`
}

// Middleware describes one named HTTP middleware invocation. Duration is
// inclusive: it contains the time spent in the middleware and downstream
// handlers.
type Middleware struct {
	Meta
	Name  string `json:"name"`
	State string `json:"state,omitempty"`
	Error string `json:"error,omitempty"`
}

// Entry is the normalized envelope returned by the profiler API and storage
// implementations. Data contains the JSON form associated with Kind.
type Entry struct {
	Cursor          uint64            `json:"cursor"`
	ID              string            `json:"id"`
	Kind            Kind              `json:"kind"`
	RequestID       string            `json:"request_id,omitempty"`
	ParentID        string            `json:"parent_id,omitempty"`
	OriginRequestID string            `json:"origin_request_id,omitempty"`
	Process         string            `json:"process,omitempty"`
	Instance        string            `json:"instance,omitempty"`
	StartedAt       time.Time         `json:"started_at"`
	RecordedAt      time.Time         `json:"recorded_at"`
	DurationNS      int64             `json:"duration_ns,omitempty"`
	Tags            map[string]string `json:"tags,omitempty"`
	Data            json.RawMessage   `json:"data"`
}

// Stats reports current profiler capacity, retention, and storage state.
type Stats struct {
	Events        int     `json:"events"`
	Bytes         int64   `json:"bytes"`
	DroppedEvents uint64  `json:"dropped_events"`
	EvictedEvents uint64  `json:"evicted_events"`
	Subscribers   int     `json:"subscribers"`
	Cursor        uint64  `json:"cursor"`
	MaxEvents     int     `json:"max_events"`
	MaxBytes      int64   `json:"max_bytes"`
	RetentionNS   int64   `json:"retention_ns"`
	Storage       string  `json:"storage"`
	StorageError  string  `json:"storage_error,omitempty"`
	BodyLimit     int64   `json:"body_limit"`
	SampleRate    float64 `json:"request_sample_rate"`
	DisabledKinds []Kind  `json:"disabled_kinds,omitempty"`
}

// RuntimeStats is a point-in-time snapshot of selected Go runtime metrics.
type RuntimeStats struct {
	RecordedAt       time.Time `json:"recorded_at"`
	UptimeNS         int64     `json:"uptime_ns"`
	CPUSeconds       float64   `json:"cpu_seconds"`
	CPUIdleSeconds   float64   `json:"cpu_idle_seconds"`
	MemoryBytes      uint64    `json:"memory_bytes"`
	HeapObjectsBytes uint64    `json:"heap_objects_bytes"`
	HeapLiveBytes    uint64    `json:"heap_live_bytes"`
	Goroutines       uint64    `json:"goroutines"`
	GCCycles         uint64    `json:"gc_cycles"`
	GOMAXPROCS       int       `json:"gomaxprocs"`
}
