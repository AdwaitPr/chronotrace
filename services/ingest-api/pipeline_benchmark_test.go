package main

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/chronotrace/chronotrace/services/ingest-api/otlp"
	"github.com/chronotrace/chronotrace/shared/bus"
	"github.com/chronotrace/chronotrace/shared/store"
	"github.com/chronotrace/chronotrace/shared/types"
	"github.com/chronotrace/chronotrace/tools/chaos-sim/topology"
)

func BenchmarkIngestPipeline(b *testing.B) {
	eventBus := bus.NewInMemoryEventBus(200000)
	defer eventBus.Close()

	ringStore := store.NewInMemoryTelemetryStore(250000)
	defer ringStore.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Consumer pipe that flushes from eventBus into ringStore
	var receivedCount int64
	var mu sync.Mutex

	err := eventBus.SubscribeSpans(ctx, func(span *types.Span) error {
		_ = ringStore.WriteSpans(ctx, []*types.Span{span})
		mu.Lock()
		receivedCount++
		mu.Unlock()
		return nil
	})
	if err != nil {
		b.Fatalf("failed to subscribe: %v", err)
	}

	topo := topology.NewECommerceTopology()
	now := time.Now()

	// Pre-generate traces
	traces := make([][]*types.Span, 1000)
	for i := 0; i < 1000; i++ {
		traces[i] = topo.GenerateTrace(now.Add(time.Duration(i)*time.Millisecond), nil)
	}

	b.ResetTimer()
	start := time.Now()

	totalSpans := 0
	for i := 0; i < b.N; i++ {
		tSpans := traces[i%1000]
		for _, s := range tSpans {
			_ = eventBus.PublishSpan(ctx, s)
			totalSpans++
		}
	}

	elapsed := time.Since(start)
	rate := float64(totalSpans) / elapsed.Seconds()

	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	rssMB := float64(ms.Sys) / (1024 * 1024)
	heapMB := float64(ms.HeapAlloc) / (1024 * 1024)

	b.ReportMetric(rate, "spans/sec")
	b.ReportMetric(heapMB, "heap_MB")
	b.ReportMetric(rssMB, "sys_MB")

	fmt.Printf("\n⚡ [Benchmark Results] %d spans processed in %s (%.0f spans/sec) | Heap: %.2f MB | Sys: %.2f MB\n",
		totalSpans, elapsed, rate, heapMB, rssMB)

	if rssMB > 128.0 {
		b.Errorf("memory exceeded limit: %.2f MB > 128 MB", rssMB)
	}
}

func TestSustainedHighThroughput_EmbeddedPipeline(t *testing.T) {
	eventBus := bus.NewInMemoryEventBus(200000)
	defer eventBus.Close()

	ringStore := store.NewInMemoryTelemetryStore(250000)
	defer ringStore.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var processedCount int64
	var mu sync.Mutex

	_ = eventBus.SubscribeSpans(ctx, func(span *types.Span) error {
		_ = ringStore.WriteSpans(ctx, []*types.Span{span})
		mu.Lock()
		processedCount++
		mu.Unlock()
		return nil
	})

	topo := topology.NewECommerceTopology()
	targetSpans := 50000 // 50,000 spans test
	startTime := time.Now()

	now := time.Now()
	for i := 0; i < targetSpans/12; i++ {
		spans := topo.GenerateTrace(now.Add(time.Duration(i)*time.Millisecond), nil)
		for _, s := range spans {
			_ = eventBus.PublishSpan(ctx, s)
		}
	}

	// Wait briefly for consumer to catch up
	time.Sleep(100 * time.Millisecond)

	elapsed := time.Since(startTime)
	rate := float64(targetSpans) / elapsed.Seconds()

	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	rssMB := float64(ms.Sys) / (1024 * 1024)

	t.Logf("Processed %d spans in %s (%.0f spans/sec) | RSS: %.2f MB", targetSpans, elapsed, rate, rssMB)

	if rate < 10000.0 {
		t.Errorf("throughput below target: %.0f spans/sec < 10,000 spans/sec", rate)
	}
	if rssMB > 128.0 {
		t.Errorf("memory exceeded bound: %.2f MB > 128 MB", rssMB)
	}
}

func TestSensitiveScrubbingInPipeline(t *testing.T) {
	s := otlp.NormalizeRaw("tr-1", "sp-1", "", "auth", "login", "SERVER", 100, 200, "OK", "", map[string]string{
		"Authorization": "Bearer supersecret",
		"Cookie":        "auth=xyz",
		"X-Api-Key":     "key123",
		"tenant_id":     "acme",
	})

	if _, ok := s.Attributes["Authorization"]; ok {
		t.Errorf("expected Authorization scrubbed")
	}
	if _, ok := s.Attributes["Cookie"]; ok {
		t.Errorf("expected Cookie scrubbed")
	}
	if _, ok := s.Attributes["X-Api-Key"]; ok {
		t.Errorf("expected X-Api-Key scrubbed")
	}
	if s.Attributes["tenant_id"] != "acme" {
		t.Errorf("expected tenant_id preserved")
	}
}
