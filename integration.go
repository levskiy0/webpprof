package webpprof

import "reflect"

type Integration[T any] interface {
	Name() string
	Profile(Scope, T) T
}

type Scope struct {
	profiler *Profiler
	name     string
}

type integrationValueKey struct {
	name string
	key  any
}

func Profile[T any](value T, integration Integration[T]) T {
	return ProfileWith(Default(), value, integration)
}

func ProfileWith[T any](profiler *Profiler, value T, integration Integration[T]) T {
	if profiler == nil || integration == nil {
		return value
	}
	return integration.Profile(Scope{profiler: profiler, name: integration.Name()}, value)
}

func (s Scope) Profiler() *Profiler {
	return s.profiler
}

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
