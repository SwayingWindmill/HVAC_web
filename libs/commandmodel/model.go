package commandmodel

import (
	"strings"
	"time"
)

type Capability string

const (
	CapabilityStart                              Capability = "START"
	CapabilityStop                               Capability = "STOP"
	CapabilityResetFault                         Capability = "RESET_FAULT"
	CapabilitySetTemperatureSetpoint             Capability = "SET_TEMPERATURE_SETPOINT"
	CapabilitySetChilledWaterTemperatureSetpoint Capability = "SET_CHILLED_WATER_TEMPERATURE_SETPOINT"
	CapabilitySetFrequency                       Capability = "SET_FREQUENCY"
	CapabilitySetFanSpeed                        Capability = "SET_FAN_SPEED"
	CapabilitySetLoadLimit                       Capability = "SET_LOAD_LIMIT"
	CapabilitySetOpening                         Capability = "SET_OPENING"
)

const (
	ParameterSetpointC    = "setpointC"
	ParameterFrequencyHz  = "frequencyHz"
	ParameterFanSpeedPct  = "fanSpeedPct"
	ParameterLoadLimitPct = "loadLimitPct"
	ParameterOpeningPct   = "openingPct"
)

type CommandParameters map[string]float64

type ScalarValue struct {
	Number  *float64 `json:"number,omitempty"`
	Text    *string  `json:"text,omitempty"`
	Boolean *bool    `json:"boolean,omitempty"`
}

func NumberScalar(value float64) ScalarValue {
	return ScalarValue{Number: &value}
}

func TextScalar(value string) ScalarValue {
	return ScalarValue{Text: &value}
}

func BooleanScalar(value bool) ScalarValue {
	return ScalarValue{Boolean: &value}
}

type RiskLevel string

const (
	RiskLow      RiskLevel = "LOW"
	RiskMedium   RiskLevel = "MEDIUM"
	RiskHigh     RiskLevel = "HIGH"
	RiskCritical RiskLevel = "CRITICAL"
)

type CapabilityProfile struct {
	Revision              string
	ParameterKey          string
	Minimum               float64
	Maximum               float64
	Step                  float64
	MaximumDelta          float64
	LowRiskDelta          float64
	MediumRiskDelta       float64
	BaseRisk              RiskLevel
	VerificationTolerance float64
}

func CapabilityProfileFor(capability Capability) (CapabilityProfile, bool) {
	switch capability {
	case CapabilityStart:
		return CapabilityProfile{Revision: "capability:start:v1", BaseRisk: RiskMedium}, true
	case CapabilityStop:
		return CapabilityProfile{Revision: "capability:stop:v1", BaseRisk: RiskMedium}, true
	case CapabilityResetFault:
		return CapabilityProfile{Revision: "capability:reset-fault:v1", BaseRisk: RiskLow}, true
	case CapabilitySetTemperatureSetpoint:
		return numericCapability("capability:set-temperature-setpoint:v1", ParameterSetpointC, 16, 30, 0.5, 3, 1, 2, 0.1), true
	case CapabilitySetChilledWaterTemperatureSetpoint:
		return numericCapability("capability:set-chilled-water-temperature-setpoint:v1", ParameterSetpointC, 5, 12, 0.5, 3, 1, 2, 0.1), true
	case CapabilitySetFrequency:
		return numericCapability("capability:set-frequency:v1", ParameterFrequencyHz, 20, 50, 0.5, 10, 2, 5, 0.1), true
	case CapabilitySetFanSpeed:
		return numericCapability("capability:set-fan-speed:v1", ParameterFanSpeedPct, 20, 100, 1, 30, 10, 20, 0.5), true
	case CapabilitySetLoadLimit:
		return numericCapability("capability:set-load-limit:v1", ParameterLoadLimitPct, 20, 100, 1, 30, 10, 20, 0.5), true
	case CapabilitySetOpening:
		return numericCapability("capability:set-opening:v1", ParameterOpeningPct, 0, 100, 1, 30, 10, 20, 0.5), true
	default:
		return CapabilityProfile{}, false
	}
}

func numericCapability(revision, parameterKey string, minimum, maximum, step, maximumDelta, lowRiskDelta, mediumRiskDelta, tolerance float64) CapabilityProfile {
	return CapabilityProfile{
		Revision: revision, ParameterKey: parameterKey, Minimum: minimum, Maximum: maximum, Step: step,
		MaximumDelta: maximumDelta, LowRiskDelta: lowRiskDelta, MediumRiskDelta: mediumRiskDelta,
		BaseRisk: RiskLow, VerificationTolerance: tolerance,
	}
}

