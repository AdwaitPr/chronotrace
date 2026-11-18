export interface ServiceNode {
	name: string;
	rps: number;
	p95_latency_ms: number;
	error_rate: number;
	status: 'healthy' | 'degraded' | 'critical';
	is_root_cause?: boolean;
}

export interface ServiceEdge {
	source: string;
	target: string;
	call_count: number;
	p95_latency_ms: number;
	error_count: number;
	is_failing: boolean;
}

export interface DeployEvent {
	id: string;
	service_name: string;
	environment: string;
	commit_hash: string;
	commit_message: string;
	author: string;
	timestamp_unix_nano: number;
	diff_summary?: string;
}

export interface CausalScore {
	service_name: string;
	deploy_event?: DeployEvent;
	confidence: number; // 0.0 to 1.0
	score: number;
	reason: string;
	anomaly_metric: string;
	blast_radius: string[];
}

export interface LogEntry {
	timestamp_unix_nano: number;
	service_name: string;
	level: string; // INFO, WARN, ERROR
	message: string;
	trace_id?: string;
	span_id?: string;
}

export interface PointInTimeState {
	timestamp_unix_nano: number;
	active_spans: number;
	total_throughput: number;
	overall_error_rate: number;
	nodes: ServiceNode[];
	edges: ServiceEdge[];
	recent_logs: LogEntry[];
	suspects: CausalScore[];
}

export interface WaveformBucket {
	timestamp_ms: number;
	rps: number;
	error_rate: number;
	p50_ms: number;
	p95_ms: number;
	p99_ms: number;
}

export interface IncidentHighlight {
	start_ms: number;
	end_ms: number;
	severity: 'critical' | 'degraded';
	title: string;
	culprit: string;
	description: string;
}

export interface TimelineOverviewResponse {
	from_ms: number;
	to_ms: number;
	buckets: WaveformBucket[];
	deploys: DeployEvent[];
	highlights: IncidentHighlight[];
}

export interface IncidentNote {
	id?: number;
	incident_id: string;
	title: string;
	author: string;
	content: string;
	created_at: number;
}

export interface SavedBookmark {
	id: string;
	name: string;
	timestamp_unix_nano: number;
	window_seconds: number;
	description?: string;
	created_at: number;
}
