// Package goredis records go-redis commands and pipelines as webpprof cache
// operations without capturing command values.
package goredis

import (
	"context"
	"net"
	"strings"
	"time"

	"github.com/levskiy0/webpprof"
	"github.com/redis/go-redis/v9"
)

// ProfilerGoRedis implements webpprof.Integration for go-redis clients.
type ProfilerGoRedis struct {
	Store string
}

// RedisHookRegistrar is the subset of a go-redis client needed to install the
// profiler hook.
type RedisHookRegistrar interface {
	// AddHook appends a Redis hook to the client.
	AddHook(redis.Hook)
}

type redisProfilerHook struct {
	profiler *webpprof.Profiler
	store    string
}

// New creates a Redis integration. The first non-empty store label is used in
// captured entries and defaults to "redis".
func New(stores ...string) ProfilerGoRedis {
	store := "redis"
	if len(stores) > 0 && stores[0] != "" {
		store = stores[0]
	}
	return ProfilerGoRedis{Store: store}
}

// Name returns the integration cache namespace.
func (ProfilerGoRedis) Name() string {
	return "go-redis"
}

// Profile installs one profiler hook on client for the current scope. Repeated
// calls for the same client and profiler are idempotent.
func (d ProfilerGoRedis) Profile(scope webpprof.Scope, client RedisHookRegistrar) RedisHookRegistrar {
	p := scope.Profiler()
	if p == nil || client == nil {
		return client
	}
	if _, loaded := scope.LoadOrStore(client, struct{}{}); loaded {
		return client
	}
	client.AddHook(&redisProfilerHook{profiler: p, store: d.Store})
	return client
}

// Profile instruments client with the default profiler.
func Profile(client RedisHookRegistrar, stores ...string) RedisHookRegistrar {
	return webpprof.Profile(client, New(stores...))
}

// ProfileWith instruments client with an explicit profiler.
func ProfileWith(profiler *webpprof.Profiler, client RedisHookRegistrar, stores ...string) RedisHookRegistrar {
	return webpprof.ProfileWith(profiler, client, New(stores...))
}

func (h *redisProfilerHook) DialHook(next redis.DialHook) redis.DialHook {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		return next(ctx, network, addr)
	}
}

func (h *redisProfilerHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, command redis.Cmder) error {
		startedAt := time.Now().UTC()
		err := next(ctx, command)
		h.record(ctx, startedAt, command, err)
		return err
	}
}

func (h *redisProfilerHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, commands []redis.Cmder) error {
		startedAt := time.Now().UTC()
		err := next(ctx, commands)
		for _, command := range commands {
			h.record(ctx, startedAt, command, command.Err())
		}
		return err
	}
}

func (h *redisProfilerHook) record(ctx context.Context, startedAt time.Time, command redis.Cmder, err error) {
	operation := strings.ToLower(command.Name())
	event := webpprof.Cache{Meta: webpprof.Meta{StartedAt: startedAt, Duration: time.Since(startedAt)}, Store: h.store, Operation: operation, Key: redisCommandKey(command), Hit: err == nil}
	if err != nil && err != redis.Nil {
		event.Error = err.Error()
	}
	h.profiler.LogCacheContext(ctx, event)
}

var _ webpprof.Integration[RedisHookRegistrar] = ProfilerGoRedis{}
var _ redis.Hook = (*redisProfilerHook)(nil)

func redisCommandKey(command redis.Cmder) string {
	args := command.Args()
	if len(args) < 2 {
		return ""
	}
	key, _ := args[1].(string)
	return key
}
