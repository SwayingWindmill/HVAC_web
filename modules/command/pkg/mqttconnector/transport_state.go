package mqttconnector

import (
	"context"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
)

// TransportState is Connectivity's durable command state and Registry routing. Every
// call names the Tenant it works in.
type TransportState interface {
	ResolveCommandRoute(ctx context.Context, envelope commandmodel.DispatchEnvelope) (commandmodel.DeviceRoute, error)
	GatewayTenant(ctx context.Context, gatewayID string) (string, error)
	Tenants(ctx context.Context) ([]string, error)
	PrepareCommandCorrelation(ctx context.Context, correlation commandmodel.CommandCorrelation) (commandmodel.CommandCorrelation, error)
	ArmCommandCorrelation(ctx context.Context, tenantID, attemptID string, executionFence uint64, armedAt time.Time) error
	RecordCommandReply(ctx context.Context, tenantID, gatewayID, commandID string, executionFence uint64, replySHA256, replyStatus string, replyEventTime time.Time, replyReasonCode string, edgeExecution *commandmodel.EdgeExecutionEvidence, repliedAt time.Time) (commandmodel.CommandCorrelation, error)
	RecoverCommandReplies(ctx context.Context, tenantID string, limit int) ([]commandmodel.CommandCorrelation, error)
	MarkCommandCorrelationResolved(ctx context.Context, tenantID, attemptID string, executionFence uint64, resolvedAt time.Time) error
}

type LateResultSink interface {
	ResolveDispatch(ctx context.Context, envelope commandmodel.DispatchEnvelope, result commandmodel.ConnectorResult) error
}
