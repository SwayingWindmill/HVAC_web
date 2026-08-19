package mqttconnector

import (
	"context"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
)

type DeviceRoute struct {
	ExternalDeviceID string
	BindingRevision  uint64
}

type OwnershipLease struct {
	Generation uint64
	Until      time.Time
}

type CorrelationState string

const (
	CorrelationPrepared  CorrelationState = "PREPARED"
	CorrelationMayCommit CorrelationState = "MAY_COMMIT"
	CorrelationReplied   CorrelationState = "REPLIED"
	CorrelationResolved  CorrelationState = "RESOLVED"
)

type CommandCorrelation struct {
	Envelope              commandmodel.DispatchEnvelope
	IntegrationInstanceID string
	ExternalDeviceID      string
	OwnerGeneration       uint64
	MappingRevision       string
	BindingRevision       string
	ProviderEndpoint      string
	ProviderMethod        string
	RequestSHA256         string
	PreparedAt            time.Time
	State                 CorrelationState
	ReplySHA256           string
	ReplyStatus           string
	ReplyEventTime        time.Time
	ReplyReasonCode       string
	RepliedAt             time.Time
}

type TransportState interface {
	ResolveCommandRoute(ctx context.Context, integrationInstanceID, tenantID, siteID, gatewayID, deviceID string) (DeviceRoute, error)
	AssertConnectorOwnership(ctx context.Context, integrationInstanceID, ownerID string, generation uint64) error
	PrepareCommandCorrelation(ctx context.Context, correlation CommandCorrelation) (CommandCorrelation, error)
	ArmCommandCorrelation(ctx context.Context, attemptID string, executionFence, ownerGeneration uint64, armedAt time.Time) error
	RecordCommandReply(ctx context.Context, integrationInstanceID, commandID string, executionFence uint64, replySHA256, replyStatus string, replyEventTime time.Time, replyReasonCode string, repliedAt time.Time) (CommandCorrelation, error)
	RecoverCommandReplies(ctx context.Context, integrationInstanceID string, limit int) ([]CommandCorrelation, error)
	MarkCommandCorrelationResolved(ctx context.Context, attemptID string, executionFence uint64, resolvedAt time.Time) error
}

type LateResultSink interface {
	ResolveDispatch(ctx context.Context, envelope commandmodel.DispatchEnvelope, result commandmodel.ConnectorResult) error
}
