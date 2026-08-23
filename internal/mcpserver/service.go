package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	webpprof "github.com/levskiy0/webpprof"
	"github.com/levskiy0/webpprof/client"
)

const (
	defaultListLimit  = 20
	maxListLimit      = 200
	defaultEventLimit = 100
	maxEventLimit     = 1_000
	defaultWait       = 30 * time.Second
	maxWait           = 2 * time.Minute
)

// ProfilerClient is the read-only surface needed by the MCP service.
type ProfilerClient interface {
	Stats(context.Context) (webpprof.Stats, error)
	ListEvents(context.Context, client.ListEventsOptions) (client.EventPage, error)
	Event(context.Context, string) (webpprof.Entry, error)
	RequestAnalysis(context.Context, string) (webpprof.RequestAnalysis, error)
	InspectRequest(context.Context, string, int) (client.RequestReport, error)
	WaitForRequest(context.Context, client.WaitForRequestOptions) (client.RequestSummary, error)
}

// Service translates raw profiler records into compact agent-facing results.
type Service struct {
	client ProfilerClient
}

// New constructs the agent-facing service over a read-only profiler client.
func New(profilerClient ProfilerClient) (*Service, error) {
	if profilerClient == nil {
		return nil, errors.New("webpprof MCP: nil profiler client")
	}
	return &Service{client: profilerClient}, nil
}

// StatusInput is intentionally empty because status has no filters.
type StatusInput struct{}

// StatusOutput confirms connectivity and exposes the profiler's current limits.
type StatusOutput struct {
	Connected bool           `json:"connected"`
	Stats     webpprof.Stats `json:"stats"`
}

// Status reads the profiler's capture and retention status.
func (s *Service) Status(ctx context.Context, _ StatusInput) (StatusOutput, error) {
	stats, err := s.client.Stats(ctx)
	if err != nil {
		return StatusOutput{}, fmt.Errorf("get profiler status: %w", err)
	}
	return StatusOutput{Connected: true, Stats: stats}, nil
}

// ListRequestsInput filters a bounded scan of recent request entries.
type ListRequestsInput struct {
	Limit         int      `json:"limit,omitempty" jsonschema:"maximum matching requests to return, capped at 200"`
	Before        uint64   `json:"before,omitempty" jsonschema:"return requests older than this cursor"`
	Method        string   `json:"method,omitempty" jsonschema:"exact HTTP method filter"`
	PathContains  string   `json:"path_contains,omitempty" jsonschema:"case-insensitive path substring"`
	Status        int      `json:"status,omitempty" jsonschema:"exact HTTP status filter"`
	MinDurationMS float64  `json:"min_duration_ms,omitempty" jsonschema:"minimum request duration in milliseconds"`
	MaxDurationMS float64  `json:"max_duration_ms,omitempty" jsonschema:"maximum request duration in milliseconds"`
	Tags          []string `json:"tags,omitempty" jsonschema:"tag filters in key or key=value form"`
}

// ListRequestsOutput contains matching request summaries and scan metadata.
type ListRequestsOutput struct {
	Requests []client.RequestSummary `json:"requests"`
	Scanned  int                     `json:"scanned"`
	HasMore  bool                    `json:"has_more"`
	Cursor   uint64                  `json:"cursor"`
}

