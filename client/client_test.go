package client_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	webpprof "github.com/levskiy0/webpprof"
	"github.com/levskiy0/webpprof/client"
)

func TestNewRejectsInvalidBaseURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		url  string
	}{
		{name: "missing scheme", url: "127.0.0.1:6061/debug/webpprof"},
		{name: "unsupported scheme", url: "file:///tmp/webpprof"},
		{name: "missing host", url: "http:///debug/webpprof"},
		{name: "query", url: "http://localhost/debug/webpprof?token=secret"},
		{name: "fragment", url: "http://localhost/debug/webpprof#events"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := client.New(test.url); err == nil {
				t.Fatalf("New(%q) succeeded, want error", test.url)
			}
		})
	}
}

func TestClientAuthenticatesAndInspectsRequest(t *testing.T) {
	t.Parallel()

	requestEntry := requestFixture(t, "request-1", 7, "/orders/42", http.StatusInternalServerError)
	queryEntry := webpprof.Entry{
		Cursor:     8,
		ID:         "query-1",
		Kind:       webpprof.KindQuery,
		RequestID:  requestEntry.ID,
		StartedAt:  requestEntry.StartedAt.Add(time.Millisecond),
		RecordedAt: requestEntry.RecordedAt,
		DurationNS: int64(120 * time.Millisecond),
		Data:       json.RawMessage(`{"sql":"select * from orders where id = 42"}`),
	}
	analysis := webpprof.RequestAnalysis{
		RequestID: requestEntry.ID,
		Findings: []webpprof.Finding{{
			Code:     webpprof.FindingSlowQuery,
			Severity: webpprof.FindingSeverityWarning,
			Title:    "Slow query",
		}},
	}

	var loginCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/debug/webpprof/session":
			loginCount.Add(1)
			var payload struct {
				Token string `json:"token"`
			}
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil || payload.Token != "test-token" {
				http.Error(response, "unauthorized", http.StatusUnauthorized)
				return
			}
			http.SetCookie(response, &http.Cookie{Name: "webpprof_session", Value: "session", Path: "/debug/webpprof"})
			response.WriteHeader(http.StatusNoContent)
			return
		}

		cookie, err := request.Cookie("webpprof_session")
		if err != nil || cookie.Value != "session" {
			writeTestJSON(t, response, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		switch request.URL.Path {
		case "/debug/webpprof/api/events/request-1":
			writeTestJSON(t, response, http.StatusOK, requestEntry)
		case "/debug/webpprof/api/events":
			writeTestJSON(t, response, http.StatusOK, client.EventPage{Events: []webpprof.Entry{requestEntry, queryEntry}})
		case "/debug/webpprof/api/requests/request-1/analysis":
			writeTestJSON(t, response, http.StatusOK, analysis)
		default:
			http.NotFound(response, request)
		}
	}))
	t.Cleanup(server.Close)

	httpClient := &http.Client{Transport: &http.Transport{MaxConnsPerHost: 1}}
	t.Cleanup(httpClient.CloseIdleConnections)
	profilerClient, err := client.New(
		server.URL+"/debug/webpprof/",
		client.WithToken("test-token"),
		client.WithHTTPClient(httpClient),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	report, err := profilerClient.InspectRequest(t.Context(), requestEntry.ID, 100)
	if err != nil {
		t.Fatalf("InspectRequest() error = %v", err)
	}
	if report.Request.Path != "/orders/42" || report.Request.Status != http.StatusInternalServerError {
		t.Fatalf("InspectRequest() request = %+v", report.Request)
	}
	if report.Counts[webpprof.KindQuery] != 1 {
		t.Fatalf("InspectRequest() query count = %d, want 1", report.Counts[webpprof.KindQuery])
	}
	if len(report.Analysis.Findings) != 1 {
		t.Fatalf("InspectRequest() findings = %d, want 1", len(report.Analysis.Findings))
	}
	if loginCount.Load() != 1 {
		t.Fatalf("login count = %d, want 1", loginCount.Load())
	}
}

func TestClientWaitForRequest(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/debug/webpprof/api/events" {
			http.NotFound(response, request)
			return
		}
		if calls.Add(1) < 2 {
			writeTestJSON(t, response, http.StatusOK, client.EventPage{})
			return
		}
		entry := requestFixture(t, "request-2", 12, "/checkout", http.StatusBadGateway)
		writeTestJSON(t, response, http.StatusOK, client.EventPage{Events: []webpprof.Entry{entry}})
	}))
	t.Cleanup(server.Close)

	profilerClient, err := client.New(server.URL + "/debug/webpprof/")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	request, err := profilerClient.WaitForRequest(ctx, client.WaitForRequestOptions{
		After:        10,
		Method:       http.MethodGet,
		PathContains: "check",
		Status:       http.StatusBadGateway,
		PollInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("WaitForRequest() error = %v", err)
	}
	if request.ID != "request-2" {
		t.Fatalf("WaitForRequest() ID = %q, want request-2", request.ID)
	}
}

func TestClientReturnsTypedAPIError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		writeTestJSON(t, response, http.StatusNotFound, map[string]string{"error": "request not found"})
	}))
	t.Cleanup(server.Close)

	profilerClient, err := client.New(server.URL)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = profilerClient.RequestAnalysis(t.Context(), "missing")
	var apiError *client.APIError
	if !errors.As(err, &apiError) {
		t.Fatalf("RequestAnalysis() error = %v, want *client.APIError", err)
	}
	if apiError.StatusCode != http.StatusNotFound || !strings.Contains(apiError.Message, "not found") {
		t.Fatalf("APIError = %+v", apiError)
	}
}

func requestFixture(t *testing.T, id string, cursor uint64, path string, status int) webpprof.Entry {
	t.Helper()
	startedAt := time.Date(2026, 8, 23, 0, 0, 0, 0, time.UTC)
	payload, err := json.Marshal(webpprof.Request{
		Meta:   webpprof.Meta{ID: id, StartedAt: startedAt, Duration: 750 * time.Millisecond},
		Method: http.MethodGet,
		Path:   path,
		Status: status,
		Error:  "upstream failed",
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return webpprof.Entry{
		Cursor:     cursor,
		ID:         id,
		Kind:       webpprof.KindRequest,
		StartedAt:  startedAt,
		RecordedAt: startedAt.Add(time.Second),
		DurationNS: int64(750 * time.Millisecond),
		Tags:       map[string]string{"environment": "test"},
		Data:       payload,
	}
}

func writeTestJSON(t *testing.T, response http.ResponseWriter, status int, value any) {
	t.Helper()
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	if err := json.NewEncoder(response).Encode(value); err != nil {
		t.Errorf("json.Encode() error = %v", err)
	}
}
