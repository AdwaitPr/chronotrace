package timetravel

import (
	"context"
	"math"
	"time"

	"github.com/chronotrace/chronotrace/services/query-engine/causal"
	"github.com/chronotrace/chronotrace/shared/store"
	"github.com/chronotrace/chronotrace/shared/types"
)

// StateReconstructor computes point-in-time states using 30s I-frames + delta P-frames.
type StateReconstructor struct {
	telemetryStore store.TelemetryStore
	keyframeStore  store.KeyframeStore
	causalScorer   *causal.CausalScorer
}

// NewStateReconstructor creates an I-frame/P-frame time-travel state reconstruction engine.
func NewStateReconstructor(st store.TelemetryStore, kf store.KeyframeStore, cs *causal.CausalScorer) *StateReconstructor {
	return &StateReconstructor{
		telemetryStore: st,
		keyframeStore:  kf,
		causalScorer:   cs,
	}
}

// ReconstructState reconstructs system state at targetUnixNano with <120ms latency.
func (r *StateReconstructor) ReconstructState(ctx context.Context, targetUnixNano int64, windowSeconds int) (*types.PointInTimeState, error) {
	if windowSeconds <= 0 {
		windowSeconds = 30
	}

	var state *types.PointInTimeState
	var err error

	targetSec := targetUnixNano / 1e9

	// 1. Try to fetch nearest prior 30s keyframe (I-Frame)
	if r.keyframeStore != nil {
		bucket, kf, kfErr := r.keyframeStore.GetNearestPriorKeyframe(ctx, targetUnixNano)
		if kfErr == nil && kf != nil {
			kfTimeNano := bucket * 1e9
			deltaNano := targetUnixNano - kfTimeNano

			if deltaNano >= 0 && deltaNano < int64(30*time.Second) {
				// Replay P-Frame delta spans over [kfTimeNano, targetUnixNano]
				state, err = r.replayDelta(ctx, kf, kfTimeNano, targetUnixNano, windowSeconds)
			}
		}
	}

	// 2. If no keyframe was available or delta replay failed, fall back to base store aggregation
	if state == nil {
		state, err = r.telemetryStore.GetPointInTimeState(ctx, targetUnixNano, windowSeconds)
		if err != nil {
			return nil, err
		}

		// Materialize and save keyframe asynchronously for future instant jumps
		if r.keyframeStore != nil {
			bucket := (targetSec / 30) * 30
			_ = r.keyframeStore.SaveKeyframe(ctx, bucket, state)
		}
	}

	// 3. Attach automated Causal Root-Cause ranking
	if r.causalScorer != nil {
		suspects, sErr := r.causalScorer.RankSuspects(ctx, targetUnixNano, state)
		if sErr == nil && len(suspects) > 0 {
			state.Suspects = suspects
			// Mark top suspect as is_root_cause in topology nodes
			topCulprit := suspects[0].ServiceName
			for i := range state.Nodes {
				if state.Nodes[i].Name == topCulprit {
					state.Nodes[i].IsRootCause = true
				}
			}
		}
	}

	return state, nil
}

// replayDelta applies delta spans onto a base keyframe (P-frame replay).
func (r *StateReconstructor) replayDelta(ctx context.Context, base *types.PointInTimeState, startNano, endNano int64, windowSeconds int) (*types.PointInTimeState, error) {
	deltaSpans, err := r.telemetryStore.QuerySpans(ctx, store.SpanQuery{
		StartNano: startNano,
		EndNano:   endNano,
		Limit:     25000,
	})
	if err != nil {
		return base, nil
	}

	// Clone base state
	cloned := &types.PointInTimeState{
		TimestampUnixNano: endNano,
		ActiveSpans:       base.ActiveSpans + len(deltaSpans),
		TotalThroughput:   base.TotalThroughput,
		OverallErrorRate:  base.OverallErrorRate,
		Nodes:             make([]types.ServiceNode, len(base.Nodes)),
		Edges:             make([]types.ServiceEdge, len(base.Edges)),
		RecentLogs:        append([]types.LogEntry{}, base.RecentLogs...),
	}
	copy(cloned.Nodes, base.Nodes)
	copy(cloned.Edges, base.Edges)

	if len(deltaSpans) == 0 {
		return cloned, nil
	}

	// Apply delta mutations to affected nodes
	nodeDeltas := make(map[string]struct {
		errCount int
		latSum   float64
		count    int
	})

	for _, s := range deltaSpans {
		d := nodeDeltas[s.ServiceName]
		d.count++
		d.latSum += s.DurationMs
		if s.StatusCode == "ERROR" {
			d.errCount++
			if len(cloned.RecentLogs) < 50 {
				cloned.RecentLogs = append([]types.LogEntry{
					{
						TimestampUnixNano: s.EndTimeUnixNano,
						ServiceName:       s.ServiceName,
						Level:             "ERROR",
						Message:           s.StatusMessage,
						TraceID:           s.TraceID,
						SpanID:            s.SpanID,
					},
				}, cloned.RecentLogs...)
			}
		}
		nodeDeltas[s.ServiceName] = d
	}

	for i := range cloned.Nodes {
		name := cloned.Nodes[i].Name
		if d, ok := nodeDeltas[name]; ok && d.count > 0 {
			avgLat := d.latSum / float64(d.count)
			if avgLat > cloned.Nodes[i].P95LatencyMs {
				cloned.Nodes[i].P95LatencyMs = math.Round(avgLat*10) / 10
			}
			errRatio := float64(d.errCount) / float64(d.count)
			if errRatio > cloned.Nodes[i].ErrorRate {
				cloned.Nodes[i].ErrorRate = math.Round(errRatio*1000) / 1000
			}
			if cloned.Nodes[i].ErrorRate > 0.10 || cloned.Nodes[i].P95LatencyMs > 1500.0 {
				cloned.Nodes[i].Status = "critical"
			} else if cloned.Nodes[i].ErrorRate > 0.02 || cloned.Nodes[i].P95LatencyMs > 500.0 {
				cloned.Nodes[i].Status = "degraded"
			}
		}
	}

	return cloned, nil
}
