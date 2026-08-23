package webpprof

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestStartOwnsSingletonServer(t *testing.T) {
	if profiler := Default(); profiler != nil {
		_ = profiler.Close()
	}
	profiler, err := Start("127.0.0.1:0", WithToken("test-token"))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = profiler.Close() })
	if Default() != profiler || URL() != profiler.URL() || profiler.URL() == "" {
		t.Fatalf("default = %p, profiler = %p, url = %q", Default(), profiler, profiler.URL())
	}

	repeated, err := Start("127.0.0.1:0")
	if err != nil || repeated != profiler {
		t.Fatalf("repeated start = %p, %v", repeated, err)
	}

	client := &http.Client{Timeout: time.Second}
	response, err := client.Get(profiler.URL())
	if err != nil {
		t.Fatalf("get UI: %v", err)
	}
	body, readErr := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if readErr != nil || response.StatusCode != http.StatusOK || !strings.Contains(string(body), "webpprof") {
		t.Fatalf("status = %d, body bytes = %d, read = %v", response.StatusCode, len(body), readErr)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if Default() != nil || Enabled() || URL() != "" {
		t.Fatal("shutdown retained the singleton")
	}
}

func TestStartRejectsMissingAuthenticationBeforeListening(t *testing.T) {
	if profiler := Default(); profiler != nil {
		_ = profiler.Close()
	}
	profiler, err := Start("127.0.0.1:0")
	if err == nil || profiler != nil || !strings.Contains(err.Error(), "authentication is required") {
		t.Fatalf("Start() = %p, %v", profiler, err)
	}
}

func TestNewReturnsExistingSingleton(t *testing.T) {
	if profiler := Default(); profiler != nil {
		_ = profiler.Close()
	}
	first := New(http.NewServeMux(), WithUnsafeUnauthenticatedAccess())
	t.Cleanup(func() { _ = first.Close() })
	second := New(http.NewServeMux(), WithBasePath("/ignored"), WithUnsafeUnauthenticatedAccess())
	if second != first || second.BasePath() != defaultBasePath {
		t.Fatalf("first = %p, second = %p, path = %q", first, second, second.BasePath())
	}
}

func TestConcurrentStartReturnsOneProfiler(t *testing.T) {
	if profiler := Default(); profiler != nil {
		_ = profiler.Close()
	}
	const callers = 16
	profilers := make(chan *Profiler, callers)
	errors := make(chan error, callers)
	var wait sync.WaitGroup
	for range callers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			profiler, err := Start("127.0.0.1:0", WithUnsafeUnauthenticatedAccess())
			profilers <- profiler
			errors <- err
		}()
	}
	wait.Wait()
	close(profilers)
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatalf("start: %v", err)
		}
	}
	profiler := Default()
	t.Cleanup(func() { _ = profiler.Close() })
	for current := range profilers {
		if current != profiler {
			t.Fatalf("current = %p, singleton = %p", current, profiler)
		}
	}
}
