package gocache

import (
	"context"
	"time"

	"github.com/levskiy0/webpprof"
	cachecontract "github.com/levskiy0/go-cache/contract"
)

type ProfilerGoCache struct {
	Store string
}

type Cache = cachecontract.Cache
type CacheDriver = cachecontract.Driver
type Lock = cachecontract.Lock

type profiledCache struct {
	inner    CacheDriver
	profiler *webpprof.Profiler
	store    string
	ctx      context.Context
}

type profiledCacheLock struct {
	inner Lock
	cache *profiledCache
	key   string
}

func New(stores ...string) ProfilerGoCache {
	store := "default"
	if len(stores) > 0 && stores[0] != "" {
		store = stores[0]
	}
	return ProfilerGoCache{Store: store}
}

func (ProfilerGoCache) Name() string {
	return "go-cache"
}

func (d ProfilerGoCache) Profile(scope webpprof.Scope, cache Cache) Cache {
	p := scope.Profiler()
	if p == nil || cache == nil {
		return cache
	}
	if wrapped, ok := cache.(*profiledCache); ok {
		if wrapped.profiler == p {
			return cache
		}
	}
	return &profiledCache{inner: cache, profiler: p, store: d.Store, ctx: context.Background()}
}

func Profile(cache Cache, stores ...string) Cache {
	return webpprof.Profile(cache, New(stores...))
}

func ProfileWith(profiler *webpprof.Profiler, cache Cache, stores ...string) Cache {
	return webpprof.ProfileWith(profiler, cache, New(stores...))
}

func (c *profiledCache) Add(key string, value any, ttl time.Duration) bool {
	startedAt := time.Now().UTC()
	ok := c.inner.Add(key, value, ttl)
	c.record(startedAt, "add", key, ok, ttl, nil)
	return ok
}

func (c *profiledCache) Decrement(key string, value ...int64) (int64, error) {
	startedAt := time.Now().UTC()
	result, err := c.inner.Decrement(key, value...)
	c.record(startedAt, "decrement", key, err == nil, 0, err)
	return result, err
}

func (c *profiledCache) Forever(key string, value any) bool {
	startedAt := time.Now().UTC()
	ok := c.inner.Forever(key, value)
	c.record(startedAt, "forever", key, ok, 0, nil)
	return ok
}

func (c *profiledCache) Forget(key string) bool {
	startedAt := time.Now().UTC()
	ok := c.inner.Forget(key)
	c.record(startedAt, "forget", key, ok, 0, nil)
	return ok
}

func (c *profiledCache) Flush() bool {
	startedAt := time.Now().UTC()
	ok := c.inner.Flush()
	c.record(startedAt, "flush", "", ok, 0, nil)
	return ok
}

func (c *profiledCache) Get(key string, def ...any) any {
	startedAt := time.Now().UTC()
	value := c.inner.Get(key, def...)
	c.record(startedAt, "get", key, value != nil, 0, nil)
	return value
}

func (c *profiledCache) GetBool(key string, def ...bool) bool {
	startedAt := time.Now().UTC()
	hit := c.inner.Has(key)
	value := c.inner.GetBool(key, def...)
	c.record(startedAt, "get_bool", key, hit, 0, nil)
	return value
}

func (c *profiledCache) GetInt(key string, def ...int) int {
	startedAt := time.Now().UTC()
	hit := c.inner.Has(key)
	value := c.inner.GetInt(key, def...)
	c.record(startedAt, "get_int", key, hit, 0, nil)
	return value
}

func (c *profiledCache) GetInt64(key string, def ...int64) int64 {
	startedAt := time.Now().UTC()
	hit := c.inner.Has(key)
	value := c.inner.GetInt64(key, def...)
	c.record(startedAt, "get_int64", key, hit, 0, nil)
	return value
}

