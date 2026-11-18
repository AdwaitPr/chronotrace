package causal

import (
	"context"
	"testing"
	"time"

	"github.com/chronotrace/chronotrace/shared/store"
	"github.com/chronotrace/chronotrace/shared/types"
	"github.com/chronotrace/chronotrace/tools/chaos-sim/scenarios"
)

func TestCausalScorer_CanaryBadPool(t *testing.T) {
	st := store.NewInMemoryTelemetryStore(50000)
	defer st.Close()

	scorer := NewCausalScorer(st)
	ctx := context.Background()
	now := time.Now()

	// 1. Correlated deploy on payment-worker 75 seconds ago
	deploy := &types.DeployEvent{
		ID:                "dep-canary-042",
		ServiceName:       "payment-worker",
		Environment:       "canary",
		CommitHash:        "f8a3b19",
		CommitMessage:     "perf(db): throttle connection pool size to max_connections=2",
		Author:            "sre-canary-runner",
		TimestampUnixNano: now.Add(-75 * time.Second).UnixNano(),
		DiffSummary:       "- max_connections: 50\n+ max_connections: 2",
	}
	if err := st.WriteDeploy(ctx, deploy); err != nil {
		t.Fatalf("WriteDeploy failed: %v", err)
	}

	// Unrelated deploy on notification-svc 4 minutes ago
	unrelatedDeploy := &types.DeployEvent{
		ID:                "dep-notif-001",
		ServiceName:       "notification-svc",
		Environment:       "production",
		CommitHash:        "1122334",
		CommitMessage:     "docs: update readme",
		Author:            "dev",
		TimestampUnixNano: now.Add(-240 * time.Second).UnixNano(),
	}
	_ = st.WriteDeploy(ctx, unrelatedDeploy)

	// 2. Incident topology state at time now:
	// payment-worker latency spiked to 3500ms, error rate 88%
	// checkout-svc has cascading errors
	state := &types.PointInTimeState{
		TimestampUnixNano: now.UnixNano(),
		Nodes: []types.ServiceNode{
			{Name: "payment-worker", RPS: 80.0, P95LatencyMs: 3500.0, ErrorRate: 0.88, Status: "critical"},
			{Name: "checkout-svc", RPS: 85.0, P95LatencyMs: 3600.0, ErrorRate: 0.85, Status: "critical"},
			{Name: "api-gateway", RPS: 90.0, P95LatencyMs: 3650.0, ErrorRate: 0.85, Status: "critical"},
			{Name: "notification-svc", RPS: 20.0, P95LatencyMs: 15.0, ErrorRate: 0.0, Status: "healthy"},
		},
		Edges: []types.ServiceEdge{
			{Source: "api-gateway", Target: "checkout-svc", IsFailing: true, ErrorCount: 85},
			{Source: "checkout-svc", Target: "payment-worker", IsFailing: true, ErrorCount: 88},
		},
	}

	suspects, err := scorer.RankSuspects(ctx, now.UnixNano(), state)
	if err != nil {
		t.Fatalf("RankSuspects failed: %v", err)
	}

	if len(suspects) == 0 {
		t.Fatalf("expected at least 1 causal suspect, got 0")
	}

	topSuspect := suspects[0]
	if topSuspect.ServiceName != "payment-worker" {
		t.Errorf("expected top suspect to be payment-worker, got %s", topSuspect.ServiceName)
	}

	if topSuspect.Confidence < 0.80 {
		t.Errorf("expected top suspect confidence > 80%%, got %.2f%%", topSuspect.Confidence*100)
	}

	if topSuspect.DeployEvent == nil || topSuspect.DeployEvent.CommitHash != "f8a3b19" {
		t.Errorf("expected culprit commit f8a3b19, got %+v", topSuspect.DeployEvent)
	}

	t.Logf("✅ Top Suspect: %s (Confidence: %.1f%%) - Reason: %s",
		topSuspect.ServiceName, topSuspect.Confidence*100, topSuspect.Reason)
}

func TestCausalScorer_AllThreeScenarios(t *testing.T) {
	scenarioNames := []string{"canary-bad-pool", "db-lock-storm", "redis-eviction-cascade"}

	for _, name := range scenarioNames {
		sc, err := scenarios.GetScenario(name)
		if err != nil {
			t.Fatalf("GetScenario failed: %v", err)
		}

		st := store.NewInMemoryTelemetryStore(10000)
		scorer := NewCausalScorer(st)
		ctx := context.Background()
		now := time.Now()

		deployTime := now.Add(-time.Duration(sc.LeadTimeSeconds) * time.Second)
		deploy := sc.GenerateDeployEvent(deployTime)
		_ = st.WriteDeploy(ctx, &deploy)

		state := &types.PointInTimeState{
			TimestampUnixNano: now.UnixNano(),
			Nodes: []types.ServiceNode{
				{
					Name:         sc.OutageParams.TargetService,
					RPS:          50.0,
					P95LatencyMs: sc.OutageParams.LatencySpikeMs,
					ErrorRate:    sc.OutageParams.ErrorRate,
					Status:       "critical",
				},
			},
		}

		suspects, err := scorer.RankSuspects(ctx, now.UnixNano(), state)
		if err != nil {
			t.Fatalf("scenario %s failed: %v", name, err)
		}

		if len(suspects) == 0 {
			t.Fatalf("scenario %s: expected suspects, got 0", name)
		}

		if suspects[0].ServiceName != sc.OutageParams.TargetService {
			t.Errorf("scenario %s: expected top suspect %s, got %s",
				name, sc.OutageParams.TargetService, suspects[0].ServiceName)
		}

		if suspects[0].Confidence < 0.80 {
			t.Errorf("scenario %s: expected confidence >= 80%%, got %.1f%%",
				name, suspects[0].Confidence*100)
		}
	}
}
