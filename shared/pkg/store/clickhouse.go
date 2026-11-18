package store

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/chronotrace/chronotrace/shared/types"
)

// ClickHouseBatchStore implements TelemetryStore with batched flushes for high-throughput ClickHouse ingestion.
type ClickHouseBatchStore struct {
	conn          driver.Conn
	batchChan     chan *types.Span
	batchSize     int
	flushInterval time.Duration
	mu            sync.RWMutex
	closed        bool
	wg            sync.WaitGroup
	ctx           context.Context
	cancel        context.CancelFunc
}

// ClickHouseOptions holds connection parameters.
type ClickHouseOptions struct {
	Addr          string
	Database      string
	Username      string
	Password      string
	BatchSize     int
	FlushInterval time.Duration
}

// NewClickHouseBatchStore initializes a ClickHouse connection and starts the async flusher loop.
func NewClickHouseBatchStore(opts ClickHouseOptions) (*ClickHouseBatchStore, error) {
	if opts.Addr == "" {
		opts.Addr = "localhost:9000"
	}
	if opts.Database == "" {
		opts.Database = "chronotrace"
	}
	if opts.Username == "" {
		opts.Username = "default"
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = 5000 // 5,000 spans default batch
	}
	if opts.FlushInterval <= 0 {
		opts.FlushInterval = 250 * time.Millisecond // 250ms default flusher interval
	}

	conn, err := clickhouse.Open(&clickhouse.Options{
		Addr: []string{opts.Addr},
		Auth: clickhouse.Auth{
			Database: opts.Database,
			Username: opts.Username,
			Password: opts.Password,
		},
		Settings: clickhouse.Settings{
			"max_execution_time": 60,
		},
		DialTimeout: 5 * time.Second,
		Compression: &clickhouse.Compression{
			Method: clickhouse.CompressionLZ4,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to open ClickHouse connection: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	store := &ClickHouseBatchStore{
		conn:          conn,
		batchChan:     make(chan *types.Span, opts.BatchSize*4),
		batchSize:     opts.BatchSize,
		flushInterval: opts.FlushInterval,
		ctx:           ctx,
		cancel:        cancel,
	}

	store.wg.Add(1)
	go store.flushLoop()

	return store, nil
}

// WriteSpans enqueues spans to the async flusher without blocking the caller.
func (s *ClickHouseBatchStore) WriteSpans(ctx context.Context, spans []*types.Span) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return fmt.Errorf("clickhouse store is closed")
	}

	for _, span := range spans {
		select {
		case s.batchChan <- span:
		case <-ctx.Done():
			return ctx.Err()
		default:
			// Buffer full, drop or block based on high-pressure policy
		}
	}
	return nil
}

// WriteDeploy writes a deployment or config change event to ClickHouse.
func (s *ClickHouseBatchStore) WriteDeploy(ctx context.Context, deploy *types.DeployEvent) error {
	query := `INSERT INTO chronotrace.deploy_events (
		id, service_name, environment, commit_hash, commit_message, author, timestamp_unix_nano, diff_summary
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`

	return s.conn.Exec(ctx, query,
		deploy.ID,
		deploy.ServiceName,
		deploy.Environment,
		deploy.CommitHash,
		deploy.CommitMessage,
		deploy.Author,
		deploy.TimestampUnixNano,
		deploy.DiffSummary,
	)
}

// flushLoop processes incoming spans in batches of batchSize or every flushInterval.
func (s *ClickHouseBatchStore) flushLoop() {
	defer s.wg.Done()

	ticker := time.NewTicker(s.flushInterval)
	defer ticker.Stop()

	batch := make([]*types.Span, 0, s.batchSize)

	flush := func() {
		if len(batch) == 0 {
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := s.flushBatch(ctx, batch); err != nil {
			// In production, log and increment error metric
			fmt.Printf("[ChronoTrace] ClickHouse batch flush failed: %v\n", err)
		}
		batch = make([]*types.Span, 0, s.batchSize)
	}

	for {
		select {
		case <-s.ctx.Done():
			flush()
			return
		case span, ok := <-s.batchChan:
			if !ok {
				flush()
				return
			}
			batch = append(batch, span)
			if len(batch) >= s.batchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

// flushBatch issues a prepared batch insert to ClickHouse.
func (s *ClickHouseBatchStore) flushBatch(ctx context.Context, batch []*types.Span) error {
	b, err := s.conn.PrepareBatch(ctx, `INSERT INTO chronotrace.spans (
		trace_id, span_id, parent_span_id, service_name, operation_name, span_kind,
		start_time_unix_nano, end_time_unix_nano, duration_ms, status_code, status_message, attributes, events
	)`)
	if err != nil {
		return fmt.Errorf("prepare batch failed: %w", err)
	}

	for _, span := range batch {
		eventsJSON := ""
		if len(span.Events) > 0 {
			if d, err := json.Marshal(span.Events); err == nil {
				eventsJSON = string(d)
			}
		}

		err := b.Append(
			span.TraceID,
			span.SpanID,
			span.ParentSpanID,
			span.ServiceName,
			span.OperationName,
			span.SpanKind,
			span.StartTimeUnixNano,
			span.EndTimeUnixNano,
			span.DurationMs,
			span.StatusCode,
			span.StatusMessage,
			span.Attributes,
			eventsJSON,
		)
		if err != nil {
			return fmt.Errorf("batch append failed: %w", err)
		}
	}

	return b.Send()
}

// QuerySpans queries stored spans from ClickHouse.
func (s *ClickHouseBatchStore) QuerySpans(ctx context.Context, query SpanQuery) ([]*types.Span, error) {
	limit := query.Limit
	if limit <= 0 {
		limit = 500
	}

	sqlQuery := `SELECT trace_id, span_id, parent_span_id, service_name, operation_name, span_kind,
		start_time_unix_nano, end_time_unix_nano, duration_ms, status_code, status_message, attributes, events
		FROM chronotrace.spans WHERE 1=1`

	var args []any
	if query.TraceID != "" {
		sqlQuery += " AND trace_id = ?"
		args = append(args, query.TraceID)
	}
	if query.ServiceName != "" {
		sqlQuery += " AND service_name = ?"
		args = append(args, query.ServiceName)
	}
	if query.StatusCode != "" {
		sqlQuery += " AND status_code = ?"
		args = append(args, query.StatusCode)
	}
	if query.StartNano > 0 {
		sqlQuery += " AND start_time_unix_nano >= ?"
		args = append(args, query.StartNano)
	}
	if query.EndNano > 0 {
		sqlQuery += " AND start_time_unix_nano <= ?"
		args = append(args, query.EndNano)
	}

	sqlQuery += fmt.Sprintf(" ORDER BY start_time_unix_nano DESC LIMIT %d", limit)

	rows, err := s.conn.Query(ctx, sqlQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("query spans failed: %w", err)
	}
	defer rows.Close()

	var result []*types.Span
	for rows.Next() {
		var (
			span       types.Span
			eventsJSON string
		)
		if err := rows.Scan(
			&span.TraceID,
			&span.SpanID,
			&span.ParentSpanID,
			&span.ServiceName,
			&span.OperationName,
			&span.SpanKind,
			&span.StartTimeUnixNano,
			&span.EndTimeUnixNano,
			&span.DurationMs,
			&span.StatusCode,
			&span.StatusMessage,
			&span.Attributes,
			&eventsJSON,
		); err != nil {
			return nil, fmt.Errorf("scan span failed: %w", err)
		}
		if eventsJSON != "" {
			_ = json.Unmarshal([]byte(eventsJSON), &span.Events)
		}
		result = append(result, &span)
	}

	return result, rows.Err()
}

// GetDeployEvents queries deploy events within a time range.
func (s *ClickHouseBatchStore) GetDeployEvents(ctx context.Context, startNano, endNano int64) ([]*types.DeployEvent, error) {
	sqlQuery := `SELECT id, service_name, environment, commit_hash, commit_message, author, timestamp_unix_nano, diff_summary
		FROM chronotrace.deploy_events WHERE 1=1`
	var args []any
	if startNano > 0 {
		sqlQuery += " AND timestamp_unix_nano >= ?"
		args = append(args, startNano)
	}
	if endNano > 0 {
		sqlQuery += " AND timestamp_unix_nano <= ?"
		args = append(args, endNano)
	}
	sqlQuery += " ORDER BY timestamp_unix_nano DESC"

	rows, err := s.conn.Query(ctx, sqlQuery, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []*types.DeployEvent
	for rows.Next() {
		var d types.DeployEvent
		if err := rows.Scan(&d.ID, &d.ServiceName, &d.Environment, &d.CommitHash, &d.CommitMessage, &d.Author, &d.TimestampUnixNano, &d.DiffSummary); err != nil {
			return nil, err
		}
		events = append(events, &d)
	}
	return events, rows.Err()
}

// GetPointInTimeState queries aggregated topology nodes and edges within a window.
func (s *ClickHouseBatchStore) GetPointInTimeState(ctx context.Context, timestampUnixNano int64, windowSeconds int) (*types.PointInTimeState, error) {
	if windowSeconds <= 0 {
		windowSeconds = 30
	}
	windowNano := int64(windowSeconds) * int64(time.Second)
	startWindow := timestampUnixNano - windowNano
	endWindow := timestampUnixNano

	// Query aggregated service metrics
	sqlQuery := `SELECT service_name,
		count() AS total_calls,
		countIf(status_code = 'ERROR') AS error_calls,
		quantile(0.95)(duration_ms) AS p95_latency
		FROM chronotrace.spans
		WHERE start_time_unix_nano >= ? AND start_time_unix_nano <= ?
		GROUP BY service_name`

	rows, err := s.conn.Query(ctx, sqlQuery, startWindow, endWindow)
	if err != nil {
		return nil, fmt.Errorf("failed to aggregate service metrics: %w", err)
	}
	defer rows.Close()

	var nodes []types.ServiceNode
	var totalCalls int64
	var totalErrors int64

	for rows.Next() {
		var (
			svcName    string
			calls      uint64
			errors     uint64
			p95Latency float64
		)
		if err := rows.Scan(&svcName, &calls, &errors, &p95Latency); err != nil {
			return nil, err
		}

		totalCalls += int64(calls)
		totalErrors += int64(errors)

		rps := float64(calls) / float64(windowSeconds)
		errRate := 0.0
		if calls > 0 {
			errRate = float64(errors) / float64(calls)
		}

		status := "healthy"
		if errRate > 0.10 || p95Latency > 1500.0 {
			status = "critical"
		} else if errRate > 0.02 || p95Latency > 500.0 {
			status = "degraded"
		}

		nodes = append(nodes, types.ServiceNode{
			Name:         svcName,
			RPS:          math.Round(rps*100) / 100,
			P95LatencyMs: math.Round(p95Latency*10) / 10,
			ErrorRate:    math.Round(errRate*1000) / 1000,
			Status:       status,
		})
	}

	overallErrRate := 0.0
	if totalCalls > 0 {
		overallErrRate = float64(totalErrors) / float64(totalCalls)
	}

	return &types.PointInTimeState{
		TimestampUnixNano: timestampUnixNano,
		ActiveSpans:       int(totalCalls),
		TotalThroughput:   math.Round((float64(totalCalls)/float64(windowSeconds))*100) / 100,
		OverallErrorRate:  math.Round(overallErrRate*1000) / 1000,
		Nodes:             nodes,
		Edges:             make([]types.ServiceEdge, 0),
		RecentLogs:        make([]types.LogEntry, 0),
		Suspects:          make([]types.CausalScore, 0),
	}, nil
}

// GetTimelineMarkers returns deploy pins and waveform metrics.
func (s *ClickHouseBatchStore) GetTimelineMarkers(ctx context.Context, startNano, endNano int64) ([]types.TimelineMarker, error) {
	deploys, err := s.GetDeployEvents(ctx, startNano, endNano)
	if err != nil {
		return nil, err
	}

	var markers []types.TimelineMarker
	for _, d := range deploys {
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
	return markers, nil
}

// Close flushes remaining spans and closes the connection.
func (s *ClickHouseBatchStore) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()

	s.cancel()
	close(s.batchChan)
	s.wg.Wait()

	if s.conn != nil {
		return s.conn.Close()
	}
	return nil
}
