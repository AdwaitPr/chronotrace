module github.com/chronotrace/chronotrace/services/ingest-api

go 1.27

require (
	github.com/chronotrace/chronotrace/shared v0.0.0
	github.com/gofiber/fiber/v2 v2.52.5
	github.com/nats-io/nats.go v1.38.0
)

replace github.com/chronotrace/chronotrace/shared => ../../shared/pkg
