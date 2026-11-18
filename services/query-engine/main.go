package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/chronotrace/chronotrace/services/ingest-api/otlp"
	"github.com/chronotrace/chronotrace/services/ingest-api/webhook"
	"github.com/chronotrace/chronotrace/services/query-engine/api"
	"github.com/chronotrace/chronotrace/services/query-engine/causal"
	"github.com/chronotrace/chronotrace/services/query-engine/timetravel"
	"github.com/chronotrace/chronotrace/services/query-engine/ws"
	"github.com/chronotrace/chronotrace/shared/bus"
	"github.com/chronotrace/chronotrace/shared/store"
	"github.com/chronotrace/chronotrace/shared/types"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/gofiber/websocket/v2"
)

func main() {
	mode := os.Getenv("CHRONOTRACE_MODE")
	if mode == "" {
		mode = "embedded"
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	var telemetryStore store.TelemetryStore
	var keyframeStore store.KeyframeStore
	var eventBus bus.EventBus
	var metadataStore store.MetadataStore
	var err error

	// 1. Initialize local SQLite for incident postmortem metadata
	metadataStore, err = store.NewSQLiteMetadataStore("data/metadata.db")
	if err != nil {
		log.Printf("[Query Engine] Warning: SQLite store init failed (%v), using memory temp db", err)
		metadataStore, _ = store.NewSQLiteMetadataStore(":memory:")
	}
	defer metadataStore.Close()

	if mode == "cluster" {
		log.Printf("[Query Engine] Booting in Cluster Mode (ClickHouse + Redis 7 + NATS)")
		chAddr := os.Getenv("CLICKHOUSE_ADDR")
		if chAddr == "" {
			chAddr = "localhost:9000"
		}
		chStore, chErr := store.NewClickHouseBatchStore(store.ClickHouseOptions{Addr: chAddr})
		if chErr != nil {
			log.Fatalf("Failed to connect to ClickHouse at %s: %v", chAddr, chErr)
		}
		telemetryStore = chStore

		redisURL := os.Getenv("REDIS_URL")
		if redisURL == "" {
			redisURL = "localhost:6379"
		}
		kfStore, rErr := store.NewRedisKeyframeStore(redisURL)
		if rErr != nil {
			log.Fatalf("Failed to connect to Redis at %s: %v", redisURL, rErr)
		}
		keyframeStore = kfStore

		natsURL := os.Getenv("NATS_URL")
		if natsURL == "" {
			natsURL = "nats://localhost:4222"
		}
		nBus, nErr := bus.NewNATSJetStreamBus(natsURL)
		if nErr != nil {
			log.Fatalf("Failed to connect to NATS at %s: %v", natsURL, nErr)
		}
		eventBus = nBus
	} else {
		log.Printf("⚡ [Query Engine] Booting in Seamless Embedded Mode (Ring Buffer + SQLite)")
		ringStore := store.NewInMemoryTelemetryStore(250000)
		telemetryStore = ringStore
		keyframeStore = store.NewInMemoryKeyframeStore()
		inMemBus := bus.NewInMemoryEventBus(100000)
		eventBus = inMemBus

		// Pipe ingested spans and deploys directly into the ring buffer
		ctx := context.Background()
		_ = inMemBus.SubscribeSpans(ctx, func(span *types.Span) error {
			return ringStore.WriteSpans(ctx, []*types.Span{span})
		})
		_ = inMemBus.SubscribeDeploys(ctx, func(deploy *types.DeployEvent) error {
			return ringStore.WriteDeploy(ctx, deploy)
		})
	}
	defer telemetryStore.Close()
	defer keyframeStore.Close()
	defer eventBus.Close()

	// 2. Initialize Causal Scorer and Time-Travel State Reconstructor
	causalScorer := causal.NewCausalScorer(telemetryStore)
	reconstructor := timetravel.NewStateReconstructor(telemetryStore, keyframeStore, causalScorer)

	// 3. Setup Fiber Application
	app := fiber.New(fiber.Config{
		DisableStartupMessage: true,
		BodyLimit:             32 * 1024 * 1024,
	})

	app.Use(recover.New())
	app.Use(cors.New(cors.Config{
		AllowOrigins: "*",
		AllowHeaders: "*",
		AllowMethods: "GET,POST,OPTIONS",
	}))

	// Health check
	app.Get("/healthz", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{
			"status": "ok",
			"mode":   mode,
			"engine": "chronotrace-query-engine",
		})
	})

	// Mount REST Endpoints (/api/timeline/overview, /api/state, /api/notes, /api/bookmarks)
	api.RegisterRoutes(app, telemetryStore, reconstructor, metadataStore)

	// In embedded mode, mount /v1/traces and /webhooks/github directly so chaos-sim works out of the box!
	if mode == "embedded" {
		app.Post("/v1/traces", func(c *fiber.Ctx) error {
			contentType := string(c.Request().Header.ContentType())
			body := c.Body()

			var spans []*types.Span
			var err error

			if strings.Contains(contentType, "application/x-protobuf") {
				spans, err = otlp.NormalizeOTLPProtobuf(body)
			} else {
				spans, err = otlp.NormalizeOTLPJSON(body)
			}

			if err != nil {
				return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
			}

			ctx := context.Background()
			_ = telemetryStore.WriteSpans(ctx, spans)
			return c.Status(fiber.StatusOK).JSON(fiber.Map{"ingested": len(spans)})
		})

		app.Post("/webhooks/github", func(c *fiber.Ctx) error {
			eventType := c.Get("X-GitHub-Event", "deployment_status")
			signature := c.Get("X-Hub-Signature-256", "")
			deployEvent, err := webhook.ParseGitHubWebhook(c.Body(), eventType, signature, "")
			if err != nil {
				return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
			}
			ctx := context.Background()
			_ = telemetryStore.WriteDeploy(ctx, deployEvent)
			return c.Status(fiber.StatusOK).JSON(fiber.Map{"status": "accepted", "deploy": deployEvent})
		})
	}

	// WebSocket upgrade middleware
	app.Use("/ws", func(c *fiber.Ctx) error {
		if websocket.IsWebSocketUpgrade(c) {
			return c.Next()
		}
		return fiber.ErrUpgradeRequired
	})

	// WS /ws/scrub
	app.Get("/ws/scrub", websocket.New(ws.HandleScrubberWebSocket(reconstructor)))

	// Graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		log.Println("[Query Engine] Shutting down...")
		_ = app.ShutdownWithTimeout(5 * time.Second)
	}()

	log.Printf("🚀 [Query Engine] Listening on :%s (Mode: %s)", port, mode)
	if err := app.Listen(":" + port); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
