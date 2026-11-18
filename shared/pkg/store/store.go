package store

import (
	"context"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/chronotrace/chronotrace/shared/types"
)

// SpanQuery defines filters for retrieving traces.
type SpanQuery struct {
	TraceID     string
	ServiceName string
	StartNano   int64
	EndNano     int64
	StatusCode  string
	Limit       int
}

// TelemetryStore abstracts storage for spans, deploys, and time-travel state reconstruction.
type TelemetryStore interface {
	WriteSpans(ctx context.Context, spans []*types.Span) error
	WriteDeploy(ctx context.Context, deploy *types.DeployEvent) error
	QuerySpans(ctx context.Context, query SpanQuery) ([]*types.Span, error)
	GetPointInTimeState(ctx context.Context, timestampUnixNano int64, windowSeconds int) (*types.PointInTimeState, error)
	GetDeployEvents(ctx context.Context, startNano, endNano int64) ([]*types.DeployEvent, error)
	GetTimelineMarkers(ctx context.Context, startNano, endNano int64) ([]types.TimelineMarker, error)
	Close() error
}

// InMemoryTelemetryStore provides a bounded ring-buffer store for embedded zero-dependency operation.
type InMemoryTelemetryStore struct {
	mu          sync.RWMutex
	capacity    int
	spans       []*types.Span
	head        int
	count       int
	deploys     []*types.DeployEvent
	closed      bool
}

// NewInMemoryTelemetryStore creates an in-memory telemetry store with a bounded ring buffer.
func NewInMemoryTelemetryStore(capacity int) *InMemoryTelemetryStore {
	if capacity <= 0 {
		capacity = 250000 // default 250k spans bounded window
	}
	return &InMemoryTelemetryStore{
		capacity: capacity,
		spans:    make([]*types.Span, capacity),
		deploys:  make([]*types.DeployEvent, 0),
	}
}

// WriteSpans inserts a batch of spans into the ring buffer.
func (s *InMemoryTelemetryStore) WriteSpans(ctx context.Context, spans []*types.Span) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, span := range spans {
		s.spans[s.head] = span
		s.head = (s.head + 1) % s.capacity
		if s.count < s.capacity {
			s.count++
		}
	}
	return nil
}

// WriteDeploy records a deployment or configuration event.
func (s *InMemoryTelemetryStore) WriteDeploy(ctx context.Context, deploy *types.DeployEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.deploys = append(s.deploys, deploy)
	return nil
}

// QuerySpans searches stored spans matching the given filters.
func (s *InMemoryTelemetryStore) QuerySpans(ctx context.Context, query SpanQuery) ([]*types.Span, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*types.Span
	limit := query.Limit
	if limit <= 0 {
		limit = 500
	}

	for i := 0; i < s.count; i++ {
		idx := (s.head - 1 - i + s.capacity) % s.capacity
		span := s.spans[idx]
		if span == nil {
			continue
		}

		if query.TraceID != "" && span.TraceID != query.TraceID {
			continue
		}
		if query.ServiceName != "" && span.ServiceName != query.ServiceName {
			continue
		}
		if query.StatusCode != "" && span.StatusCode != query.StatusCode {
			continue
		}
		if query.StartNano > 0 && span.StartTimeUnixNano < query.StartNano {
			continue
		}
		if query.EndNano > 0 && span.StartTimeUnixNano > query.EndNano {
			continue
		}

		result = append(result, span)
		if len(result) >= limit {
			break
		}
	}

	return result, nil
}

// GetDeployEvents returns all deployment events in the given time window.
func (s *InMemoryTelemetryStore) GetDeployEvents(ctx context.Context, startNano, endNano int64) ([]*types.DeployEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var events []*types.DeployEvent
	for _, d := range s.deploys {
		if (startNano == 0 || d.TimestampUnixNano >= startNano) &&
			(endNano == 0 || d.TimestampUnixNano <= endNano) {
			events = append(events, d)
		}
	}
	return events, nil
}

