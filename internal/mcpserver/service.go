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

func New(profilerClient ProfilerClient) (*Service, error) {
	if profilerClient == nil {
		return nil, errors.New("webpprof MCP: nil profiler client")
	}
	return &Service{client: profilerClient}, nil
}

type StatusInput struct{}

type StatusOutput struct {
	Connected bool           `json:"connected"`
	Stats     webpprof.Stats `json:"stats"`
}

func (s *Service) Status(ctx context.Context, _ StatusInput) (StatusOutput, error) {
	stats, err := s.client.Stats(ctx)
	if err != nil {
		return StatusOutput{}, fmt.Errorf("get profiler status: %w", err)
	}
	return StatusOutput{Connected: true, Stats: stats}, nil
}

type ListRequestsInput struct {
	Limit         int      `json:"limit,omitempty" jsonschema:"maximum=200,description=Maximum matching requests to return"`
	Before        uint64   `json:"before,omitempty" jsonschema:"description=Return requests older than this cursor"`
	Method        string   `json:"method,omitempty" jsonschema:"description=Exact HTTP method filter"`
	PathContains  string   `json:"path_contains,omitempty" jsonschema:"description=Case-insensitive path substring"`
	Status        int      `json:"status,omitempty" jsonschema:"description=Exact HTTP status filter"`
	MinDurationMS float64  `json:"min_duration_ms,omitempty" jsonschema:"minimum=0,description=Minimum request duration in milliseconds"`
	Tags          []string `json:"tags,omitempty" jsonschema:"description=Tag filters in key or key=value form"`
}

type ListRequestsOutput struct {
	Requests []client.RequestSummary `json:"requests"`
	Scanned  int                     `json:"scanned"`
	HasMore  bool                    `json:"has_more"`
	Cursor   uint64                  `json:"cursor"`
}

func (s *Service) ListRequests(ctx context.Context, input ListRequestsInput) (ListRequestsOutput, error) {
	limit := boundedLimit(input.Limit, defaultListLimit, maxListLimit)
	page, err := s.client.ListEvents(ctx, client.ListEventsOptions{
		Kind:   webpprof.KindRequest,
		Tags:   input.Tags,
		Before: input.Before,
		Limit:  maxListLimit,
	})
	if err != nil {
		return ListRequestsOutput{}, fmt.Errorf("list requests: %w", err)
	}

	output := ListRequestsOutput{Requests: make([]client.RequestSummary, 0, limit), Scanned: len(page.Events), HasMore: page.HasMore, Cursor: page.Stats.Cursor}
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
		if float64(request.DurationNS)/float64(time.Millisecond) < input.MinDurationMS {
			continue
		}
		output.Requests = append(output.Requests, request)
	}
	return output, nil
}

type InspectRequestInput struct {
	RequestID       string `json:"request_id" jsonschema:"description=Captured request ID"`
	MaxEvents       int    `json:"max_events,omitempty" jsonschema:"maximum=1000,description=Maximum correlated events to return"`
	IncludePayloads bool   `json:"include_payloads,omitempty" jsonschema:"description=Include bounded captured bodies values arguments and stacks"`
}

type InspectRequestOutput struct {
	Request  client.RequestSummary `json:"request"`
	Findings []webpprof.Finding    `json:"findings"`
	Counts   map[webpprof.Kind]int `json:"counts"`
	Events   []EventSummary        `json:"events"`
	HasMore  bool                  `json:"has_more"`
}

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

type SearchEventsInput struct {
	Query     string   `json:"query,omitempty" jsonschema:"description=Case-insensitive text found in event JSON"`
	Kind      string   `json:"kind,omitempty" jsonschema:"description=Event kind such as query cache log http_call or exception"`
	RequestID string   `json:"request_id,omitempty" jsonschema:"description=Restrict results to one request timeline"`
	Tags      []string `json:"tags,omitempty" jsonschema:"description=Tag filters in key or key=value form"`
	After     uint64   `json:"after,omitempty" jsonschema:"description=Return events newer than this cursor"`
	Before    uint64   `json:"before,omitempty" jsonschema:"description=Return events older than this cursor"`
	Limit     int      `json:"limit,omitempty" jsonschema:"maximum=200,description=Maximum matching events to return"`
}

type SearchEventsOutput struct {
	Events  []EventSummary `json:"events"`
	Scanned int            `json:"scanned"`
	HasMore bool           `json:"has_more"`
	Cursor  uint64         `json:"cursor"`
}

func (s *Service) SearchEvents(ctx context.Context, input SearchEventsInput) (SearchEventsOutput, error) {
	limit := boundedLimit(input.Limit, defaultListLimit, maxListLimit)
	page, err := s.client.ListEvents(ctx, client.ListEventsOptions{
		Kind:      webpprof.Kind(strings.TrimSpace(input.Kind)),
		RequestID: strings.TrimSpace(input.RequestID),
		Tags:      input.Tags,
		After:     input.After,
		Before:    input.Before,
		Limit:     maxListLimit,
	})
	if err != nil {
		return SearchEventsOutput{}, fmt.Errorf("search events: %w", err)
	}
	query := strings.ToLower(strings.TrimSpace(input.Query))
	output := SearchEventsOutput{Events: make([]EventSummary, 0, limit), Scanned: len(page.Events), HasMore: page.HasMore, Cursor: page.Stats.Cursor}
	for index := len(page.Events) - 1; index >= 0 && len(output.Events) < limit; index-- {
		entry := page.Events[index]
		if query != "" && !strings.Contains(strings.ToLower(string(entry.Data)), query) {
			continue
		}
		output.Events = append(output.Events, summarizeEntry(entry, false))
	}
	return output, nil
}

type WaitForRequestInput struct {
	After         uint64  `json:"after,omitempty" jsonschema:"description=Wait only for requests newer than this cursor"`
	Method        string  `json:"method,omitempty" jsonschema:"description=Exact HTTP method filter"`
	PathContains  string  `json:"path_contains,omitempty" jsonschema:"description=Path substring filter"`
	Status        int     `json:"status,omitempty" jsonschema:"description=Exact HTTP status filter"`
	MinDurationMS float64 `json:"min_duration_ms,omitempty" jsonschema:"minimum=0,description=Minimum request duration in milliseconds"`
	TimeoutMS     int64   `json:"timeout_ms,omitempty" jsonschema:"maximum=120000,description=Maximum wait time in milliseconds"`
}

type WaitForRequestOutput struct {
	Request client.RequestSummary `json:"request"`
}

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
