module github.com/chronotrace/chronotrace/services/ingest-api

go 1.27

require (
	github.com/chronotrace/chronotrace/shared v0.0.0
	github.com/chronotrace/chronotrace/tools/chaos-sim v0.0.0
	github.com/gofiber/fiber/v2 v2.52.5
	github.com/nats-io/nats.go v1.38.0
	go.opentelemetry.io/proto/otlp v1.3.1
	google.golang.org/protobuf v1.34.2
)

replace github.com/chronotrace/chronotrace/shared => ../../shared/pkg
replace github.com/chronotrace/chronotrace/tools/chaos-sim => ../../tools/chaos-sim
