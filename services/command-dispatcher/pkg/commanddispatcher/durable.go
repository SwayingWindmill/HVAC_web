package commanddispatcher

import (
	"context"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
)

// DurableCommandStore is implemented by Command Service. Dispatcher never owns
// command_runtime credentials and only receives a fenced DispatchEnvelope.
type DurableCommandStore interface {
	ClaimDispatch(ctx context.Context, organizationID, leaseOwner string, leaseFor time.Duration) (commandmodel.DispatchEnvelope, error)
	ResolveDispatch(ctx context.Context, envelope commandmodel.DispatchEnvelope, result commandmodel.ConnectorResult) error
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

// RunOnce claims exactly one governed Command. A Connector error is deliberately
// not converted to a retry: Command Service will reconcile the expired prepared
// Attempt to OUTCOME_UNKNOWN unless the Connector returned explicit evidence.
func (d *DurableDispatcher) RunOnce(ctx context.Context, organizationID string) error {
	envelope, err := d.store.ClaimDispatch(ctx, organizationID, d.workerID, d.leaseFor)
	if err != nil {
		return err
	}
	result, err := d.connector.Execute(ctx, envelope)
	if err != nil {
		return err
	}
	return d.store.ResolveDispatch(ctx, envelope, result)
}
