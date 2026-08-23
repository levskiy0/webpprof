package webpprof

import (
	"net/http"
	"testing"
)

type testIntegration struct {
	name string
}

func (d testIntegration) Name() string {
	return d.name
}

func (d testIntegration) Profile(scope Scope, value string) string {
	if actual, loaded := scope.LoadOrStore(value, value+"-profiled"); loaded {
		return actual.(string)
	}
	return value + "-profiled"
}

func TestProfileUsesIntegrationScope(t *testing.T) {
	profiler := New(http.NewServeMux(), WithUnsafeUnauthenticatedAccess())
	t.Cleanup(func() { _ = profiler.Close() })
	integration := testIntegration{name: "test"}
	if got := Profile("client", integration); got != "client-profiled" {
		t.Fatalf("profiled value = %q", got)
	}
	if got := ProfileWith(profiler, "client", integration); got != "client-profiled" {
		t.Fatalf("idempotent value = %q", got)
	}
	if got := ProfileWith[string](nil, "client", integration); got != "client" {
		t.Fatalf("disabled value = %q", got)
	}
	scope := Scope{profiler: profiler, name: "test"}
	if actual, loaded := scope.LoadOrStore([]string{"not", "comparable"}, "ignored"); loaded || actual != "ignored" {
		t.Fatalf("uncomparable key result = %v, %v", actual, loaded)
	}
}
