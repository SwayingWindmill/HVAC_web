package outbounddelivery

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type Store interface {
	RecoverExpired(ctx context.Context, scope Scope, now time.Time) (int, error)
	ClaimNext(ctx context.Context, scope Scope, workerID string, now time.Time, leaseDuration time.Duration) (*ClaimedDelivery, error)
	CompleteAttempt(ctx context.Context, scope Scope, attemptID, workerID string, result AdapterResult, now time.Time) error
}

type Adapter interface {
	Deliver(ctx context.Context, delivery ClaimedDelivery) AdapterResult
}

type Worker struct {
	store         Store
	adapters      map[AdapterType]Adapter
	workerID      string
	leaseDuration time.Duration
	now           func() time.Time
}

func NewWorker(store Store, adapters map[AdapterType]Adapter, workerID string, leaseDuration time.Duration, now func() time.Time) (*Worker, error) {
	if store == nil {
		return nil, errors.New("delivery store is required")
	}
	if len(adapters) == 0 {
		return nil, errors.New("at least one delivery adapter is required")
	}
	if workerID == "" {
		return nil, errors.New("delivery worker id is required")
	}
	if leaseDuration <= 0 {
		return nil, errors.New("delivery lease duration must be positive")
	}
	if now == nil {
		now = time.Now
	}
	return &Worker{store: store, adapters: adapters, workerID: workerID, leaseDuration: leaseDuration, now: now}, nil
}

func (worker *Worker) ProcessOne(ctx context.Context, scope Scope) (bool, error) {
	now := worker.now().UTC()
	if _, err := worker.store.RecoverExpired(ctx, scope, now); err != nil {
		return false, fmt.Errorf("recover expired delivery leases: %w", err)
	}
	claim, err := worker.store.ClaimNext(ctx, scope, worker.workerID, now, worker.leaseDuration)
	if errors.Is(err, ErrNothingReady) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("claim delivery intent: %w", err)
	}
	adapter, ok := worker.adapters[claim.Integration.AdapterType]
	if !ok {
		result := AdapterResult{Outcome: OutcomeNotSent, ErrorCode: "ADAPTER_NOT_CONFIGURED"}
		if err := worker.store.CompleteAttempt(ctx, scope, claim.Attempt.ID, worker.workerID, result, worker.now().UTC()); err != nil {
			return true, fmt.Errorf("complete missing-adapter attempt: %w", err)
		}
		return true, nil
	}
	result := adapter.Deliver(ctx, *claim)
	if err := worker.store.CompleteAttempt(ctx, scope, claim.Attempt.ID, worker.workerID, result, worker.now().UTC()); err != nil {
		return true, fmt.Errorf("complete delivery attempt: %w", err)
	}
	return true, nil
}
