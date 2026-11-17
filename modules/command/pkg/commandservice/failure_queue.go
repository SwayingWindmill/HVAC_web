package commandservice

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
)

const (
	controlFailureQueueCapacity = 256
	controlFailureNotifyTimeout = 3 * time.Second
	controlFailureCloseTimeout  = 15 * time.Second
)

type controlFailureDelivery struct {
	notifier ControlFailureNotifier
	event    ControlFailureEvent
}

type controlFailureQueue struct {
	logger  *slog.Logger
	metrics *observability.Registry
	mu      sync.Mutex
	started bool
	closed  bool
	items   chan controlFailureDelivery
	done    chan struct{}
}

func (q *controlFailureQueue) recoveryDeps() (*slog.Logger, *observability.Registry) {
	if q == nil {
		return slog.Default(), nil
	}
	logger := q.logger
	if logger == nil {
		logger = slog.Default()
	}
	return logger, q.metrics
}

func (q *controlFailureQueue) enqueue(notifier ControlFailureNotifier, event ControlFailureEvent) {
	if q == nil || notifier == nil {
		return
	}
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return
	}
	if !q.started {
		q.items = make(chan controlFailureDelivery, controlFailureQueueCapacity)
		q.done = make(chan struct{})
		q.started = true
		go q.run()
	}
	select {
	case q.items <- controlFailureDelivery{notifier: notifier, event: event}:
	default:
	}
	q.mu.Unlock()
}

func (q *controlFailureQueue) run() {
	defer close(q.done)
	logger, metrics := q.recoveryDeps()
	defer observability.Recover(logger, metrics, "command.control_failure_notifier")
	for item := range q.items {
		q.deliver(item)
	}
}

func (q *controlFailureQueue) deliver(item controlFailureDelivery) {
	if item.notifier == nil {
		return
	}
	logger, metrics := q.recoveryDeps()
	defer observability.Recover(logger, metrics, "command.control_failure_notifier")
	ctx, cancel := context.WithTimeout(context.Background(), controlFailureNotifyTimeout)
	defer cancel()
	item.notifier(ctx, item.event)
}

func (q *controlFailureQueue) Close(ctx context.Context) error {
	if q == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	q.mu.Lock()
	if !q.started {
		q.closed = true
		q.mu.Unlock()
		return nil
	}
	if !q.closed {
		q.closed = true
		close(q.items)
	}
	done := q.done
	q.mu.Unlock()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
