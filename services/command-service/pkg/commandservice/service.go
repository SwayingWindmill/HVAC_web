package commandservice

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
)

var (
	ErrInvalidRequest           = errors.New("invalid command request")
	ErrCapabilityDenied         = errors.New("capability is not permitted")
	ErrCurrentStateUnsafe       = errors.New("current telemetry state is unsafe for control")
	ErrIdempotencyConflict      = errors.New("idempotency key was reused with a different payload")
	ErrCommandNotFound          = errors.New("command not found")
	ErrCommandNotDispatchable   = errors.New("command is not dispatchable")
	ErrAttemptNotFound          = errors.New("command attempt not found")
	ErrStaleFence               = errors.New("execution fence is stale")
	ErrUnsupportedResult        = errors.New("unsupported connector result")
	ErrVerificationNotAvailable = errors.New("no acknowledged command is available for verification")
)

const verificationWindow = 2 * time.Minute

func commandCapabilityProfile(capability commandmodel.Capability) (revision string, minimum, maximum float64, ok bool) {
	profile, ok := commandmodel.CapabilityProfileFor(capability)
	if !ok {
		return "", 0, 0, false
	}
	return profile.Revision, profile.Minimum, profile.Maximum, true
}

type SubmitResult struct {
	Intent   commandmodel.CommandIntent
	Replayed bool
}

type Service struct {
	mu sync.Mutex

	now func() time.Time

	nextCommandID  uint64
	intents        map[string]*commandmodel.CommandIntent
	idempotency    map[string]idempotencyRecord
	deviceSequence map[string]uint64
	deviceFence    map[string]uint64
}

type idempotencyRecord struct {
	CommandID   string
	PayloadHash string
}

func New(now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{
		now:            now,
		intents:        make(map[string]*commandmodel.CommandIntent),
		idempotency:    make(map[string]idempotencyRecord),
		deviceSequence: make(map[string]uint64),
		deviceFence:    make(map[string]uint64),
	}
}

func (s *Service) Submit(request commandmodel.SubmitRequest) (SubmitResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	payloadHash, err := validateAndHash(request)
	if err != nil {
		return SubmitResult{}, err
	}

	idempotencyScope := strings.Join([]string{request.TenantID, request.DeviceID, request.IdempotencyKey}, "\x00")
	if existing, ok := s.idempotency[idempotencyScope]; ok {
		if existing.PayloadHash != payloadHash {
			return SubmitResult{}, ErrIdempotencyConflict
		}
		return SubmitResult{Intent: cloneIntent(*s.intents[existing.CommandID]), Replayed: true}, nil
	}

	now := s.now().UTC()
	risk, approvalPolicy, err := evaluateGovernance(request, now)
	if err != nil {
		return SubmitResult{}, err
	}
	s.nextCommandID++
	commandID := fmt.Sprintf("cmd-%08d", s.nextCommandID)
	s.deviceSequence[request.DeviceID]++

	capabilityRevision, _, _, _ := commandCapabilityProfile(request.Capability)
	intent := &commandmodel.CommandIntent{
		ID:                    commandID,
		TenantID:              request.TenantID,
		SiteID:                request.SiteID,
		DeviceID:              request.DeviceID,
		PointID:               request.PointID,
		PrincipalID:           request.PrincipalID,
		IdempotencyKey:        request.IdempotencyKey,
		Capability:            request.Capability,
		CapabilityRevision:    capabilityRevision,
		Risk:                  risk.Level,
		RiskSnapshot:          risk,
		ApprovalPolicy:        approvalPolicy,
		Authorization:         request.Authorization,
		Authorizations:        []commandmodel.AuthorizationSnapshot{request.Authorization},
		RetryPolicy:           commandmodel.RetryPreSendOnly,
		Parameters:            cloneParameters(request.Parameters),
		VerificationPointKey:  request.VerificationPointKey,
		PayloadHash:           payloadHash,
		SnapshotRevision:      request.CurrentState.BusinessRevision,
		DeviceCommandSequence: s.deviceSequence[request.DeviceID],
		Status:                commandmodel.IntentSubmitted,
		Version:               1,
		CreatedAt:             now,
		UpdatedAt:             now,
	}
	appendTransition(intent, "", commandmodel.IntentSubmitted, "COMMAND_ACCEPTED", request.PrincipalID, now, "submit", request.Authorization.GrantID)
	transition(intent, commandmodel.IntentValidating, "CAPABILITY_CURRENT_STATE_AND_AUTHORIZATION_VALIDATED", "command-service", now, "submit", request.Authorization.GrantID)
	if approvalPolicy == commandmodel.ApprovalNone {
		transition(intent, commandmodel.IntentQueued, "READY_FOR_SYNTHETIC_DISPATCH", "command-service", now, "submit", request.Authorization.GrantID)
	} else {
		transition(intent, commandmodel.IntentAwaitingApproval, "RISK_REQUIRES_APPROVAL", "command-service", now, "submit", request.Authorization.GrantID)
	}

	s.intents[commandID] = intent
	s.idempotency[idempotencyScope] = idempotencyRecord{CommandID: commandID, PayloadHash: payloadHash}

	return SubmitResult{Intent: cloneIntent(*intent)}, nil
}

