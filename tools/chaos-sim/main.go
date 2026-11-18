package main

import (
	"flag"
	"fmt"
	"log"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chronotrace/chronotrace/shared/types"
	"github.com/chronotrace/chronotrace/tools/chaos-sim/generator"
	"github.com/chronotrace/chronotrace/tools/chaos-sim/scenarios"
	"github.com/chronotrace/chronotrace/tools/chaos-sim/topology"
)

func main() {
	scenarioName := flag.String("scenario", "canary-bad-pool", "Outage scenario (canary-bad-pool, db-lock-storm, redis-eviction-cascade)")
	mode := flag.String("mode", "live", "Execution mode: live (real-time stream) or backfill (fast historical generation)")
	rate := flag.Int("rate", 10000, "Target spans/sec throughput")
	duration := flag.Duration("duration", 10*time.Second, "Test run duration (e.g. 10s, 30s, 1m)")
	target := flag.String("target", "http://localhost:4318", "Ingest API base URL")
	backfillMinutes := flag.Int("backfill-minutes", 15, "Duration of historical timeline in minutes for backfill mode")
	concurrency := flag.Int("concurrency", 8, "Number of concurrent worker goroutines")
	flag.Parse()

	sc, err := scenarios.GetScenario(*scenarioName)
	if err != nil {
		log.Fatalf("Invalid scenario: %v", err)
	}

	emitter := generator.NewHTTPEmitter(*target)

	fmt.Println("==================================================")
	fmt.Println("🔥 ChronoTrace Chaos & Outage Simulator")
	fmt.Printf("   Scenario:    %s (%s)\n", sc.Name, sc.Title)
	fmt.Printf("   Target URL:  %s\n", *target)
	fmt.Printf("   Mode:        %s\n", *mode)
	if *mode == "live" {
		fmt.Printf("   Target Rate: %d spans/sec\n", *rate)
		fmt.Printf("   Duration:    %s\n", *duration)
		fmt.Printf("   Workers:     %d\n", *concurrency)
	} else {
		fmt.Printf("   Backfill:    %d minutes\n", *backfillMinutes)
	}
	fmt.Println("==================================================")

	if *mode == "backfill" {
		runBackfill(emitter, sc, *backfillMinutes)
		return
	}

	runLive(emitter, sc, *rate, *duration, *concurrency)
}

func runBackfill(emitter *generator.HTTPEmitter, sc *scenarios.Scenario, minutes int) {
	fmt.Printf("⚡ Generating %d-minute historical timeline...\n", minutes)
	startTime := time.Now()

	topo := topology.NewECommerceTopology()
	now := time.Now()
	timelineStart := now.Add(-time.Duration(minutes) * time.Minute)

	// Send deploy webhook 7 minutes ago
	deployTime := timelineStart.Add(time.Duration(minutes/2) * time.Minute)
	deployEvent := sc.GenerateDeployEvent(deployTime)
	if err := emitter.SendDeployEvent(&deployEvent); err != nil {
		fmt.Printf("⚠️ Warning: failed to send deploy event to ingest API (%v)\n", err)
	} else {
		fmt.Printf("📌 Emitted correlated DeployEvent pin: %s @ %s\n", deployEvent.ServiceName, deployTime.Format("15:04:05"))
	}

	outageStart := deployTime.Add(time.Duration(sc.LeadTimeSeconds) * time.Second)
	outageEnd := outageStart.Add(3 * time.Minute)

	totalSpans := 0
	batch := make([]*types.Span, 0, 500)

	// Step every 3 seconds
	step := 3 * time.Second
	for curr := timelineStart; curr.Before(now); curr = curr.Add(step) {
		isOutage := curr.After(outageStart) && curr.Before(outageEnd)
		var outParams *topology.OutageParams
		if isOutage {
			outParams = &sc.OutageParams
		}

		// 15 traces per step
		for i := 0; i < 15; i++ {
			tSpans := topo.GenerateTrace(curr.Add(time.Duration(i*50)*time.Millisecond), outParams)
			batch = append(batch, tSpans...)
			totalSpans += len(tSpans)

			if len(batch) >= 500 {
				_ = emitter.SendSpansBatch(batch)
				batch = make([]*types.Span, 0, 500)
			}
		}
	}

	if len(batch) > 0 {
		_ = emitter.SendSpansBatch(batch)
	}

	elapsed := time.Since(startTime)
	fmt.Printf("✅ Backfill Complete: %d spans generated and emitted in %s (%.0f spans/sec)\n",
		totalSpans, elapsed, float64(totalSpans)/elapsed.Seconds())
}

