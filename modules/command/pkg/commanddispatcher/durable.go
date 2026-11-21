package commanddispatcher

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/modules/command/pkg/commandservice"
)

var commandDispatcherTracer = observability.NewTracer("command-dispatcher", nil)

func withCommandTraceContext(ctx context.Context, rawTraceID string) (context.Context, string) {
	normalized := commandmodel.NormalizeTraceID(rawTraceID)
	if observability.TraceID(ctx) == "" && normalized != "" {
		ctx = observability.ContextWithRemoteParent(ctx, commandDispatcherTracer, "00-"+normalized+"-0000000000000001-01", "")
	}
	if active := commandmodel.NormalizeTraceID(observability.TraceID(ctx)); active != "" {
		return ctx, active
	}
	return ctx, normalized
}

type Connector interface {
	Execute(ctx context.Context, envelope commandmodel.DispatchEnvelope) (commandmodel.ConnectorResult, error)
}

// DurableCommandStore is implemented by Command Service. Dispatcher never owns
// command_runtime credentials and only receives a fenced DispatchEnvelope.
type DurableCommandStore interface {
	ClaimDispatch(ctx context.Context, tenantID, leaseOwner string, leaseFor time.Duration) (commandmodel.DispatchEnvelope, error)
	ResolveDispatch(ctx context.Context, envelope commandmodel.DispatchEnvelope, result commandmodel.ConnectorResult) error
}

// AnyTenantCommandStore is an optional extension of DurableCommandStore supporting
// cross-tenant fair claim with bounded wait.
type AnyTenantCommandStore interface {
	DurableCommandStore
	ClaimAnyDispatch(ctx context.Context, leaseOwner string, leaseFor, waitDuration time.Duration) (commandmodel.DispatchEnvelope, error)
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

func replyBackedResult(result commandmodel.ConnectorResult) bool {
	return result.Acknowledged ||
		result.Phase == commandmodel.ConnectorAcknowledged ||
		result.Phase == commandmodel.ConnectorExecutionRejected ||
		strings.HasPrefix(result.FailureCode, "MQTT_EDGE_") ||
		result.FailureCode == "MQTT_COMMAND_REPLY_INVALID"
}

// RunOnce claims and executes one pre-dispatch gated Command. Command Service
// enforces the 3-phase Twin + Live gate inside ClaimDispatch before issuing
// a DispatchEnvelope, so only pre-dispatch approved intents reach Connector.Execute.
func (d *DurableDispatcher) RunOnce(ctx context.Context, tenantID string) error {
	envelope, err := d.store.ClaimDispatch(ctx, tenantID, d.workerID, d.leaseFor)
	if err != nil {
		return err
	}
	return d.executeAndResolve(ctx, envelope)
}

// RunAnyOnce claims the next due pre-dispatch gated Command across all tenants and executes it.
func (d *DurableDispatcher) RunAnyOnce(ctx context.Context, waitDuration time.Duration) error {
	anyStore, ok := d.store.(AnyTenantCommandStore)
	if !ok {
		return errors.New("command store does not support cross-tenant claim")
	}
	envelope, err := anyStore.ClaimAnyDispatch(ctx, d.workerID, d.leaseFor, waitDuration)
	if err != nil {
		return err
	}
	return d.executeAndResolve(ctx, envelope)
}

func (d *DurableDispatcher) executeAndResolve(ctx context.Context, envelope commandmodel.DispatchEnvelope) error {
	execCtx, traceID := withCommandTraceContext(ctx, envelope.TraceID)
	result, err := d.connector.Execute(execCtx, envelope)
	if err != nil {
		slog.WarnContext(execCtx, "command_dispatch_execute_failed", "command_id", envelope.CommandID, "attempt_id", envelope.AttemptID, "execution_fence", envelope.ExecutionFence, "trace_id", traceID, "error", err.Error())
		return err
	}
	if err := d.store.ResolveDispatch(execCtx, envelope, result); err != nil {
		slog.WarnContext(execCtx, "command_dispatch_resolve_failed", "command_id", envelope.CommandID, "attempt_id", envelope.AttemptID, "execution_fence", envelope.ExecutionFence, "trace_id", traceID, "phase", result.Phase, "failure_code", result.FailureCode, "error", err.Error())
		if finalizer, ok := d.connector.(DispatchResolutionFinalizer); ok && errors.Is(err, commandservice.ErrStaleFence) && replyBackedResult(result) {
			return finalizer.FinalizeDispatch(execCtx, envelope, result)
		}
		return err
	}
	slog.InfoContext(execCtx, "command_dispatch_resolved", "command_id", envelope.CommandID, "attempt_id", envelope.AttemptID, "execution_fence", envelope.ExecutionFence, "trace_id", traceID, "phase", result.Phase, "failure_code", result.FailureCode)
	if finalizer, ok := d.connector.(DispatchResolutionFinalizer); ok {
		return finalizer.FinalizeDispatch(execCtx, envelope, result)
	}
	return nil
}
