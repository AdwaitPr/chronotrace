package bus

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/chronotrace/chronotrace/shared/types"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

const (
	StreamSpans    = "CHRONOTRACE_SPANS"
	StreamDeploys  = "CHRONOTRACE_DEPLOYS"
	SubjectSpans   = "chronotrace.spans"
	SubjectDeploys = "chronotrace.deploys"
)

// NATSJetStreamBus implements EventBus via NATS JetStream durable messaging.
type NATSJetStreamBus struct {
	nc       *nats.Conn
	js       jetstream.JetStream
	mu       sync.RWMutex
	closed   bool
	consumer jetstream.ConsumeContext
}

// NewNATSJetStreamBus connects to NATS and initializes JetStream streams.
func NewNATSJetStreamBus(natsURL string) (*NATSJetStreamBus, error) {
	if natsURL == "" {
		natsURL = nats.DefaultURL
	}

	opts := []nats.Option{
		nats.Name("chronotrace-bus"),
		nats.Timeout(5 * time.Second),
		nats.ReconnectWait(1 * time.Second),
		nats.MaxReconnects(60),
	}

	nc, err := nats.Connect(natsURL, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to NATS at %s: %w", natsURL, err)
	}

	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("failed to initialize JetStream: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Ensure Spans stream exists with bounded file/memory retention
	_, err = js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:      StreamSpans,
		Subjects:  []string{SubjectSpans},
		Storage:   jetstream.FileStorage,
		Retention: jetstream.LimitsPolicy,
		MaxBytes:  2 * 1024 * 1024 * 1024, // 2GB retention
		Discard:   jetstream.DiscardOld,
	})
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("failed to create/update JetStream stream %s: %w", StreamSpans, err)
	}

	// Ensure Deploys stream exists
	_, err = js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:      StreamDeploys,
		Subjects:  []string{SubjectDeploys},
		Storage:   jetstream.FileStorage,
		Retention: jetstream.LimitsPolicy,
		MaxBytes:  256 * 1024 * 1024, // 256MB
	})
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("failed to create/update JetStream stream %s: %w", StreamDeploys, err)
	}

	return &NATSJetStreamBus{
		nc: nc,
		js: js,
	}, nil
}

// PublishSpan publishes a span to NATS JetStream.
func (b *NATSJetStreamBus) PublishSpan(ctx context.Context, span *types.Span) error {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if b.closed {
		return ErrBusClosed
	}

	data, err := json.Marshal(span)
	if err != nil {
		return fmt.Errorf("failed to marshal span: %w", err)
	}

	_, err = b.js.Publish(ctx, SubjectSpans, data)
	return err
}

// PublishDeploy publishes a deploy event to NATS JetStream.
func (b *NATSJetStreamBus) PublishDeploy(ctx context.Context, deploy *types.DeployEvent) error {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if b.closed {
		return ErrBusClosed
	}

	data, err := json.Marshal(deploy)
	if err != nil {
		return fmt.Errorf("failed to marshal deploy event: %w", err)
	}

	_, err = b.js.Publish(ctx, SubjectDeploys, data)
	return err
}

// SubscribeSpans creates a durable pull consumer and handles spans asynchronously.
func (b *NATSJetStreamBus) SubscribeSpans(ctx context.Context, handler func(span *types.Span) error) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return ErrBusClosed
	}

	consumer, err := b.js.CreateOrUpdateConsumer(ctx, StreamSpans, jetstream.ConsumerConfig{
		Durable:       "chronotrace-spans-consumer",
		AckPolicy:     jetstream.AckExplicitPolicy,
		MaxAckPending: 10000,
	})
	if err != nil {
		return fmt.Errorf("failed to create JetStream spans consumer: %w", err)
	}

	consumeCtx, err := consumer.Consume(func(msg jetstream.Msg) {
		var s types.Span
		if err := json.Unmarshal(msg.Data(), &s); err == nil {
			_ = handler(&s)
		}
		_ = msg.Ack()
	})
	if err != nil {
		return fmt.Errorf("failed to start consuming spans: %w", err)
	}

	b.consumer = consumeCtx
	return nil
}

// SubscribeDeploys creates a durable consumer for deploy events.
func (b *NATSJetStreamBus) SubscribeDeploys(ctx context.Context, handler func(deploy *types.DeployEvent) error) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return ErrBusClosed
	}

	consumer, err := b.js.CreateOrUpdateConsumer(ctx, StreamDeploys, jetstream.ConsumerConfig{
		Durable:       "chronotrace-deploys-consumer",
		AckPolicy:     jetstream.AckExplicitPolicy,
		MaxAckPending: 1000,
	})
	if err != nil {
		return fmt.Errorf("failed to create JetStream deploys consumer: %w", err)
	}

	_, err = consumer.Consume(func(msg jetstream.Msg) {
		var d types.DeployEvent
		if err := json.Unmarshal(msg.Data(), &d); err == nil {
			_ = handler(&d)
		}
		_ = msg.Ack()
	})
	return err
}

// Close gracefully closes the NATS JetStream connection.
func (b *NATSJetStreamBus) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return nil
	}
	b.closed = true

	if b.consumer != nil {
		b.consumer.Stop()
	}
	if b.nc != nil {
		b.nc.Drain()
		b.nc.Close()
	}
	return nil
}
