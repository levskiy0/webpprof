package main

import (
	"context"

	"github.com/levskiy0/webpprof/internal/mcpserver"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const serverInstructions = "Use webpprof to inspect captured requests and standalone executions from the running Go application. Start with webpprof_status, webpprof_list_requests, or webpprof_search_events, then inspect a request or event by ID. All tools are read-only. Captured payloads and stacks are omitted unless include_payloads is explicitly enabled."

func newServer(service *mcpserver.Service, releaseVersion string) *mcp.Server {
	if releaseVersion == "" {
		releaseVersion = "dev"
	}
	server := mcp.NewServer(
		&mcp.Implementation{Name: "webpprof-mcp", Version: releaseVersion},
		&mcp.ServerOptions{Instructions: serverInstructions, Capabilities: &mcp.ServerCapabilities{}},
	)
	annotations := readOnlyAnnotations()

	mcp.AddTool(server, &mcp.Tool{Name: "webpprof_status", Title: "Webpprof status", Description: "Check connectivity and return capture capacity, retention, storage, sampling, and cursor information.", Annotations: annotations}, func(ctx context.Context, _ *mcp.CallToolRequest, input mcpserver.StatusInput) (*mcp.CallToolResult, mcpserver.StatusOutput, error) {
		output, err := service.Status(ctx, input)
		return nil, output, err
	})
	mcp.AddTool(server, &mcp.Tool{Name: "webpprof_list_requests", Title: "List captured requests", Description: "List recent captured HTTP requests, newest first. Filter by method, path, status, duration, tags, or pagination cursor.", Annotations: annotations}, func(ctx context.Context, _ *mcp.CallToolRequest, input mcpserver.ListRequestsInput) (*mcp.CallToolResult, mcpserver.ListRequestsOutput, error) {
		output, err := service.ListRequests(ctx, input)
		return nil, output, err
	})
	mcp.AddTool(server, &mcp.Tool{Name: "webpprof_inspect_request", Title: "Inspect captured request", Description: "Return one request, automatic performance findings, event counts, and a bounded correlated timeline. Payloads and stacks are omitted by default.", Annotations: annotations}, func(ctx context.Context, _ *mcp.CallToolRequest, input mcpserver.InspectRequestInput) (*mcp.CallToolResult, mcpserver.InspectRequestOutput, error) {
		output, err := service.InspectRequest(ctx, input)
		return nil, output, err
	})
	mcp.AddTool(server, &mcp.Tool{Name: "webpprof_inspect_event", Title: "Inspect execution event", Description: "Return one standalone execution root, such as a schedule, callable, or task, with automatic findings, event counts, and its bounded ParentID hierarchy. Payloads and stacks are omitted by default.", Annotations: annotations}, func(ctx context.Context, _ *mcp.CallToolRequest, input mcpserver.InspectEventInput) (*mcp.CallToolResult, mcpserver.InspectEventOutput, error) {
		output, err := service.InspectEvent(ctx, input)
		return nil, output, err
	})
	mcp.AddTool(server, &mcp.Tool{Name: "webpprof_search_events", Title: "Search captured events", Description: "Search bounded profiler events by text, kind, request ID, execution scope, tags, and cursor. Returns compact summaries without payload bodies.", Annotations: annotations}, func(ctx context.Context, _ *mcp.CallToolRequest, input mcpserver.SearchEventsInput) (*mcp.CallToolResult, mcpserver.SearchEventsOutput, error) {
		output, err := service.SearchEvents(ctx, input)
		return nil, output, err
	})
	mcp.AddTool(server, &mcp.Tool{Name: "webpprof_wait_for_request", Title: "Wait for captured request", Description: "Wait up to 120 seconds for a newly captured request matching method, path, status, duration, and cursor filters.", Annotations: annotations}, func(ctx context.Context, _ *mcp.CallToolRequest, input mcpserver.WaitForRequestInput) (*mcp.CallToolResult, mcpserver.WaitForRequestOutput, error) {
		output, err := service.WaitForRequest(ctx, input)
		return nil, output, err
	})
	return server
}

func readOnlyAnnotations() *mcp.ToolAnnotations {
	destructive := false
	openWorld := false
	return &mcp.ToolAnnotations{ReadOnlyHint: true, DestructiveHint: &destructive, IdempotentHint: true, OpenWorldHint: &openWorld}
}
