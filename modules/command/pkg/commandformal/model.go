// Package commandformal provides an explicit-state exhaustive model checker over
// real commandservice and mqttconnector protocol transitions (ADR 0006, ADR 0018, PRODUCT.md §7).
package commandformal

import (
	"fmt"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/modules/command/pkg/mqttconnector"
)

const (
	baseTenantID             = "01900000-0000-7000-8000-000000000001"
	baseSiteID               = "01900000-0000-7000-8000-000000000002"
	baseDeviceID             = "01900000-0000-7000-8000-000000000003"
	baseGatewayID            = "01900000-0000-7000-8000-000000000004"
	basePointID              = "01900000-0000-7000-8000-000000000005"
	submitPrincipalID        = "01900000-0000-7000-8000-000000000010"
	approverPrincipalID      = "01900000-0000-7000-8000-000000000011"
	reconcilePrincipalID     = "01900000-0000-7000-8000-000000000012"
	setpointVerificationKey  = "zone.temperature_setpoint"
	setpointCapabilityRev    = "capability:set-temperature-setpoint:v1"
	dispatchLeaseWindow      = time.Minute
	grantLifetime            = 25 * time.Second
	maxAttemptsPerCommand    = 2
	defaultVerificationGrace = 2 * time.Minute
)

var baseModelTime = time.Date(2026, 7, 26, 9, 0, 0, 0, time.UTC)

// PropertyID identifies each formal safety or liveness invariant in the command-safety specification.
type PropertyID string

const (
	PropertyP1 PropertyID = "P1"
	PropertyP2 PropertyID = "P2"
	PropertyP3 PropertyID = "P3"
	PropertyP4 PropertyID = "P4"
	PropertyP5 PropertyID = "P5"
	PropertyP6 PropertyID = "P6"
)

// FaultHooks allows negative tests to inject single-guard regressions and prove every invariant
// P1-P6 is capable of failing with an explicit counterexample trace.
type FaultHooks struct {
	BypassExpiredGrantOnClaim     bool
	AllowPostArmRepublish         bool
	UnfreezeWithoutReconcile      bool
	AllowAckWithoutEdgeExecution  bool
	IgnoreVerificationDeadline    bool
	DropStaleReplyRecoveryAsError bool
}

// Counterexample captures the failing invariant, suite name, and shortest BFS trace from initial state.
type Counterexample struct {
	Property PropertyID `json:"property"`
	Suite    string     `json:"suite"`
	Summary  string     `json:"summary"`
	Trace    []string   `json:"trace"`
}

func (c Counterexample) Error() string {
	return fmt.Sprintf("[%s/%s] %s; trace: %s", c.Suite, c.Property, c.Summary, strings.Join(c.Trace, " -> "))
}

// SuiteReport summarizes an exhaustive BFS exploration for one verification suite.
type SuiteReport struct {
	Suite          string          `json:"suite"`
	VisitedStates  int             `json:"visitedStates"`
	Transitions    int             `json:"transitions"`
	Passed         bool            `json:"passed"`
	Counterexample *Counterexample `json:"counterexample,omitempty"`
}

// FullReport summarizes all command-safety formal verification suites.
type FullReport struct {
	Passed         bool            `json:"passed"`
	TotalStates    int             `json:"totalStates"`
	TotalEdges     int             `json:"totalEdges"`
	Suites         []SuiteReport   `json:"suites"`
	Properties     []PropertyID    `json:"properties"`
	Counterexample *Counterexample `json:"counterexample,omitempty"`
}

func validCurrentState(currentValue float64, at time.Time) commandmodel.CurrentStateEvidence {
	value := currentValue
	return commandmodel.CurrentStateEvidence{
		EvaluationAvailability: "AVAILABLE",
		Presence:               "ONLINE",
		Readiness:              "CURRENT",
		Quality:                "GOOD",
		BusinessRevision:       17,
		CurrentValue:           &value,
		ObservedAt:             at,
	}
}

func submitAuthorization(principalID string, issuedAt time.Time, maxRisk commandmodel.RiskLevel) commandmodel.AuthorizationSnapshot {
	return commandmodel.AuthorizationSnapshot{
		GrantID:                     "grant-submit-" + issuedAt.Format("150405"),
		PolicyRevision:              "command-policy-1",
		Purpose:                     commandmodel.AuthorizationCommandSubmit,
		PrincipalID:                 principalID,
		TenantID:                    baseTenantID,
		SiteID:                      baseSiteID,
		DeviceID:                    baseDeviceID,
		Capability:                  commandmodel.CapabilitySetTemperatureSetpoint,
		MaximumRisk:                 maxRisk,
		CapabilityRevision:          setpointCapabilityRev,
		EmergencyRevocationRevision: 1,
		IssuedAt:                    issuedAt.Add(-5 * time.Second),
		ExpiresAt:                   issuedAt.Add(grantLifetime),
	}
}

