package client

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	webpprof "github.com/levskiy0/webpprof"
)

// ListEventsOptions controls a bounded event query.
type ListEventsOptions struct {
	// Kind matches one exact event kind.
	Kind webpprof.Kind
	// RequestID matches the request itself and directly or asynchronously
	// correlated entries.
	RequestID string
	// ScopeID matches one execution root and every entry connected to it through
	// the ParentID hierarchy. It is useful for standalone execution roots.
	ScopeID string
	// Tags contains key or key=value selectors. Every selector must match.
	Tags []string
	// Query performs a case-insensitive search over entry metadata, tags, and
	// the bounded, redacted event data.
	Query string
	// Method matches request entries by HTTP method, case-insensitively.
	Method string
	// PathContains matches a case-insensitive request-path substring.
	PathContains string
	// Status matches request entries by exact HTTP status.
	Status int
	// MinDuration defines an inclusive lower duration bound when positive.
	MinDuration time.Duration
	// MaxDuration defines an inclusive upper duration bound when positive.
	MaxDuration time.Duration
	// After excludes this cursor and all older entries when positive.
	After uint64
	// Before excludes this cursor and all newer entries when positive.
	Before uint64
	// Limit controls the page size. The default is 200 and the maximum is 1,000.
	Limit int
}

// EventPage is the response returned by webpprof's event endpoint.
type EventPage struct {
	Events []webpprof.Entry `json:"events"`
	// Scanned reports how many cursor-eligible entries the server inspected to
	// fill this filtered page.
	Scanned int            `json:"scanned"`
	HasMore bool           `json:"has_more"`
	Stats   webpprof.Stats `json:"stats"`
}

// RequestSummary is a compact, stable representation of a request entry.
type RequestSummary struct {
	ID         string            `json:"id"`
	Cursor     uint64            `json:"cursor"`
	StartedAt  time.Time         `json:"started_at"`
	Method     string            `json:"method"`
	Path       string            `json:"path"`
	Route      string            `json:"route,omitempty"`
	Status     int               `json:"status"`
	DurationNS int64             `json:"duration_ns"`
	Error      string            `json:"error,omitempty"`
	Tags       map[string]string `json:"tags,omitempty"`
}

// RequestReport contains the complete bounded context for one request.
type RequestReport struct {
	Request  RequestSummary           `json:"request"`
	Entry    webpprof.Entry           `json:"entry"`
	Events   []webpprof.Entry         `json:"events"`
	Counts   map[webpprof.Kind]int    `json:"counts"`
	HasMore  bool                     `json:"has_more"`
	Analysis webpprof.RequestAnalysis `json:"analysis"`
}

// EventReport contains one execution root and its bounded ParentID hierarchy.
type EventReport struct {
	Entry    webpprof.Entry        `json:"entry"`
	Events   []webpprof.Entry      `json:"events"`
	Counts   map[webpprof.Kind]int `json:"counts"`
	HasMore  bool                  `json:"has_more"`
	Findings []webpprof.Finding    `json:"findings,omitempty"`
}

// WaitForRequestOptions filters newly captured request events.
type WaitForRequestOptions struct {
	After        uint64
	Method       string
	PathContains string
	Status       int
	MinDuration  time.Duration
	MaxDuration  time.Duration
	PollInterval time.Duration
}

func (options WaitForRequestOptions) matches(request RequestSummary) bool {
	if options.Method != "" && !strings.EqualFold(options.Method, request.Method) {
		return false
	}
	if options.PathContains != "" && !strings.Contains(request.Path, options.PathContains) {
		return false
	}
	if options.Status != 0 && options.Status != request.Status {
		return false
	}
	duration := time.Duration(request.DurationNS)
	return duration >= options.MinDuration && (options.MaxDuration <= 0 || duration <= options.MaxDuration)
}

// DecodeRequest validates and decodes a request entry.
func DecodeRequest(entry webpprof.Entry) (RequestSummary, error) {
	if entry.Kind != webpprof.KindRequest {
		return RequestSummary{}, errors.New("entry is not a request")
	}
	var request struct {
		Method string `json:"method"`
		Path   string `json:"path"`
		Route  string `json:"route,omitempty"`
		Status int    `json:"status"`
		Error  string `json:"error,omitempty"`
	}
	if err := json.Unmarshal(entry.Data, &request); err != nil {
		return RequestSummary{}, err
	}
	return RequestSummary{
		ID:         entry.ID,
		Cursor:     entry.Cursor,
		StartedAt:  entry.StartedAt,
		Method:     request.Method,
		Path:       request.Path,
		Route:      request.Route,
		Status:     request.Status,
		DurationNS: entry.DurationNS,
		Error:      request.Error,
		Tags:       entry.Tags,
	}, nil
}