func (s *Service) Approve(request commandmodel.ApproveRequest) (commandmodel.CommandIntent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	intent, ok := s.intents[request.CommandID]
	if !ok || intent.TenantID != request.TenantID {
		return commandmodel.CommandIntent{}, ErrCommandNotFound
	}
	if intent.Status != commandmodel.IntentAwaitingApproval {
		return commandmodel.CommandIntent{}, ErrApprovalInvalid
	}
	now := s.now().UTC()
	if err := validateApproval(*intent, request.Approval, now); err != nil {
		return commandmodel.CommandIntent{}, err
	}
	intent.Approvals = append(intent.Approvals, request.Approval)
	intent.Authorizations = append(intent.Authorizations, request.Approval.Authorization)
	intent.Authorization = request.Approval.Authorization
	if len(intent.Approvals) < requiredApprovalCount(intent.ApprovalPolicy) {
		return cloneIntent(*intent), ErrApprovalRequired
	}
	transition(intent, commandmodel.IntentApproved, "REQUIRED_APPROVALS_CAPTURED", request.Approval.ApproverID, now, request.Approval.ApprovalID, request.Approval.ApprovalID)
	transition(intent, commandmodel.IntentQueued, "APPROVED_AND_READY_FOR_DISPATCH", "command-service", now, request.Approval.ApprovalID, request.Approval.ApprovalID)
	return cloneIntent(*intent), nil
}

func (s *Service) Get(commandID string) (commandmodel.CommandIntent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	intent, ok := s.intents[commandID]
	if !ok {
		return commandmodel.CommandIntent{}, ErrCommandNotFound
	}
	return cloneIntent(*intent), nil
}

