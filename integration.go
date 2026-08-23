package webpprof

import "reflect"

// Integration describes an adapter that instruments values of type T.
// Name scopes cached wrappers; Profile may return value unchanged when the
// underlying dependency cannot be wrapped.
type Integration[T any] interface {
	// Name returns the stable cache namespace for this integration.
	Name() string
	// Profile instruments value using the supplied profiler scope.
	Profile(Scope, T) T
}

// Scope gives an integration access to the active profiler and a namespaced,
// concurrency-safe cache for reusing wrappers.
type Scope struct {
	profiler *Profiler
	name     string
}

type integrationValueKey struct {
	name string
	key  any
}

// Profile instruments value with the default profiler. It returns value
// unchanged when profiling is disabled or integration is nil.
func Profile[T any](value T, integration Integration[T]) T {
	return ProfileWith(Default(), value, integration)
}

// ProfileWith instruments value with an explicit profiler. It returns value
// unchanged when profiler or integration is nil.
func ProfileWith[T any](profiler *Profiler, value T, integration Integration[T]) T {
	if profiler == nil || integration == nil {
		return value
	}
	return integration.Profile(Scope{profiler: profiler, name: integration.Name()}, value)
}

// Profiler returns the profiler associated with this integration call.
func (s Scope) Profiler() *Profiler {
	return s.profiler
}

// Load retrieves a value previously cached by this integration. Non-comparable
// keys are rejected and return no value.
func (s Scope) Load(key any) (any, bool) {
	if s.profiler == nil {
		return nil, false
	}
	valueKey, ok := s.valueKey(key)
	if !ok {
		return nil, false
	}
	return s.profiler.integrationValues.Load(valueKey)
}

// Store caches value under a key scoped to this integration. It ignores
// non-comparable keys and inactive scopes.
func (s Scope) Store(key, value any) {
	if s.profiler == nil {
		return
	}
	valueKey, ok := s.valueKey(key)
	if !ok {
		return
	}
	s.profiler.integrationValues.Store(valueKey, value)
}

// LoadOrStore returns an existing scoped value when present or stores value.
// The loaded result follows sync.Map semantics.
func (s Scope) LoadOrStore(key, value any) (any, bool) {
	if s.profiler == nil {
		return value, false
	}
	valueKey, ok := s.valueKey(key)
	if !ok {
		return value, false
	}
	return s.profiler.integrationValues.LoadOrStore(valueKey, value)
}

func (s Scope) valueKey(key any) (integrationValueKey, bool) {
	if key != nil && !reflect.TypeOf(key).Comparable() {
		return integrationValueKey{}, false
	}
	return integrationValueKey{name: s.name, key: key}, true
}
