package mqttconnector

import (
	"context"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
)

type TransportState interface {
	ResolveCommandRoute(ctx context.Context, integrationInstanceID, tenantID, siteID, gatewayID, deviceID string) (commandmodel.DeviceRoute, error)
	AssertConnectorOwnership(ctx context.Context, integrationInstanceID, ownerID string, generation uint64) error
	PrepareCommandCorrelation(ctx context.Context, correlation commandmodel.CommandCorrelation) (commandmodel.CommandCorrelation, error)
	ArmCommandCorrelation(ctx context.Context, attemptID string, executionFence, ownerGeneration uint64, armedAt time.Time) error
	RecordCommandReply(ctx context.Context, integrationInstanceID, commandID string, executionFence uint64, replySHA256, replyStatus string, replyEventTime time.Time, replyReasonCode string, edgeExecution *commandmodel.EdgeExecutionEvidence, repliedAt time.Time) (commandmodel.CommandCorrelation, error)
	RecoverCommandReplies(ctx context.Context, integrationInstanceID string, limit int) ([]commandmodel.CommandCorrelation, error)
	MarkCommandCorrelationResolved(ctx context.Context, attemptID string, executionFence uint64, resolvedAt time.Time) error
}

type LateResultSink interface {
	ResolveDispatch(ctx context.Context, envelope commandmodel.DispatchEnvelope, result commandmodel.ConnectorResult) error
}
