package commandmodel

import (
	"errors"
	"math"
	"strings"
	"time"
)

type Capability string

const (
	CapabilityStart                              Capability = "START"
	CapabilityStop                               Capability = "STOP"
	CapabilityStartUnitSequence                  Capability = "START_UNIT_SEQUENCE"
	CapabilityStopUnitSequence                   Capability = "STOP_UNIT_SEQUENCE"
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

const (
	defaultVerificationWindow  = 2 * time.Minute
	sequenceVerificationWindow = 10 * time.Minute
)

type CapabilityProfile struct {
	Revision                string
	ParameterKey            string
	Minimum                 float64
	Maximum                 float64
	Step                    float64
	MaximumDelta            float64
	LowRiskDelta            float64
	MediumRiskDelta         float64
	BaseRisk                RiskLevel
	VerificationTolerance   float64
	VerificationWindow      time.Duration
	SupportsEmergencyBypass bool
}

func CapabilityProfileFor(capability Capability) (CapabilityProfile, bool) {
	switch capability {
	case CapabilityStart:
		return CapabilityProfile{Revision: "capability:start:v1", BaseRisk: RiskHigh, VerificationWindow: sequenceVerificationWindow}, true
	case CapabilityStop:
		return CapabilityProfile{Revision: "capability:stop:v1", BaseRisk: RiskHigh, VerificationWindow: sequenceVerificationWindow, SupportsEmergencyBypass: true}, true
	case CapabilityStartUnitSequence:
		return CapabilityProfile{Revision: "capability:start-unit-sequence:v1", BaseRisk: RiskHigh, VerificationWindow: sequenceVerificationWindow}, true
	case CapabilityStopUnitSequence:
		return CapabilityProfile{Revision: "capability:stop-unit-sequence:v1", BaseRisk: RiskHigh, VerificationWindow: sequenceVerificationWindow, SupportsEmergencyBypass: true}, true
	case CapabilityResetFault:
		return CapabilityProfile{Revision: "capability:reset-fault:v1", BaseRisk: RiskLow, VerificationWindow: defaultVerificationWindow}, true
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

// VerificationWindowFor returns how long readback verification waits for reported-state
// confirmation after an edge acknowledge. Equipment start/stop and unit-sequence actions
// allow 10 minutes for telemetry intervals and ramp/coast inertia; setpoints keep 2 minutes.
func VerificationWindowFor(capability Capability) time.Duration {
	if profile, ok := CapabilityProfileFor(capability); ok && profile.VerificationWindow > 0 {
		return profile.VerificationWindow
	}
	return defaultVerificationWindow
}

func numericCapability(revision, parameterKey string, minimum, maximum, step, maximumDelta, lowRiskDelta, mediumRiskDelta, tolerance float64) CapabilityProfile {
	return CapabilityProfile{
		Revision: revision, ParameterKey: parameterKey, Minimum: minimum, Maximum: maximum, Step: step,
		MaximumDelta: maximumDelta, LowRiskDelta: lowRiskDelta, MediumRiskDelta: mediumRiskDelta,
		BaseRisk: RiskLow, VerificationTolerance: tolerance, VerificationWindow: defaultVerificationWindow,
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

// EvaluateRiskLevel computes the deterministic command risk level and reasons from
// the capability profile and, for numeric capabilities, the current reported value.
func EvaluateRiskLevel(capability Capability, parameters CommandParameters, currentValue *float64) (RiskLevel, []string, bool) {
	profile, ok := CapabilityProfileFor(capability)
	if !ok {
		return "", nil, false
	}
	if profile.ParameterKey == "" {
		return profile.BaseRisk, []string{"CAPABILITY_BASE_RISK"}, true
	}
	value, valid := ParameterValue(capability, parameters)
	if !valid || currentValue == nil {
		return "", nil, false
	}
	delta := math.Abs(value - *currentValue)
	switch {
	case delta <= profile.LowRiskDelta:
		return RiskLow, []string{"CAPABILITY_DELTA_LOW"}, true
	case delta <= profile.MediumRiskDelta:
		return RiskMedium, []string{"CAPABILITY_DELTA_MEDIUM"}, true
	case delta <= profile.MaximumDelta:
		return RiskHigh, []string{"CAPABILITY_DELTA_HIGH"}, true
	default:
		return "", nil, false
	}
}

func ExpectedReportedValue(capability Capability, parameters CommandParameters) (ScalarValue, bool) {
	switch capability {
	case CapabilityStart, CapabilityStartUnitSequence:
		return TextScalar("RUNNING"), len(parameters) == 0
	case CapabilityStop, CapabilityStopUnitSequence:
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
		// The relative term absorbs binary representation error, as numpy.isclose's rtol does,
		// so a reading exactly at the tolerance (48.1 against 48 within 0.1) matches.
		return math.Abs(*actual.Number-*expected.Number) <= tolerance+1e-9*math.Abs(*expected.Number)
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
	WorkloadSPIFFE              string
	LeaseID                     string
	ArmedBy                     string
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
	TraceID              string `json:"traceId,omitempty"`
	Capability           Capability
	Parameters           CommandParameters
	VerificationPointKey string
	PreconditionMode     PreconditionMode
	BypassReason         string
	PriorityTier         PriorityTier  `json:"priorityTier,omitempty"`
	CommandSource        CommandSource `json:"commandSource,omitempty"`
	DeliveryMode         DeliveryMode  `json:"deliveryMode,omitempty"`
	TTLSeconds           *int          `json:"ttlSeconds,omitempty"`
	ExpiresAt            *time.Time    `json:"expiresAt,omitempty"`
	BatchID              *string       `json:"batchId,omitempty"`
	BatchItemIndex       *int          `json:"batchItemIndex,omitempty"`
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
	Requested             ScalarValue              `json:"requested"`
	Effective             *ScalarValue             `json:"effective,omitempty"`
	Applied               *ScalarValue             `json:"applied,omitempty"`
	Readback              *ScalarValue             `json:"readback,omitempty"`
	LocalReadbackVerified *bool                    `json:"localReadbackVerified,omitempty"`
	LocalReadbackAt       *int64                   `json:"localReadbackAt,omitempty"`
	Constraints           []EdgeConstraintEvidence `json:"constraints,omitempty"`
	WinnerControllerID    string                   `json:"winnerControllerId"`
	Cycle                 uint64                   `json:"cycle"`
	LeaseExpiresAt        *int64                   `json:"leaseExpiresAt,omitempty"`
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
	if e.Readback != nil && !e.Readback.Valid() {
		return false
	}
	if e.LocalReadbackAt != nil && *e.LocalReadbackAt <= 0 {
		return false
	}
	if e.LeaseExpiresAt != nil && *e.LeaseExpiresAt <= 0 {
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

func (e EdgeExecutionEvidence) validAppliedText() (string, bool) {
	if !e.ValidExecuted() || e.Applied == nil || e.Applied.Text == nil || e.Applied.Number != nil || e.Applied.Boolean != nil {
		return "", false
	}
	return *e.Applied.Text, true
}

// ValidPulseDualReadback reports whether edge execution evidence proves a pulse relay
// closed and then returned to 0 (dual readback: "${channel}:pulse(${duration}ms)->0").
func (e EdgeExecutionEvidence) ValidPulseDualReadback() bool {
	applied, ok := e.validAppliedText()
	if !ok {
		return false
	}
	return strings.Contains(applied, ":pulse(") && strings.HasSuffix(applied, ")->0")
}

// ValidEmergencyDoReleaseReadback reports whether edge execution evidence proves
// an emergency DO release read back all outputs released ("all-do:0").
func (e EdgeExecutionEvidence) ValidEmergencyDoReleaseReadback() bool {
	applied, ok := e.validAppliedText()
	return ok && applied == "all-do:0"
}

// ValidUnitSequenceAcceptance reports whether edge execution evidence proves
// a unit-controller sequence command was accepted ("${unit}:sequence-accepted").
func (e EdgeExecutionEvidence) ValidUnitSequenceAcceptance() bool {
	applied, ok := e.validAppliedText()
	return ok && strings.HasSuffix(applied, ":sequence-accepted")
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
	TraceID               string `json:"traceId,omitempty"`
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
	PreconditionMode      PreconditionMode
	BypassReason          string
	PriorityTier          PriorityTier  `json:"priorityTier,omitempty"`
	CommandSource         CommandSource `json:"commandSource,omitempty"`
	DeliveryMode          DeliveryMode  `json:"deliveryMode,omitempty"`
	TTLSeconds            *int          `json:"ttlSeconds,omitempty"`
	ExpiresAt             *time.Time    `json:"expiresAt,omitempty"`
	NextDispatchAfter     *time.Time    `json:"nextDispatchAfter,omitempty"`
	BatchID               *string       `json:"batchId,omitempty"`
	BatchItemIndex        *int          `json:"batchItemIndex,omitempty"`
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

// ReconciledOutcome is what a person with control authority states about a command whose
// outcome the platform could not prove.
type ReconciledOutcome string

const (
	ReconciledApplied    ReconciledOutcome = "APPLIED"
	ReconciledNotApplied ReconciledOutcome = "NOT_APPLIED"
)

// ReconcileRequest settles an OUTCOME_UNKNOWN command. Authorization is the reconciler's
// COMMAND_SUBMIT grant for the command's Device and Capability.
type ReconcileRequest struct {
	TenantID      string
	CommandID     string
	Outcome       ReconciledOutcome
	Authorization AuthorizationSnapshot
}

// CancelRequest cancels an AWAITING_APPROVAL or QUEUED command before dispatch starts.
// Authorization is the caller's COMMAND_SUBMIT grant for the command's Device and Capability.
type CancelRequest struct {
	TenantID      string
	CommandID     string
	Reason        string
	Authorization AuthorizationSnapshot
}

// CancelPendingRequest cancels pending commands scoped to a Site (and optional Device IDs)
// when an operator activates a control exit or asset lock.
type CancelPendingRequest struct {
	TenantID             string
	SiteID               string
	DeviceIDs            []string
	PrincipalID          string
	Reason               string
	CausationID          string
	AbortActiveSequences bool `json:"abortActiveSequences,omitempty"`
}

type DispatchEnvelope struct {
	CommandID             string
	AttemptID             string
	TenantID              string
	SiteID                string
	DeviceID              string
	PointID               string
	TraceID               string `json:"traceId,omitempty"`
	Capability            Capability
	CapabilityRevision    string
	Parameters            CommandParameters
	DeliveryMode          DeliveryMode `json:"deliveryMode,omitempty"`
	TTLSeconds            *int         `json:"ttlSeconds,omitempty"`
	ExpiresAt             *time.Time   `json:"expiresAt,omitempty"`
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

	ReasonPulseDeliveredPowerUnconfirmed = "PULSE_DELIVERED_POWER_UNCONFIRMED"
)

type VerificationEnvelope struct {
	CommandID                string
	AttemptID                string
	TenantID                 string
	SiteID                   string
	DeviceID                 string
	PointID                  string
	TraceID                  string `json:"traceId,omitempty"`
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

// InterlockCheck describes a prerequisite peer device state that must hold before
// executing a command (for example requiring pumps and cooling towers to be RUNNING before
// pulsing a chiller ON, or requiring a chiller to be STOPPED before opening a pump contactor).
type InterlockCheck struct {
	DeviceID string `json:"deviceId"`
	Key      string `json:"key"`
	Expected string `json:"expected"`
	Reason   string `json:"reason,omitempty"`
}

// DeviceRoute is where a command for a Device goes: the Gateway the Device is behind and
// the source key the Gateway knows it by, from the Registry read port. Optional actuation
// fields stay at zero values unless the active COMMAND point declares source_metadata.
type DeviceRoute struct {
	GatewayID        string
	ExternalDeviceID string
	BindingRevision  uint64
	PointCode        string
	ActuationMode    string
	Channel          string
	CoilAddress      int
	HasCoilAddress   bool
	PulseDurationMs  int
}

// Command routing failures, each known before anything is sent to a Gateway.
var (
	ErrCommandRouteNotFound             = errors.New("command Device is not behind a registered Gateway")
	ErrCommandControlDisabled           = errors.New("command Point is not an active writable Point of the Device")
	ErrCommandGatewayCredentialInactive = errors.New("command Gateway holds no active credential")
)

// RouteFailureCode is the operator-facing reason for a routing failure, or "" when err is
// not one. Dispatching such a command again cannot succeed until the Registry or the
// Gateway Credential changes, so the command ends rejected with this reason.
func RouteFailureCode(err error) string {
	switch {
	case errors.Is(err, ErrCommandRouteNotFound):
		return "COMMAND_ROUTE_NOT_FOUND"
	case errors.Is(err, ErrCommandControlDisabled):
		return "COMMAND_CONTROL_DISABLED"
	case errors.Is(err, ErrCommandGatewayCredentialInactive):
		return "COMMAND_GATEWAY_CREDENTIAL_INACTIVE"
	default:
		return ""
	}
}

// TerminalPreSendRejection reports whether a command refused before send must end
// rejected rather than be dispatched again.
// DeliveryMode selects how dispatch handles offline or sleeping target devices.
type DeliveryMode string

const (
	DeliveryModeFailFast       DeliveryMode = "FAIL_FAST"
	DeliveryModeQueueIfOffline DeliveryMode = "QUEUE_IF_OFFLINE"
)

const (
	MinCommandTTLSeconds     = 1
	MaxCommandTTLSeconds     = 86400
	MaxCommandTTL            = time.Duration(MaxCommandTTLSeconds) * time.Second
	MaxBatchCommandTargets   = 100
	InitialOfflineDeferDelay = 5 * time.Second
	MaxOfflineDeferDelay     = 60 * time.Second
)

var (
	ErrInvalidDeliveryMode     = errors.New("command delivery mode must be FAIL_FAST or QUEUE_IF_OFFLINE")
	ErrOfflineQueueUnsupported = errors.New("capability does not support QUEUE_IF_OFFLINE delivery mode")
	ErrInvalidTTLSeconds       = errors.New("command ttlSeconds must be between 1 and 86400 seconds and is required for QUEUE_IF_OFFLINE")
)

func NormalizeDeliveryMode(mode DeliveryMode) DeliveryMode {
	trimmed := strings.ToUpper(strings.TrimSpace(string(mode)))
	if trimmed == "" {
		return DeliveryModeFailFast
	}
	return DeliveryMode(trimmed)
}

func CapabilityAllowsOfflineQueue(capability Capability) bool {
	switch capability {
	case CapabilitySetTemperatureSetpoint,
		CapabilitySetChilledWaterTemperatureSetpoint,
		CapabilitySetFrequency,
		CapabilitySetFanSpeed,
		CapabilitySetLoadLimit,
		CapabilitySetOpening:
		return true
	default:
		return false
	}
}

func ValidateDeliveryAndTTL(capability Capability, rawMode DeliveryMode, ttlSeconds *int, rawExpiresAt *time.Time, now time.Time) (DeliveryMode, *int, *time.Time, error) {
	mode := NormalizeDeliveryMode(rawMode)
	switch mode {
	case DeliveryModeFailFast, DeliveryModeQueueIfOffline:
	default:
		return "", nil, nil, ErrInvalidDeliveryMode
	}
	if mode == DeliveryModeQueueIfOffline && !CapabilityAllowsOfflineQueue(capability) {
		return "", nil, nil, ErrOfflineQueueUnsupported
	}
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}

	var normalizedTTL *int
	if ttlSeconds != nil {
		if *ttlSeconds < MinCommandTTLSeconds || *ttlSeconds > MaxCommandTTLSeconds {
			return "", nil, nil, ErrInvalidTTLSeconds
		}
		v := *ttlSeconds
		normalizedTTL = &v
	}

	var normalizedExpires *time.Time
	if rawExpiresAt != nil && !rawExpiresAt.IsZero() {
		exp := rawExpiresAt.UTC()
		remaining := exp.Sub(now)
		if remaining <= 0 || remaining > MaxCommandTTL {
			return "", nil, nil, ErrInvalidTTLSeconds
		}
		normalizedExpires = &exp
		if normalizedTTL == nil {
			secs := int((remaining + time.Second - 1) / time.Second)
			if secs < MinCommandTTLSeconds {
				secs = MinCommandTTLSeconds
			}
			if secs > MaxCommandTTLSeconds {
				secs = MaxCommandTTLSeconds
			}
			normalizedTTL = &secs
		}
	} else if normalizedTTL != nil {
		exp := now.Add(time.Duration(*normalizedTTL) * time.Second)
		normalizedExpires = &exp
	}

	if mode == DeliveryModeQueueIfOffline && normalizedExpires == nil {
		return "", nil, nil, ErrInvalidTTLSeconds
	}
	return mode, normalizedTTL, normalizedExpires, nil
}

func IsDeferrableOfflinePreSendCode(failureCode string) bool {
	switch strings.TrimSpace(failureCode) {
	case "DISPATCH_SAFETY_DEVICE_NOT_ONLINE",
		"DISPATCH_SAFETY_DEVICE_OFFLINE",
		"DISPATCH_SAFETY_DEVICE_SLEEPING",
		"DISPATCH_SAFETY_STATE_UNAVAILABLE",
		"DISPATCH_SAFETY_STATE_NOT_CURRENT",
		"DISPATCH_SAFETY_STATE_NOT_FRESH",
		"DISPATCH_SAFETY_EVALUATION_FAILED",
		"DISPATCH_SAFETY_TELEMETRY_STALE",
		"COMMAND_ROUTE_TEMPORARILY_UNAVAILABLE",
		"MQTT_GATEWAY_DISCONNECTED":
		return true
	default:
		return false
	}
}

func IsTTLExpiredFailureCode(failureCode string) bool {
	switch strings.TrimSpace(failureCode) {
	case "COMMAND_TTL_EXPIRED", "MQTT_EDGE_EXPIRED", "EXPIRED", "MQTT_EDGE_TTL_EXCEEDS_MAX":
		return true
	default:
		return false
	}
}

func OfflineDeferBackoff(deferredAttempts int) time.Duration {
	if deferredAttempts <= 1 {
		return InitialOfflineDeferDelay
	}
	delay := InitialOfflineDeferDelay
	for step := 1; step < deferredAttempts; step++ {
		if delay >= MaxOfflineDeferDelay {
			return MaxOfflineDeferDelay
		}
		delay *= 2
	}
	if delay > MaxOfflineDeferDelay {
		return MaxOfflineDeferDelay
	}
	return delay
}

func TerminalPreSendRejection(failureCode string) bool {
	code := strings.TrimSpace(failureCode)
	if IsTTLExpiredFailureCode(code) {
		return true
	}
	switch code {
	case "COMMAND_ROUTE_NOT_FOUND", "COMMAND_CONTROL_DISABLED", "COMMAND_GATEWAY_CREDENTIAL_INACTIVE",
		"DISPATCH_SAFETY_CONTROL_LOCKED", "DISPATCH_SAFETY_MAINTENANCE_WINDOW",
		"DEVICE_PRIORITY_LOCKED", "PRIORITY_PREEMPTED":
		return true
	case "DISPATCH_SAFETY_EVALUATION_FAILED":
		return false
	}
	return strings.HasPrefix(code, "DISPATCH_SAFETY_")
}

type CorrelationState string

const (
	CorrelationPrepared  CorrelationState = "PREPARED"
	CorrelationMayCommit CorrelationState = "MAY_COMMIT"
	CorrelationReplied   CorrelationState = "REPLIED"
	CorrelationResolved  CorrelationState = "RESOLVED"
)

var (
	ErrCommandCorrelationNotFound = errors.New("command correlation record not found")
	ErrCommandCorrelationMismatch = errors.New("command correlation does not match durable state")
)

type CommandCorrelation struct {
	Envelope         DispatchEnvelope
	GatewayID        string
	ExternalDeviceID string
	MappingRevision  string
	BindingRevision  string
	ProviderEndpoint string
	ProviderMethod   string
	RequestSHA256    string
	PreparedAt       time.Time
	State            CorrelationState
	ReplySHA256      string
	ReplyStatus      string
	ReplyEventTime   time.Time
	ReplyReasonCode  string
	EdgeExecution    *EdgeExecutionEvidence
	RepliedAt        time.Time
}

// NormalizeTraceID validates a W3C 32-hex distributed trace ID, rejects all-zero values,
// and returns the lowercase 32-character form or empty string when invalid.
func NormalizeTraceID(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if len(trimmed) != 32 {
		return ""
	}
	var normalized [32]byte
	nonZero := false
	for i := 0; i < 32; i++ {
		ch := trimmed[i]
		switch {
		case ch >= '0' && ch <= '9':
			if ch != '0' {
				nonZero = true
			}
			normalized[i] = ch
		case ch >= 'a' && ch <= 'f':
			nonZero = true
			normalized[i] = ch
		case ch >= 'A' && ch <= 'F':
			nonZero = true
			normalized[i] = ch + ('a' - 'A')
		default:
			return ""
		}
	}
	if !nonZero {
		return ""
	}
	return string(normalized[:])
}

type PriorityTier string

const (
	PrioritySafetyInterlock PriorityTier = "SAFETY_INTERLOCK"
	PriorityManualOverride  PriorityTier = "MANUAL_OVERRIDE"
	PriorityProtectionRule  PriorityTier = "PROTECTION_RULE"
	PriorityAIOptimization  PriorityTier = "AI_OPTIMIZATION"
	PrioritySchedule        PriorityTier = "SCHEDULE"
)

func (p PriorityTier) Valid() bool {
	switch p {
	case PrioritySafetyInterlock, PriorityManualOverride, PriorityProtectionRule, PriorityAIOptimization, PrioritySchedule:
		return true
	default:
		return false
	}
}

// PriorityRank maps each priority tier to a deterministic numeric precedence rank.
func PriorityRank(tier PriorityTier) int {
	switch tier {
	case PrioritySafetyInterlock:
		return 500
	case PriorityManualOverride:
		return 400
	case PriorityProtectionRule:
		return 300
	case PriorityAIOptimization:
		return 200
	case PrioritySchedule:
		return 100
	default:
		return 0
	}
}

type CommandSource string

const (
	SourceSafetyInterlock CommandSource = "SAFETY_INTERLOCK"
	SourceOperator        CommandSource = "OPERATOR"
	SourceProtectionRule  CommandSource = "PROTECTION_RULE"
	SourceOptimization    CommandSource = "OPTIMIZATION"
	SourceSchedule        CommandSource = "SCHEDULE"
)

func (s CommandSource) Valid() bool {
	switch s {
	case SourceSafetyInterlock, SourceOperator, SourceProtectionRule, SourceOptimization, SourceSchedule:
		return true
	default:
		return false
	}
}

type IntentPriorityState struct {
	Status       IntentStatus
	PriorityTier PriorityTier
}

type DevicePriorityLock struct {
	TenantID       string        `json:"tenantId"`
	SiteID         string        `json:"siteId"`
	DeviceID       string        `json:"deviceId"`
	LockedPriority PriorityTier  `json:"lockedPriority"`
	CommandSource  CommandSource `json:"commandSource"`
	LockedBy       string        `json:"lockedBy"`
	LockedUntil    time.Time     `json:"lockedUntil"`
	Reason         string        `json:"reason"`
	CreatedAt      time.Time     `json:"createdAt"`
	UpdatedAt      time.Time     `json:"updatedAt"`
}

var ErrInvalidCommandPriority = errors.New("invalid or incompatible command priority")

func defaultSourceForPriority(tier PriorityTier) CommandSource {
	return DefaultCommandSourceForPriority(tier)
}

// DefaultCommandSourceForPriority returns the canonical CommandSource for a valid PriorityTier.
func DefaultCommandSourceForPriority(tier PriorityTier) CommandSource {
	switch tier {
	case PrioritySafetyInterlock:
		return SourceSafetyInterlock
	case PriorityManualOverride:
		return SourceOperator
	case PriorityProtectionRule:
		return SourceProtectionRule
	case PriorityAIOptimization:
		return SourceOptimization
	case PrioritySchedule:
		return SourceSchedule
	default:
		return ""
	}
}

func defaultPriorityForSource(source CommandSource) PriorityTier {
	switch source {
	case SourceSafetyInterlock:
		return PrioritySafetyInterlock
	case SourceOperator:
		return PriorityManualOverride
	case SourceProtectionRule:
		return PriorityProtectionRule
	case SourceOptimization:
		return PriorityAIOptimization
	case SourceSchedule:
		return PrioritySchedule
	default:
		return ""
	}
}

func PrioritySourceCompatible(tier PriorityTier, source CommandSource) bool {
	return tier.Valid() && source.Valid() && defaultSourceForPriority(tier) == source
}

func hasWorkloadLease(auth AuthorizationSnapshot) bool {
	return strings.TrimSpace(auth.WorkloadSPIFFE) != "" || strings.TrimSpace(auth.LeaseID) != ""
}

func hasCompleteWorkloadLease(auth AuthorizationSnapshot) bool {
	return strings.TrimSpace(auth.WorkloadSPIFFE) != "" && strings.TrimSpace(auth.LeaseID) != ""
}

func deriveDefaultPriority(request SubmitRequest) (PriorityTier, CommandSource) {
	if request.PreconditionMode == PreconditionEmergencyBypass {
		return PrioritySafetyInterlock, SourceSafetyInterlock
	}
	if hasCompleteWorkloadLease(request.Authorization) {
		return PriorityAIOptimization, SourceOptimization
	}
	return PriorityManualOverride, SourceOperator
}

// ResolveCommandPriority resolves explicit or derived command priority and source, rejecting
// unknown values, tier/source mismatches, or unauthorized elevation by automated leases.
func ResolveCommandPriority(request SubmitRequest) (PriorityTier, CommandSource, error) {
	rawTier := PriorityTier(strings.TrimSpace(string(request.PriorityTier)))
	rawSource := CommandSource(strings.TrimSpace(string(request.CommandSource)))
	if rawTier != "" && !rawTier.Valid() {
		return "", "", ErrInvalidCommandPriority
	}
	if rawSource != "" && !rawSource.Valid() {
		return "", "", ErrInvalidCommandPriority
	}

	var tier PriorityTier
	var source CommandSource
	switch {
	case rawTier != "" && rawSource != "":
		if !PrioritySourceCompatible(rawTier, rawSource) {
			return "", "", ErrInvalidCommandPriority
		}
		tier, source = rawTier, rawSource
	case rawTier != "":
		tier = rawTier
		source = defaultSourceForPriority(rawTier)
	case rawSource != "":
		tier = defaultPriorityForSource(rawSource)
		source = rawSource
	default:
		tier, source = deriveDefaultPriority(request)
	}

	if hasWorkloadLease(request.Authorization) && PriorityRank(tier) > PriorityRank(PriorityProtectionRule) {
		profile, ok := CapabilityProfileFor(request.Capability)
		if tier != PrioritySafetyInterlock || !ok || !profile.SupportsEmergencyBypass || request.PreconditionMode != PreconditionEmergencyBypass {
			return "", "", ErrInvalidCommandPriority
		}
	}
	return tier, source, nil
}

// DeriveCommandPriority returns the deterministic priority tier and command source for a submit request.
func DeriveCommandPriority(request SubmitRequest) (PriorityTier, CommandSource) {
	tier, source, err := ResolveCommandPriority(request)
	if err == nil {
		return tier, source
	}
	if hasCompleteWorkloadLease(request.Authorization) {
		return PriorityAIOptimization, SourceOptimization
	}
	return PriorityManualOverride, SourceOperator
}