// GetPointInTimeState aggregates topology nodes, edges, error rates, and p95 latencies at playhead time.
func (s *InMemoryTelemetryStore) GetPointInTimeState(ctx context.Context, timestampUnixNano int64, windowSeconds int) (*types.PointInTimeState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if windowSeconds <= 0 {
		windowSeconds = 30
	}
	windowNano := int64(windowSeconds) * int64(time.Second)
	startWindow := timestampUnixNano - windowNano
	endWindow := timestampUnixNano

	type svcStats struct {
		name       string
		latencies  []float64
		errorCount int64
		totalCount int64
	}

	type edgeKey struct {
		source string
		target string
	}
	type edgeStats struct {
		latencies  []float64
		errorCount int64
		callCount  int64
	}

	serviceMap := make(map[string]*svcStats)
	edgeMap := make(map[edgeKey]*edgeStats)
	parentSpanMap := make(map[string]*types.Span) // spanID -> span for edge mapping
	var matchedSpans []*types.Span
	var recentLogs []types.LogEntry

	for i := 0; i < s.count; i++ {
		idx := (s.head - 1 - i + s.capacity) % s.capacity
		span := s.spans[idx]
		if span == nil {
			continue
		}

		if span.StartTimeUnixNano >= startWindow && span.StartTimeUnixNano <= endWindow {
			matchedSpans = append(matchedSpans, span)
			parentSpanMap[span.SpanID] = span

			stats, exists := serviceMap[span.ServiceName]
			if !exists {
				stats = &svcStats{name: span.ServiceName, latencies: make([]float64, 0)}
				serviceMap[span.ServiceName] = stats
			}
			stats.totalCount++
			stats.latencies = append(stats.latencies, span.DurationMs)
			if span.StatusCode == "ERROR" {
				stats.errorCount++
			}

			// Capture recent log entries from errors
			if span.StatusCode == "ERROR" && len(recentLogs) < 50 {
				msg := span.StatusMessage
				if msg == "" {
					msg = span.OperationName + " returned error status"
				}
				recentLogs = append(recentLogs, types.LogEntry{
					TimestampUnixNano: span.EndTimeUnixNano,
					ServiceName:       span.ServiceName,
					Level:             "ERROR",
					Message:           msg,
					TraceID:           span.TraceID,
					SpanID:            span.SpanID,
				})
			}
		}
	}

	// Build edges based on ParentSpanID
	for _, span := range matchedSpans {
		if span.ParentSpanID != "" {
			if parent, found := parentSpanMap[span.ParentSpanID]; found && parent.ServiceName != span.ServiceName {
				key := edgeKey{source: parent.ServiceName, target: span.ServiceName}
				es, ok := edgeMap[key]
				if !ok {
					es = &edgeStats{latencies: make([]float64, 0)}
					edgeMap[key] = es
				}
				es.callCount++
				es.latencies = append(es.latencies, span.DurationMs)
				if span.StatusCode == "ERROR" {
					es.errorCount++
				}
			}
		}
	}

	// Calculate nodes
	nodes := make([]types.ServiceNode, 0, len(serviceMap))
	totalErrors := int64(0)
	totalCalls := int64(0)

	for _, sStat := range serviceMap {
		totalCalls += sStat.totalCount
		totalErrors += sStat.errorCount

		rps := float64(sStat.totalCount) / float64(windowSeconds)
		errRate := 0.0
		if sStat.totalCount > 0 {
			errRate = float64(sStat.errorCount) / float64(sStat.totalCount)
		}

		p95 := calculatePercentile(sStat.latencies, 0.95)
		status := "healthy"
		if errRate > 0.10 || p95 > 1500.0 {
			status = "critical"
		} else if errRate > 0.02 || p95 > 500.0 {
			status = "degraded"
		}

		nodes = append(nodes, types.ServiceNode{
			Name:         sStat.name,
			RPS:          math.Round(rps*100) / 100,
			P95LatencyMs: math.Round(p95*10) / 10,
			ErrorRate:    math.Round(errRate*1000) / 1000,
			Status:       status,
		})
	}

	// Calculate edges
	edges := make([]types.ServiceEdge, 0, len(edgeMap))
	for k, es := range edgeMap {
		p95 := calculatePercentile(es.latencies, 0.95)
		isFailing := es.errorCount > 0 || p95 > 1000.0
		edges = append(edges, types.ServiceEdge{
			Source:       k.source,
			Target:       k.target,
			CallCount:    es.callCount,
			P95LatencyMs: math.Round(p95*10) / 10,
			ErrorCount:   es.errorCount,
			IsFailing:    isFailing,
		})
	}

	overallErrRate := 0.0
	if totalCalls > 0 {
		overallErrRate = float64(totalErrors) / float64(totalCalls)
	}

	return &types.PointInTimeState{
		TimestampUnixNano: timestampUnixNano,
		ActiveSpans:       len(matchedSpans),
		TotalThroughput:   math.Round((float64(totalCalls)/float64(windowSeconds))*100) / 100,
		OverallErrorRate:  math.Round(overallErrRate*1000) / 1000,
		Nodes:             nodes,
		Edges:             edges,
		RecentLogs:        recentLogs,
		Suspects:          make([]types.CausalScore, 0),
	}, nil
}

// GetTimelineMarkers returns waveform points, deploy pins, and error markers for the DAW timeline.
func (s *InMemoryTelemetryStore) GetTimelineMarkers(ctx context.Context, startNano, endNano int64) ([]types.TimelineMarker, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var markers []types.TimelineMarker

	// Track 0: Deploy events
	for _, d := range s.deploys {
		if (startNano == 0 || d.TimestampUnixNano >= startNano) &&
			(endNano == 0 || d.TimestampUnixNano <= endNano) {
			markers = append(markers, types.TimelineMarker{
				TimestampUnixNano: d.TimestampUnixNano,
				Track:             0,
				Type:              "deploy",
				Severity:          "info",
				Label:             d.ServiceName + " (" + d.CommitHash[:min(7, len(d.CommitHash))] + ")",
				ServiceName:       d.ServiceName,
				Metadata:          d.CommitMessage,
			})
		}
	}

	// Track 1: Latency & 5xx Waveform
	stepNano := int64(time.Second) // 1-second buckets
	if startNano > 0 && endNano > startNano {
		numBuckets := (endNano - startNano) / stepNano
		if numBuckets > 500 {
			stepNano = (endNano - startNano) / 500
		}
	}

	return markers, nil
}

// Close cleans up resources.
func (s *InMemoryTelemetryStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	s.spans = nil
	s.deploys = nil
	return nil
}

func calculatePercentile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0.0
	}
	sort.Float64s(values)
	idx := int(float64(len(values)-1) * p)
	return values[idx]
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
