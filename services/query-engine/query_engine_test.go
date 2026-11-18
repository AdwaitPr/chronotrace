package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"sort"
	"testing"
	"time"

	"github.com/chronotrace/chronotrace/services/query-engine/api"
	"github.com/chronotrace/chronotrace/services/query-engine/causal"
	"github.com/chronotrace/chronotrace/services/query-engine/timetravel"
	"github.com/chronotrace/chronotrace/shared/store"
	"github.com/chronotrace/chronotrace/shared/types"
	"github.com/chronotrace/chronotrace/tools/chaos-sim/scenarios"
	"github.com/chronotrace/chronotrace/tools/chaos-sim/topology"
	"github.com/gofiber/fiber/v2"
)

func TestQueryEngine_APIRoutes(t *testing.T) {
	st := store.NewInMemoryTelemetryStore(10000)
	defer st.Close()
	kf := store.NewInMemoryKeyframeStore()
	defer kf.Close()
	cs := causal.NewCausalScorer(st)
	rec := timetravel.NewStateReconstructor(st, kf, cs)
	meta, err := store.NewSQLiteMetadataStore(":memory:")
	if err != nil {
		t.Fatalf("failed to init sqlite: %v", err)
	}
	defer meta.Close()

	app := fiber.New()
	api.RegisterRoutes(app, st, rec, meta)

	// 1. GET /api/timeline/overview
	reqOverview := httptest.NewRequest("GET", "/api/timeline/overview?buckets=100", nil)
	respOverview, err := app.Test(reqOverview)
	if err != nil {
		t.Fatalf("GET /api/timeline/overview failed: %v", err)
	}
	if respOverview.StatusCode != 200 {
		t.Errorf("expected 200, got %d", respOverview.StatusCode)
	}
	body, _ := io.ReadAll(respOverview.Body)
	var overviewResp api.TimelineOverviewResponse
	if err := json.Unmarshal(body, &overviewResp); err != nil {
		t.Fatalf("failed to unmarshal overview: %v", err)
	}
	if len(overviewResp.Buckets) != 100 {
		t.Errorf("expected 100 buckets, got %d", len(overviewResp.Buckets))
	}

	// 2. GET /api/state
	reqState := httptest.NewRequest("GET", "/api/state", nil)
	respState, err := app.Test(reqState)
	if err != nil {
		t.Fatalf("GET /api/state failed: %v", err)
	}
	if respState.StatusCode != 200 {
		t.Errorf("expected 200, got %d", respState.StatusCode)
	}

	// 3. POST & GET /api/notes
	noteJSON := `{"incident_id":"inc-101","title":"Postmortem Title","author":"sre","content":"Root cause identified"}`
	reqNote := httptest.NewRequest("POST", "/api/notes", bytes.NewReader([]byte(noteJSON)))
	reqNote.Header.Set("Content-Type", "application/json")
	respNote, err := app.Test(reqNote)
	if err != nil || respNote.StatusCode != 201 {
		t.Fatalf("POST /api/notes failed (status: %d)", respNote.StatusCode)
	}

	reqGetNotes := httptest.NewRequest("GET", "/api/notes?incident_id=inc-101", nil)
	respGetNotes, _ := app.Test(reqGetNotes)
	notesBody, _ := io.ReadAll(respGetNotes.Body)
	var notes []store.IncidentNote
	_ = json.Unmarshal(notesBody, &notes)
	if len(notes) != 1 || notes[0].Title != "Postmortem Title" {
		t.Errorf("unexpected notes returned: %+v", notes)
	}
}

