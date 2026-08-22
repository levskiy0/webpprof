package goredis

import (
	"context"
	"net"
	"strings"
	"time"

	"github.com/levskiy0/webpprof"
	"github.com/redis/go-redis/v9"
)

type ProfilerGoRedis struct {
	Store string
}

type RedisHookRegistrar interface {
	AddHook(redis.Hook)
}

type redisProfilerHook struct {
	profiler *webpprof.Profiler
	store    string
}

func New(stores ...string) ProfilerGoRedis {
	store := "redis"
	if len(stores) > 0 && stores[0] != "" {
		store = stores[0]
	}
	return ProfilerGoRedis{Store: store}
}

func (ProfilerGoRedis) Name() string {
	return "go-redis"
}

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

func Profile(client RedisHookRegistrar, stores ...string) RedisHookRegistrar {
	return webpprof.Profile(client, New(stores...))
}

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
