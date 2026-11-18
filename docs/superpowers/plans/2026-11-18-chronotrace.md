# ChronoTrace Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build ChronoTrace, a production-grade, local-first Time-Travel Outage & Postmortem Debugger featuring dual-mode backend (embedded in-memory / cluster ClickHouse+NATS+Redis), 60 FPS Canvas 2D DAW timeline scrubber, WebGL2 dependency graph, and automated causal root-cause ranking.

**Architecture:** Monorepo (`apps/web`, `services/ingest-api`, `services/query-engine`, `tools/chaos-sim`, `deploy/docker`, `shared/pkg`) with clean `EventBus` and `TelemetryStore` abstractions enabling seamless switching between zero-dependency embedded in-memory mode (`CHRONOTRACE_MODE=embedded`) and clustered production mode (`CHRONOTRACE_MODE=cluster`). Local SQLite (`modernc.org/sqlite`) handles postmortem notes and saved incident state without heavy Postgres overhead.

**Tech Stack:** Go 1.23+, Fiber v3, NATS JetStream, ClickHouse (`clickhouse-go/v2`), Redis 7 (`go-redis/v9`), SQLite (`modernc.org/sqlite`), SvelteKit (Svelte 5 runes `$state`, `$derived`, `$effect`), TypeScript strict, Tailwind CSS, Canvas 2D, WebGL2, Web Workers.

**Spec:** Local-first time-travel debugger targeting ≥10k spans/sec ingestion, <120ms p99 time-travel point-in-time state reconstruction across 10M+ indexed spans, locked 60 FPS DAW multi-track timeline, and automated causal ranking.

## Global Constraints
- `CHRONOTRACE_MODE`: Supports `embedded` (default, in-memory bus + ring buffer store + SQLite, zero external dependencies required) and `cluster` (ClickHouse + NATS JetStream + Redis).
- Docker Compose: Lightweight ARM64/AMD64 images for ClickHouse, NATS JetStream, Redis 7 with hard memory limits (ClickHouse 1.5GB, Redis 256MB, NATS 256MB). No PostgreSQL container (pure Go SQLite used instead).
- Go Workspace: Monorepo with root `go.work` linking `services/ingest-api`, `services/query-engine`, `tools/chaos-sim`, and `shared/pkg`.
- Frontend: SvelteKit + Svelte 5 runes (`$state`, `$derived`, `$effect`), strict TypeScript, Canvas 2D DAW scrubber with J/K/L shuttle controls and I/O loop brackets, WebGL2 topology graph, Web Worker binary frame decoder.

## Review Focus
- EventBus fallback: In-memory channel bus drops slow consumer spans or blocks gracefully without unbounded memory growth (<128MB RSS).
- TelemetryStore ring buffer: 250k-span sliding window maintains correct chronological ordering and timestamp search during rapid ingestion.
- ClickHouse schema efficiency: LowCardinality and ZSTD compression on service name, trace ID, status code, and span attributes.
- Canvas 2D timeline performance: 50,000 timeline markers scrubbed at locked 60 FPS using viewport culling and spatial indexing.
- Binary WebSocket protocol: TypedArray / binary payloads decoded off-thread in a dedicated Web Worker to prevent UI frame drops.

---

### Task 1: Monorepo & Go Workspace Architecture
**Files:**
- Create: `/Users/adwaitpratapsingh/.gemini/antigravity/scratch/chronotrace/go.work`
- Create: `/Users/adwaitpratapsingh/.gemini/antigravity/scratch/chronotrace/shared/pkg/types/span.go`
- Create: `/Users/adwaitpratapsingh/.gemini/antigravity/scratch/chronotrace/shared/pkg/bus/bus.go`
- Create: `/Users/adwaitpratapsingh/.gemini/antigravity/scratch/chronotrace/shared/pkg/store/store.go`
- Create: `/Users/adwaitpratapsingh/.gemini/antigravity/scratch/chronotrace/shared/pkg/store/sqlite.go`
- Test: `/Users/adwaitpratapsingh/.gemini/antigravity/scratch/chronotrace/shared/pkg/bus/bus_test.go`
- Test: `/Users/adwaitpratapsingh/.gemini/antigravity/scratch/chronotrace/shared/pkg/store/store_test.go`

**Interfaces:**
- Consumes: Standard Go libraries and `modernc.org/sqlite`.
- Produces: `types.Span`, `types.DeployEvent`, `bus.EventBus`, `store.TelemetryStore`, `store.MetadataStore`.

- [ ] **Step 1: Write tests for in-memory EventBus and TelemetryStore ring buffer**
- [ ] **Step 2: Run test to verify it fails**
- [ ] **Step 3: Implement domain types, in-memory bus, ring-buffer store, and SQLite metadata manager**
- [ ] **Step 4: Run test to verify it passes**
- [ ] **Step 5: Commit task deliverable**

---

