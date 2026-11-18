package store

import (
	"context"
	"testing"
	"time"

	"github.com/chronotrace/chronotrace/shared/types"
)

func TestInMemoryRingBufferStore_WriteAndQuery(t *testing.T) {
	st := NewInMemoryTelemetryStore(1000)
	defer st.Close()

	ctx := context.Background()
	now := time.Now()

	spans := []*types.Span{
		{
			TraceID:           "trace-1",
			SpanID:            "span-1",
			ServiceName:       "order-service",
			OperationName:     "PlaceOrder",
			StartTimeUnixNano: now.Add(-10 * time.Second).UnixNano(),
			EndTimeUnixNano:   now.Add(-9 * time.Second).UnixNano(),
			DurationMs:        1000,
			StatusCode:        "OK",
		},
		{
			TraceID:           "trace-1",
			SpanID:            "span-2",
			ParentSpanID:      "span-1",
			ServiceName:       "payment-service",
			OperationName:     "ChargeCard",
			StartTimeUnixNano: now.Add(-9 * time.Second).UnixNano(),
			EndTimeUnixNano:   now.Add(-8 * time.Second).UnixNano(),
			DurationMs:        1000,
			StatusCode:        "ERROR",
			StatusMessage:     "504 Gateway Timeout",
		},
	}

	if err := st.WriteSpans(ctx, spans); err != nil {
		t.Fatalf("WriteSpans failed: %v", err)
	}

	deploy := &types.DeployEvent{
		ID:                "dep-1",
		ServiceName:       "payment-service",
		Environment:       "production",
		CommitHash:        "deadbeef",
		CommitMessage:     "feat: upgrade pool timeout",
		TimestampUnixNano: now.Add(-15 * time.Second).UnixNano(),
	}

	if err := st.WriteDeploy(ctx, deploy); err != nil {
		t.Fatalf("WriteDeploy failed: %v", err)
	}

	// Query point-in-time state around now - 8s
	pitState, err := st.GetPointInTimeState(ctx, now.Add(-8*time.Second).UnixNano(), 30)
	if err != nil {
		t.Fatalf("GetPointInTimeState failed: %v", err)
	}

	if len(pitState.Nodes) == 0 {
		t.Errorf("expected nodes in topology, got 0")
	}

	foundPayment := false
	for _, n := range pitState.Nodes {
		if n.Name == "payment-service" {
			foundPayment = true
			if n.Status != "critical" && n.Status != "degraded" {
				t.Errorf("expected payment-service status critical/degraded due to error, got %s", n.Status)
			}
		}
	}
	if !foundPayment {
		t.Errorf("expected payment-service in topology nodes")
	}
}

func TestSQLiteMetadataStore_IncidentNotes(t *testing.T) {
	tmpDB := t.TempDir() + "/metadata_test.db"
	ms, err := NewSQLiteMetadataStore(tmpDB)
	if err != nil {
		t.Fatalf("NewSQLiteMetadataStore failed: %v", err)
	}
	defer ms.Close()

	ctx := context.Background()
	note := &IncidentNote{
		IncidentID: "inc-001",
		Title:      "Cascading Payment Gateway Outage",
		Author:     "oncall-sre",
		Content:    "Investigating bad pool exhaustion after deploy deadbeef",
		CreatedAt:  time.Now().UnixNano(),
	}

	if err := ms.SaveIncidentNote(ctx, note); err != nil {
		t.Fatalf("SaveIncidentNote failed: %v", err)
	}

	notes, err := ms.GetIncidentNotes(ctx, "inc-001")
	if err != nil {
		t.Fatalf("GetIncidentNotes failed: %v", err)
	}

	if len(notes) != 1 || notes[0].Title != note.Title {
		t.Errorf("unexpected incident notes returned: %+v", notes)
	}
}
