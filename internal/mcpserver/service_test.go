package mcpserver

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	webpprof "github.com/levskiy0/webpprof"
	"github.com/levskiy0/webpprof/client"
)

type fakeProfilerClient struct {
	stats         webpprof.Stats
	page          client.EventPage
	report        client.RequestReport
	waitedRequest client.RequestSummary
	listOptions   client.ListEventsOptions
	waitOptions   client.WaitForRequestOptions
}

func (fake *fakeProfilerClient) Stats(context.Context) (webpprof.Stats, error) {
	return fake.stats, nil
}

func (fake *fakeProfilerClient) ListEvents(_ context.Context, options client.ListEventsOptions) (client.EventPage, error) {
	fake.listOptions = options
	return fake.page, nil
}

func (*fakeProfilerClient) Event(context.Context, string) (webpprof.Entry, error) {
	return webpprof.Entry{}, nil
}

func (*fakeProfilerClient) RequestAnalysis(context.Context, string) (webpprof.RequestAnalysis, error) {
	return webpprof.RequestAnalysis{}, nil
}

func (fake *fakeProfilerClient) InspectRequest(context.Context, string, int) (client.RequestReport, error) {
	return fake.report, nil
}

func (fake *fakeProfilerClient) WaitForRequest(_ context.Context, options client.WaitForRequestOptions) (client.RequestSummary, error) {
	fake.waitOptions = options
	return fake.waitedRequest, nil
}

func TestServiceListRequestsFiltersNewestFirst(t *testing.T) {
	t.Parallel()

	fake := &fakeProfilerClient{page: client.EventPage{
		Events: []webpprof.Entry{
			requestEntry(t, "one", 1, "GET", "/health", 200, 10*time.Millisecond),
			requestEntry(t, "two", 2, "POST", "/orders", 201, 700*time.Millisecond),
			requestEntry(t, "three", 3, "POST", "/orders/42", 500, 900*time.Millisecond),
		},
		Stats: webpprof.Stats{Cursor: 3},
	}}
	service, err := New(fake)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	output, err := service.ListRequests(t.Context(), ListRequestsInput{
		Limit:         1,
		Method:        "post",
		PathContains:  "orders",
		MinDurationMS: 500,
	})
	if err != nil {
		t.Fatalf("ListRequests() error = %v", err)
	}
	if len(output.Requests) != 1 || output.Requests[0].ID != "three" {
		t.Fatalf("ListRequests() requests = %+v", output.Requests)
	}
	if output.Scanned != 3 || output.Cursor != 3 {
		t.Fatalf("ListRequests() metadata = %+v", output)
	}
	if fake.listOptions.Kind != webpprof.KindRequest || fake.listOptions.Limit != 1 || fake.listOptions.Method != "post" || fake.listOptions.PathContains != "orders" || fake.listOptions.MinDuration != 500*time.Millisecond {
		t.Fatalf("ListEvents() options = %+v", fake.listOptions)
	}
}

func TestServiceInspectRequestOmitsPayloadsByDefault(t *testing.T) {
	t.Parallel()

	cachePayload, err := json.Marshal(webpprof.Cache{Operation: "get", Key: "session:42", Value: "secret-value", Hit: true})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	fake := &fakeProfilerClient{report: client.RequestReport{
		Request: client.RequestSummary{ID: "request-1", Path: "/account"},
		Events: []webpprof.Entry{{
			ID:   "cache-1",
			Kind: webpprof.KindCache,
			Data: cachePayload,
		}},
		Counts: map[webpprof.Kind]int{webpprof.KindCache: 1},
		Analysis: webpprof.RequestAnalysis{Findings: []webpprof.Finding{{
			Code:  webpprof.FindingHighCacheMissRate,
			Title: "High cache miss rate",
		}}},
	}}
	service, err := New(fake)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	output, err := service.InspectRequest(t.Context(), InspectRequestInput{RequestID: "request-1"})
	if err != nil {
		t.Fatalf("InspectRequest() error = %v", err)
	}
	cache, ok := output.Events[0].Detail.(webpprof.Cache)
	if !ok {
		t.Fatalf("InspectRequest() detail type = %T", output.Events[0].Detail)
	}
	if cache.Value != "" {
		t.Fatalf("InspectRequest() cache value = %q, want omitted", cache.Value)
	}
	if cache.Key != "session:42" || len(output.Findings) != 1 {
		t.Fatalf("InspectRequest() output = %+v", output)
	}
}

