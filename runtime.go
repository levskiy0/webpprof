package webpprof

import (
	"runtime"
	"runtime/metrics"
	"time"
)

const (
	cpuTotalMetric     = "/cpu/classes/total:cpu-seconds"
	cpuIdleMetric      = "/cpu/classes/idle:cpu-seconds"
	memoryTotalMetric  = "/memory/classes/total:bytes"
	heapReleasedMetric = "/memory/classes/heap/released:bytes"
	heapObjectsMetric  = "/memory/classes/heap/objects:bytes"
	heapLiveMetric     = "/gc/heap/live:bytes"
	goroutinesMetric   = "/sched/goroutines:goroutines"
	gcCyclesMetric     = "/gc/cycles/total:gc-cycles"
)

func (p *Profiler) RuntimeStats() RuntimeStats {
	if p == nil {
		return RuntimeStats{}
	}
	now := time.Now().UTC()
	samples := []metrics.Sample{
		{Name: cpuTotalMetric},
		{Name: cpuIdleMetric},
		{Name: memoryTotalMetric},
		{Name: heapReleasedMetric},
		{Name: heapObjectsMetric},
		{Name: heapLiveMetric},
		{Name: goroutinesMetric},
		{Name: gcCyclesMetric},
	}
	metrics.Read(samples)
	values := make(map[string]metrics.Value, len(samples))
	for _, sample := range samples {
		values[sample.Name] = sample.Value
	}
	totalMemory := metricUint64(values[memoryTotalMetric])
	releasedMemory := metricUint64(values[heapReleasedMetric])
	if releasedMemory > totalMemory {
		releasedMemory = totalMemory
	}
	return RuntimeStats{
		RecordedAt:       now,
		UptimeNS:         int64(now.Sub(p.startedAt)),
		CPUSeconds:       metricFloat64(values[cpuTotalMetric]),
		CPUIdleSeconds:   metricFloat64(values[cpuIdleMetric]),
		MemoryBytes:      totalMemory - releasedMemory,
		HeapObjectsBytes: metricUint64(values[heapObjectsMetric]),
		HeapLiveBytes:    metricUint64(values[heapLiveMetric]),
		Goroutines:       metricUint64(values[goroutinesMetric]),
		GCCycles:         metricUint64(values[gcCyclesMetric]),
		GOMAXPROCS:       runtime.GOMAXPROCS(0),
	}
}

func metricFloat64(value metrics.Value) float64 {
	if value.Kind() != metrics.KindFloat64 {
		return 0
	}
	return value.Float64()
}

func metricUint64(value metrics.Value) uint64 {
	if value.Kind() != metrics.KindUint64 {
		return 0
	}
	return value.Uint64()
}
