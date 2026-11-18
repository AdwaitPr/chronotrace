package generator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/chronotrace/chronotrace/shared/store"
	"github.com/chronotrace/chronotrace/shared/types"
	"github.com/chronotrace/chronotrace/tools/chaos-sim/scenarios"
	"github.com/chronotrace/chronotrace/tools/chaos-sim/topology"
)

// HTTPEmitter sends OTLP spans and GitHub webhook events to the ingest API.
type HTTPEmitter struct {
	client     *http.Client
	tracesURL  string
	webhookURL string
}

// NewHTTPEmitter creates a tuned high-throughput HTTP emitter.
func NewHTTPEmitter(baseURL string) *HTTPEmitter {
	transport := &http.Transport{
		MaxIdleConns:        500,
		MaxIdleConnsPerHost: 500,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  true, // Low latency
	}

	return &HTTPEmitter{
		client: &http.Client{
			Transport: transport,
			Timeout:   10 * time.Second,
		},
		tracesURL:  baseURL + "/v1/traces",
		webhookURL: baseURL + "/webhooks/github",
	}
}

// SendSpansBatch formats and POSTs an OTLP JSON batch of spans.
func (e *HTTPEmitter) SendSpansBatch(spans []*types.Span) error {
	type otlpAttr struct {
		Key   string         `json:"key"`
		Value map[string]any `json:"value"`
	}

	type otlpSpan struct {
		TraceID           string     `json:"traceId"`
		SpanID            string     `json:"spanId"`
		ParentSpanID      string     `json:"parentSpanId,omitempty"`
		Name              string     `json:"name"`
		Kind              int        `json:"kind"`
		StartTimeUnixNano string     `json:"startTimeUnixNano"`
		EndTimeUnixNano   string     `json:"endTimeUnixNano"`
		Attributes        []otlpAttr `json:"attributes,omitempty"`
		Status            struct {
			Code    int    `json:"code"`
			Message string `json:"message,omitempty"`
		} `json:"status"`
	}

	type scopeSpan struct {
		Spans []otlpSpan `json:"spans"`
	}

	type resourceSpan struct {
		Resource struct {
			Attributes []otlpAttr `json:"attributes"`
		} `json:"resource"`
		ScopeSpans []scopeSpan `json:"scopeSpans"`
	}

	// Group spans by service name
	grouped := make(map[string][]otlpSpan)
	for _, s := range spans {
		var attrs []otlpAttr
		for k, v := range s.Attributes {
			attrs = append(attrs, otlpAttr{
				Key:   k,
				Value: map[string]any{"stringValue": v},
			})
		}

		statusCode := 1 // OK
		if s.StatusCode == "ERROR" {
			statusCode = 2 // ERROR
		}

		os := otlpSpan{
			TraceID:           s.TraceID,
			SpanID:            s.SpanID,
			ParentSpanID:      s.ParentSpanID,
			Name:              s.OperationName,
			Kind:              2, // SERVER
			StartTimeUnixNano: fmt.Sprintf("%d", s.StartTimeUnixNano),
			EndTimeUnixNano:   fmt.Sprintf("%d", s.EndTimeUnixNano),
			Attributes:        attrs,
		}
		os.Status.Code = statusCode
		os.Status.Message = s.StatusMessage

		grouped[s.ServiceName] = append(grouped[s.ServiceName], os)
	}

	var resourceSpans []resourceSpan
	for svcName, sList := range grouped {
		var rs resourceSpan
		rs.Resource.Attributes = []otlpAttr{
			{Key: "service.name", Value: map[string]any{"stringValue": svcName}},
		}
		rs.ScopeSpans = []scopeSpan{{Spans: sList}}
		resourceSpans = append(resourceSpans, rs)
	}

	payload := map[string]any{
		"resourceSpans": resourceSpans,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", e.tracesURL, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d from %s", resp.StatusCode, e.tracesURL)
	}
	return nil
}

// SendDeployEvent sends a deploy event webhook to the ingest API.
func (e *HTTPEmitter) SendDeployEvent(deploy *types.DeployEvent) error {
	payload := map[string]any{
		"deployment": map[string]any{
			"id":          1001,
			"sha":         deploy.CommitHash,
			"ref":         "main",
			"environment": deploy.Environment,
			"description": deploy.CommitMessage,
			"creator": map[string]any{
				"login": deploy.Author,
			},
		},
		"repository": map[string]any{
			"name": deploy.ServiceName,
		},
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", e.webhookURL, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Event", "deployment_status")

	resp, err := e.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d from %s", resp.StatusCode, e.webhookURL)
	}
	return nil
}

// FastBackfillHistoricalTimeline generates a complete 15-minute historical timeline in <2 seconds.
func FastBackfillHistoricalTimeline(ctx context.Context, st store.TelemetryStore, sc *scenarios.Scenario, backfillMinutes int) (int, error) {
	if backfillMinutes <= 0 {
		backfillMinutes = 15
	}

	topo := topology.NewECommerceTopology()
	now := time.Now()
	startTime := now.Add(-time.Duration(backfillMinutes) * time.Minute)

	// Timeline structure:
	// Minute 0 to 8: Normal healthy baseline traffic (all OK)
	// Minute 8 (7 mins ago): Correlated Deploy event deployed
	// Minute 9.5 to 13: Outage window (severe latency spike + cascading 504s)
	// Minute 13 to 15: Outage mitigation / rollback (recovering traffic)

	deployTime := startTime.Add(8 * time.Minute)
	deployEvent := sc.GenerateDeployEvent(deployTime)
	if err := st.WriteDeploy(ctx, &deployEvent); err != nil {
		return 0, fmt.Errorf("failed to write deploy event: %w", err)
	}

	outageStart := deployTime.Add(time.Duration(sc.LeadTimeSeconds) * time.Second)
	outageEnd := outageStart.Add(4 * time.Minute)

	totalSpans := 0
	batch := make([]*types.Span, 0, 5000)

	// Generate 15 minutes of trace batches (step = 2 seconds)
	step := 2 * time.Second
	for curr := startTime; curr.Before(now); curr = curr.Add(step) {
		isOutage := curr.After(outageStart) && curr.Before(outageEnd)

		var outParams *topology.OutageParams
		if isOutage {
			outParams = &sc.OutageParams
		}

		// Generate 30 traces per 2-second tick = 15 traces/sec = 180 spans/sec
		for i := 0; i < 30; i++ {
			tSpans := topo.GenerateTrace(curr.Add(time.Duration(i*50)*time.Millisecond), outParams)
			batch = append(batch, tSpans...)
			totalSpans += len(tSpans)

			if len(batch) >= 5000 {
				if err := st.WriteSpans(ctx, batch); err != nil {
					return totalSpans, err
				}
				batch = make([]*types.Span, 0, 5000)
			}
		}
	}

	if len(batch) > 0 {
		if err := st.WriteSpans(ctx, batch); err != nil {
			return totalSpans, err
		}
	}

	return totalSpans, nil
}
