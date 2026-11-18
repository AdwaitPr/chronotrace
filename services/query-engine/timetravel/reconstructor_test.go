package timetravel

import (
	"context"
	"testing"
	"time"

	"github.com/chronotrace/chronotrace/shared/store"
	"github.com/chronotrace/chronotrace/shared/types"
)

func TestStateReconstruction_IFramePFrame(t *testing.T) {
	ringStore := store.NewInMemoryTelemetryStore(10000)
	defer ringStore.Close()

	kfStore := store.NewInMemoryKeyframeStore()
	defer kfStore.Close()

	rec := NewStateReconstructor(ringStore, kfStore, nil)

	ctx := context.Background()
	now := time.Now()
	nowNano := now.UnixNano()

	// 1. Save an I-Frame at t - 15s (aligned to 30s bucket)
	bucket30s := ((nowNano / 1e9) / 30) * 30
	baseState := &types.PointInTimeState{
		TimestampUnixNano: bucket30s * 1e9,
		ActiveSpans:       100,
		TotalThroughput:   50.0,
		Nodes: []types.ServiceNode{
			{Name: "checkout-svc", RPS: 50.0, P95LatencyMs: 30.0, Status: "healthy"},
		},
	}
	if err := kfStore.SaveKeyframe(ctx, bucket30s, baseState); err != nil {
		t.Fatalf("SaveKeyframe failed: %v", err)
	}

	// 2. Add delta P-frame spans between bucket30s and bucket30s + 10s
	deltaStartNano := bucket30s * 1e9
	deltaSpan := &types.Span{
		TraceID:           "tr-delta-1",
		SpanID:            "sp-delta-1",
		ServiceName:       "checkout-svc",
		OperationName:     "Process",
		StartTimeUnixNano: deltaStartNano + int64(2*time.Second),
		EndTimeUnixNano:   deltaStartNano + int64(2*time.Second) + int64(1500*time.Millisecond),
		DurationMs:        1500.0,
		StatusCode:        "ERROR",
		StatusMessage:     "upstream pool exhausted",
	}
	if err := ringStore.WriteSpans(ctx, []*types.Span{deltaSpan}); err != nil {
		t.Fatalf("WriteSpans failed: %v", err)
	}

	// 3. Reconstruct at bucket30s + 10s
	targetNano := deltaStartNano + int64(10*time.Second)
	reconstructed, err := rec.ReconstructState(ctx, targetNano, 30)
	if err != nil {
		t.Fatalf("ReconstructState failed: %v", err)
	}

	if reconstructed == nil {
		t.Fatal("expected non-nil reconstructed state")
	}

	// Verify checkout-svc status updated from delta span
	found := false
	for _, n := range reconstructed.Nodes {
		if n.Name == "checkout-svc" {
			found = true
			if n.Status != "degraded" && n.Status != "critical" {
				t.Errorf("expected checkout-svc to reflect error status, got %s", n.Status)
			}
		}
	}
	if !found {
		t.Errorf("checkout-svc node missing in reconstructed state")
	}
}