func (s *Service) PrepareDispatch(commandID, leaseOwner string, leaseUntil time.Time) (commandmodel.DispatchEnvelope, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	intent, ok := s.intents[commandID]
	if !ok {
		return commandmodel.DispatchEnvelope{}, ErrCommandNotFound
	}
	if intent.Status != commandmodel.IntentQueued {
		return commandmodel.DispatchEnvelope{}, ErrCommandNotDispatchable
	}
	now := s.now().UTC()
	if err := validateExecutionGovernance(*intent, now); err != nil {
		return commandmodel.DispatchEnvelope{}, err
	}
	if strings.TrimSpace(leaseOwner) == "" || !leaseUntil.After(now) {
		return commandmodel.DispatchEnvelope{}, ErrInvalidRequest
	}
	s.deviceFence[intent.DeviceID]++
	fence := s.deviceFence[intent.DeviceID]
	attemptID := fmt.Sprintf("%s-attempt-%03d", intent.ID, len(intent.Attempts)+1)
	attempt := commandmodel.CommandAttempt{
		ID:             attemptID,
		CommandID:      intent.ID,
		Status:         commandmodel.AttemptPrepared,
		Version:        1,
		ExecutionFence: fence,
		PayloadHash:    intent.PayloadHash,
		LeaseOwner:     leaseOwner,
		LeaseUntil:     leaseUntil.UTC(),
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	intent.Attempts = append(intent.Attempts, attempt)
	intent.ActiveFence = fence
	transition(intent, commandmodel.IntentDispatching, "DISPATCH_ATTEMPT_PREPARED", leaseOwner, now, attemptID, "")

	return commandmodel.DispatchEnvelope{
		CommandID:             intent.ID,
		AttemptID:             attemptID,
		TenantID:              intent.TenantID,
		SiteID:                intent.SiteID,
		DeviceID:              intent.DeviceID,
		PointID:               intent.PointID,
		Capability:            intent.Capability,
		CapabilityRevision:    intent.CapabilityRevision,
		Parameters:            cloneParameters(intent.Parameters),
		PayloadHash:           intent.PayloadHash,
		ExecutionFence:        fence,
		DeviceCommandSequence: intent.DeviceCommandSequence,
	}, nil
}

func (s *Service) ResolveDispatch(envelope commandmodel.DispatchEnvelope, result commandmodel.ConnectorResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	intent, ok := s.intents[envelope.CommandID]
	if !ok {
		return ErrCommandNotFound
	}
	attemptIndex := -1
	for index := range intent.Attempts {
		if intent.Attempts[index].ID == envelope.AttemptID {
			attemptIndex = index
			break
		}
	}
	if attemptIndex < 0 {
		return ErrAttemptNotFound
	}
	attempt := &intent.Attempts[attemptIndex]
	if envelope.ExecutionFence != intent.ActiveFence || envelope.ExecutionFence != attempt.ExecutionFence {
		return ErrStaleFence
	}
	if envelope.PointID != intent.PointID || envelope.PayloadHash != intent.PayloadHash || envelope.PayloadHash != attempt.PayloadHash {
		return ErrInvalidRequest
	}

	switch result.Phase {
	case commandmodel.ConnectorPreSendRejected, commandmodel.ConnectorRequestCommitted:
	case commandmodel.ConnectorAcknowledged:
		if !result.Acknowledged || result.Verified || strings.TrimSpace(result.EvidenceID) == "" {
			return ErrUnsupportedResult
		}
	default:
		return ErrUnsupportedResult
	}

	now := s.now().UTC()
	attempt.Version++
	attempt.UpdatedAt = now

	switch result.Phase {
	case commandmodel.ConnectorPreSendRejected:
		attempt.Status = commandmodel.AttemptNotSent
		transition(intent, commandmodel.IntentQueued, "PRE_SEND_REJECTED_SAFE_TO_RETRY", "command-dispatcher", now, attempt.ID, result.EvidenceID)
	case commandmodel.ConnectorRequestCommitted:
		attempt.Status = commandmodel.AttemptOutcomeUnknown
		transition(intent, commandmodel.IntentOutcomeUnknown, "REQUEST_COMMITTED_WITHOUT_PROVABLE_OUTCOME", "command-dispatcher", now, attempt.ID, result.EvidenceID)
	case commandmodel.ConnectorAcknowledged:
		attempt.Status = commandmodel.AttemptAcknowledged
		attempt.ConnectorEvidenceID = result.EvidenceID
		attempt.AcknowledgedAt = now
		attempt.VerificationDeadline = now.Add(verificationWindow)
		transition(intent, commandmodel.IntentDispatching, "PROVIDER_ACKNOWLEDGED_AWAITING_REPORTED_STATE", "command-dispatcher", now, attempt.ID, result.EvidenceID)
	default:
		return ErrUnsupportedResult
	}
	return nil
}

func (s *Service) PrepareVerification(commandID, leaseOwner string, leaseUntil time.Time) (commandmodel.VerificationEnvelope, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	intent, ok := s.intents[commandID]
	if !ok {
		return commandmodel.VerificationEnvelope{}, ErrCommandNotFound
	}
	if intent.Status != commandmodel.IntentDispatching || strings.TrimSpace(leaseOwner) == "" {
		return commandmodel.VerificationEnvelope{}, ErrVerificationNotAvailable
	}
	now := s.now().UTC()
	for index := range intent.Attempts {
		attempt := &intent.Attempts[index]
		if attempt.Status != commandmodel.AttemptAcknowledged || attempt.ExecutionFence != intent.ActiveFence {
			continue
		}
		if !attempt.VerificationDeadline.After(now) {
			attempt.Status = commandmodel.AttemptOutcomeUnknown
			attempt.Version++
			attempt.UpdatedAt = now
			transition(intent, commandmodel.IntentOutcomeUnknown, "REPORTED_STATE_VERIFICATION_DEADLINE_EXPIRED", "command-verifier", now, attempt.ID, attempt.ConnectorEvidenceID)
			return commandmodel.VerificationEnvelope{}, ErrVerificationNotAvailable
		}
		if !attempt.VerificationLeaseUntil.IsZero() && attempt.VerificationLeaseUntil.After(now) {
			return commandmodel.VerificationEnvelope{}, ErrVerificationNotAvailable
		}
		if !leaseUntil.After(now) || leaseUntil.After(attempt.VerificationDeadline) {
			return commandmodel.VerificationEnvelope{}, ErrInvalidRequest
		}
		attempt.VerificationLeaseOwner = leaseOwner
		attempt.VerificationLeaseUntil = leaseUntil.UTC()
		attempt.Version++
		attempt.UpdatedAt = now
		return commandmodel.VerificationEnvelope{
			CommandID: intent.ID, AttemptID: attempt.ID, TenantID: intent.TenantID,
			SiteID: intent.SiteID, DeviceID: intent.DeviceID, PointID: intent.PointID, Capability: intent.Capability,
			CapabilityRevision: intent.CapabilityRevision, Parameters: cloneParameters(intent.Parameters), VerificationPointKey: intent.VerificationPointKey,
			PayloadHash: intent.PayloadHash, ExecutionFence: attempt.ExecutionFence,
			BaselineBusinessRevision: intent.SnapshotRevision, AcknowledgedAt: attempt.AcknowledgedAt,
			VerificationDeadline: attempt.VerificationDeadline, LeaseOwner: leaseOwner,
			LeaseUntil: attempt.VerificationLeaseUntil, ConnectorEvidenceID: attempt.ConnectorEvidenceID,
		}, nil
	}
	return commandmodel.VerificationEnvelope{}, ErrVerificationNotAvailable
}

func (s *Service) ResolveVerification(envelope commandmodel.VerificationEnvelope, result commandmodel.VerificationResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	intent, ok := s.intents[envelope.CommandID]
	if !ok {
		return ErrCommandNotFound
	}
	if intent.Status != commandmodel.IntentDispatching || envelope.ExecutionFence != intent.ActiveFence ||
		envelope.PointID != intent.PointID || envelope.PayloadHash != intent.PayloadHash || strings.TrimSpace(envelope.LeaseOwner) == "" || strings.TrimSpace(result.EvidenceID) == "" {
		return ErrStaleFence
	}
	switch result.Outcome {
	case commandmodel.VerificationSucceeded, commandmodel.VerificationInconclusive, commandmodel.VerificationMismatch, commandmodel.VerificationTimedOut:
	default:
		return ErrUnsupportedResult
	}
	attemptIndex := -1
	for index := range intent.Attempts {
		if intent.Attempts[index].ID == envelope.AttemptID {
			attemptIndex = index
			break
		}
	}
	if attemptIndex < 0 {
		return ErrAttemptNotFound
	}
	attempt := &intent.Attempts[attemptIndex]
	if attempt.Status != commandmodel.AttemptAcknowledged || attempt.ExecutionFence != envelope.ExecutionFence ||
		attempt.PayloadHash != envelope.PayloadHash || attempt.VerificationLeaseOwner != envelope.LeaseOwner ||
		attempt.VerificationLeaseUntil != envelope.LeaseUntil || attempt.ConnectorEvidenceID != envelope.ConnectorEvidenceID {
		return ErrStaleFence
	}
	now := s.now().UTC()
	attempt.Version++
	attempt.UpdatedAt = now
	attempt.VerificationEvidenceID = result.EvidenceID
	attempt.VerificationLeaseOwner = ""
	attempt.VerificationLeaseUntil = time.Time{}

	if result.Outcome == commandmodel.VerificationSucceeded && validReportedState(*intent, envelope, result.Reported) {
		attempt.Status = commandmodel.AttemptVerified
		transition(intent, commandmodel.IntentSucceeded, "ACKNOWLEDGED_AND_REPORTED_STATE_VERIFIED", "command-verifier", now, attempt.ID, result.EvidenceID)
		return nil
	}
	switch result.Outcome {
	case commandmodel.VerificationSucceeded, commandmodel.VerificationInconclusive, commandmodel.VerificationMismatch, commandmodel.VerificationTimedOut:
		attempt.Status = commandmodel.AttemptOutcomeUnknown
		transition(intent, commandmodel.IntentOutcomeUnknown, "REPORTED_STATE_VERIFICATION_NOT_PROVEN", "command-verifier", now, attempt.ID, result.EvidenceID)
		return nil
	default:
		return ErrUnsupportedResult
	}
}

func validReportedState(intent commandmodel.CommandIntent, envelope commandmodel.VerificationEnvelope, reported commandmodel.ReportedStateEvidence) bool {
	expected, ok := commandmodel.ExpectedReportedValue(intent.Capability, intent.Parameters)
	profile, supported := commandmodel.CapabilityProfileFor(intent.Capability)
	if !ok || !supported {
		return false
	}
	return reported.TenantID == intent.TenantID && reported.SiteID == intent.SiteID && reported.DeviceID == intent.DeviceID &&
		reported.EvaluationAvailability == "AVAILABLE" && reported.Presence == "ONLINE" && reported.Readiness == "CURRENT" &&
		reported.Freshness == "FRESH" && reported.Quality == "GOOD" &&
		reported.BusinessRevision > envelope.BaselineBusinessRevision && reported.ObservedAt.After(envelope.AcknowledgedAt) &&
		commandmodel.ScalarMatches(reported.ReportedValue, expected, profile.VerificationTolerance)
}

func validateAndHash(request commandmodel.SubmitRequest) (string, error) {
	if strings.TrimSpace(request.TenantID) == "" || strings.TrimSpace(request.SiteID) == "" ||
		strings.TrimSpace(request.DeviceID) == "" || strings.TrimSpace(request.PointID) == "" || strings.TrimSpace(request.PrincipalID) == "" ||
		strings.TrimSpace(request.IdempotencyKey) == "" || strings.TrimSpace(request.VerificationPointKey) == "" {
		return "", ErrInvalidRequest
	}
	profile, ok := commandmodel.CapabilityProfileFor(request.Capability)
	if !ok {
		return "", ErrCapabilityDenied
	}
	parameterCanonical := ""
	if profile.ParameterKey == "" {
		if len(request.Parameters) != 0 {
			return "", ErrCapabilityDenied
		}
	} else {
		value, valid := commandmodel.ParameterValue(request.Capability, request.Parameters)
		if !valid || value < profile.Minimum || value > profile.Maximum {
			return "", ErrCapabilityDenied
		}
		parameterCanonical = profile.ParameterKey + "=" + strconv.FormatFloat(value, 'f', 3, 64)
	}
	state := request.CurrentState
	if state.EvaluationAvailability != "AVAILABLE" || state.Presence != "ONLINE" ||
		state.Readiness != "CURRENT" || state.Quality != "GOOD" || state.BusinessRevision == 0 ||
		state.ObservedAt.IsZero() {
		return "", ErrCurrentStateUnsafe
	}
	if profile.ParameterKey != "" {
		value, _ := commandmodel.ParameterValue(request.Capability, request.Parameters)
		if state.CurrentValue == nil || absolute(value-*state.CurrentValue) > profile.MaximumDelta {
			return "", ErrCapabilityDenied
		}
	}

	canonical := strings.Join([]string{
		request.TenantID,
		request.SiteID,
		request.DeviceID,
		request.PointID,
		string(request.Capability),
		parameterCanonical,
		request.VerificationPointKey,
		profile.Revision,
	}, "|")
	digest := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(digest[:]), nil
}

func transition(intent *commandmodel.CommandIntent, to commandmodel.IntentStatus, reason, actor string, at time.Time, causation, evidenceID string) {
	from := intent.Status
	intent.Status = to
	intent.Version++
	intent.UpdatedAt = at
	appendTransition(intent, from, to, reason, actor, at, causation, evidenceID)
}

func appendTransition(intent *commandmodel.CommandIntent, from, to commandmodel.IntentStatus, reason, actor string, at time.Time, causation, evidenceID string) {
	intent.Transitions = append(intent.Transitions, commandmodel.Transition{
		From:       from,
		To:         to,
		Reason:     reason,
		Actor:      actor,
		At:         at,
		Version:    intent.Version,
		Causation:  causation,
		EvidenceID: evidenceID,
	})
}

func cloneIntent(intent commandmodel.CommandIntent) commandmodel.CommandIntent {
	intent.Parameters = cloneParameters(intent.Parameters)
	intent.Transitions = append([]commandmodel.Transition(nil), intent.Transitions...)
	intent.Attempts = append([]commandmodel.CommandAttempt(nil), intent.Attempts...)
	intent.Approvals = append([]commandmodel.ApprovalEvidence(nil), intent.Approvals...)
	intent.Authorizations = append([]commandmodel.AuthorizationSnapshot(nil), intent.Authorizations...)
	intent.RiskSnapshot.Reasons = append([]string(nil), intent.RiskSnapshot.Reasons...)
	return intent
}

func cloneParameters(parameters commandmodel.CommandParameters) commandmodel.CommandParameters {
	if len(parameters) == 0 {
		return commandmodel.CommandParameters{}
	}
	cloned := make(commandmodel.CommandParameters, len(parameters))
	for key, value := range parameters {
		cloned[key] = value
	}
	return cloned
}

func absolute(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
}
