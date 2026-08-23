package webpprof

import "context"

// EntryStorage persists the bounded event window outside the core package.
// Calls are serialized. Implementations must preserve the supplied monotonic
// cursor across restarts. Once selected by WithStorage, the active storage is
// owned and closed by Profiler.Close.
type EntryStorage interface {
	// Name identifies the backend in profiler storage statistics.
	Name() string
	// Load restores entries in ascending cursor order and the last cursor.
	Load(context.Context) ([]Entry, uint64, error)
	// Put inserts or replaces an entry and persists the latest cursor.
	Put(context.Context, Entry, uint64) error
	// Delete removes an entry evicted from the bounded window.
	Delete(context.Context, string) error
	// Clear removes all entries while preserving the supplied cursor.
	Clear(context.Context, uint64) error
	// Close releases resources held by the backend.
	Close() error
}