func TestQueryEngine_LatencyBenchmark_250kSpans(t *testing.T) {
	st := store.NewInMemoryTelemetryStore(250000)
	defer st.Close()
	kf := store.NewInMemoryKeyframeStore()
	defer kf.Close()
	cs := causal.NewCausalScorer(st)
	rec := timetravel.NewStateReconstructor(st, kf, cs)

	ctx := context.Background()
	topo := topology.NewECommerceTopology()
	now := time.Now()

	t.Log("Seeding 250,000 spans across 15-minute window...")
	totalSpans := 0
	batch := make([]*types.Span, 0, 5000)

	// Seed 250k spans
	tracesNeeded := 250000 / 12
	step := time.Duration(15*60*1000/tracesNeeded) * time.Millisecond
	for i := 0; i < tracesNeeded; i++ {
		ts := now.Add(-15*time.Minute + time.Duration(i)*step)
		spans := topo.GenerateTrace(ts, nil)
		batch = append(batch, spans...)
		totalSpans += len(spans)

		if len(batch) >= 5000 {
			_ = st.WriteSpans(ctx, batch)
			batch = batch[:0]
		}
	}
	if len(batch) > 0 {
		_ = st.WriteSpans(ctx, batch)
	}

	t.Logf("Seeded %d spans. Pre-materializing keyframes...", totalSpans)

	// Materialize keyframes every 30 seconds
	for sec := 0; sec < 15*60; sec += 30 {
		kfTime := now.Add(-15*time.Minute + time.Duration(sec)*time.Second)
		pState, err := st.GetPointInTimeState(ctx, kfTime.UnixNano(), 30)
		if err == nil {
			_ = kf.SaveKeyframe(ctx, kfTime.Unix(), pState)
		}
	}

	// Benchmark 100 random scrub queries
	var latencies []float64
	numQueries := 100

	for i := 0; i < numQueries; i++ {
		// Random time between -15m and now
		offsetSec := float64(i * 9) // scrub across timeline
		queryTime := now.Add(-15*time.Minute + time.Duration(offsetSec)*time.Second)

		qStart := time.Now()
		state, err := rec.ReconstructState(ctx, queryTime.UnixNano(), 30)
		elapsedMs := float64(time.Since(qStart).Microseconds()) / 1000.0

		if err != nil || state == nil {
			t.Fatalf("query %d failed: %v", i, err)
		}
		latencies = append(latencies, elapsedMs)
	}

	sort.Float64s(latencies)
	p50 := latencies[int(float64(len(latencies))*0.50)]
	p95 := latencies[int(float64(len(latencies))*0.95)]
	p99 := latencies[int(float64(len(latencies))*0.99)]

	t.Logf("🎯 Reconstruction Latencies across 250k spans:")
	t.Logf("   p50: %.2f ms", p50)
	t.Logf("   p95: %.2f ms", p95)
	t.Logf("   p99: %.2f ms (Target: <120 ms)", p99)

	if p99 >= 120.0 {
		t.Errorf("p99 latency exceeded target: %.2f ms >= 120 ms", p99)
	}
}

func TestQueryEngine_AllScenariosRankCulpritFirst(t *testing.T) {
	scenarioNames := []string{"canary-bad-pool", "db-lock-storm", "redis-eviction-cascade"}

	for _, name := range scenarioNames {
		sc, err := scenarios.GetScenario(name)
		if err != nil {
			t.Fatalf("GetScenario failed: %v", err)
		}

		st := store.NewInMemoryTelemetryStore(25000)
		defer st.Close()
		kf := store.NewInMemoryKeyframeStore()
		defer kf.Close()
		cs := causal.NewCausalScorer(st)
		rec := timetravel.NewStateReconstructor(st, kf, cs)

		ctx := context.Background()
		now := time.Now()

		// 1. Emit correlated deploy pin
		deployTime := now.Add(-time.Duration(sc.LeadTimeSeconds) * time.Second)
		deployEvent := sc.GenerateDeployEvent(deployTime)
		_ = st.WriteDeploy(ctx, &deployEvent)

		// 2. Emit outage traces leading up to now
		topo := topology.NewECommerceTopology()
		for i := 0; i < 50; i++ {
			tSpans := topo.GenerateTrace(now.Add(-time.Duration(50-i)*200*time.Millisecond), &sc.OutageParams)
			_ = st.WriteSpans(ctx, tSpans)
		}

		// 3. Reconstruct state at outage time
		state, err := rec.ReconstructState(ctx, now.UnixNano(), 30)
		if err != nil {
			t.Fatalf("scenario %s failed to reconstruct state: %v", name, err)
		}

		if len(state.Suspects) == 0 {
			t.Fatalf("scenario %s: expected suspects, got 0", name)
		}

		topSuspect := state.Suspects[0]
		expectedTarget := sc.OutageParams.TargetService

		if topSuspect.ServiceName != expectedTarget {
			t.Errorf("scenario %s: expected top suspect %s, got %s",
				name, expectedTarget, topSuspect.ServiceName)
		}

		if topSuspect.Confidence < 0.80 {
			t.Errorf("scenario %s: expected confidence >= 80%%, got %.2f%%",
				name, topSuspect.Confidence*100)
		}

		fmt.Printf("✅ Scenario [%s]: Culprit=%s | Confidence=%.1f%% | Reason=%s\n",
			name, topSuspect.ServiceName, topSuspect.Confidence*100, topSuspect.Reason)
	}
}
