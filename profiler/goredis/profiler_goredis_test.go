package goredis

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/levskiy0/webpprof"
	"github.com/redis/go-redis/v9"
)

type testClient struct {
	hooks []redis.Hook
}

func (c *testClient) AddHook(hook redis.Hook) {
	c.hooks = append(c.hooks, hook)
}

func TestProfileInstallsOneHookAndRecordsMiss(t *testing.T) {
	mux := http.NewServeMux()
	profiler := webpprof.New(mux)
	t.Cleanup(func() { _ = profiler.Close() })
	client := &testClient{}
	ProfileWith(profiler, client, "sessions")
	ProfileWith(profiler, client, "sessions")
	if len(client.hooks) != 1 {
		t.Fatalf("hooks = %d", len(client.hooks))
	}
	ctx := context.Background()
	command := redis.NewStringCmd(ctx, "get", "session:42")
	err := client.hooks[0].ProcessHook(func(context.Context, redis.Cmder) error { return redis.Nil })(ctx, command)
	if !errors.Is(err, redis.Nil) {
		t.Fatalf("error = %v", err)
	}
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/debug/webpprof/api/events?kind=cache&limit=10", nil))
	var response struct {
		Events []webpprof.Entry `json:"events"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Events) != 1 {
		t.Fatalf("events = %+v", response.Events)
	}
	var event webpprof.Cache
	if err := json.Unmarshal(response.Events[0].Data, &event); err != nil {
		t.Fatal(err)
	}
	if event.Store != "sessions" || event.Key != "session:42" || event.Hit || event.Error != "" {
		t.Fatalf("cache = %+v", event)
	}
}