### Task 2: Infrastructure & ClickHouse Schemas
**Files:**
- Create: `/Users/adwaitpratapsingh/.gemini/antigravity/scratch/chronotrace/deploy/docker/docker-compose.yml`
- Create: `/Users/adwaitpratapsingh/.gemini/antigravity/scratch/chronotrace/deploy/docker/clickhouse/init.sql`
- Create: `/Users/adwaitpratapsingh/.gemini/antigravity/scratch/chronotrace/deploy/docker/nats/nats-server.conf`
- Create: `/Users/adwaitpratapsingh/.gemini/antigravity/scratch/chronotrace/deploy/docker/redis/redis.conf`
- Create: `/Users/adwaitpratapsingh/.gemini/antigravity/scratch/chronotrace/scripts/dev.sh`
- Test: `/Users/adwaitpratapsingh/.gemini/antigravity/scratch/chronotrace/deploy/docker/validate_schemas_test.go`

**Interfaces:**
- Consumes: Docker Compose specifications and ClickHouse SQL DDL syntax.
- Produces: Validated multi-arch docker compose file and DDL migrations for spans, deploy events, and service dependency graph edges.

- [ ] **Step 1: Write schema validation test ensuring ClickHouse DDL syntax and partition keys are well-formed**
- [ ] **Step 2: Run test to verify schema parser/validator**
- [ ] **Step 3: Implement Docker Compose, ClickHouse DDL migrations (`spans`, `deploy_events`, `service_dependencies`), and NATS/Redis configs**
- [ ] **Step 4: Run validation test to verify all configs and DDL statements**
- [ ] **Step 5: Commit task deliverable**

---

### Task 3: Ingestion Service (`services/ingest-api`)
**Files:**
- Create: `/Users/adwaitpratapsingh/.gemini/antigravity/scratch/chronotrace/services/ingest-api/go.mod`
- Create: `/Users/adwaitpratapsingh/.gemini/antigravity/scratch/chronotrace/services/ingest-api/main.go`
- Create: `/Users/adwaitpratapsingh/.gemini/antigravity/scratch/chronotrace/services/ingest-api/otlp/receiver.go`
- Create: `/Users/adwaitpratapsingh/.gemini/antigravity/scratch/chronotrace/services/ingest-api/webhook/github.go`
- Create: `/Users/adwaitpratapsingh/.gemini/antigravity/scratch/chronotrace/services/ingest-api/batcher/flusher.go`
- Test: `/Users/adwaitpratapsingh/.gemini/antigravity/scratch/chronotrace/services/ingest-api/otlp/receiver_test.go`
- Test: `/Users/adwaitpratapsingh/.gemini/antigravity/scratch/chronotrace/services/ingest-api/batcher/flusher_test.go`

**Interfaces:**
- Consumes: `shared/pkg/types`, `shared/pkg/bus`, Fiber v3.
- Produces: HTTP endpoints for OTLP spans (`/v1/traces`), GitHub deployment webhooks (`/webhooks/deploy`), and health probes.

- [ ] **Step 1: Write tests for OTLP span unmarshaling and batch flusher**
- [ ] **Step 2: Run test to verify it fails**
- [ ] **Step 3: Implement ingest-api receiver, webhook listener, and batched flusher (supporting embedded & NATS modes)**
- [ ] **Step 4: Run test to verify it passes**
- [ ] **Step 5: Commit task deliverable**

---

### Task 4: Chaos & Replay Simulator (`tools/chaos-sim`)
**Files:**
- Create: `/Users/adwaitpratapsingh/.gemini/antigravity/scratch/chronotrace/tools/chaos-sim/go.mod`
- Create: `/Users/adwaitpratapsingh/.gemini/antigravity/scratch/chronotrace/tools/chaos-sim/main.go`
- Create: `/Users/adwaitpratapsingh/.gemini/antigravity/scratch/chronotrace/tools/chaos-sim/topology/ecommerce.go`
- Create: `/Users/adwaitpratapsingh/.gemini/antigravity/scratch/chronotrace/tools/chaos-sim/scenarios/scenarios.go`
- Test: `/Users/adwaitpratapsingh/.gemini/antigravity/scratch/chronotrace/tools/chaos-sim/topology/ecommerce_test.go`

**Interfaces:**
- Consumes: `shared/pkg/types`.
- Produces: 12-microservice topology generator and outage injection scenarios (canary deploy crash, DB lock exhaustion, cascading 504 timeouts).

- [ ] **Step 1: Write test for 12-microservice e-commerce topology graph and trace generation**
- [ ] **Step 2: Run test to verify it fails**
- [ ] **Step 3: Implement chaos generator with scripted outage scenarios and throughput benchmark emitter**
- [ ] **Step 4: Run test to verify it passes and emits expected trace hierarchies**
- [ ] **Step 5: Commit task deliverable**

---

