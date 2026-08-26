package mqttconnector

import (
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
)

func TestS11EdgeReplyClassificationPreservesBusinessBoundary(t *testing.T) {
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	requested := commandmodel.NumberScalar(50)
	effective := commandmodel.NumberScalar(45)
	edge := &commandmodel.EdgeExecutionEvidence{
		Requested: requested, Effective: &effective, Applied: &effective,
		WinnerControllerID: "capability-limits", Cycle: 7,
		Constraints: []commandmodel.EdgeConstraintEvidence{{ControllerID: "capability-limits", Reason: "DEVICE_CAPABILITY_LIMIT"}},
	}
	base := commandmodel.CommandCorrelation{
		Envelope:         commandmodel.DispatchEnvelope{AttemptID: "attempt", CommandID: "command", TenantID: "tenant", SiteID: "site", DeviceID: "device", ExecutionFence: 2},
		ExternalDeviceID: "external", MappingRevision: "mapping", BindingRevision: "binding", ProviderEndpoint: "topic", ProviderMethod: "setFrequency",
		RequestSHA256: "request", PreparedAt: now.Add(-time.Second), ReplySHA256: "reply", ReplyEventTime: now, RepliedAt: now,
	}

	executed := base
	executed.ReplyStatus = "EXECUTED"
	executed.EdgeExecution = edge
	if got := completedEvidenceFromCorrelation(executed); got.ConnectorPhase != commandmodel.ConnectorAcknowledged || got.EdgeExecution == nil {
		t.Fatalf("EXECUTED classification=%+v", got)
	}

	rejected := base
	rejected.ReplyStatus = "REJECTED"
	rejected.ReplyReasonCode = "INTERLOCK_OPEN"
	rejected.EdgeExecution = &commandmodel.EdgeExecutionEvidence{Requested: requested, WinnerControllerID: "safety-interlock", Cycle: 8, Constraints: []commandmodel.EdgeConstraintEvidence{{ControllerID: "safety-interlock", Reason: "INTERLOCK_OPEN"}}}
	if got := completedEvidenceFromCorrelation(rejected); got.ConnectorPhase != commandmodel.ConnectorExecutionRejected || got.FailureCode != "MQTT_EDGE_INTERLOCK_OPEN" {
		t.Fatalf("REJECTED classification=%+v", got)
	}

	failed := base
	failed.ReplyStatus = "FAILED"
	failed.ReplyReasonCode = "DEVICE_WRITE_FAILED"
	if got := completedEvidenceFromCorrelation(failed); got.ConnectorPhase != commandmodel.ConnectorRequestCommitted {
		t.Fatalf("FAILED classification=%+v", got)
	}
}
