package main

import (
	"context"
	"errors"
	"testing"

	webpprof "github.com/levskiy0/webpprof"
	"github.com/levskiy0/webpprof/client"
	"github.com/levskiy0/webpprof/internal/mcpserver"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type profilerClientStub struct{ stats webpprof.Stats }

func (f profilerClientStub) Stats(context.Context) (webpprof.Stats, error) { return f.stats, nil }
func (profilerClientStub) ListEvents(context.Context, client.ListEventsOptions) (client.EventPage, error) {
	return client.EventPage{}, nil
}
func (profilerClientStub) Event(context.Context, string) (webpprof.Entry, error) {
	return webpprof.Entry{}, nil
}
func (profilerClientStub) RequestAnalysis(context.Context, string) (webpprof.RequestAnalysis, error) {
	return webpprof.RequestAnalysis{}, nil
}
func (profilerClientStub) InspectRequest(context.Context, string, int) (client.RequestReport, error) {
	return client.RequestReport{}, nil
}
func (profilerClientStub) WaitForRequest(context.Context, client.WaitForRequestOptions) (client.RequestSummary, error) {
	return client.RequestSummary{}, nil
}

func TestNewServerExposesReadOnlyTools(t *testing.T) {
	service, err := mcpserver.New(profilerClientStub{stats: webpprof.Stats{Events: 42, Cursor: 9}})
	if err != nil {
		t.Fatal(err)
	}
	server := newServer(service, "test")
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	serverErrors := make(chan error, 1)
	go func() { serverErrors <- server.Run(ctx, serverTransport) }()

	mcpClient := mcp.NewClient(&mcp.Implementation{Name: "webpprof-test", Version: "test"}, nil)
	session, err := mcpClient.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	tools := make(map[string]*mcp.Tool)
	for tool, err := range session.Tools(ctx, nil) {
		if err != nil {
			t.Fatal(err)
		}
		tools[tool.Name] = tool
	}
	for _, name := range []string{"webpprof_status", "webpprof_list_requests", "webpprof_inspect_request", "webpprof_search_events", "webpprof_wait_for_request"} {
		tool := tools[name]
		if tool == nil || tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Errorf("tool %q is absent or not read-only", name)
		}
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "webpprof_status", Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	if result.IsError {
		t.Fatalf("CallTool() result = %+v", result)
	}
	structured, ok := result.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("StructuredContent type = %T", result.StructuredContent)
	}
	if connected, _ := structured["connected"].(bool); !connected {
		t.Fatalf("StructuredContent = %+v", structured)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := <-serverErrors; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
