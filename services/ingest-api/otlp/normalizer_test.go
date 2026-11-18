package otlp

import (
	"encoding/json"
	"testing"
	"time"
)

func TestNormalizeOTLPJSON_ScrubSensitiveHeaders(t *testing.T) {
	otlpJSON := `{
		"resourceSpans": [
			{
				"resource": {
					"attributes": [
						{"key": "service.name", "value": {"stringValue": "payment-svc"}},
						{"key": "authorization", "value": {"stringValue": "Bearer super-secret-token"}},
						{"key": "cookie", "value": {"stringValue": "session_id=12345"}}
					]
				},
				"scopeSpans": [
					{
						"spans": [
							{
								"traceId": "4bf92f3577b34da6a3ce929d0e0e4736",
								"spanId": "00f067aa0ba902b7",
								"name": "ProcessCard",
								"kind": 1,
								"startTimeUnixNano": "1700000000000000000",
								"endTimeUnixNano": "1700000000500000000",
								"attributes": [
									{"key": "http.status_code", "value": {"intValue": "500"}},
									{"key": "x-api-key", "value": {"stringValue": "secret-key-xyz"}},
									{"key": "account.id", "value": {"stringValue": "acc-999"}}
								],
								"status": {
									"code": 2,
									"message": "Internal Server Error"
								}
							}
						]
					}
				]
			}
		]
	}`

	var payload any
	if err := json.Unmarshal([]byte(otlpJSON), &payload); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	spans, err := NormalizeOTLPJSON([]byte(otlpJSON))
	if err != nil {
		t.Fatalf("NormalizeOTLPJSON failed: %v", err)
	}

	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}

	span := spans[0]
	if span.ServiceName != "payment-svc" {
		t.Errorf("expected service name payment-svc, got %s", span.ServiceName)
	}
	if span.StatusCode != "ERROR" {
		t.Errorf("expected status code ERROR, got %s", span.StatusCode)
	}
	if span.DurationMs < 490 || span.DurationMs > 510 {
		t.Errorf("expected duration ~500ms, got %f", span.DurationMs)
	}

	// Verify sensitive headers scrubbed
	if _, found := span.Attributes["authorization"]; found {
		t.Errorf("authorization header was NOT scrubbed")
	}
	if _, found := span.Attributes["cookie"]; found {
		t.Errorf("cookie header was NOT scrubbed")
	}
	if _, found := span.Attributes["x-api-key"]; found {
		t.Errorf("x-api-key attribute was NOT scrubbed")
	}
	if span.Attributes["account.id"] != "acc-999" {
		t.Errorf("expected non-sensitive account.id retained, got %s", span.Attributes["account.id"])
	}
}

func TestNormalizeOTLPSpan_Direct(t *testing.T) {
	now := time.Now()
	s := NormalizeRaw(
		"trace-1",
		"span-1",
		"",
		"edge-cdn",
		"GET /checkout",
		"SERVER",
		now.UnixNano(),
		now.Add(25*time.Millisecond).UnixNano(),
		"OK",
		"",
		map[string]string{
			"http.method":   "GET",
			"Authorization": "Bearer secret",
		},
	)

	if _, ok := s.Attributes["Authorization"]; ok {
		t.Errorf("expected Authorization to be scrubbed")
	}
	if s.DurationMs < 24 || s.DurationMs > 26 {
		t.Errorf("expected ~25ms duration, got %f", s.DurationMs)
	}
}