// ListRequests returns matching requests newest first.
func (s *Service) ListRequests(ctx context.Context, input ListRequestsInput) (ListRequestsOutput, error) {
	limit := boundedLimit(input.Limit, defaultListLimit, maxListLimit)
	page, err := s.client.ListEvents(ctx, client.ListEventsOptions{
		Kind:         webpprof.KindRequest,
		Tags:         input.Tags,
		Before:       input.Before,
		Method:       input.Method,
		PathContains: input.PathContains,
		Status:       input.Status,
		MinDuration:  time.Duration(input.MinDurationMS * float64(time.Millisecond)),
		MaxDuration:  time.Duration(input.MaxDurationMS * float64(time.Millisecond)),
		Limit:        limit,
	})
	if err != nil {
		return ListRequestsOutput{}, fmt.Errorf("list requests: %w", err)
	}

	scanned := page.Scanned
	if scanned == 0 && len(page.Events) > 0 {
		scanned = len(page.Events)
	}
	output := ListRequestsOutput{Requests: make([]client.RequestSummary, 0, limit), Scanned: scanned, HasMore: page.HasMore, Cursor: page.Stats.Cursor}
	pathContains := strings.ToLower(strings.TrimSpace(input.PathContains))
	for index := len(page.Events) - 1; index >= 0 && len(output.Requests) < limit; index-- {
		request, err := client.DecodeRequest(page.Events[index])
		if err != nil {
			continue
		}
		if input.Method != "" && !strings.EqualFold(input.Method, request.Method) {
			continue
		}
		if pathContains != "" && !strings.Contains(strings.ToLower(request.Path), pathContains) {
			continue
		}
		if input.Status != 0 && input.Status != request.Status {
			continue
		}
		durationMS := float64(request.DurationNS) / float64(time.Millisecond)
		if durationMS < input.MinDurationMS || input.MaxDurationMS > 0 && durationMS > input.MaxDurationMS {
			continue
		}
		output.Requests = append(output.Requests, request)
	}
	return output, nil
}

// InspectRequestInput selects one request and controls timeline detail.
type InspectRequestInput struct {
	RequestID       string `json:"request_id" jsonschema:"captured request ID"`
	MaxEvents       int    `json:"max_events,omitempty" jsonschema:"maximum correlated events to return, capped at 1000"`
	IncludePayloads bool   `json:"include_payloads,omitempty" jsonschema:"include bounded captured bodies values arguments and stacks"`
}

// InspectRequestOutput contains findings and a compact correlated timeline.
type InspectRequestOutput struct {
	Request  client.RequestSummary `json:"request"`
	Findings []webpprof.Finding    `json:"findings"`
	Counts   map[webpprof.Kind]int `json:"counts"`
	Events   []EventSummary        `json:"events"`
	HasMore  bool                  `json:"has_more"`
}

// InspectRequest builds an agent-safe diagnostic view of one request.
func (s *Service) InspectRequest(ctx context.Context, input InspectRequestInput) (InspectRequestOutput, error) {
	requestID := strings.TrimSpace(input.RequestID)
	if requestID == "" {
		return InspectRequestOutput{}, errors.New("inspect request: request_id is required")
	}
	limit := boundedLimit(input.MaxEvents, defaultEventLimit, maxEventLimit)
	report, err := s.client.InspectRequest(ctx, requestID, limit)
	if err != nil {
		return InspectRequestOutput{}, fmt.Errorf("inspect request: %w", err)
	}
	events := make([]EventSummary, 0, len(report.Events))
	for _, entry := range report.Events {
		events = append(events, summarizeEntry(entry, input.IncludePayloads))
	}
	return InspectRequestOutput{
		Request:  report.Request,
		Findings: report.Analysis.Findings,
		Counts:   report.Counts,
		Events:   events,
		HasMore:  report.HasMore,
	}, nil
}

// SearchEventsInput filters a bounded scan of captured events.
type SearchEventsInput struct {
	Query         string   `json:"query,omitempty" jsonschema:"case-insensitive text found in event JSON"`
	Kind          string   `json:"kind,omitempty" jsonschema:"event kind such as query cache log http_call or exception"`
	RequestID     string   `json:"request_id,omitempty" jsonschema:"restrict results to one request timeline"`
	Tags          []string `json:"tags,omitempty" jsonschema:"tag filters in key or key=value form"`
	After         uint64   `json:"after,omitempty" jsonschema:"return events newer than this cursor"`
	Before        uint64   `json:"before,omitempty" jsonschema:"return events older than this cursor"`
	Limit         int      `json:"limit,omitempty" jsonschema:"maximum matching events to return, capped at 200"`
	MinDurationMS float64  `json:"min_duration_ms,omitempty" jsonschema:"minimum event duration in milliseconds"`
	MaxDurationMS float64  `json:"max_duration_ms,omitempty" jsonschema:"maximum event duration in milliseconds"`
}