### Task 5: Time-Travel Engine & Causal Scorer (`services/query-engine`)
**Files:**
- Create: `/Users/adwaitpratapsingh/.gemini/antigravity/scratch/chronotrace/services/query-engine/go.mod`
- Create: `/Users/adwaitpratapsingh/.gemini/antigravity/scratch/chronotrace/services/query-engine/main.go`
- Create: `/Users/adwaitpratapsingh/.gemini/antigravity/scratch/chronotrace/services/query-engine/timetravel/reconstructor.go`
- Create: `/Users/adwaitpratapsingh/.gemini/antigravity/scratch/chronotrace/services/query-engine/causal/scorer.go`
- Create: `/Users/adwaitpratapsingh/.gemini/antigravity/scratch/chronotrace/services/query-engine/ws/streamer.go`
- Test: `/Users/adwaitpratapsingh/.gemini/antigravity/scratch/chronotrace/services/query-engine/timetravel/reconstructor_test.go`
- Test: `/Users/adwaitpratapsingh/.gemini/antigravity/scratch/chronotrace/services/query-engine/causal/scorer_test.go`

**Interfaces:**
- Consumes: `shared/pkg/store`, `shared/pkg/bus`, `shared/pkg/types`.
- Produces: Point-in-time state reconstruction (<120ms target), root-cause confidence ranking, binary WebSocket streaming handler.

- [ ] **Step 1: Write tests for point-in-time state reconstruction (I-frame + delta replay) and causal correlation scorer**
- [ ] **Step 2: Run test to verify it fails**
- [ ] **Step 3: Implement reconstructor, causal scorer, and binary WebSocket streamer**
- [ ] **Step 4: Run test to verify state reconstruction accuracy and causal ranking scores**
- [ ] **Step 5: Commit task deliverable**

---

### Task 6: SvelteKit "Obsidian Telemetry Studio" (`apps/web`)
**Files:**
- Create: `/Users/adwaitpratapsingh/.gemini/antigravity/scratch/chronotrace/apps/web/package.json`
- Create: `/Users/adwaitpratapsingh/.gemini/antigravity/scratch/chronotrace/apps/web/svelte.config.js`
- Create: `/Users/adwaitpratapsingh/.gemini/antigravity/scratch/chronotrace/apps/web/vite.config.ts`
- Create: `/Users/adwaitpratapsingh/.gemini/antigravity/scratch/chronotrace/apps/web/tailwind.config.ts`
- Create: `/Users/adwaitpratapsingh/.gemini/antigravity/scratch/chronotrace/apps/web/src/lib/workers/decoder.worker.ts`
- Create: `/Users/adwaitpratapsingh/.gemini/antigravity/scratch/chronotrace/apps/web/src/lib/components/TimelineDAW.svelte`
- Create: `/Users/adwaitpratapsingh/.gemini/antigravity/scratch/chronotrace/apps/web/src/lib/components/TopologyWebGL.svelte`
- Create: `/Users/adwaitpratapsingh/.gemini/antigravity/scratch/chronotrace/apps/web/src/lib/components/InspectorDrawer.svelte`
- Create: `/Users/adwaitpratapsingh/.gemini/antigravity/scratch/chronotrace/apps/web/src/lib/state/telemetry.svelte.ts`
- Create: `/Users/adwaitpratapsingh/.gemini/antigravity/scratch/chronotrace/apps/web/src/routes/+page.svelte`
- Test: `/Users/adwaitpratapsingh/.gemini/antigravity/scratch/chronotrace/apps/web/tests/state.test.ts`

**Interfaces:**
- Consumes: Binary WebSocket stream and REST APIs from `query-engine`.
- Produces: 60 FPS interactive DAW scrubber, WebGL2 live blast-radius topology graph, synchronized logs and causal confidence drawer.

- [ ] **Step 1: Write unit tests for telemetry state runes and binary decoder protocol**
- [ ] **Step 2: Run test to verify it fails**
- [ ] **Step 3: Implement SvelteKit app, Svelte 5 runes state store, Web Worker decoder, Canvas 2D DAW scrubber with J/K/L hotkeys, and WebGL2 topology renderer**
- [ ] **Step 4: Run test and build check (`pnpm run build` / `npm run build`)**
- [ ] **Step 5: Commit task deliverable**

---

### Task 7: End-to-End Milestone Verification & Verification Report
**Files:**
- Create: `/Users/adwaitpratapsingh/.gemini/antigravity/scratch/chronotrace/scripts/verify_milestones.sh`
- Modify: `/Users/adwaitpratapsingh/.gemini/antigravity/scratch/chronotrace/README.md`

- [ ] **Step 1: Execute end-to-end integration test (ingest-api -> chaos-sim -> query-engine -> web)**
- [ ] **Step 2: Validate 10k spans/sec throughput benchmark and <120ms p99 query latency**
- [ ] **Step 3: Document commands, architecture diagram, and postmortem workflow in README.md**
- [ ] **Step 4: Commit and finalize**