func approveAuthorization(principalID string, approvalID string, issuedAt time.Time, maxRisk commandmodel.RiskLevel) commandmodel.AuthorizationSnapshot {
	return commandmodel.AuthorizationSnapshot{
		GrantID:                     "grant-approve-" + approvalID,
		PolicyRevision:              "command-policy-1",
		Purpose:                     commandmodel.AuthorizationCommandApprove,
		PrincipalID:                 principalID,
		TenantID:                    baseTenantID,
		SiteID:                      baseSiteID,
		DeviceID:                    baseDeviceID,
		Capability:                  commandmodel.CapabilitySetTemperatureSetpoint,
		MaximumRisk:                 maxRisk,
		CapabilityRevision:          setpointCapabilityRev,
		EmergencyRevocationRevision: 1,
		IssuedAt:                    issuedAt.Add(-5 * time.Second),
		ExpiresAt:                   issuedAt.Add(grantLifetime),
	}
}

func makeSubmitRequest(idempotencyKey string, setpoint float64, issuedAt time.Time) commandmodel.SubmitRequest {
	return commandmodel.SubmitRequest{
		TenantID:             baseTenantID,
		SiteID:               baseSiteID,
		DeviceID:             baseDeviceID,
		PointID:              basePointID,
		PrincipalID:          submitPrincipalID,
		IdempotencyKey:       idempotencyKey,
		Capability:           commandmodel.CapabilitySetTemperatureSetpoint,
		Parameters:           commandmodel.CommandParameters{commandmodel.ParameterSetpointC: setpoint},
		VerificationPointKey: setpointVerificationKey,
		CurrentState:         validCurrentState(23, issuedAt),
		Authorization:        submitAuthorization(submitPrincipalID, issuedAt, commandmodel.RiskHigh),
	}
}

func validEdgeExecution(effective float64) *commandmodel.EdgeExecutionEvidence {
	requested := commandmodel.NumberScalar(24)
	applied := commandmodel.NumberScalar(effective)
	return &commandmodel.EdgeExecutionEvidence{
		Requested:          requested,
		Effective:          &applied,
		Applied:            &applied,
		WinnerControllerID: "cloud-command-intent",
		Cycle:              1,
	}
}

func validReportedEvidence(value float64, observedAt time.Time, revision uint64) commandmodel.ReportedStateEvidence {
	return commandmodel.ReportedStateEvidence{
		TenantID:               baseTenantID,
		SiteID:                 baseSiteID,
		DeviceID:               baseDeviceID,
		EvaluationAvailability: "AVAILABLE",
		Presence:               "ONLINE",
		Readiness:              "CURRENT",
		Freshness:              "FRESH",
		Quality:                "GOOD",
		BusinessRevision:       revision,
		ReportedValue:          commandmodel.NumberScalar(value),
		ObservedAt:             observedAt,
	}
}

func buildCommandCorrelation(envelope commandmodel.DispatchEnvelope, state commandmodel.CorrelationState, replyStatus string, repliedAt time.Time, edgeExec *commandmodel.EdgeExecutionEvidence) commandmodel.CommandCorrelation {
	corr := commandmodel.CommandCorrelation{
		Envelope:         envelope,
		ExternalDeviceID: "ext-chiller-1",
		GatewayID:        baseGatewayID,
		MappingRevision:  "1",
		BindingRevision:  "1",
		ProviderEndpoint: "mqtt://edge/" + baseGatewayID,
		ProviderMethod:   "device.set_temperature_setpoint",
		RequestSHA256:    "req-sha-" + envelope.AttemptID,
		State:            state,
		PreparedAt:       repliedAt.Add(-2 * time.Second),
	}
	if state == commandmodel.CorrelationReplied || state == commandmodel.CorrelationResolved {
		corr.ReplySHA256 = "reply-sha-" + envelope.AttemptID
		corr.ReplyStatus = replyStatus
		corr.ReplyEventTime = repliedAt
		corr.RepliedAt = repliedAt
		corr.EdgeExecution = edgeExec
	}
	return corr
}

func isIntentTerminal(status commandmodel.IntentStatus) bool {
	return status == commandmodel.IntentSucceeded ||
		status == commandmodel.IntentFailed ||
		status == commandmodel.IntentRejected
}

func checkResumeActionMatchesCorrelation(state commandmodel.CorrelationState, action mqttconnector.ResumeAction) bool {
	switch state {
	case commandmodel.CorrelationPrepared:
		return action == mqttconnector.ResumePublish
	case commandmodel.CorrelationMayCommit:
		return action == mqttconnector.ResumeOutcomeUnknown
	case commandmodel.CorrelationReplied, commandmodel.CorrelationResolved:
		return action == mqttconnector.ResumeUseReply
	default:
		return action == mqttconnector.ResumeInvalid
	}
}