func (c *profiledCache) GetString(key string, def ...string) string {
	startedAt := time.Now().UTC()
	hit := c.inner.Has(key)
	value := c.inner.GetString(key, def...)
	c.record(startedAt, "get_string", key, hit, 0, nil)
	return value
}

func (c *profiledCache) Has(key string) bool {
	startedAt := time.Now().UTC()
	hit := c.inner.Has(key)
	c.record(startedAt, "has", key, hit, 0, nil)
	return hit
}

func (c *profiledCache) Increment(key string, value ...int64) (int64, error) {
	startedAt := time.Now().UTC()
	result, err := c.inner.Increment(key, value...)
	c.record(startedAt, "increment", key, err == nil, 0, err)
	return result, err
}

func (c *profiledCache) Lock(key string, ttl ...time.Duration) Lock {
	return &profiledCacheLock{inner: c.inner.Lock(key, ttl...), cache: c, key: key}
}

func (c *profiledCache) Put(key string, value any, ttl time.Duration) error {
	startedAt := time.Now().UTC()
	err := c.inner.Put(key, value, ttl)
	c.record(startedAt, "put", key, err == nil, ttl, err)
	return err
}

func (c *profiledCache) Pull(key string, def ...any) any {
	startedAt := time.Now().UTC()
	hit := c.inner.Has(key)
	value := c.inner.Pull(key, def...)
	c.record(startedAt, "pull", key, hit, 0, nil)
	return value
}

func (c *profiledCache) Remember(key string, ttl time.Duration, callback func() (any, error)) (any, error) {
	startedAt := time.Now().UTC()
	hit := c.inner.Has(key)
	value, err := c.inner.Remember(key, ttl, callback)
	c.record(startedAt, "remember", key, hit, ttl, err)
	return value, err
}

func (c *profiledCache) RememberForever(key string, callback func() (any, error)) (any, error) {
	startedAt := time.Now().UTC()
	hit := c.inner.Has(key)
	value, err := c.inner.RememberForever(key, callback)
	c.record(startedAt, "remember_forever", key, hit, 0, err)
	return value, err
}

func (c *profiledCache) WithContext(ctx context.Context) CacheDriver {
	return &profiledCache{inner: c.inner.WithContext(ctx), profiler: c.profiler, store: c.store, ctx: ctx}
}

func (c *profiledCache) record(startedAt time.Time, operation, key string, hit bool, ttl time.Duration, err error) {
	event := webpprof.Cache{Meta: webpprof.Meta{StartedAt: startedAt, Duration: time.Since(startedAt)}, Store: c.store, Operation: operation, Key: key, Hit: hit, TTL: ttl}
	if err != nil {
		event.Error = err.Error()
	}
	c.profiler.LogCacheContext(c.ctx, event)
}

var _ webpprof.Integration[Cache] = ProfilerGoCache{}
var _ Cache = (*profiledCache)(nil)
var _ Lock = (*profiledCacheLock)(nil)

func (l *profiledCacheLock) Block(ttl time.Duration, callback ...func()) bool {
	startedAt := time.Now().UTC()
	ok := l.inner.Block(ttl, callback...)
	l.cache.record(startedAt, "lock_block", l.key, ok, ttl, nil)
	return ok
}

func (l *profiledCacheLock) Get(callback ...func()) bool {
	startedAt := time.Now().UTC()
	ok := l.inner.Get(callback...)
	l.cache.record(startedAt, "lock_get", l.key, ok, 0, nil)
	return ok
}

func (l *profiledCacheLock) Release() bool {
	startedAt := time.Now().UTC()
	ok := l.inner.Release()
	l.cache.record(startedAt, "lock_release", l.key, ok, 0, nil)
	return ok
}

func (l *profiledCacheLock) ForceRelease() bool {
	startedAt := time.Now().UTC()
	ok := l.inner.ForceRelease()
	l.cache.record(startedAt, "lock_force_release", l.key, ok, 0, nil)
	return ok
}
