#!/usr/bin/env bash
set -euo pipefail

MODE="${CHRONOTRACE_MODE:-embedded}"
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

echo "=================================================="
echo "⚡ ChronoTrace: Local-First Time-Travel Debugger"
echo "   Mode: ${MODE}"
echo "   Root: ${ROOT_DIR}"
echo "=================================================="

if [ "${MODE}" = "cluster" ]; then
    echo "🐳 Starting Cluster Infrastructure (ClickHouse, NATS JetStream, Redis 7)..."
    cd "${ROOT_DIR}/deploy/docker"
    docker compose up -d
    echo "Waiting for ClickHouse to report healthy..."
    until curl -s http://localhost:8123/ping | grep -q "Ok."; do
        sleep 1
    done
    echo "✅ Cluster containers active."
else
    echo "⚡ Running in Embedded Mode (In-Memory Ring Buffer + SQLite, Zero Docker Required)"
    mkdir -p "${ROOT_DIR}/data"
fi

echo "🚀 Ready for Ingestion and Query Services."
