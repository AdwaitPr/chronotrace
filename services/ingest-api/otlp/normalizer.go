package otlp

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/chronotrace/chronotrace/shared/types"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
)

var sensitiveKeySet = map[string]struct{}{
	"authorization": {},
	"cookie":        {},
	"set-cookie":    {},
	"x-api-key":     {},
	"api_key":       {},
	"apikey":        {},
	"token":         {},
	"password":      {},
	"secret":        {},
	"jwt":           {},
	"bearer":        {},
}

// IsSensitiveKey checks whether an attribute key should be scrubbed.
func IsSensitiveKey(key string) bool {
	lower := strings.ToLower(strings.TrimSpace(key))
	if _, ok := sensitiveKeySet[lower]; ok {
		return true
	}
	return strings.Contains(lower, "password") ||
		strings.Contains(lower, "secret") ||
		strings.Contains(lower, "token") ||
		strings.Contains(lower, "api-key")
}

// NormalizeRaw constructs a sanitized types.Span directly from raw inputs.
func NormalizeRaw(
	traceID, spanID, parentSpanID, serviceName, operationName, spanKind string,
	startNano, endNano int64,
	statusCode, statusMsg string,
	attrs map[string]string,
) *types.Span {
	sanitizedAttrs := make(map[string]string, len(attrs))
	for k, v := range attrs {
		if !IsSensitiveKey(k) {
			sanitizedAttrs[k] = v
		}
	}

	durationMs := float64(endNano-startNano) / 1e6
	if durationMs < 0 {
		durationMs = 0
	}

	return &types.Span{
		TraceID:           traceID,
		SpanID:            spanID,
		ParentSpanID:      parentSpanID,
		ServiceName:       serviceName,
		OperationName:     operationName,
		SpanKind:          spanKind,
		StartTimeUnixNano: startNano,
		EndTimeUnixNano:   endNano,
		DurationMs:        durationMs,
		StatusCode:        statusCode,
		StatusMessage:     statusMsg,
		Attributes:        sanitizedAttrs,
	}
}

// NormalizeOTLPProtobuf decodes an OpenTelemetry Protobuf trace export request.
func NormalizeOTLPProtobuf(data []byte) ([]*types.Span, error) {
	var req coltracepb.ExportTraceServiceRequest
	if err := proto.Unmarshal(data, &req); err != nil {
		return nil, fmt.Errorf("failed to unmarshal OTLP proto: %w", err)
	}

	var spans []*types.Span
	for _, rs := range req.GetResourceSpans() {
		svcName := "unknown-service"
		if rs.GetResource() != nil {
			for _, attr := range rs.GetResource().GetAttributes() {
				if attr.GetKey() == "service.name" {
					svcName = attr.GetValue().GetStringValue()
					break
				}
			}
		}

		for _, ss := range rs.GetScopeSpans() {
			for _, s := range ss.GetSpans() {
				traceID := hex.EncodeToString(s.GetTraceId())
				spanID := hex.EncodeToString(s.GetSpanId())
				parentSpanID := ""
				if len(s.GetParentSpanId()) > 0 {
					parentSpanID = hex.EncodeToString(s.GetParentSpanId())
				}

				statusCode := "OK"
				statusMsg := ""
				if s.GetStatus() != nil {
					switch s.GetStatus().GetCode() {
					case tracepb.Status_STATUS_CODE_ERROR:
						statusCode = "ERROR"
					case tracepb.Status_STATUS_CODE_UNSET:
						statusCode = "UNSET"
					default:
						statusCode = "OK"
					}
					statusMsg = s.GetStatus().GetMessage()
				}

				attrs := make(map[string]string)
				for _, a := range s.GetAttributes() {
					if !IsSensitiveKey(a.GetKey()) {
						attrs[a.GetKey()] = formatProtoValue(a.GetValue())
					}
				}

				startNano := int64(s.GetStartTimeUnixNano())
				endNano := int64(s.GetEndTimeUnixNano())
				durationMs := float64(endNano-startNano) / 1e6

				spans = append(spans, &types.Span{
					TraceID:           traceID,
					SpanID:            spanID,
					ParentSpanID:      parentSpanID,
					ServiceName:       svcName,
					OperationName:     s.GetName(),
					SpanKind:          s.GetKind().String(),
					StartTimeUnixNano: startNano,
					EndTimeUnixNano:   endNano,
					DurationMs:        durationMs,
					StatusCode:        statusCode,
					StatusMessage:     statusMsg,
					Attributes:        attrs,
				})
			}
		}
	}

	return spans, nil
}

