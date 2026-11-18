package bus

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/chronotrace/chronotrace/shared/types"
)

func TestInMemoryEventBus_PublishAndSubscribeSpans(t *testing.T) {
	b := NewInMemoryEventBus(100)
	defer b.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var receivedSpans []*types.Span
	var mu sync.Mutex
	done := make(chan struct{})

	err := b.SubscribeSpans(ctx, func(s *types.Span) error {
		mu.Lock()
		receivedSpans = append(receivedSpans, s)
		if len(receivedSpans) == 5 {
			close(done)
		}
		mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatalf("SubscribeSpans failed: %v", err)
	}

	for i := 0; i < 5; i++ {
		span := &types.Span{
			TraceID:       "trace-1",
			SpanID:        "span-1",
			ServiceName:   "checkout-svc",
			OperationName: "ProcessPayment",
			StatusCode:    "OK",
		}
		if err := b.PublishSpan(ctx, span); err != nil {
			t.Fatalf("PublishSpan failed: %v", err)
		}
	}

	select {
	case <-done:
		mu.Lock()
		count := len(receivedSpans)
		mu.Unlock()
		if count != 5 {
			t.Errorf("expected 5 spans, got %d", count)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for spans to be received")
	}
}

func TestInMemoryEventBus_PublishAndSubscribeDeploys(t *testing.T) {
	b := NewInMemoryEventBus(100)
	defer b.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	received := make(chan *types.DeployEvent, 1)
	err := b.SubscribeDeploys(ctx, func(d *types.DeployEvent) error {
		received <- d
		return nil
	})
	if err != nil {
		t.Fatalf("SubscribeDeploys failed: %v", err)
	}

	deploy := &types.DeployEvent{
		ID:          "deploy-101",
		ServiceName: "payment-gateway",
		Environment: "production",
		CommitHash:  "abc1234",
	}

	if err := b.PublishDeploy(ctx, deploy); err != nil {
		t.Fatalf("PublishDeploy failed: %v", err)
	}

	select {
	case d := <-received:
		if d.ID != "deploy-101" || d.ServiceName != "payment-gateway" {
			t.Errorf("unexpected deploy received: %+v", d)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for deploy event")
	}
}
