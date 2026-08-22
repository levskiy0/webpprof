package webpprof

import (
	"context"
	"sort"
	"strings"
	"time"
)

type QueueStatsSource interface {
	QueueStats(context.Context) (QueueStats, error)
}

type QueueStatsSourceFunc func(context.Context) (QueueStats, error)

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

type QueueState struct {
	Name          string `json:"name"`
	WorkersActive int64  `json:"workers_active"`
	WorkersTotal  int64  `json:"workers_total"`
	Processed     uint64 `json:"processed"`
	Succeeded     uint64 `json:"succeeded"`
	Failed        uint64 `json:"failed"`
	Pending       int64  `json:"pending"`
}

type QueueStatsResponse struct {
	RecordedAt time.Time    `json:"recorded_at"`
	Sources    []QueueStats `json:"sources"`
}

func (f QueueStatsSourceFunc) QueueStats(ctx context.Context) (QueueStats, error) {
	return f(ctx)
}

func RegisterQueueStats(source QueueStatsSource, names ...string) QueueStatsSource {
	return Default().RegisterQueueStats(source, names...)
}

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
