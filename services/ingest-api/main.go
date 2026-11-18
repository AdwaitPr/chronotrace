package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/chronotrace/chronotrace/services/ingest-api/metrics"
	"github.com/chronotrace/chronotrace/services/ingest-api/otlp"
	"github.com/chronotrace/chronotrace/services/ingest-api/webhook"
	"github.com/chronotrace/chronotrace/shared/bus"
	"github.com/chronotrace/chronotrace/shared/types"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/recover"
)

func main() {
	mode := os.Getenv("CHRONOTRACE_MODE")
	if mode == "" {
		mode = "embedded"
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "4318"
	}

	var eventBus bus.EventBus
	var err error

	if mode == "cluster" {
		natsURL := os.Getenv("NATS_URL")
		if natsURL == "" {
			natsURL = "nats://localhost:4222"
		}
		log.Printf("[Ingest API] Connecting to NATS JetStream at %s...", natsURL)
		eventBus, err = bus.NewNATSJetStreamBus(natsURL)
		if err != nil {
			log.Printf("[Ingest API] Warning: NATS connection failed (%v), falling back to in-memory bus", err)
			eventBus = bus.NewInMemoryEventBus(100000)
		}
	} else {
		log.Printf("[Ingest API] Running in Embedded In-Memory Mode")
		eventBus = bus.NewInMemoryEventBus(100000)
	}
	defer eventBus.Close()

	app := SetupServer(eventBus, mode)

	// Graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		log.Println("[Ingest API] Shutting down server gracefully...")
		_ = app.ShutdownWithTimeout(5 * time.Second)
	}()

	log.Printf("🚀 [Ingest API] Listening on :%s (Mode: %s)", port, mode)
	if err := app.Listen(":" + port); err != nil {
		log.Fatalf("Server stopped: %v", err)
	}
}

// SetupServer configures the Fiber routes and middleware.
func SetupServer(eventBus bus.EventBus, mode string) *fiber.App {
	app := fiber.New(fiber.Config{
		DisableStartupMessage: true,
		BodyLimit:             32 * 1024 * 1024, // 32MB max OTLP batch
		ReadTimeout:           10 * time.Second,
		WriteTimeout:          10 * time.Second,
	})

	app.Use(recover.New())
	app.Use(cors.New(cors.Config{
		AllowOrigins: "*",
		AllowHeaders: "*",
		AllowMethods: "GET,POST,OPTIONS",
	}))

	// Health and real-time telemetry metrics
	app.Get("/healthz", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{
			"status": "ok",
			"mode":   mode,
			"time":   time.Now().UnixNano(),
		})
	})

	app.Get("/metrics", func(c *fiber.Ctx) error {
		snap := metrics.Global().Snapshot()
		return c.JSON(snap)
	})

	// OpenTelemetry Traces Ingestion
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
			metrics.Global().RecordDropped(1)
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": fmt.Sprintf("invalid trace payload: %v", err),
			})
		}

		ctx := context.Background()
		for _, span := range spans {
			if err := eventBus.PublishSpan(ctx, span); err != nil {
				metrics.Global().RecordDropped(1)
			} else {
				metrics.Global().RecordIngested(1)
			}
		}

		return c.Status(fiber.StatusOK).JSON(fiber.Map{
			"ingested": len(spans),
		})
	})

	// GitHub Deployment & Commit Webhook
	app.Post("/webhooks/github", func(c *fiber.Ctx) error {
		eventType := c.Get("X-GitHub-Event", "deployment_status")
		signature := c.Get("X-Hub-Signature-256", "")
		secret := os.Getenv("GITHUB_WEBHOOK_SECRET")

		deployEvent, err := webhook.ParseGitHubWebhook(c.Body(), eventType, signature, secret)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": err.Error(),
			})
		}

		ctx := context.Background()
		if err := eventBus.PublishDeploy(ctx, deployEvent); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "failed to publish deploy event",
			})
		}

		return c.Status(fiber.StatusOK).JSON(fiber.Map{
			"status": "accepted",
			"deploy": deployEvent,
		})
	})

	return app
}
