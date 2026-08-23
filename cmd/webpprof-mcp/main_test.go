package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestParseConfigUsesEnvironmentAndFlags(t *testing.T) {
	t.Parallel()

	environment := map[string]string{
		"WEBPPROF_URL":   "http://localhost:6061/debug/webpprof/",
		"WEBPPROF_TOKEN": "secret",
	}
	configuration, err := parseConfig(
		[]string{"--url", "http://127.0.0.1:3030/debug/webpprof/"},
		func(key string) string { return environment[key] },
		&bytes.Buffer{},
	)
	if err != nil {
		t.Fatalf("parseConfig() error = %v", err)
	}
	if configuration.profilerURL != "http://127.0.0.1:3030/debug/webpprof/" {
		t.Fatalf("profiler URL = %q", configuration.profilerURL)
	}
	if configuration.token != "secret" {
		t.Fatalf("token = %q, want environment value", configuration.token)
	}
}

func TestValidateProfilerURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		url         string
		allowRemote bool
		wantError   bool
	}{
		{name: "IPv4 loopback", url: "http://127.0.0.1:6061/debug/webpprof/"},
		{name: "IPv6 loopback", url: "http://[::1]:6061/debug/webpprof/"},
		{name: "localhost", url: "http://localhost:6061/debug/webpprof/"},
		{name: "localhost trailing dot", url: "http://localhost.:6061/debug/webpprof/"},
		{name: "remote rejected", url: "https://profiler.internal/debug/webpprof/", wantError: true},
		{name: "remote HTTPS allowed", url: "https://profiler.internal/debug/webpprof/", allowRemote: true},
		{name: "remote HTTP rejected", url: "http://profiler.internal/debug/webpprof/", allowRemote: true, wantError: true},
		{name: "localhost lookalike rejected", url: "http://localhost.example/debug/webpprof/", wantError: true},
		{name: "unsupported scheme", url: "file://localhost/tmp/webpprof", wantError: true},
		{name: "missing host", url: "http:///debug/webpprof/", wantError: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := validateProfilerURL(test.url, test.allowRemote)
			if test.wantError && err == nil {
				t.Fatalf("validateProfilerURL(%q) succeeded, want error", test.url)
			}
			if !test.wantError && err != nil {
				t.Fatalf("validateProfilerURL(%q) error = %v", test.url, err)
			}
		})
	}
}

func TestRunPrintsVersionToStdout(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	clientTransport, _ := mcp.NewInMemoryTransports()
	err := run(context.Background(), []string{"--version"}, func(string) string { return "" }, &stdout, &stderr, clientTransport)
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if strings.TrimSpace(stdout.String()) != effectiveVersion() {
		t.Fatalf("stdout = %q, want version %q", stdout.String(), effectiveVersion())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestRunHelpSucceedsWithoutStartingServer(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	clientTransport, _ := mcp.NewInMemoryTransports()
	err := run(context.Background(), []string{"--help"}, func(string) string { return "" }, &stdout, &stderr, clientTransport)
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if !strings.Contains(stderr.String(), "Usage: webpprof-mcp") {
		t.Fatalf("stderr = %q, want usage", stderr.String())
	}
}
