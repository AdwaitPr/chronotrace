package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http/httptest"
	"testing"

	"github.com/chronotrace/chronotrace/shared/bus"
)

func TestIngestServer_Routes(t *testing.T) {
	b := bus.NewInMemoryEventBus(1000)
	defer b.Close()

	app := SetupServer(b, "embedded")

	// 1. Test /healthz
	req := httptest.NewRequest("GET", "/healthz", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("GET /healthz failed: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("expected 200 OK from /healthz, got %d", resp.StatusCode)
	}

	// 2. Test /v1/traces
	tracePayload := `{
		"resourceSpans": [
			{
				"resource": {
					"attributes": [
						{"key": "service.name", "value": {"stringValue": "checkout-svc"}}
					]
				},
				"scopeSpans": [
					{
						"spans": [
							{
								"traceId": "trace12345",
								"spanId": "span12345",
								"name": "SubmitOrder",
								"startTimeUnixNano": "1700000000000000000",
								"endTimeUnixNano": "1700000000100000000"
							}
						]
					}
				]
			}
		]
	}`

	reqTrace := httptest.NewRequest("POST", "/v1/traces", bytes.NewReader([]byte(tracePayload)))
	reqTrace.Header.Set("Content-Type", "application/json")
	respTrace, err := app.Test(reqTrace)
	if err != nil {
		t.Fatalf("POST /v1/traces failed: %v", err)
	}
	if respTrace.StatusCode != 200 {
		t.Errorf("expected 200 from /v1/traces, got %d", respTrace.StatusCode)
	}

	// 3. Test /webhooks/github
	deployPayload := `{
		"deployment": {
			"id": 888,
			"sha": "fedcba987654",
			"ref": "main",
			"environment": "production",
			"description": "Production rollout v3.0",
			"creator": {"login": "deploybot"}
		},
		"repository": {"name": "checkout-svc"}
	}`
	reqDeploy := httptest.NewRequest("POST", "/webhooks/github", bytes.NewReader([]byte(deployPayload)))
	reqDeploy.Header.Set("Content-Type", "application/json")
	reqDeploy.Header.Set("X-GitHub-Event", "deployment_status")
	respDeploy, err := app.Test(reqDeploy)
	if err != nil {
		t.Fatalf("POST /webhooks/github failed: %v", err)
	}
	if respDeploy.StatusCode != 200 {
		t.Errorf("expected 200 from /webhooks/github, got %d", respDeploy.StatusCode)
	}

	// 4. Test /metrics
	reqMetrics := httptest.NewRequest("GET", "/metrics", nil)
	respMetrics, err := app.Test(reqMetrics)
	if err != nil {
		t.Fatalf("GET /metrics failed: %v", err)
	}
	body, _ := io.ReadAll(respMetrics.Body)
	var stats map[string]any
	if err := json.Unmarshal(body, &stats); err != nil {
		t.Fatalf("failed to unmarshal /metrics json: %v", err)
	}
	if _, ok := stats["heap_alloc_mb"]; !ok {
		t.Errorf("expected heap_alloc_mb in metrics response")
	}
}
