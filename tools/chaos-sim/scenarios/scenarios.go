package scenarios

import (
	"fmt"
	"time"

	"github.com/chronotrace/chronotrace/shared/types"
	"github.com/chronotrace/chronotrace/tools/chaos-sim/topology"
)

// Scenario represents a pre-scripted outage scenario with a correlated deploy pin.
type Scenario struct {
	Name            string
	Title           string
	Description     string
	CorrelatedDeploy types.DeployEvent
	OutageParams    topology.OutageParams
	LeadTimeSeconds int // Seconds between deploy pin and outage eruption
}

var registry = map[string]*Scenario{
	"canary-bad-pool": {
		Name:        "canary-bad-pool",
		Title:       "Canary Connection Pool Starvation",
		Description: "A canary release on payment-worker restricts connection pool to max_connections=2, starving threads under concurrency.",
		LeadTimeSeconds: 75,
		CorrelatedDeploy: types.DeployEvent{
			ID:            "dep-canary-042",
			ServiceName:   "payment-worker",
			Environment:   "canary",
			CommitHash:    "f8a3b19",
			CommitMessage: "perf(db): throttle connection pool size to max_connections=2",
			Author:        "sre-canary-runner",
			DiffSummary:   "- max_connections: 50\n+ max_connections: 2",
		},
		OutageParams: topology.OutageParams{
			TargetService:  "payment-worker",
			ErrorRate:      0.88,
			LatencySpikeMs: 3500.0,
			ErrorMessage:   "FATAL: remaining connection slots are reserved for non-replication superuser connections",
		},
	},
	"db-lock-storm": {
		Name:        "db-lock-storm",
		Title:       "Primary Database Table Lock Contention",
		Description: "An unindexed migration takes exclusive table lock on ledger_accounts, blocking all write paths.",
		LeadTimeSeconds: 60,
		CorrelatedDeploy: types.DeployEvent{
			ID:            "dep-migr-109",
			ServiceName:   "postgres-primary",
			Environment:   "production",
			CommitHash:    "c7d2e48",
			CommitMessage: "migration: add foreign key constraint on ledger_accounts without NOT VALID",
			Author:        "db-admin",
			DiffSummary:   "ALTER TABLE ledger_accounts ADD CONSTRAINT fk_user_id FOREIGN KEY (user_id) REFERENCES users(id);",
		},
		OutageParams: topology.OutageParams{
			TargetService:  "postgres-primary",
			ErrorRate:      0.92,
			LatencySpikeMs: 8000.0,
			ErrorMessage:   "canceling statement due to statement timeout (exclusive table lock wait)",
		},
	},
	"redis-eviction-cascade": {
		Name:        "redis-eviction-cascade",
		Title:       "Redis Cache TTL Eviction Stampede",
		Description: "Config update drops cache TTL from 24h to 10s with allkeys-lru eviction, slamming database with un-cached queries.",
		LeadTimeSeconds: 90,
		CorrelatedDeploy: types.DeployEvent{
			ID:            "dep-cfg-881",
			ServiceName:   "redis-cache",
			Environment:   "production",
			CommitHash:    "b3f11a0",
			CommitMessage: "config: aggressively reduce idempotent payment cache TTL to 10s",
			Author:        "infra-lead",
			DiffSummary:   "- DEFAULT_KEY_TTL = 86400\n+ DEFAULT_KEY_TTL = 10",
		},
		OutageParams: topology.OutageParams{
			TargetService:  "redis-cache",
			ErrorRate:      0.95,
			LatencySpikeMs: 1200.0,
			ErrorMessage:   "OOM command not allowed when used memory > 'maxmemory'",
		},
	},
}

// GetScenario returns the scenario by name.
func GetScenario(name string) (*Scenario, error) {
	if s, exists := registry[name]; exists {
		return s, nil
	}
	return nil, fmt.Errorf("unknown scenario: %s (available: canary-bad-pool, db-lock-storm, redis-eviction-cascade)", name)
}

// ListScenarios returns all available scenario names.
func ListScenarios() []string {
	keys := make([]string, 0, len(registry))
	for k := range registry {
		keys = append(keys, k)
	}
	return keys
}

// GenerateDeployEvent creates a timestamped DeployEvent for the scenario.
func (s *Scenario) GenerateDeployEvent(timestamp time.Time) types.DeployEvent {
	d := s.CorrelatedDeploy
	d.TimestampUnixNano = timestamp.UnixNano()
	return d
}
