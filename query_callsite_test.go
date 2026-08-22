package webpprof

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestQueryCallsiteCaptureAndSourceLink(t *testing.T) {
	profiler := newProfiler(WithSourceLink(func(frame SourceFrame) string {
		return "vscode://file/" + frame.File
	}))
	t.Cleanup(func() { _ = profiler.Close() })
	recordTestQuery(profiler, "query-callsite")

	entry, ok := profiler.store.get("query-callsite")
	if !ok {
		t.Fatal("query was not recorded")
	}
	var query Query
	if err := json.Unmarshal(entry.Data, &query); err != nil {
		t.Fatal(err)
	}
	if len(query.Callsite) == 0 || !strings.HasSuffix(query.Callsite[0].File, "query_callsite_test.go") {
		t.Fatalf("callsite = %+v", query.Callsite)
	}
	if !strings.HasPrefix(query.Callsite[0].URL, "vscode://file/") {
		t.Fatalf("source URL = %q", query.Callsite[0].URL)
	}
}

func TestQueryCallsiteCanBeDisabled(t *testing.T) {
	profiler := newProfiler(WithQueryCallsite(false))
	t.Cleanup(func() { _ = profiler.Close() })
	recordTestQuery(profiler, "query-without-callsite")

	entry, ok := profiler.store.get("query-without-callsite")
	if !ok {
		t.Fatal("query was not recorded")
	}
	var query Query
	if err := json.Unmarshal(entry.Data, &query); err != nil {
		t.Fatal(err)
	}
	if len(query.Callsite) != 0 {
		t.Fatalf("callsite = %+v", query.Callsite)
	}
}

//go:noinline
func recordTestQuery(profiler *Profiler, id string) {
	profiler.LogQuery(Query{Meta: Meta{ID: id}, SQL: "SELECT 1"})
}
