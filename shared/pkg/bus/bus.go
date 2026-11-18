package bus

import (
	"context"
	"errors"
	"sync"

	"github.com/chronotrace/chronotrace/shared/types"
)

var (
	ErrBusClosed = errors.New("event bus is closed")
)

// EventBus abstracts messaging between ingestion, batching, and query services.
type EventBus interface {
	PublishSpan(ctx context.Context, span *types.Span) error
	PublishDeploy(ctx context.Context, deploy *types.DeployEvent) error
	SubscribeSpans(ctx context.Context, handler func(span *types.Span) error) error
	SubscribeDeploys(ctx context.Context, handler func(deploy *types.DeployEvent) error) error
	Close() error
}

// InMemoryEventBus provides an in-memory channel-based event bus with bounded channels.
type InMemoryEventBus struct {
	mu            sync.RWMutex
	closed        bool
	spanSubs      []chan *types.Span
	deploySubs    []chan *types.DeployEvent
	subBufferSize int
	cancelFuncs   []context.CancelFunc
}

// NewInMemoryEventBus creates an in-memory event bus with bounded subscriber buffers.
func NewInMemoryEventBus(bufferSize int) *InMemoryEventBus {
	if bufferSize <= 0 {
		bufferSize = 1000
	}
	return &InMemoryEventBus{
		subBufferSize: bufferSize,
		spanSubs:      make([]chan *types.Span, 0),
		deploySubs:    make([]chan *types.DeployEvent, 0),
	}
}

// PublishSpan fans out a span to all active span subscribers without blocking indefinitely.
func (b *InMemoryEventBus) PublishSpan(ctx context.Context, span *types.Span) error {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if b.closed {
		return ErrBusClosed
	}

	for _, ch := range b.spanSubs {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ch <- span:
		default:
			// Non-blocking drop or overflow to preserve ingestion throughput (<128MB RSS constraint)
		}
	}
	return nil
}

// PublishDeploy fans out a deploy event to all active deploy subscribers.
func (b *InMemoryEventBus) PublishDeploy(ctx context.Context, deploy *types.DeployEvent) error {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if b.closed {
		return ErrBusClosed
	}

	for _, ch := range b.deploySubs {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ch <- deploy:
		default:
		}
	}
	return nil
}

// SubscribeSpans attaches a consumer handler to the span stream.
func (b *InMemoryEventBus) SubscribeSpans(ctx context.Context, handler func(span *types.Span) error) error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return ErrBusClosed
	}

	ch := make(chan *types.Span, b.subBufferSize)
	b.spanSubs = append(b.spanSubs, ch)
	subCtx, cancel := context.WithCancel(ctx)
	b.cancelFuncs = append(b.cancelFuncs, cancel)
	b.mu.Unlock()

	go func() {
		for {
			select {
			case <-subCtx.Done():
				return
			case span, ok := <-ch:
				if !ok {
					return
				}
				_ = handler(span)
			}
		}
	}()

	return nil
}

// SubscribeDeploys attaches a consumer handler to the deploy event stream.
func (b *InMemoryEventBus) SubscribeDeploys(ctx context.Context, handler func(deploy *types.DeployEvent) error) error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return ErrBusClosed
	}

	ch := make(chan *types.DeployEvent, b.subBufferSize)
	b.deploySubs = append(b.deploySubs, ch)
	subCtx, cancel := context.WithCancel(ctx)
	b.cancelFuncs = append(b.cancelFuncs, cancel)
	b.mu.Unlock()

	go func() {
		for {
			select {
			case <-subCtx.Done():
				return
			case deploy, ok := <-ch:
				if !ok {
					return
				}
				_ = handler(deploy)
			}
		}
	}()

	return nil
}

// Close gracefully closes all subscriber channels and stops background workers.
func (b *InMemoryEventBus) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return nil
	}
	b.closed = true

	for _, cancel := range b.cancelFuncs {
		cancel()
	}
	for _, ch := range b.spanSubs {
		close(ch)
	}
	for _, ch := range b.deploySubs {
		close(ch)
	}
	b.spanSubs = nil
	b.deploySubs = nil
	return nil
}
