package causal

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/chronotrace/chronotrace/shared/store"
	"github.com/chronotrace/chronotrace/shared/types"
)

// CausalScorer performs automated root-cause ranking of distributed outage events.
type CausalScorer struct {
	store store.TelemetryStore
}

// NewCausalScorer creates a new causal analysis engine.
func NewCausalScorer(st store.TelemetryStore) *CausalScorer {
	return &CausalScorer{store: st}
}

// RankSuspects correlates deploy/config changes in [T-5m, T] with downstream failures.
func (c *CausalScorer) RankSuspects(ctx context.Context, timestampUnixNano int64, state *types.PointInTimeState) ([]types.CausalScore, error) {
	if state == nil || len(state.Nodes) == 0 {
		return nil, nil
	}

	fiveMinNano := int64(5 * time.Minute)
	startWindow := timestampUnixNano - fiveMinNano
	endWindow := timestampUnixNano

	// 1. Fetch deploy events in the 5-minute causal window
	deploys, err := c.store.GetDeployEvents(ctx, startWindow, endWindow)
	if err != nil {
		return nil, fmt.Errorf("failed to get deploy events: %w", err)
	}

	// Index nodes by name
	nodeMap := make(map[string]types.ServiceNode)
	for _, n := range state.Nodes {
		nodeMap[n.Name] = n
	}

	// Build caller/callee adjacency for blast radius
	upstreamMap := make(map[string][]string) // callee -> callers
	downstreamMap := make(map[string][]string) // caller -> callees
	for _, e := range state.Edges {
		upstreamMap[e.Target] = append(upstreamMap[e.Target], e.Source)
		downstreamMap[e.Source] = append(downstreamMap[e.Source], e.Target)
	}

	var candidates []types.CausalScore

	for _, d := range deploys {
		deltaNano := timestampUnixNano - d.TimestampUnixNano
		if deltaNano < 0 {
			continue
		}
		deltaSec := float64(deltaNano) / 1e9

		// Exponential time decay: lambda = exp(-delta_t / 180s)
		lambda := math.Exp(-deltaSec / 180.0)

		targetNode, hasNode := nodeMap[d.ServiceName]
		anomalyZ := 1.0
		anomalyMetric := "config/deploy change"
		blastRadius := make([]string, 0)

		proximityWeight := 0.60
		if hasNode {
			// Compute anomaly score
			latencyZ := math.Max(0, (targetNode.P95LatencyMs-50.0)/100.0)
			errorZ := targetNode.ErrorRate * 10.0
			anomalyZ = math.Max(1.0, (latencyZ*0.5)+(errorZ*0.5))

			if targetNode.Status == "critical" {
				proximityWeight = 1.00
				anomalyMetric = fmt.Sprintf("p95 latency %.0fms, error rate %.1f%%",
					targetNode.P95LatencyMs, targetNode.ErrorRate*100)
			} else if targetNode.Status == "degraded" {
				proximityWeight = 0.85
				anomalyMetric = fmt.Sprintf("p95 latency %.0fms (degraded)", targetNode.P95LatencyMs)
			}

			// Trace blast radius to upstream affected services
			visited := make(map[string]bool)
			queue := []string{d.ServiceName}
			for len(queue) > 0 {
				curr := queue[0]
				queue = queue[1:]
				for _, up := range upstreamMap[curr] {
					if !visited[up] {
						visited[up] = true
						blastRadius = append(blastRadius, up)
						queue = append(queue, up)
					}
				}
			}
		} else {
			// Service might be backend DB or cache (e.g. postgres-primary) whose caller fails
			for caller, callees := range downstreamMap {
				for _, callee := range callees {
					if callee == d.ServiceName {
						if cNode, ok := nodeMap[caller]; ok && cNode.Status == "critical" {
							proximityWeight = 0.95
							anomalyZ = 5.0
							blastRadius = append(blastRadius, caller)
							break
						}
					}
				}
			}
		}

		rawScore := lambda * proximityWeight * anomalyZ
		// Normalize confidence between 0.0 and 0.98
		confidence := 1.0 / (1.0 + math.Exp(-(rawScore - 1.5)))
		if confidence > 0.98 {
			confidence = 0.98
		}

		// Plain-English explanation
		shortSHA := d.CommitHash
		if len(shortSHA) > 7 {
			shortSHA = shortSHA[:7]
		}
		blastSummary := "isolated to service"
		if len(blastRadius) > 0 {
			blastSummary = "propagated upstream to " + strings.Join(blastRadius, ", ")
		}

		reason := fmt.Sprintf("Deploy %s by %s on %s (%s) occurred %.0fs prior to outage; %s",
			shortSHA, d.Author, d.ServiceName, d.CommitMessage, deltaSec, blastSummary)

		candidates = append(candidates, types.CausalScore{
			ServiceName:   d.ServiceName,
			DeployEvent:   d,
			Confidence:    confidence,
			Score:         math.Round(rawScore*100) / 100,
			Reason:        reason,
			AnomalyMetric: anomalyMetric,
			BlastRadius:   blastRadius,
		})
	}

	// Sort candidates by confidence descending
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Confidence > candidates[j].Confidence
	})

	if len(candidates) > 5 {
		candidates = candidates[:5]
	}

	return candidates, nil
}