// SearchEventsOutput contains matching compact events and scan metadata.
type SearchEventsOutput struct {
	Events  []EventSummary `json:"events"`
	Scanned int            `json:"scanned"`
	HasMore bool           `json:"has_more"`
	Cursor  uint64         `json:"cursor"`
}

// SearchEvents finds recent events without returning captured payload bodies.
func (s *Service) SearchEvents(ctx context.Context, input SearchEventsInput) (SearchEventsOutput, error) {
	limit := boundedLimit(input.Limit, defaultListLimit, maxListLimit)
	page, err := s.client.ListEvents(ctx, client.ListEventsOptions{
		Kind:        webpprof.Kind(strings.TrimSpace(input.Kind)),
		RequestID:   strings.TrimSpace(input.RequestID),
		Tags:        input.Tags,
		Query:       input.Query,
		MinDuration: time.Duration(input.MinDurationMS * float64(time.Millisecond)),
		MaxDuration: time.Duration(input.MaxDurationMS * float64(time.Millisecond)),
		After:       input.After,
		Before:      input.Before,
		Limit:       limit,
	})
	if err != nil {
		return SearchEventsOutput{}, fmt.Errorf("search events: %w", err)
	}
	scanned := page.Scanned
	if scanned == 0 && len(page.Events) > 0 {
		scanned = len(page.Events)
	}
	query := strings.ToLower(strings.TrimSpace(input.Query))
	output := SearchEventsOutput{Events: make([]EventSummary, 0, limit), Scanned: scanned, HasMore: page.HasMore, Cursor: page.Stats.Cursor}
	for index := len(page.Events) - 1; index >= 0 && len(output.Events) < limit; index-- {
		entry := page.Events[index]
		if query != "" && !strings.Contains(strings.ToLower(string(entry.Data)), query) {
			continue
		}
		output.Events = append(output.Events, summarizeEntry(entry, false))
	}
	return output, nil
}

// WaitForRequestInput describes the next request an agent is waiting for.
type WaitForRequestInput struct {
	After         uint64  `json:"after,omitempty" jsonschema:"wait only for requests newer than this cursor"`
	Method        string  `json:"method,omitempty" jsonschema:"exact HTTP method filter"`
	PathContains  string  `json:"path_contains,omitempty" jsonschema:"path substring filter"`
	Status        int     `json:"status,omitempty" jsonschema:"exact HTTP status filter"`
	MinDurationMS float64 `json:"min_duration_ms,omitempty" jsonschema:"minimum request duration in milliseconds"`
	MaxDurationMS float64 `json:"max_duration_ms,omitempty" jsonschema:"maximum request duration in milliseconds"`
	TimeoutMS     int64   `json:"timeout_ms,omitempty" jsonschema:"maximum wait time in milliseconds, capped at 120000"`
}

// WaitForRequestOutput contains the first newly captured matching request.
type WaitForRequestOutput struct {
	Request client.RequestSummary `json:"request"`
}

// WaitForRequest blocks until a matching request is captured or the bounded
// timeout expires.
func (s *Service) WaitForRequest(ctx context.Context, input WaitForRequestInput) (WaitForRequestOutput, error) {
	timeout := time.Duration(input.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = defaultWait
	}
	if timeout > maxWait {
		timeout = maxWait
	}
	waitContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	request, err := s.client.WaitForRequest(waitContext, client.WaitForRequestOptions{
		After:        input.After,
		Method:       strings.TrimSpace(input.Method),
		PathContains: strings.TrimSpace(input.PathContains),
		Status:       input.Status,
		MinDuration:  time.Duration(input.MinDurationMS * float64(time.Millisecond)),
		MaxDuration:  time.Duration(input.MaxDurationMS * float64(time.Millisecond)),
	})
	if err != nil {
		return WaitForRequestOutput{}, fmt.Errorf("wait for request: %w", err)
	}
	return WaitForRequestOutput{Request: request}, nil
}

