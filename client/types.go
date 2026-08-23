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
	Kind      webpprof.Kind
	RequestID string
	Tags      []string
	After     uint64
	Before    uint64
	Limit     int
}

// EventPage is the response returned by webpprof's event endpoint.
type EventPage struct {
	Events  []webpprof.Entry `json:"events"`
	HasMore bool             `json:"has_more"`
	Stats   webpprof.Stats   `json:"stats"`
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

// WaitForRequestOptions filters newly captured request events.
type WaitForRequestOptions struct {
	After        uint64
	Method       string
	PathContains string
	Status       int
	MinDuration  time.Duration
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
	return time.Duration(request.DurationNS) >= options.MinDuration
}

// DecodeRequest validates and decodes a request entry.
func DecodeRequest(entry webpprof.Entry) (RequestSummary, error) {
	if entry.Kind != webpprof.KindRequest {
		return RequestSummary{}, errors.New("entry is not a request")
	}
	var request webpprof.Request
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
