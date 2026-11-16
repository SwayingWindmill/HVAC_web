package commanddispatcher

import (
	"context"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
)

type Connector interface {
	Execute(ctx context.Context, envelope commandmodel.DispatchEnvelope) (commandmodel.ConnectorResult, error)
}

// DurableCommandStore is implemented by Command Service. Dispatcher never owns
// command_runtime credentials and only receives a fenced DispatchEnvelope.
type DurableCommandStore interface {
	ClaimDispatch(ctx context.Context, tenantID, leaseOwner string, leaseFor time.Duration) (commandmodel.DispatchEnvelope, error)
	ResolveDispatch(ctx context.Context, envelope commandmodel.DispatchEnvelope, result commandmodel.ConnectorResult) error
}

type DispatchResolutionFinalizer interface {
	FinalizeDispatch(ctx context.Context, envelope commandmodel.DispatchEnvelope, result commandmodel.ConnectorResult) error
}

type DurableDispatcher struct {
	store     DurableCommandStore
	connector Connector
	workerID  string
	leaseFor  time.Duration
}

func NewDurable(store DurableCommandStore, connector Connector, workerID string, leaseFor time.Duration) *DurableDispatcher {
	if leaseFor <= 0 {
		leaseFor = 30 * time.Second
	}
	return &DurableDispatcher{store: store, connector: connector, workerID: workerID, leaseFor: leaseFor}
}

// RunOnce claims and executes one pre-dispatch gated Command. Command Service
// enforces the 3-phase Twin + Live gate inside ClaimDispatch before issuing
// a DispatchEnvelope, so only pre-dispatch approved intents reach Connector.Execute.
func (d *DurableDispatcher) RunOnce(ctx context.Context, tenantID string) error {
	envelope, err := d.store.ClaimDispatch(ctx, tenantID, d.workerID, d.leaseFor)
	if err != nil {
		return err
	}
	result, err := d.connector.Execute(ctx, envelope)
	if err != nil {
		return err
	}
	if err := d.store.ResolveDispatch(ctx, envelope, result); err != nil {
		return err
	}
	if finalizer, ok := d.connector.(DispatchResolutionFinalizer); ok {
		return finalizer.FinalizeDispatch(ctx, envelope, result)
	}
	return nil
}