// NormalizeOTLPJSON parses JSON-encoded OTLP payload.
func NormalizeOTLPJSON(data []byte) ([]*types.Span, error) {
	var payload struct {
		ResourceSpans []struct {
			Resource struct {
				Attributes []struct {
					Key   string `json:"key"`
					Value struct {
						StringValue string `json:"stringValue"`
					} `json:"value"`
				} `json:"attributes"`
			} `json:"resource"`
			ScopeSpans []struct {
				Spans []struct {
					TraceID           string `json:"traceId"`
					SpanID            string `json:"spanId"`
					ParentSpanID      string `json:"parentSpanId"`
					Name              string `json:"name"`
					Kind              int    `json:"kind"`
					StartTimeUnixNano any    `json:"startTimeUnixNano"`
					EndTimeUnixNano   any    `json:"endTimeUnixNano"`
					Attributes        []struct {
						Key   string `json:"key"`
						Value any    `json:"value"`
					} `json:"attributes"`
					Status struct {
						Code    int    `json:"code"`
						Message string `json:"message"`
					} `json:"status"`
				} `json:"spans"`
			} `json:"scopeSpans"`
		} `json:"resourceSpans"`
	}

	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("failed to parse OTLP JSON: %w", err)
	}

	var result []*types.Span

	for _, rs := range payload.ResourceSpans {
		svcName := "unknown-service"
		for _, attr := range rs.Resource.Attributes {
			if attr.Key == "service.name" {
				svcName = attr.Value.StringValue
				break
			}
		}

		for _, ss := range rs.ScopeSpans {
			for _, s := range ss.Spans {
				startNano := parseNano(s.StartTimeUnixNano)
				endNano := parseNano(s.EndTimeUnixNano)
				durationMs := float64(endNano-startNano) / 1e6
				if durationMs < 0 {
					durationMs = 0
				}

				statusCode := "OK"
				if s.Status.Code == 2 {
					statusCode = "ERROR"
				} else if s.Status.Code == 0 {
					statusCode = "UNSET"
				}

				attrs := make(map[string]string)
				for _, attr := range s.Attributes {
					if !IsSensitiveKey(attr.Key) {
						attrs[attr.Key] = extractJSONValue(attr.Value)
					}
				}

				result = append(result, &types.Span{
					TraceID:           s.TraceID,
					SpanID:            s.SpanID,
					ParentSpanID:      s.ParentSpanID,
					ServiceName:       svcName,
					OperationName:     s.Name,
					SpanKind:          kindToString(s.Kind),
					StartTimeUnixNano: startNano,
					EndTimeUnixNano:   endNano,
					DurationMs:        durationMs,
					StatusCode:        statusCode,
					StatusMessage:     s.Status.Message,
					Attributes:        attrs,
				})
			}
		}
	}

	return result, nil
}

func parseNano(val any) int64 {
	switch v := val.(type) {
	case string:
		n, _ := strconv.ParseInt(v, 10, 64)
		return n
	case float64:
		return int64(v)
	case int64:
		return v
	default:
		return 0
	}
}

func extractJSONValue(val any) string {
	if m, ok := val.(map[string]any); ok {
		for _, v := range m {
			return fmt.Sprintf("%v", v)
		}
	}
	return fmt.Sprintf("%v", val)
}

func formatProtoValue(v any) string {
	return fmt.Sprintf("%v", v)
}

func kindToString(k int) string {
	switch k {
	case 1:
		return "INTERNAL"
	case 2:
		return "SERVER"
	case 3:
		return "CLIENT"
	case 4:
		return "PRODUCER"
	case 5:
		return "CONSUMER"
	default:
		return "SERVER"
	}
}
