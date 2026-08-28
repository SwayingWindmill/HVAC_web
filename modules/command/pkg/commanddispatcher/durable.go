package commanddispatcher

import (
	"context"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
)

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
	safety    DispatchSafetyVerifier
	connector Connector
	workerID  string
	leaseFor  time.Duration
}

func NewDurable(store DurableCommandStore, safety DispatchSafetyVerifier, connector Connector, workerID string, leaseFor time.Duration) *DurableDispatcher {
	if leaseFor <= 0 {
		leaseFor = 30 * time.Second
	}
	return &DurableDispatcher{store: store, safety: safety, connector: connector, workerID: workerID, leaseFor: leaseFor}
}

// RunOnce claims exactly one governed Command. After Approval/queueing and before
// MQTT publication it re-reads authoritative current state. Unsafe dynamic state
// is resolved as provably-not-sent and never reaches the Connector.
func (d *DurableDispatcher) RunOnce(ctx context.Context, tenantID string) error {
	envelope, err := d.store.ClaimDispatch(ctx, tenantID, d.workerID, d.leaseFor)
	if err != nil {
		return err
	}
	if d.safety == nil {
		return d.store.ResolveDispatch(ctx, envelope, commandmodel.ConnectorResult{
			Phase: commandmodel.ConnectorPreSendRejected, FailureCode: "DISPATCH_SAFETY_UNAVAILABLE",
		})
	}
	safety, err := d.safety.VerifyBeforeDispatch(ctx, envelope)
	if err != nil {
		// Current-state uncertainty is fail-closed. Because no Connector call has
		// occurred yet, Command Service can record a deterministic no-send result.
		return d.store.ResolveDispatch(ctx, envelope, commandmodel.ConnectorResult{
			Phase: commandmodel.ConnectorPreSendRejected, FailureCode: "DISPATCH_SAFETY_EVALUATION_FAILED",
		})
	}
	if !safety.Safe {
		failureCode := strings.TrimSpace(safety.FailureCode)
		if failureCode == "" {
			failureCode = "DISPATCH_SAFETY_REJECTED"
		}
		return d.store.ResolveDispatch(ctx, envelope, commandmodel.ConnectorResult{
			Phase: commandmodel.ConnectorPreSendRejected, FailureCode: failureCode, EvidenceID: safety.EvidenceID,
		})
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
