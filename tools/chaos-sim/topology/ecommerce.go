package topology

import (
	"crypto/rand"
	"encoding/hex"
	mathrand "math/rand"
	"time"

	"github.com/chronotrace/chronotrace/shared/types"
)

// ECommerceTopology models a production 12-microservice e-commerce system.
type ECommerceTopology struct {
	Services []string
}

// OutageParams models active chaos injection parameters.
type OutageParams struct {
	TargetService  string
	ErrorRate      float64
	LatencySpikeMs float64
	ErrorMessage   string
}

// NewECommerceTopology initializes the 12-microservice DAG.
func NewECommerceTopology() *ECommerceTopology {
	return &ECommerceTopology{
		Services: []string{
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
		},
	}
}

// HasService verifies if a service exists in the topology.
func (t *ECommerceTopology) HasService(name string) bool {
	for _, s := range t.Services {
		if s == name {
			return true
		}
	}
	return false
}

func randomHex(bytes int) string {
	b := make([]byte, bytes)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// GenerateTrace generates a complete, correlated multi-span distributed trace across the 12-service DAG.
func (t *ECommerceTopology) GenerateTrace(timestamp time.Time, outage *OutageParams) []*types.Span {
	traceID := randomHex(16)
	baseNano := timestamp.UnixNano()

	var spans []*types.Span

	// 1. Root: edge-cdn
	edgeSpanID := randomHex(8)
	edgeLatency := 15.0 + mathrand.Float64()*10.0
	isOutageTarget := outage != nil && outage.TargetService == "edge-cdn"

	// 2. api-gateway (Child of edge-cdn)
	gwSpanID := randomHex(8)
	gwLatency := 25.0 + mathrand.Float64()*15.0

	// 3. auth-svc
	authSpanID := randomHex(8)
	authLatency := 10.0 + mathrand.Float64()*5.0

	// 4. catalog-svc
	catSpanID := randomHex(8)
	catLatency := 12.0 + mathrand.Float64()*8.0

	// 5. cart-svc
	cartSpanID := randomHex(8)
	cartLatency := 14.0 + mathrand.Float64()*6.0

	// 6. checkout-svc
	chkSpanID := randomHex(8)
	chkLatency := 45.0 + mathrand.Float64()*20.0

	// Downstream of checkout-svc:
	// 7. fraud-engine
	fraudSpanID := randomHex(8)
	fraudLatency := 18.0 + mathrand.Float64()*7.0

	// 8. inventory-svc
	invSpanID := randomHex(8)
	invLatency := 22.0 + mathrand.Float64()*10.0

	// 9. notification-svc (async worker)
	notifSpanID := randomHex(8)
	notifLatency := 8.0 + mathrand.Float64()*5.0

	// 10. payment-worker
	paySpanID := randomHex(8)
	payLatency := 35.0 + mathrand.Float64()*15.0
	payStatus := "OK"
	payMsg := ""

	// 11. postgres-primary
	pgSpanID := randomHex(8)
	pgLatency := 5.0 + mathrand.Float64()*5.0
	pgStatus := "OK"
	pgMsg := ""

	// 12. redis-cache
	redisSpanID := randomHex(8)
	redisLatency := 1.5 + mathrand.Float64()*1.0
	redisStatus := "OK"
	redisMsg := ""

	// Check outage injection
	if outage != nil {
		if outage.TargetService == "payment-worker" && mathrand.Float64() < outage.ErrorRate {
			payLatency += outage.LatencySpikeMs
			payStatus = "ERROR"
			payMsg = outage.ErrorMessage
			// Cascading failure propagates up to checkout-svc and api-gateway
			chkLatency += payLatency
			gwLatency += payLatency
			edgeLatency += payLatency
		} else if outage.TargetService == "postgres-primary" && mathrand.Float64() < outage.ErrorRate {
			pgLatency += outage.LatencySpikeMs
			pgStatus = "ERROR"
			pgMsg = outage.ErrorMessage
			payLatency += pgLatency
			payStatus = "ERROR"
			payMsg = "downstream database failure: " + outage.ErrorMessage
			chkLatency += payLatency
			gwLatency += payLatency
		} else if outage.TargetService == "redis-cache" && mathrand.Float64() < outage.ErrorRate {
			redisStatus = "ERROR"
			redisMsg = outage.ErrorMessage
			// Cache stampede drives 5x DB latency
			pgLatency += 200.0 + mathrand.Float64()*150.0
			payLatency += pgLatency
		}
	}

	currNano := baseNano

	// Span 1: edge-cdn
	edgeEndNano := currNano + int64(edgeLatency*1e6)
	spans = append(spans, &types.Span{
		TraceID:           traceID,
		SpanID:            edgeSpanID,
		ServiceName:       "edge-cdn",
		OperationName:     "HTTP GET /checkout",
		SpanKind:          "SERVER",
		StartTimeUnixNano: currNano,
		EndTimeUnixNano:   edgeEndNano,
		DurationMs:        edgeLatency,
		StatusCode:        pickStatus(isOutageTarget || payStatus == "ERROR"),
		StatusMessage:     pickMsg(payStatus == "ERROR", "504 Gateway Timeout"),
		Attributes: map[string]string{
			"http.status_code": pickCode(payStatus == "ERROR", "504", "200"),
			"http.route":       "/checkout",
		},
	})

	// Span 2: api-gateway
	currNano += int64(2 * time.Millisecond)
	gwEndNano := currNano + int64(gwLatency*1e6)
	spans = append(spans, &types.Span{
		TraceID:           traceID,
		SpanID:            gwSpanID,
		ParentSpanID:      edgeSpanID,
		ServiceName:       "api-gateway",
		OperationName:     "RouteTraffic",
		SpanKind:          "INTERNAL",
		StartTimeUnixNano: currNano,
		EndTimeUnixNano:   gwEndNano,
		DurationMs:        gwLatency,
		StatusCode:        pickStatus(payStatus == "ERROR"),
		StatusMessage:     pickMsg(payStatus == "ERROR", "upstream timeout from checkout-svc"),
	})

	// Span 3: auth-svc
	spans = append(spans, &types.Span{
		TraceID:           traceID,
		SpanID:            authSpanID,
		ParentSpanID:      gwSpanID,
		ServiceName:       "auth-svc",
		OperationName:     "VerifySessionToken",
		SpanKind:          "CLIENT",
		StartTimeUnixNano: currNano + int64(1*time.Millisecond),
		EndTimeUnixNano:   currNano + int64(1*time.Millisecond) + int64(authLatency*1e6),
		DurationMs:        authLatency,
		StatusCode:        "OK",
	})

	// Span 4: catalog-svc
	spans = append(spans, &types.Span{
		TraceID:           traceID,
		SpanID:            catSpanID,
		ParentSpanID:      gwSpanID,
		ServiceName:       "catalog-svc",
		OperationName:     "GetProductDetails",
		SpanKind:          "CLIENT",
		StartTimeUnixNano: currNano + int64(2*time.Millisecond),
		EndTimeUnixNano:   currNano + int64(2*time.Millisecond) + int64(catLatency*1e6),
		DurationMs:        catLatency,
		StatusCode:        "OK",
	})

	// Span 5: cart-svc
	spans = append(spans, &types.Span{
		TraceID:           traceID,
		SpanID:            cartSpanID,
		ParentSpanID:      gwSpanID,
		ServiceName:       "cart-svc",
		OperationName:     "LockCartItems",
		SpanKind:          "CLIENT",
		StartTimeUnixNano: currNano + int64(3*time.Millisecond),
		EndTimeUnixNano:   currNano + int64(3*time.Millisecond) + int64(cartLatency*1e6),
		DurationMs:        cartLatency,
		StatusCode:        "OK",
	})

	// Span 6: checkout-svc
	chkStartNano := currNano + int64(5*time.Millisecond)
	chkEndNano := chkStartNano + int64(chkLatency*1e6)
	spans = append(spans, &types.Span{
		TraceID:           traceID,
		SpanID:            chkSpanID,
		ParentSpanID:      gwSpanID,
		ServiceName:       "checkout-svc",
		OperationName:     "ExecuteOrderPipeline",
		SpanKind:          "SERVER",
		StartTimeUnixNano: chkStartNano,
		EndTimeUnixNano:   chkEndNano,
		DurationMs:        chkLatency,
		StatusCode:        pickStatus(payStatus == "ERROR"),
		StatusMessage:     pickMsg(payStatus == "ERROR", "payment-worker timed out after retry"),
	})

	// Span 7: fraud-engine
	spans = append(spans, &types.Span{
		TraceID:           traceID,
		SpanID:            fraudSpanID,
		ParentSpanID:      chkSpanID,
		ServiceName:       "fraud-engine",
		OperationName:     "EvaluateRiskScore",
		SpanKind:          "CLIENT",
		StartTimeUnixNano: chkStartNano + int64(1*time.Millisecond),
		EndTimeUnixNano:   chkStartNano + int64(1*time.Millisecond) + int64(fraudLatency*1e6),
		DurationMs:        fraudLatency,
		StatusCode:        "OK",
	})

	// Span 8: inventory-svc
	spans = append(spans, &types.Span{
		TraceID:           traceID,
		SpanID:            invSpanID,
		ParentSpanID:      chkSpanID,
		ServiceName:       "inventory-svc",
		OperationName:     "DeductInventoryAllocation",
		SpanKind:          "CLIENT",
		StartTimeUnixNano: chkStartNano + int64(2*time.Millisecond),
		EndTimeUnixNano:   chkStartNano + int64(2*time.Millisecond) + int64(invLatency*1e6),
		DurationMs:        invLatency,
		StatusCode:        "OK",
	})

	// Span 9: notification-svc
	spans = append(spans, &types.Span{
		TraceID:           traceID,
		SpanID:            notifSpanID,
		ParentSpanID:      chkSpanID,
		ServiceName:       "notification-svc",
		OperationName:     "QueueReceiptDispatch",
		SpanKind:          "PRODUCER",
		StartTimeUnixNano: chkStartNano + int64(3*time.Millisecond),
		EndTimeUnixNano:   chkStartNano + int64(3*time.Millisecond) + int64(notifLatency*1e6),
		DurationMs:        notifLatency,
		StatusCode:        "OK",
	})

	// Span 10: payment-worker
	payStartNano := chkStartNano + int64(4*time.Millisecond)
	payEndNano := payStartNano + int64(payLatency*1e6)
	spans = append(spans, &types.Span{
		TraceID:           traceID,
		SpanID:            paySpanID,
		ParentSpanID:      chkSpanID,
		ServiceName:       "payment-worker",
		OperationName:     "CaptureCharge",
		SpanKind:          "SERVER",
		StartTimeUnixNano: payStartNano,
		EndTimeUnixNano:   payEndNano,
		DurationMs:        payLatency,
		StatusCode:        payStatus,
		StatusMessage:     payMsg,
	})

	// Span 11: redis-cache
	spans = append(spans, &types.Span{
		TraceID:           traceID,
		SpanID:            redisSpanID,
		ParentSpanID:      paySpanID,
		ServiceName:       "redis-cache",
		OperationName:     "GET payment_idem_key",
		SpanKind:          "CLIENT",
		StartTimeUnixNano: payStartNano + int64(1*time.Millisecond),
		EndTimeUnixNano:   payStartNano + int64(1*time.Millisecond) + int64(redisLatency*1e6),
		DurationMs:        redisLatency,
		StatusCode:        redisStatus,
		StatusMessage:     redisMsg,
	})

	// Span 12: postgres-primary
	spans = append(spans, &types.Span{
		TraceID:           traceID,
		SpanID:            pgSpanID,
		ParentSpanID:      paySpanID,
		ServiceName:       "postgres-primary",
		OperationName:     "UPDATE ledger_accounts",
		SpanKind:          "CLIENT",
		StartTimeUnixNano: payStartNano + int64(3*time.Millisecond),
		EndTimeUnixNano:   payStartNano + int64(3*time.Millisecond) + int64(pgLatency*1e6),
		DurationMs:        pgLatency,
		StatusCode:        pgStatus,
		StatusMessage:     pgMsg,
	})

	return spans
}

func pickStatus(isError bool) string {
	if isError {
		return "ERROR"
	}
	return "OK"
}

func pickMsg(isError bool, msg string) string {
	if isError {
		return msg
	}
	return ""
}

func pickCode(isError bool, errCode, okCode string) string {
	if isError {
		return errCode
	}
	return okCode
}