func ParameterValue(capability Capability, parameters CommandParameters) (float64, bool) {
	profile, ok := CapabilityProfileFor(capability)
	if !ok || profile.ParameterKey == "" || len(parameters) != 1 {
		return 0, false
	}
	value, ok := parameters[profile.ParameterKey]
	return value, ok
}

func ParametersValid(capability Capability, parameters CommandParameters) bool {
	profile, ok := CapabilityProfileFor(capability)
	if !ok {
		return false
	}
	if profile.ParameterKey == "" {
		return len(parameters) == 0
	}
	value, ok := ParameterValue(capability, parameters)
	return ok && value >= profile.Minimum && value <= profile.Maximum
}

func ExpectedReportedValue(capability Capability, parameters CommandParameters) (ScalarValue, bool) {
	switch capability {
	case CapabilityStart:
		return TextScalar("RUNNING"), len(parameters) == 0
	case CapabilityStop:
		return TextScalar("STOPPED"), len(parameters) == 0
	case CapabilityResetFault:
		return TextScalar(""), len(parameters) == 0
	default:
		value, ok := ParameterValue(capability, parameters)
		return NumberScalar(value), ok
	}
}

func ScalarMatches(actual, expected ScalarValue, tolerance float64) bool {
	if expected.Number != nil {
		if actual.Number == nil {
			return false
		}
		delta := *actual.Number - *expected.Number
		if delta < 0 {
			delta = -delta
		}
		return delta <= tolerance
	}
	if expected.Text != nil {
		return actual.Text != nil && *actual.Text == *expected.Text
	}
	if expected.Boolean != nil {
		return actual.Boolean != nil && *actual.Boolean == *expected.Boolean
	}
	return false
}

type ApprovalPolicy string

const (
	ApprovalNone           ApprovalPolicy = "NONE"
	ApprovalSingleApprover ApprovalPolicy = "SINGLE_APPROVER"
	ApprovalTwoPerson      ApprovalPolicy = "TWO_PERSON"
)

type AuthorizationPurpose string

const (
	AuthorizationCommandSubmit  AuthorizationPurpose = "COMMAND_SUBMIT"
	AuthorizationCommandApprove AuthorizationPurpose = "COMMAND_APPROVE"
)

type RetryPolicy string

const RetryPreSendOnly RetryPolicy = "PRE_SEND_ONLY"

type IntentStatus string

const (
	IntentSubmitted        IntentStatus = "SUBMITTED"
	IntentValidating       IntentStatus = "VALIDATING"
	IntentAwaitingApproval IntentStatus = "AWAITING_APPROVAL"
	IntentApproved         IntentStatus = "APPROVED"
	IntentQueued           IntentStatus = "QUEUED"
	IntentDispatching      IntentStatus = "DISPATCHING"
	IntentSucceeded        IntentStatus = "SUCCEEDED"
	IntentFailed           IntentStatus = "FAILED"
	IntentRejected         IntentStatus = "REJECTED"
	IntentCancelled        IntentStatus = "CANCELLED"
	IntentExpired          IntentStatus = "EXPIRED"
	IntentOutcomeUnknown   IntentStatus = "OUTCOME_UNKNOWN"
)

type AttemptStatus string

const (
	AttemptPrepared         AttemptStatus = "PREPARED"
	AttemptRequestCommitted AttemptStatus = "REQUEST_COMMITTED"
	AttemptAcknowledged     AttemptStatus = "ACKNOWLEDGED"
	AttemptVerified         AttemptStatus = "VERIFIED"
	AttemptNotSent          AttemptStatus = "NOT_SENT"
	AttemptFailed           AttemptStatus = "FAILED"
	AttemptOutcomeUnknown   AttemptStatus = "OUTCOME_UNKNOWN"
)

type ConnectorPhase string

const (
	ConnectorPreSendRejected   ConnectorPhase = "PRE_SEND_REJECTED"
	ConnectorExecutionRejected ConnectorPhase = "EXECUTION_REJECTED"
	ConnectorRequestCommitted  ConnectorPhase = "REQUEST_COMMITTED"
	ConnectorAcknowledged      ConnectorPhase = "ACKNOWLEDGED"
)

