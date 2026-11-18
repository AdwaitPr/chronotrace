package types

import "time"

// Span represents a distributed trace span adhering to OpenTelemetry standards.
type Span struct {
	TraceID           string            `json:"trace_id"`
	SpanID            string            `json:"span_id"`
	ParentSpanID      string            `json:"parent_span_id,omitempty"`
	ServiceName       string            `json:"service_name"`
	OperationName     string            `json:"operation_name"`
	SpanKind          string            `json:"span_kind"` // SERVER, CLIENT, PRODUCER, CONSUMER, INTERNAL
	StartTimeUnixNano int64             `json:"start_time_unix_nano"`
	EndTimeUnixNano   int64             `json:"end_time_unix_nano"`
	DurationMs        float64           `json:"duration_ms"`
	StatusCode        string            `json:"status_code"` // OK, ERROR, UNSET
	StatusMessage     string            `json:"status_message,omitempty"`
	Attributes        map[string]string `json:"attributes,omitempty"`
	Events            []SpanEvent       `json:"events,omitempty"`
}

// SpanEvent represents an in-span event or log line.
type SpanEvent struct {
	TimestampUnixNano int64             `json:"timestamp_unix_nano"`
	Name              string            `json:"name"`
	Attributes        map[string]string `json:"attributes,omitempty"`
}

// DeployEvent captures code deployments, configuration changes, or canary releases.
type DeployEvent struct {
	ID                string `json:"id"`
	ServiceName       string `json:"service_name"`
	Environment       string `json:"environment"` // production, canary, staging
	CommitHash        string `json:"commit_hash"`
	CommitMessage     string `json:"commit_message"`
	Author            string `json:"author"`
	TimestampUnixNano int64  `json:"timestamp_unix_nano"`
	DiffSummary       string `json:"diff_summary,omitempty"`
}

// ServiceNode represents a node in the dependency topology.
type ServiceNode struct {
	Name         string  `json:"name"`
	RPS          float64 `json:"rps"`
	P95LatencyMs float64 `json:"p95_latency_ms"`
	ErrorRate    float64 `json:"error_rate"`
	Status       string  `json:"status"` // healthy, degraded, critical
	IsRootCause  bool    `json:"is_root_cause"`
}

// ServiceEdge represents a call dependency between services.
type ServiceEdge struct {
	Source       string  `json:"source"`
	Target       string  `json:"target"`
	CallCount    int64   `json:"call_count"`
	P95LatencyMs float64 `json:"p95_latency_ms"`
	ErrorCount   int64   `json:"error_count"`
	IsFailing    bool    `json:"is_failing"`
}

// CausalScore represents automated ranking of root-cause candidates for an outage.
type CausalScore struct {
	ServiceName   string       `json:"service_name"`
	DeployEvent   *DeployEvent `json:"deploy_event,omitempty"`
	Confidence    float64      `json:"confidence"` // 0.0 to 1.0
	Score         float64      `json:"score"`
	Reason        string       `json:"reason"`
	AnomalyMetric string       `json:"anomaly_metric"`
	BlastRadius   []string     `json:"blast_radius"`
}

// LogEntry represents synchronized log lines tied to the playhead time.
type LogEntry struct {
	TimestampUnixNano int64  `json:"timestamp_unix_nano"`
	ServiceName       string `json:"service_name"`
	Level             string `json:"level"` // INFO, WARN, ERROR
	Message           string `json:"message"`
	TraceID           string `json:"trace_id,omitempty"`
	SpanID            string `json:"span_id,omitempty"`
}

// TimelineMarker represents a lightweight point on the DAW timeline.
type TimelineMarker struct {
	TimestampUnixNano int64   `json:"timestamp_unix_nano"`
	Track             int     `json:"track"` // 0: Deploy/Commit, 1: Latency & 5xx, 2: Causal Swimlanes, 3: Infra/DB Locks
	Type              string  `json:"type"`
	Severity          string  `json:"severity"` // info, warning, error, critical
	Label             string  `json:"label"`
	ServiceName       string  `json:"service_name"`
	Value             float64 `json:"value"`
	Metadata          string  `json:"metadata,omitempty"`
}

// PointInTimeState represents the reconstructed snapshot of the entire distributed system.
type PointInTimeState struct {
	TimestampUnixNano int64            `json:"timestamp_unix_nano"`
	ActiveSpans       int              `json:"active_spans"`
	TotalThroughput   float64          `json:"total_throughput"`
	OverallErrorRate  float64          `json:"overall_error_rate"`
	Nodes             []ServiceNode    `json:"nodes"`
	Edges             []ServiceEdge    `json:"edges"`
	RecentLogs        []LogEntry       `json:"recent_logs"`
	Suspects          []CausalScore    `json:"suspects"`
	TimelineMarkers   []TimelineMarker `json:"timeline_markers,omitempty"`
}

// Helper to convert time.Time to UnixNano.
func ToUnixNano(t time.Time) int64 {
	return t.UnixNano()
}

// Helper to convert UnixNano to time.Time.
func FromUnixNano(nano int64) time.Time {
	return time.Unix(0, nano)
}
