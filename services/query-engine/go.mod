module github.com/chronotrace/chronotrace/services/query-engine

go 1.27

require (
	github.com/ClickHouse/clickhouse-go/v2 v2.26.0
	github.com/chronotrace/chronotrace/services/ingest-api v0.0.0
	github.com/chronotrace/chronotrace/shared v0.0.0
	github.com/chronotrace/chronotrace/tools/chaos-sim v0.0.0
	github.com/gofiber/fiber/v2 v2.52.5
	github.com/gofiber/websocket/v2 v2.2.1
	github.com/nats-io/nats.go v1.38.0
	github.com/redis/go-redis/v9 v9.6.1
)

replace github.com/chronotrace/chronotrace/shared => ../../shared/pkg
replace github.com/chronotrace/chronotrace/services/ingest-api => ../ingest-api
replace github.com/chronotrace/chronotrace/tools/chaos-sim => ../../tools/chaos-sim