func runLive(emitter *generator.HTTPEmitter, sc *scenarios.Scenario, targetRate int, duration time.Duration, concurrency int) {
	// Send correlated deploy pin
	deployEvent := sc.GenerateDeployEvent(time.Now())
	if err := emitter.SendDeployEvent(&deployEvent); err != nil {
		fmt.Printf("⚠️ Warning: failed to send initial deploy event: %v\n", err)
	} else {
		fmt.Printf("📌 Emitted Deploy Pin: [%s] %s (%s)\n", deployEvent.ServiceName, deployEvent.CommitHash, deployEvent.CommitMessage)
	}

	topo := topology.NewECommerceTopology()
	spansPerTrace := 12
	targetTracesPerSec := targetRate / spansPerTrace
	if targetTracesPerSec < 1 {
		targetTracesPerSec = 1
	}

	var totalSpans atomic.Int64
	var totalErrors atomic.Int64
	stopChan := make(chan struct{})

	// Traces per worker per tick (tick = 50ms = 20 ticks/sec)
	ticksPerSec := 20
	tracesPerTick := int(math.Ceil(float64(targetTracesPerSec) / float64(ticksPerSec*concurrency)))
	if tracesPerTick < 1 {
		tracesPerTick = 1
	}

	startTime := time.Now()
	var wg sync.WaitGroup

	for w := 0; w < concurrency; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			ticker := time.NewTicker(time.Duration(1000/ticksPerSec) * time.Millisecond)
			defer ticker.Stop()

			batch := make([]*types.Span, 0, tracesPerTick*spansPerTrace)

			for {
				select {
				case <-stopChan:
					return
				case tickTime := <-ticker.C:
					batch = batch[:0]
					// Check if in outage window (e.g. after 3 seconds of test run)
					elapsedSec := time.Since(startTime).Seconds()
					var outParams *topology.OutageParams
					if elapsedSec > 3.0 {
						outParams = &sc.OutageParams
					}

					for i := 0; i < tracesPerTick; i++ {
						tSpans := topo.GenerateTrace(tickTime, outParams)
						batch = append(batch, tSpans...)
					}

					if err := emitter.SendSpansBatch(batch); err != nil {
						totalErrors.Add(1)
					} else {
						totalSpans.Add(int64(len(batch)))
					}
				}
			}
		}(w)
	}

	// Live stats reporter
	reportTicker := time.NewTicker(1 * time.Second)
	defer reportTicker.Stop()

	timer := time.NewTimer(duration)
	defer timer.Stop()

	var lastSpans int64
	var lastTime = startTime

loop:
	for {
		select {
		case <-timer.C:
			close(stopChan)
			break loop
		case t := <-reportTicker.C:
			current := totalSpans.Load()
			delta := current - lastSpans
			elapsed := t.Sub(lastTime).Seconds()
			rps := float64(delta) / elapsed
			fmt.Printf("⚡ [Live Ingestion] Total: %d spans | Current Rate: %.0f spans/sec | Network Drops: %d\n",
				current, rps, totalErrors.Load())
			lastSpans = current
			lastTime = t
		}
	}

	wg.Wait()
	totalElapsed := time.Since(startTime).Seconds()
	finalSpans := totalSpans.Load()
	avgRate := float64(finalSpans) / totalElapsed

	fmt.Println("==================================================")
	fmt.Printf("🎯 Benchmark Results:\n")
	fmt.Printf("   Duration:      %.2f seconds\n", totalElapsed)
	fmt.Printf("   Total Spans:   %d\n", finalSpans)
	fmt.Printf("   Avg Rate:      %.0f spans/sec\n", avgRate)
	fmt.Printf("   Network Drops: %d\n", totalErrors.Load())
	fmt.Println("==================================================")

	if avgRate >= 10000.0 && totalErrors.Load() == 0 {
		fmt.Println("✅ Benchmark PASSED: ≥10,000 spans/sec sustained throughput with 0 drops.")
	} else if avgRate >= 10000.0 {
		fmt.Printf("⚠️ Throughput target met (%.0f spans/sec), but %d network drops recorded.\n", avgRate, totalErrors.Load())
	}
}