func TestServiceSearchEventsForwardsServerFilters(t *testing.T) {
	t.Parallel()
	fake := &fakeProfilerClient{page: client.EventPage{Events: []webpprof.Entry{{ID: "query-1", Kind: webpprof.KindQuery, DurationNS: int64(25 * time.Millisecond), Data: json.RawMessage(`{"sql":"select needle"}`)}}, Scanned: 7}}
	service, err := New(fake)
	if err != nil {
		t.Fatal(err)
	}
	output, err := service.SearchEvents(t.Context(), SearchEventsInput{Query: "needle", Kind: string(webpprof.KindQuery), MinDurationMS: 10, MaxDurationMS: 50, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(output.Events) != 1 || output.Scanned != 7 {
		t.Fatalf("SearchEvents() = %+v", output)
	}
	if fake.listOptions.Query != "needle" || fake.listOptions.Kind != webpprof.KindQuery || fake.listOptions.MinDuration != 10*time.Millisecond || fake.listOptions.MaxDuration != 50*time.Millisecond || fake.listOptions.Limit != 1 {
		t.Fatalf("ListEvents() options = %+v", fake.listOptions)
	}
}

func TestServiceSearchEventsTrustsServerQueryMatches(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		query string
		entry webpprof.Entry
	}{
		{
			name:  "entry ID",
			query: "matching-id",
			entry: webpprof.Entry{ID: "matching-id", Kind: webpprof.KindEvent, Data: json.RawMessage(`{"name":"unrelated"}`)},
		},
		{
			name:  "process",
			query: "worker-42",
			entry: webpprof.Entry{ID: "event-1", Kind: webpprof.KindEvent, Process: "worker-42", Data: json.RawMessage(`{"name":"unrelated"}`)},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fake := &fakeProfilerClient{page: client.EventPage{Events: []webpprof.Entry{test.entry}, Scanned: 1}}
			service, err := New(fake)
			if err != nil {
				t.Fatal(err)
			}
			output, err := service.SearchEvents(t.Context(), SearchEventsInput{Query: test.query})
			if err != nil {
				t.Fatal(err)
			}
			if len(output.Events) != 1 || output.Events[0].ID != test.entry.ID {
				t.Fatalf("SearchEvents() = %+v", output)
			}
			if fake.listOptions.Query != test.query {
				t.Fatalf("ListEvents() query = %q", fake.listOptions.Query)
			}
		})
	}
}

func TestServiceWaitForRequestBoundsTimeoutAndConvertsDuration(t *testing.T) {
	t.Parallel()

	fake := &fakeProfilerClient{waitedRequest: client.RequestSummary{ID: "request-2"}}
	service, err := New(fake)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	output, err := service.WaitForRequest(t.Context(), WaitForRequestInput{
		After:         9,
		MinDurationMS: 12.5,
		TimeoutMS:     int64((10 * time.Minute) / time.Millisecond),
	})
	if err != nil {
		t.Fatalf("WaitForRequest() error = %v", err)
	}
	if output.Request.ID != "request-2" {
		t.Fatalf("WaitForRequest() output = %+v", output)
	}
	if fake.waitOptions.After != 9 || fake.waitOptions.MinDuration != 12_500*time.Microsecond {
		t.Fatalf("WaitForRequest() options = %+v", fake.waitOptions)
	}
}

func requestEntry(t *testing.T, id string, cursor uint64, method, path string, status int, duration time.Duration) webpprof.Entry {
	t.Helper()
	payload, err := json.Marshal(webpprof.Request{Method: method, Path: path, Status: status})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return webpprof.Entry{ID: id, Cursor: cursor, Kind: webpprof.KindRequest, DurationNS: int64(duration), Data: payload}
}
