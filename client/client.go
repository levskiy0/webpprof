// Package client provides a read-only HTTP client for a running webpprof
// instance. It is intended for diagnostic integrations such as MCP servers.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	webpprof "github.com/levskiy0/webpprof"
)

const (
	defaultTimeout  = 15 * time.Second
	maxResponseSize = 16 << 20
)

// APIError describes a non-success response from webpprof.
type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("webpprof API returned status %d: %s", e.StatusCode, e.Message)
}

// Client reads diagnostic data from one running webpprof instance.
type Client struct {
	baseURL    *url.URL
	httpClient *http.Client
	token      string

	authMu        sync.Mutex
	authenticated bool
}

// Option configures a Client.
type Option func(*config) error

type config struct {
	httpClient *http.Client
	timeout    time.Duration
	token      string
}

// WithToken configures the token passed to webpprof's session endpoint.
func WithToken(token string) Option {
	return func(config *config) error {
		config.token = token
		return nil
	}
}

// WithHTTPClient supplies the HTTP client used for profiler requests. The
// value is cloned so webpprof can install its own cookie jar safely.
func WithHTTPClient(httpClient *http.Client) Option {
	return func(config *config) error {
		if httpClient == nil {
			return errors.New("webpprof client: nil HTTP client")
		}
		config.httpClient = httpClient
		return nil
	}
}

// WithTimeout sets the default HTTP timeout. Context deadlines still take
// precedence. A zero duration disables the client-wide timeout.
func WithTimeout(timeout time.Duration) Option {
	return func(config *config) error {
		if timeout < 0 {
			return errors.New("webpprof client: timeout must not be negative")
		}
		config.timeout = timeout
		return nil
	}
}

// New constructs a read-only client. rawURL must point at webpprof's base URL,
// for example http://127.0.0.1:6061/debug/webpprof/.
func New(rawURL string, options ...Option) (*Client, error) {
	baseURL, err := normalizeBaseURL(rawURL)
	if err != nil {
		return nil, err
	}

	configuration := config{timeout: defaultTimeout}
	for _, option := range options {
		if option == nil {
			continue
		}
		if err := option(&configuration); err != nil {
			return nil, fmt.Errorf("webpprof client: configure: %w", err)
		}
	}

	httpClient := &http.Client{Timeout: configuration.timeout}
	if configuration.httpClient != nil {
		copy := *configuration.httpClient
		httpClient = &copy
		if httpClient.Timeout == 0 {
			httpClient.Timeout = configuration.timeout
		}
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("webpprof client: create cookie jar: %w", err)
	}
	httpClient.Jar = jar

	return &Client{baseURL: baseURL, httpClient: httpClient, token: configuration.token}, nil
}