type CurrentStateEvidence struct {
	EvaluationAvailability string
	Presence               string
	Readiness              string
	Quality                string
	BusinessRevision       uint64
	CurrentValue           *float64
	ObservedAt             time.Time
}

type AuthorizationSnapshot struct {
	GrantID                     string
	PolicyRevision              string
	Purpose                     AuthorizationPurpose
	PrincipalID                 string
	TenantID                    string
	SiteID                      string
	DeviceID                    string
	Capability                  Capability
	MaximumRisk                 RiskLevel
	CapabilityRevision          string
	EmergencyRevocationRevision uint64
	IssuedAt                    time.Time
	ExpiresAt                   time.Time
}

type RiskSnapshot struct {
	Level        RiskLevel
	RuleRevision string
	Reasons      []string
	EvaluatedAt  time.Time
}

type ApprovalEvidence struct {
	ApprovalID         string
	ApproverID         string
	ApproverRole       string
	Policy             ApprovalPolicy
	PayloadHash        string
	CapabilityRevision string
	Risk               RiskLevel
	RiskRuleRevision   string
	Authorization      AuthorizationSnapshot
	IssuedAt           time.Time
	ExpiresAt          time.Time
}

type SubmitRequest struct {
	TenantID             string
	SiteID               string
	DeviceID             string
	PointID              string
	PrincipalID          string
	IdempotencyKey       string
	Capability           Capability
	Parameters           CommandParameters
	VerificationPointKey string
	CurrentState         CurrentStateEvidence
	Authorization        AuthorizationSnapshot
}

type Transition struct {
	From       IntentStatus
	To         IntentStatus
	Reason     string
	Actor      string
	At         time.Time
	Version    uint64
	Causation  string
	EvidenceID string
}

type EdgeConstraintEvidence struct {
	ControllerID string `json:"controllerId"`
	Reason       string `json:"reason"`
}

type EdgeExecutionEvidence struct {
	Requested          ScalarValue              `json:"requested"`
	Effective          *ScalarValue             `json:"effective,omitempty"`
	Applied            *ScalarValue             `json:"applied,omitempty"`
	Constraints        []EdgeConstraintEvidence `json:"constraints,omitempty"`
	WinnerControllerID string                   `json:"winnerControllerId"`
	Cycle              uint64                   `json:"cycle"`
}

func (e EdgeExecutionEvidence) Valid() bool {
	if e.Cycle == 0 || strings.TrimSpace(e.WinnerControllerID) == "" || !e.Requested.Valid() {
		return false
	}
	if e.Effective != nil && !e.Effective.Valid() {
		return false
	}
	if e.Applied != nil && !e.Applied.Valid() {
		return false
	}
	for _, constraint := range e.Constraints {
		if strings.TrimSpace(constraint.ControllerID) == "" || strings.TrimSpace(constraint.Reason) == "" {
			return false
		}
	}
	return true
}

func (e EdgeExecutionEvidence) ValidExecuted() bool {
	return e.Valid() && e.Effective != nil
}

func (value ScalarValue) Valid() bool {
	count := 0
	if value.Number != nil {
		count++
	}
	if value.Text != nil {
		count++
	}
	if value.Boolean != nil {
		count++
	}
	return count == 1
}

func ExpectedVerificationValue(capability Capability, parameters CommandParameters, edge *EdgeExecutionEvidence) (ScalarValue, bool) {
	profile, supported := CapabilityProfileFor(capability)
	if !supported {
		return ScalarValue{}, false
	}
	if profile.ParameterKey != "" && edge != nil && edge.ValidExecuted() {
		if edge.Applied != nil && edge.Applied.Number != nil {
			return *edge.Applied, true
		}
		if edge.Effective != nil && edge.Effective.Number != nil {
			return *edge.Effective, true
		}
	}
	return ExpectedReportedValue(capability, parameters)
}

