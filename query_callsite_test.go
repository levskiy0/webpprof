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

func TestWithCallsiteKindsCapturesSelectedEntities(t *testing.T) {
	tests := []struct {
		name string
		kind Kind
		log  func(*Profiler, string)
	}{
		{name: "query", kind: KindQuery, log: func(p *Profiler, id string) { p.LogQuery(Query{Meta: Meta{ID: id}, SQL: "SELECT 1"}) }},
		{name: "cache", kind: KindCache, log: func(p *Profiler, id string) { p.LogCache(Cache{Meta: Meta{ID: id}, Key: "player:1"}) }},
		{name: "email", kind: KindEmail, log: func(p *Profiler, id string) { p.LogEmail(Email{Meta: Meta{ID: id}, Subject: "Welcome"}) }},
		{name: "job", kind: KindJob, log: func(p *Profiler, id string) { p.LogJob(Job{Meta: Meta{ID: id}, Name: "refresh-player"}) }},
		{name: "http call", kind: KindHTTPCall, log: func(p *Profiler, id string) {
			p.LogHTTPCall(HTTPCall{Meta: Meta{ID: id}, Method: "GET", URL: "https://example.test"})
		}},
		{name: "schedule", kind: KindSchedule, log: func(p *Profiler, id string) { p.LogSchedule(Schedule{Meta: Meta{ID: id}, Name: "cleanup"}) }},
	}

	kinds := make([]Kind, 0, len(tests))
	for _, test := range tests {
		kinds = append(kinds, test.kind)
	}
	profiler := newProfiler(WithCallsiteKinds(kinds...))
	t.Cleanup(func() { _ = profiler.Close() })

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			id := "callsite-" + string(test.kind)
			test.log(profiler, id)

			entry, ok := profiler.store.get(id)
			if !ok {
				t.Fatalf("%s was not recorded", test.kind)
			}
			var payload struct {
				Callsite []SourceFrame `json:"callsite"`
			}
			if err := json.Unmarshal(entry.Data, &payload); err != nil {
				t.Fatal(err)
			}
			if len(payload.Callsite) == 0 || !strings.HasSuffix(payload.Callsite[0].File, "query_callsite_test.go") {
				t.Fatalf("callsite = %+v", payload.Callsite)
			}
		})
	}
}

func TestWithCallsiteKindsReplacesDefaultQueryCapture(t *testing.T) {
	profiler := newProfiler(WithCallsiteKinds(KindCache, KindCache, KindLog))
	t.Cleanup(func() { _ = profiler.Close() })

	recordTestQuery(profiler, "query-not-selected")
	profiler.LogCache(Cache{Meta: Meta{ID: "cache-selected"}, Key: "player:1"})

	for _, test := range []struct {
		name         string
		id           string
		wantCallsite bool
	}{
		{name: "default query removed", id: "query-not-selected", wantCallsite: false},
		{name: "selected cache captured", id: "cache-selected", wantCallsite: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			entry, ok := profiler.store.get(test.id)
			if !ok {
				t.Fatalf("entry %q was not recorded", test.id)
			}
			var payload struct {
				Callsite []SourceFrame `json:"callsite"`
			}
			if err := json.Unmarshal(entry.Data, &payload); err != nil {
				t.Fatal(err)
			}
			if got := len(payload.Callsite) > 0; got != test.wantCallsite {
				t.Fatalf("callsite captured = %v, want %v", got, test.wantCallsite)
			}
		})
	}
}

func TestCallsiteInfrastructureFrames(t *testing.T) {
	for _, test := range []struct {
		name     string
		kind     Kind
		function string
		want     bool
	}{
		{name: "profiled cache receiver", kind: KindCache, function: "github.com/levskiy0/webpprof/profiler/gocache.(*profiledCache).Get", want: true},
		{name: "redis profiler receiver", kind: KindCache, function: "github.com/levskiy0/webpprof/profiler/goredis.(*redisProfilerHook).record", want: true},
		{name: "exported profiler receiver", kind: KindSchedule, function: "github.com/levskiy0/webpprof/profiler/schedule.ProfilerSchedule.Profile.func1", want: true},
		{name: "integration application test", kind: KindQuery, function: "github.com/levskiy0/webpprof/profiler/sql.TestProfilerSQLConnectorRecordsContextQuery", want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := isCallsiteInfrastructureFrame(test.kind, test.function); got != test.want {
				t.Fatalf("isCallsiteInfrastructureFrame() = %v, want %v", got, test.want)
			}
		})
	}
}

//go:noinline
func recordTestQuery(profiler *Profiler, id string) {
	profiler.LogQuery(Query{Meta: Meta{ID: id}, SQL: "SELECT 1"})
}
