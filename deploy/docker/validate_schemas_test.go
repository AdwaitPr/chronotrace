package docker

import (
	"os"
	"strings"
	"testing"
)

func TestDockerCompose_ResourceLimits(t *testing.T) {
	data, err := os.ReadFile("docker-compose.yml")
	if err != nil {
		t.Fatalf("failed to read docker-compose.yml: %v", err)
	}
	content := string(data)

	// Validate services present
	services := []string{"clickhouse:", "nats:", "redis:"}
	for _, s := range services {
		if !strings.Contains(content, s) {
			t.Errorf("expected service %s in docker-compose.yml", s)
		}
	}

	// Validate memory caps
	if !strings.Contains(content, "memory: 1536M") {
		t.Errorf("expected ClickHouse memory capped at 1536M (1.5GB)")
	}
	if !strings.Contains(content, "memory: 256M") {
		t.Errorf("expected Redis/NATS memory capped at 256M")
	}

	// Ensure postgres is NOT present (as requested for lightweight ARM64 footprint)
	if strings.Contains(strings.ToLower(content), "postgres") {
		t.Errorf("postgres should not be present in docker-compose.yml")
	}
}

func TestClickHouse_DDLValid(t *testing.T) {
	data, err := os.ReadFile("clickhouse/init.sql")
	if err != nil {
		t.Fatalf("failed to read init.sql: %v", err)
	}
	sql := string(data)

	requiredClauses := []string{
		"CREATE DATABASE IF NOT EXISTS chronotrace;",
		"CREATE TABLE IF NOT EXISTS chronotrace.spans",
		"LowCardinality(String)",
		"CODEC(ZSTD(1))",
		"CODEC(Gorilla, ZSTD(1))",
		"CREATE TABLE IF NOT EXISTS chronotrace.deploy_events",
		"CREATE TABLE IF NOT EXISTS chronotrace.service_metrics_1s",
		"CREATE MATERIALIZED VIEW IF NOT EXISTS chronotrace.mv_service_metrics_1s",
	}

	for _, clause := range requiredClauses {
		if !strings.Contains(sql, clause) {
			t.Errorf("missing expected DDL clause: %s", clause)
		}
	}
}

func TestNATSAndRedis_Configs(t *testing.T) {
	natsData, err := os.ReadFile("nats/nats-server.conf")
	if err != nil {
		t.Fatalf("failed to read nats-server.conf: %v", err)
	}
	if !strings.Contains(string(natsData), "max_mem: 268435456") {
		t.Errorf("expected NATS max_mem 256MB cap")
	}

	redisData, err := os.ReadFile("redis/redis.conf")
	if err != nil {
		t.Fatalf("failed to read redis.conf: %v", err)
	}
	if !strings.Contains(string(redisData), "maxmemory 256mb") {
		t.Errorf("expected Redis maxmemory 256mb cap")
	}
}