// EventSummary keeps high-signal diagnostic fields while omitting captured
// payloads by default. Detail is shaped according to Kind.
type EventSummary struct {
	ID              string            `json:"id"`
	Kind            webpprof.Kind     `json:"kind"`
	RequestID       string            `json:"request_id,omitempty"`
	ParentID        string            `json:"parent_id,omitempty"`
	OriginRequestID string            `json:"origin_request_id,omitempty"`
	StartedAt       time.Time         `json:"started_at"`
	DurationNS      int64             `json:"duration_ns,omitempty"`
	Tags            map[string]string `json:"tags,omitempty"`
	Detail          any               `json:"detail"`
}

func summarizeEntry(entry webpprof.Entry, includePayloads bool) EventSummary {
	return EventSummary{
		ID:              entry.ID,
		Kind:            entry.Kind,
		RequestID:       entry.RequestID,
		ParentID:        entry.ParentID,
		OriginRequestID: entry.OriginRequestID,
		StartedAt:       entry.StartedAt,
		DurationNS:      entry.DurationNS,
		Tags:            entry.Tags,
		Detail:          summarizeDetail(entry, includePayloads),
	}
}

func summarizeDetail(entry webpprof.Entry, includePayloads bool) any {
	switch entry.Kind {
	case webpprof.KindRequest:
		request, err := client.DecodeRequest(entry)
		if err == nil {
			return request
		}
	case webpprof.KindQuery:
		var value webpprof.Query
		if json.Unmarshal(entry.Data, &value) == nil {
			return value
		}
	case webpprof.KindCache:
		var value webpprof.Cache
		if json.Unmarshal(entry.Data, &value) == nil {
			if !includePayloads {
				value.Value = ""
			}
			return value
		}
	case webpprof.KindHTTPCall:
		var value webpprof.HTTPCall
		if json.Unmarshal(entry.Data, &value) == nil {
			if !includePayloads {
				value.Request = webpprof.HTTPMessage{}
				value.Response = webpprof.HTTPMessage{}
			}
			return value
		}
	case webpprof.KindJob:
		var value webpprof.Job
		if json.Unmarshal(entry.Data, &value) == nil {
			if !includePayloads {
				value.Arguments = nil
			}
			return value
		}
	case webpprof.KindEmail:
		var value webpprof.Email
		if json.Unmarshal(entry.Data, &value) == nil {
			if !includePayloads {
				value.Text = ""
				value.HTML = ""
			}
			return value
		}
	case webpprof.KindLog:
		var value webpprof.Log
		if json.Unmarshal(entry.Data, &value) == nil {
			if !includePayloads {
				value.Stack = ""
			}
			return value
		}
	case webpprof.KindException:
		var value webpprof.Exception
		if json.Unmarshal(entry.Data, &value) == nil {
			if !includePayloads {
				value.Stack = ""
			}
			return value
		}
	case webpprof.KindSchedule:
		var value webpprof.Schedule
		if json.Unmarshal(entry.Data, &value) == nil {
			if !includePayloads {
				value.Payload = nil
			}
			return value
		}
	case webpprof.KindEvent:
		var value webpprof.Event
		if json.Unmarshal(entry.Data, &value) == nil {
			return value
		}
	case webpprof.KindMiddleware:
		var value webpprof.Middleware
		if json.Unmarshal(entry.Data, &value) == nil {
			return value
		}
	}
	return map[string]string{"decode_error": "unsupported or invalid event payload"}
}

func boundedLimit(value, defaultValue, maximum int) int {
	if value <= 0 {
		return defaultValue
	}
	return min(value, maximum)
}
