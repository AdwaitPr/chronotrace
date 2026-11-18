package topology

import (
	"testing"
	"time"
)

func TestECommerceTopology_GenerateTrace(t *testing.T) {
	topo := NewECommerceTopology()

	// Verify all 12 services exist in DAG
	if len(topo.Services) != 12 {
		t.Fatalf("expected 12 microservices, got %d", len(topo.Services))
	}

	expectedServices := []string{
		"edge-cdn",
		"api-gateway",
		"auth-svc",
		"catalog-svc",
		"cart-svc",
		"checkout-svc",
		"inventory-svc",
		"payment-worker",
		"fraud-engine",
		"notification-svc",
		"postgres-primary",
		"redis-cache",
	}

	for _, svc := range expectedServices {
		if !topo.HasService(svc) {
			t.Errorf("missing service in DAG: %s", svc)
		}
	}

	// Generate a normal trace
	now := time.Now()
	spans := topo.GenerateTrace(now, nil)

	if len(spans) < 5 {
		t.Errorf("expected at least 5 spans per checkout trace, got %d", len(spans))
	}

	// Verify root span is edge-cdn
	rootSpan := spans[0]
	if rootSpan.ServiceName != "edge-cdn" {
		t.Errorf("expected root span to be edge-cdn, got %s", rootSpan.ServiceName)
	}
	if rootSpan.ParentSpanID != "" {
		t.Errorf("expected root span to have empty ParentSpanID, got %s", rootSpan.ParentSpanID)
	}

	// Verify all spans share the same TraceID
	for _, s := range spans {
		if s.TraceID != rootSpan.TraceID {
			t.Errorf("span %s has mismatching trace ID %s vs %s", s.SpanID, s.TraceID, rootSpan.TraceID)
		}
	}
}

func TestECommerceTopology_OutageInjection(t *testing.T) {
	topo := NewECommerceTopology()
	now := time.Now()

	// Simulate outage on payment-worker
	outageParams := &OutageParams{
		TargetService:  "payment-worker",
		ErrorRate:      0.90,
		LatencySpikeMs: 3500,
		ErrorMessage:   "connection pool timeout (max_connections=2)",
	}

	spans := topo.GenerateTrace(now, outageParams)

	hasError := false
	for _, s := range spans {
		if s.ServiceName == "payment-worker" && s.StatusCode == "ERROR" {
			hasError = true
			if s.DurationMs < 3000 {
				t.Errorf("expected latency spike > 3000ms, got %f", s.DurationMs)
			}
		}
	}

	if !hasError {
		t.Errorf("expected payment-worker to return ERROR status under outage injection")
	}
}
