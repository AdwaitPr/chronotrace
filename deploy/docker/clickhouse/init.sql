-- ClickHouse Schema for ChronoTrace Time-Travel Debugger
CREATE DATABASE IF NOT EXISTS chronotrace;

-- Spans table optimized with LowCardinality dictionary encoding and ZSTD/Gorilla compression codecs
CREATE TABLE IF NOT EXISTS chronotrace.spans (
    trace_id String CODEC(ZSTD(1)),
    span_id String CODEC(ZSTD(1)),
    parent_span_id String CODEC(ZSTD(1)),
    service_name LowCardinality(String) CODEC(ZSTD(1)),
    operation_name LowCardinality(String) CODEC(ZSTD(1)),
    span_kind LowCardinality(String) CODEC(ZSTD(1)),
    start_time_unix_nano Int64 CODEC(DoubleDelta, ZSTD(1)),
    end_time_unix_nano Int64 CODEC(DoubleDelta, ZSTD(1)),
    duration_ms Float64 CODEC(Gorilla, ZSTD(1)),
    status_code LowCardinality(String) CODEC(ZSTD(1)),
    status_message String CODEC(ZSTD(1)),
    attributes Map(String, String) CODEC(ZSTD(1)),
    events String CODEC(ZSTD(1)),
    timestamp DateTime64(3) MATERIALIZED fromUnixTimestamp64Nano(start_time_unix_nano)
) ENGINE = MergeTree()
PARTITION BY toYYYYMM(timestamp)
PRIMARY KEY (service_name, status_code, timestamp)
ORDER BY (service_name, status_code, timestamp, trace_id, span_id)
TTL timestamp + INTERVAL 30 DAY DELETE;

-- Deploy and configuration change events for causal correlation
CREATE TABLE IF NOT EXISTS chronotrace.deploy_events (
    id String,
    service_name LowCardinality(String),
    environment LowCardinality(String),
    commit_hash String,
    commit_message String,
    author LowCardinality(String),
    timestamp_unix_nano Int64,
    diff_summary String,
    timestamp DateTime64(3) MATERIALIZED fromUnixTimestamp64Nano(timestamp_unix_nano)
) ENGINE = MergeTree()
ORDER BY (service_name, timestamp, id);

-- 1-second aggregated service metrics materialized view for instant timeline waveform rendering
CREATE TABLE IF NOT EXISTS chronotrace.service_metrics_1s (
    bucket_time DateTime,
    service_name LowCardinality(String),
    total_calls UInt64,
    error_calls UInt64,
    p50_latency_ms Float64,
    p95_latency_ms Float64,
    p99_latency_ms Float64
) ENGINE = SummingMergeTree()
PRIMARY KEY (bucket_time, service_name)
ORDER BY (bucket_time, service_name);

-- Materialized view to automatically populate 1-second metrics from raw spans
CREATE MATERIALIZED VIEW IF NOT EXISTS chronotrace.mv_service_metrics_1s
TO chronotrace.service_metrics_1s AS
SELECT
    toStartOfSecond(timestamp) AS bucket_time,
    service_name,
    count() AS total_calls,
    countIf(status_code = 'ERROR') AS error_calls,
    quantile(0.50)(duration_ms) AS p50_latency_ms,
    quantile(0.95)(duration_ms) AS p95_latency_ms,
    quantile(0.99)(duration_ms) AS p99_latency_ms
FROM chronotrace.spans
GROUP BY bucket_time, service_name;