func normalizeBaseURL(rawURL string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, fmt.Errorf("webpprof client: parse URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errors.New("webpprof client: URL scheme must be http or https")
	}
	if parsed.Host == "" {
		return nil, errors.New("webpprof client: URL host is required")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("webpprof client: base URL must not contain a query or fragment")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/"
	return parsed, nil
}

// Stats returns capture and retention status from the profiler.
func (c *Client) Stats(ctx context.Context) (webpprof.Stats, error) {
	var stats webpprof.Stats
	if err := c.getJSON(ctx, "api/stats", nil, &stats); err != nil {
		return webpprof.Stats{}, fmt.Errorf("webpprof client: get stats: %w", err)
	}
	return stats, nil
}

// ListEvents returns one bounded page of captured events.
func (c *Client) ListEvents(ctx context.Context, options ListEventsOptions) (EventPage, error) {
	query := make(url.Values)
	if options.Kind != "" {
		query.Set("kind", string(options.Kind))
	}
	if options.RequestID != "" {
		query.Set("request_id", options.RequestID)
	}
	for _, tag := range options.Tags {
		if strings.TrimSpace(tag) != "" {
			query.Add("tag", tag)
		}
	}
	if strings.TrimSpace(options.Query) != "" {
		query.Set("q", strings.TrimSpace(options.Query))
	}
	if strings.TrimSpace(options.Method) != "" {
		query.Set("method", strings.TrimSpace(options.Method))
	}
	if strings.TrimSpace(options.PathContains) != "" {
		query.Set("path_contains", strings.TrimSpace(options.PathContains))
	}
	if options.Status != 0 {
		query.Set("status", strconv.Itoa(options.Status))
	}
	if options.MinDuration > 0 {
		query.Set("min_duration_ms", strconv.FormatFloat(float64(options.MinDuration)/float64(time.Millisecond), 'f', -1, 64))
	}
	if options.MaxDuration > 0 {
		query.Set("max_duration_ms", strconv.FormatFloat(float64(options.MaxDuration)/float64(time.Millisecond), 'f', -1, 64))
	}
	if options.After > 0 {
		query.Set("after", strconv.FormatUint(options.After, 10))
	}
	if options.Before > 0 {
		query.Set("before", strconv.FormatUint(options.Before, 10))
	}
	limit := options.Limit
	if limit <= 0 {
		limit = 200
	}
	if limit > 1_000 {
		limit = 1_000
	}
	query.Set("limit", strconv.Itoa(limit))

	var page EventPage
	if err := c.getJSON(ctx, "api/events", query, &page); err != nil {
		return EventPage{}, fmt.Errorf("webpprof client: list events: %w", err)
	}
	return page, nil
}

// Event returns one captured event by ID.
func (c *Client) Event(ctx context.Context, id string) (webpprof.Entry, error) {
	if strings.TrimSpace(id) == "" {
		return webpprof.Entry{}, errors.New("webpprof client: event ID is required")
	}
	var entry webpprof.Entry
	if err := c.getJSON(ctx, "api/events/"+url.PathEscape(id), nil, &entry); err != nil {
		return webpprof.Entry{}, fmt.Errorf("webpprof client: get event: %w", err)
	}
	return entry, nil
}

// RequestAnalysis returns the automatic findings for a captured request.
func (c *Client) RequestAnalysis(ctx context.Context, requestID string) (webpprof.RequestAnalysis, error) {
	if strings.TrimSpace(requestID) == "" {
		return webpprof.RequestAnalysis{}, errors.New("webpprof client: request ID is required")
	}
	var analysis webpprof.RequestAnalysis
	endpoint := "api/requests/" + url.PathEscape(requestID) + "/analysis"
	if err := c.getJSON(ctx, endpoint, nil, &analysis); err != nil {
		return webpprof.RequestAnalysis{}, fmt.Errorf("webpprof client: analyze request: %w", err)
	}
	return analysis, nil
}

// InspectRequest combines the request entry, its correlated events, and the
// profiler's automatic findings in one response suited to diagnostic tools.
func (c *Client) InspectRequest(ctx context.Context, requestID string, limit int) (RequestReport, error) {
	requestEntry, err := c.Event(ctx, requestID)
	if err != nil {
		return RequestReport{}, err
	}
	request, err := DecodeRequest(requestEntry)
	if err != nil {
		return RequestReport{}, fmt.Errorf("webpprof client: decode request: %w", err)
	}
	page, err := c.ListEvents(ctx, ListEventsOptions{RequestID: requestID, Limit: limit})
	if err != nil {
		return RequestReport{}, err
	}
	analysis, err := c.RequestAnalysis(ctx, requestID)
	if err != nil {
		return RequestReport{}, err
	}

	counts := make(map[webpprof.Kind]int)
	for _, entry := range page.Events {
		counts[entry.Kind]++
	}
	return RequestReport{
		Request:  request,
		Entry:    requestEntry,
		Events:   page.Events,
		Counts:   counts,
		HasMore:  page.HasMore,
		Analysis: analysis,
	}, nil
}

// WaitForRequest polls until a captured request matches the supplied filters
// or ctx is cancelled. It starts after options.After, so callers can avoid
// returning an already observed request.
func (c *Client) WaitForRequest(ctx context.Context, options WaitForRequestOptions) (RequestSummary, error) {
	pollInterval := options.PollInterval
	if pollInterval <= 0 {
		pollInterval = 250 * time.Millisecond
	}
	cursor := options.After

	for {
		page, err := c.ListEvents(ctx, ListEventsOptions{Kind: webpprof.KindRequest, After: cursor, Method: options.Method, PathContains: options.PathContains, Status: options.Status, MinDuration: options.MinDuration, MaxDuration: options.MaxDuration, Limit: 200})
		if err != nil {
			return RequestSummary{}, fmt.Errorf("webpprof client: wait for request: %w", err)
		}
		for _, entry := range page.Events {
			if entry.Cursor > cursor {
				cursor = entry.Cursor
			}
			request, err := DecodeRequest(entry)
			if err != nil {
				continue
			}
			if options.matches(request) {
				return request, nil
			}
		}

		timer := time.NewTimer(pollInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return RequestSummary{}, fmt.Errorf("webpprof client: wait for request: %w", ctx.Err())
		case <-timer.C:
		}
	}
}

func (c *Client) getJSON(ctx context.Context, endpoint string, query url.Values, target any) error {
	requestURL := c.baseURL.ResolveReference(&url.URL{Path: endpoint})
	requestURL.RawQuery = query.Encode()
	return c.doJSON(ctx, http.MethodGet, requestURL, nil, target, true)
}

func (c *Client) doJSON(ctx context.Context, method string, requestURL *url.URL, body []byte, target any, retryAuth bool) error {
	request, err := http.NewRequestWithContext(ctx, method, requestURL.String(), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	if len(body) > 0 {
		request.Header.Set("Content-Type", "application/json")
	}

	response, err := c.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}

	if response.StatusCode == http.StatusUnauthorized && retryAuth && c.token != "" {
		if err := response.Body.Close(); err != nil {
			return fmt.Errorf("close unauthorized response: %w", err)
		}
		if err := c.authenticate(ctx, true); err != nil {
			return err
		}
		return c.doJSON(ctx, method, requestURL, body, target, false)
	}
	defer response.Body.Close()

	limited := io.LimitReader(response.Body, maxResponseSize+1)
	payload, err := io.ReadAll(limited)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if len(payload) > maxResponseSize {
		return errors.New("response exceeds 16 MiB limit")
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		message := strings.TrimSpace(http.StatusText(response.StatusCode))
		var apiError struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(payload, &apiError) == nil && strings.TrimSpace(apiError.Error) != "" {
			message = apiError.Error
		}
		return &APIError{StatusCode: response.StatusCode, Message: message}
	}
	if target == nil || len(payload) == 0 {
		return nil
	}
	if err := json.Unmarshal(payload, target); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func (c *Client) authenticate(ctx context.Context, force bool) error {
	c.authMu.Lock()
	defer c.authMu.Unlock()
	if c.authenticated && !force {
		return nil
	}

	payload, err := json.Marshal(struct {
		Token string `json:"token"`
	}{Token: c.token})
	if err != nil {
		return fmt.Errorf("webpprof client: encode session: %w", err)
	}
	sessionURL := c.baseURL.ResolveReference(&url.URL{Path: "session"})
	if err := c.doJSON(ctx, http.MethodPost, sessionURL, payload, nil, false); err != nil {
		return fmt.Errorf("webpprof client: create session: %w", err)
	}
	c.authenticated = true
	return nil
}
