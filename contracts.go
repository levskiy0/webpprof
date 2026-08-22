package webpprof

import (
	"encoding/json"
	"time"
)

type Kind string

const (
	KindRequest    Kind = "request"
	KindQuery      Kind = "query"
	KindEmail      Kind = "email"
	KindCache      Kind = "cache"
	KindJob        Kind = "job"
	KindLog        Kind = "log"
	KindHTTPCall   Kind = "http_call"
	KindSchedule   Kind = "schedule"
	KindException  Kind = "exception"
	KindEvent      Kind = "event"
	KindMiddleware Kind = "middleware"
)

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

type HTTPMessage struct {
	Headers     map[string][]string `json:"headers,omitempty"`
	ContentType string              `json:"content_type,omitempty"`
	Body        string              `json:"body,omitempty"`
	Size        int64               `json:"size,omitempty"`
	Truncated   bool                `json:"truncated,omitempty"`
}

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

type RequestResult struct {
	Status       int
	ResponseSize int64
	Response     HTTPMessage
	Error        string
}

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

type Address struct {
	Name  string `json:"name,omitempty"`
	Email string `json:"email"`
}

type Email struct {
	Meta
	Transport string    `json:"transport,omitempty"`
	From      Address   `json:"from"`
	To        []Address `json:"to,omitempty"`
	CC        []Address `json:"cc,omitempty"`
	BCC       []Address `json:"bcc,omitempty"`
	Subject   string    `json:"subject,omitempty"`
	Text      string    `json:"text,omitempty"`
	HTML      string    `json:"html,omitempty"`
	Status    string    `json:"status,omitempty"`
	Error     string    `json:"error,omitempty"`
}

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
	Error     string        `json:"error,omitempty"`
}

type Argument struct {
	Name      string `json:"name,omitempty"`
	Type      string `json:"type,omitempty"`
	Value     string `json:"value,omitempty"`
	Size      int64  `json:"size,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

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
	Error       string        `json:"error,omitempty"`
}

type Log struct {
	Meta
	Level   string         `json:"level,omitempty"`
	Message string         `json:"message"`
	Fields  map[string]any `json:"fields,omitempty"`
	Stack   string         `json:"stack,omitempty"`
}

type HTTPCall struct {
	Meta
	Method       string      `json:"method"`
	URL          string      `json:"url"`
	Status       int         `json:"status,omitempty"`
	Request      HTTPMessage `json:"request,omitempty"`
	Response     HTTPMessage `json:"response,omitempty"`
	ResponseSize int64       `json:"response_size,omitempty"`
	Error        string      `json:"error,omitempty"`
}

type Schedule struct {
	Meta
	Name      string    `json:"name"`
	State     string    `json:"state,omitempty"`
	PlannedAt time.Time `json:"planned_at,omitempty"`
	Payload   any       `json:"payload,omitempty"`
	Error     string    `json:"error,omitempty"`
	Panic     string    `json:"panic,omitempty"`
}

type Exception struct {
	Meta
	Type    string `json:"type,omitempty"`
	Message string `json:"message"`
	Stack   string `json:"stack,omitempty"`
}

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
