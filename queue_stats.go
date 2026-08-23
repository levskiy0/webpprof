package webpprof

import (
	"context"
	"sort"
	"strings"
	"time"
)

// QueueStatsSource provides a snapshot of one queue backend. Implementations
// should honor cancellation and deadlines from ctx.
type QueueStatsSource interface {
	// QueueStats collects the current queue snapshot.
	QueueStats(context.Context) (QueueStats, error)
}

// QueueStatsSourceFunc adapts a function to QueueStatsSource.
type QueueStatsSourceFunc func(context.Context) (QueueStats, error)

// QueueStats contains aggregate worker and job counters from one registered
// queue source.
type QueueStats struct {
	Source        string       `json:"source"`
	RecordedAt    time.Time    `json:"recorded_at"`
	StartedAt     time.Time    `json:"started_at,omitempty"`
	WorkersActive int64        `json:"workers_active"`
	WorkersTotal  int64        `json:"workers_total"`
	Processed     uint64       `json:"processed"`
	Succeeded     uint64       `json:"succeeded"`
	Failed        uint64       `json:"failed"`
	Pending       int64        `json:"pending"`
	Queues        []QueueState `json:"queues"`
	Error         string       `json:"error,omitempty"`
}

// QueueState contains worker and job counters for one named queue.
type QueueState struct {
	Name          string `json:"name"`
	WorkersActive int64  `json:"workers_active"`
	WorkersTotal  int64  `json:"workers_total"`
	Processed     uint64 `json:"processed"`
	Succeeded     uint64 `json:"succeeded"`
	Failed        uint64 `json:"failed"`
	Pending       int64  `json:"pending"`
}

// QueueStatsResponse combines snapshots from every registered queue source.
type QueueStatsResponse struct {
	RecordedAt time.Time    `json:"recorded_at"`
	Sources    []QueueStats `json:"sources"`
}

// QueueStats calls f with ctx.
func (f QueueStatsSourceFunc) QueueStats(ctx context.Context) (QueueStats, error) {
	return f(ctx)
}

// RegisterQueueStats registers source on the default profiler and returns it for
// convenient inline wrapping. The optional first non-blank name identifies it.
func RegisterQueueStats(source QueueStatsSource, names ...string) QueueStatsSource {
	return Default().RegisterQueueStats(source, names...)
}

// RegisterQueueStats registers source on this profiler and returns it. A later
// source with the same name replaces the earlier registration.
func (p *Profiler) RegisterQueueStats(source QueueStatsSource, names ...string) QueueStatsSource {
	if p == nil || source == nil {
		return source
	}
	name := "default"
	if len(names) > 0 && strings.TrimSpace(names[0]) != "" {
		name = strings.TrimSpace(names[0])
	}
	p.queueStatsMu.Lock()
	if p.queueStats == nil {
		p.queueStats = make(map[string]QueueStatsSource)
	}
	p.queueStats[name] = source
	p.queueStatsMu.Unlock()
	return source
}

// QueueStats collects registered sources in name order under the configured
// aggregate timeout. Source errors are embedded in the corresponding snapshot.
func (p *Profiler) QueueStats(ctx context.Context) QueueStatsResponse {
	response := QueueStatsResponse{RecordedAt: time.Now().UTC(), Sources: []QueueStats{}}
	if p == nil {
		return response
	}
	if ctx == nil {
		ctx = context.Background()
	}
	p.queueStatsMu.RLock()
	names := make([]string, 0, len(p.queueStats))
	sources := make(map[string]QueueStatsSource, len(p.queueStats))
	for name, source := range p.queueStats {
		names = append(names, name)
		sources[name] = source
	}
	p.queueStatsMu.RUnlock()
	sort.Strings(names)
	ctx, cancel := context.WithTimeout(ctx, p.config.queueTimeout)
	defer cancel()
	for _, name := range names {
		stats, err := sources[name].QueueStats(ctx)
		stats.Source = name
		stats.RecordedAt = response.RecordedAt
		if err != nil {
			stats.Error = err.Error()
		}
		response.Sources = append(response.Sources, stats)
	}
	return response
}
