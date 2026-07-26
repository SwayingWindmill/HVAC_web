package commandmodel

import "time"

type Capability string

const CapabilitySetTemperatureSetpoint Capability = "SET_TEMPERATURE_SETPOINT"

type RiskLevel string

const (
	RiskLow      RiskLevel = "LOW"
	RiskMedium   RiskLevel = "MEDIUM"
	RiskHigh     RiskLevel = "HIGH"
	RiskCritical RiskLevel = "CRITICAL"
)

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
	ConnectorPreSendRejected  ConnectorPhase = "PRE_SEND_REJECTED"
	ConnectorRequestCommitted ConnectorPhase = "REQUEST_COMMITTED"
	ConnectorAcknowledged     ConnectorPhase = "ACKNOWLEDGED"
)

type CurrentStateEvidence struct {
	EvaluationAvailability string
	Presence               string
	Readiness              string
	Quality                string
	BusinessRevision       uint64
	CurrentTemperatureC    float64
	ObservedAt             time.Time
}

type AuthorizationSnapshot struct {
	GrantID                     string
	PolicyRevision              string
	Purpose                     AuthorizationPurpose
	PrincipalID                 string
	OrganizationID              string
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
	OrganizationID string
	SiteID         string
	DeviceID       string
	PrincipalID    string
	IdempotencyKey string
	Capability     Capability
	SetpointC      float64
	CurrentState   CurrentStateEvidence
	Authorization  AuthorizationSnapshot
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
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

type CommandIntent struct {
	ID                    string
	OrganizationID        string
	SiteID                string
	DeviceID              string
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
	SetpointC             float64
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
	OrganizationID string
	CommandID      string
	Approval       ApprovalEvidence
}

type DispatchEnvelope struct {
	CommandID             string
	AttemptID             string
	OrganizationID        string
	SiteID                string
	DeviceID              string
	Capability            Capability
	CapabilityRevision    string
	SetpointC             float64
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
	OrganizationID           string
	SiteID                   string
	DeviceID                 string
	Capability               Capability
	CapabilityRevision       string
	SetpointC                float64
	PayloadHash              string
	ExecutionFence           uint64
	BaselineBusinessRevision uint64
	AcknowledgedAt           time.Time
	VerificationDeadline     time.Time
	LeaseOwner               string
	LeaseUntil               time.Time
	ConnectorEvidenceID      string
}

type ReportedStateEvidence struct {
	OrganizationID         string
	SiteID                 string
	DeviceID               string
	EvaluationAvailability string
	Presence               string
	Readiness              string
	Freshness              string
	Quality                string
	BusinessRevision       uint64
	ReportedSetpointC      float64
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
	OrganizationID   string
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
}

type ConnectorResult struct {
	Phase        ConnectorPhase
	Verified     bool
	FailureCode  string
	EvidenceID   string
	Acknowledged bool
}
