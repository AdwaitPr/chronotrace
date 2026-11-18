package metrics

import (
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

// TelemetryMetrics collects real-time ingestion rate, drops, and bounded memory stats.
type TelemetryMetrics struct {
	IngestedSpansTotal atomic.Uint64 `json:"ingested_spans_total"`
	DroppedSpansTotal  atomic.Uint64 `json:"dropped_spans_total"`
	SpansPerSec        float64       `json:"spans_per_sec"`
	HeapAllocMB        float64       `json:"heap_alloc_mb"`
	SysRSSMB           float64       `json:"sys_rss_mb"`
	lastCount          uint64
	lastTime           time.Time
	mu                 sync.RWMutex
}

var globalMetrics = &TelemetryMetrics{
	lastTime: time.Now(),
}

// Global returns the singleton metrics instance.
func Global() *TelemetryMetrics {
	return globalMetrics
}

// RecordIngested increments the count of successfully ingested spans.
func (m *TelemetryMetrics) RecordIngested(count int) {
	m.IngestedSpansTotal.Add(uint64(count))
}

// RecordDropped increments the dropped spans count.
func (m *TelemetryMetrics) RecordDropped(count int) {
	m.DroppedSpansTotal.Add(uint64(count))
}

// Snapshot calculates the current rate and memory stats and returns a snapshot.
func (m *TelemetryMetrics) Snapshot() TelemetryMetrics {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(m.lastTime).Seconds()
	current := m.IngestedSpansTotal.Load()

	if elapsed >= 0.5 {
		delta := current - m.lastCount
		m.SpansPerSec = float64(delta) / elapsed
		m.lastCount = current
		m.lastTime = now
	}

	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	m.HeapAllocMB = float64(ms.HeapAlloc) / (1024 * 1024)
	m.SysRSSMB = float64(ms.Sys) / (1024 * 1024)

	return TelemetryMetrics{
		IngestedSpansTotal: m.IngestedSpansTotal,
		DroppedSpansTotal:  m.DroppedSpansTotal,
		SpansPerSec:        m.SpansPerSec,
		HeapAllocMB:        m.HeapAllocMB,
		SysRSSMB:           m.SysRSSMB,
	}
}
