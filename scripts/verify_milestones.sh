#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
echo "=========================================================="
echo "⚡ ChronoTrace: Full Engineering Verification Suite"
echo "   Root: ${ROOT_DIR}"
echo "=========================================================="

echo "🧪 1/4: Running Go Race Detector across all workspace modules..."
cd "${ROOT_DIR}"
go test -race ./shared/pkg/... ./services/ingest-api/... ./tools/chaos-sim/... ./deploy/docker/... ./services/query-engine/...
echo "✅ All Go tests passed with zero data races."

echo "🎨 2/4: Running SvelteKit 5 / TypeScript / Tailwind Build Check..."
cd "${ROOT_DIR}/apps/web"
pnpm run check
pnpm run build
echo "✅ SvelteKit application passed typecheck and built in production mode."

echo "🔥 3/4: Executing Ingestion Throughput Benchmark..."
cd "${ROOT_DIR}"
go test -bench=BenchmarkIngestPipeline -run=^$ ./services/ingest-api/...
echo "✅ Throughput benchmark verified."

echo "⏱️ 4/4: Executing Time-Travel Reconstruction Latency Benchmark (250,000 Spans)..."
go test -v -run TestQueryEngine_LatencyBenchmark_250kSpans ./services/query-engine/...
echo "✅ Query & Scrubbing Latency verified < 120ms p99."

echo "=========================================================="
echo "🎉 ALL CHRONOTRACE MILESTONES VERIFIED SUCCESSFULLY!"
echo "=========================================================="
