package pool

import (
	"runtime"
	"sync/atomic"
	"time"
)

// Process counters intentionally reset on restart. Subscription body bytes exclude
// HTTP headers, transport overhead, probes and traffic sent directly by clients.
type serviceMetrics struct {
	startedAt    time.Time
	sourceBytes  atomic.Int64
	sourceRuns   atomic.Int64
	sourceErrors atomic.Int64
	probeRuns    atomic.Int64
	probeErrors  atomic.Int64
}

type RuntimeStatistics struct {
	StartedAt       time.Time `json:"started_at"`
	UptimeSeconds   int64     `json:"uptime_seconds"`
	HeapBytes       uint64    `json:"heap_bytes"`
	Goroutines      int       `json:"goroutines"`
	SourceBodyBytes int64     `json:"source_body_bytes"`
	SourceRuns      int64     `json:"source_runs"`
	SourceErrors    int64     `json:"source_errors"`
	ProbeRuns       int64     `json:"probe_runs"`
	ProbeErrors     int64     `json:"probe_errors"`
}

func (s *Service) RuntimeStatistics() RuntimeStatistics {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	m := &s.metrics
	// 次数先于失败数递增；先读失败数再读次数，保证采样中失败数不超过次数
	sourceErrors, probeErrors := m.sourceErrors.Load(), m.probeErrors.Load()
	sourceRuns, probeRuns := m.sourceRuns.Load(), m.probeRuns.Load()
	return RuntimeStatistics{
		StartedAt: m.startedAt, UptimeSeconds: int64(time.Since(m.startedAt).Seconds()),
		HeapBytes: mem.HeapAlloc, Goroutines: runtime.NumGoroutine(),
		SourceBodyBytes: m.sourceBytes.Load(), SourceRuns: sourceRuns, SourceErrors: sourceErrors,
		ProbeRuns: probeRuns, ProbeErrors: probeErrors,
	}
}

// Public 隐藏进程内存与协程数
func (r RuntimeStatistics) Public() RuntimeStatistics {
	r.HeapBytes, r.Goroutines = 0, 0
	return r
}
