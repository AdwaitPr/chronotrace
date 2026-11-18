module github.com/chronotrace/chronotrace/services/query-engine

go 1.27

require (
	github.com/ClickHouse/clickhouse-go/v2 v2.26.0
	github.com/chronotrace/chronotrace/shared v0.0.0
	github.com/gofiber/fiber/v2 v2.52.5
	github.com/nats-io/nats.go v1.38.0
	github.com/redis/go-redis/v9 v9.6.1
)

replace github.com/chronotrace/chronotrace/shared => ../../shared/pkg