type CommandAttempt struct {
	ID                     string
	CommandID              string
	Status                 AttemptStatus
	Version                uint64
	ExecutionFence         uint64
	PayloadHash            string
	LeaseOwner             string
	LeaseUntil             time.Time
	ConnectorEvidenceID    string
	AcknowledgedAt         time.Time
	VerificationDeadline   time.Time
	VerificationLeaseOwner string
	VerificationLeaseUntil time.Time
	VerificationEvidenceID string
	EdgeExecution          *EdgeExecutionEvidence
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

type CommandIntent struct {
	ID                    string
	TenantID              string
	SiteID                string
	DeviceID              string
	PointID               string
	PrincipalID           string
	IdempotencyKey        string
	Capability            Capability
	CapabilityRevision    string
	Risk                  RiskLevel
	RiskSnapshot          RiskSnapshot
	ApprovalPolicy        ApprovalPolicy
	Authorization         AuthorizationSnapshot
	Authorizations        []AuthorizationSnapshot
	Approvals             []ApprovalEvidence
	RetryPolicy           RetryPolicy
	Parameters            CommandParameters
	VerificationPointKey  string
	PayloadHash           string
	SnapshotRevision      uint64
	DeviceCommandSequence uint64
	Status                IntentStatus
	Version               uint64
	ActiveFence           uint64
	CreatedAt             time.Time
	UpdatedAt             time.Time
	Transitions           []Transition
	Attempts              []CommandAttempt
}

type ApproveRequest struct {
	TenantID  string
	CommandID string
	Approval  ApprovalEvidence
}

type DispatchEnvelope struct {
	CommandID             string
	AttemptID             string
	TenantID              string
	SiteID                string
	DeviceID              string
	PointID               string
	Capability            Capability
	CapabilityRevision    string
	Parameters            CommandParameters
	PayloadHash           string
	ExecutionFence        uint64
	DeviceCommandSequence uint64
	LeaseOwner            string
	LeaseUntil            time.Time
}

type VerificationOutcome string

const (
	VerificationSucceeded    VerificationOutcome = "VERIFIED"
	VerificationInconclusive VerificationOutcome = "INCONCLUSIVE"
	VerificationMismatch     VerificationOutcome = "MISMATCH"
	VerificationTimedOut     VerificationOutcome = "TIMED_OUT"
)

type VerificationEnvelope struct {
	CommandID                string
	AttemptID                string
	TenantID                 string
	SiteID                   string
	DeviceID                 string
	PointID                  string
	Capability               Capability
	CapabilityRevision       string
	Parameters               CommandParameters
	VerificationPointKey     string
	PayloadHash              string
	ExecutionFence           uint64
	BaselineBusinessRevision uint64
	AcknowledgedAt           time.Time
	VerificationDeadline     time.Time
	LeaseOwner               string
	LeaseUntil               time.Time
	ConnectorEvidenceID      string
	EdgeExecution            *EdgeExecutionEvidence
}

type ReportedStateEvidence struct {
	TenantID               string
	SiteID                 string
	DeviceID               string
	EvaluationAvailability string
	Presence               string
	Readiness              string
	Freshness              string
	Quality                string
	BusinessRevision       uint64
	ReportedValue          ScalarValue
	ObservedAt             time.Time
}

type VerificationResult struct {
	Outcome     VerificationOutcome
	EvidenceID  string
	FailureCode string
	Reported    ReportedStateEvidence
}

type PreparedConnectorEvidence struct {
	AttemptID        string
	CommandID        string
	TenantID         string
	SiteID           string
	DeviceID         string
	ExternalDeviceID string
	ExecutionFence   uint64
	PayloadHash      string
	MappingRevision  string
	BindingRevision  string
	ProviderEndpoint string
	ProviderMethod   string
	RequestSHA256    string
	PreparedAt       time.Time
}

type CompletedConnectorEvidence struct {
	PreparedConnectorEvidence
	ProviderStatusCode int
	ResponseSHA256     string
	RequestWritten     bool
	ConnectorPhase     ConnectorPhase
	FailureCode        string
	CompletedAt        time.Time
	EdgeExecution      *EdgeExecutionEvidence
}

type ConnectorResult struct {
	Phase         ConnectorPhase
	Verified      bool
	FailureCode   string
	EvidenceID    string
	Acknowledged  bool
	EdgeExecution *EdgeExecutionEvidence
}

type DeviceRoute struct {
	ExternalDeviceID string
	BindingRevision  uint64
}

type CorrelationState string

const (
	CorrelationPrepared  CorrelationState = "PREPARED"
	CorrelationMayCommit CorrelationState = "MAY_COMMIT"
	CorrelationReplied   CorrelationState = "REPLIED"
	CorrelationResolved  CorrelationState = "RESOLVED"
)

type CommandCorrelation struct {
	Envelope              DispatchEnvelope
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
	EdgeExecution         *EdgeExecutionEvidence
	RepliedAt             time.Time
}
