package commandservice

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
)

var (
	ErrInvalidRequest            = errors.New("invalid command request")
	ErrCapabilityDenied          = errors.New("capability is not permitted")
	ErrCurrentStateUnsafe        = errors.New("current telemetry state is unsafe for control")
	ErrIdempotencyConflict       = errors.New("idempotency key was reused with a different payload")
	ErrCommandNotFound           = errors.New("command not found")
	ErrCommandNotDispatchable    = errors.New("command is not dispatchable")
	ErrAttemptNotFound           = errors.New("command attempt not found")
	ErrStaleFence                = errors.New("execution fence is stale")
	ErrUnsupportedResult         = errors.New("unsupported connector result")
	ErrVerificationNotAvailable  = errors.New("no acknowledged command is available for verification")
	ErrCapabilityProfileConflict = errors.New("capability profile belongs to another owner")
)

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

	nextCommandID   uint64
	intents         map[string]*commandmodel.CommandIntent
	idempotency     map[string]idempotencyRecord
	deviceSequence  map[string]uint64
	deviceFence     map[string]uint64
	priorityLocks   map[string]*commandmodel.DevicePriorityLock
	failureNotifier ControlFailureNotifier
	failureQueue    controlFailureQueue
}

// Close drains queued control failure notifications before returning.
func (s *Service) Close() {
	if s == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), controlFailureCloseTimeout)
	defer cancel()
	_ = s.failureQueue.Close(ctx)
}

// ControlFailureEvent describes an operational control failure that needs immediate operator visibility.
type ControlFailureEvent struct {
	Kind       string
	TenantID   string
	SiteID     string
	DeviceID   string
	PointID    string
	CommandID  string
	Capability commandmodel.Capability
	ReasonCode string
	Detail     string
	OccurredAt time.Time
	ActorType  string
	ActorID    string
}

// ControlFailureNotifier asynchronously receives operational control failure events without delaying state settlement.
type ControlFailureNotifier func(context.Context, ControlFailureEvent)

// SetControlFailureNotifier registers an optional non-blocking notifier for OUTCOME_UNKNOWN and unconfirmed-power settlements.
func (s *Service) SetControlFailureNotifier(notifier ControlFailureNotifier) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failureNotifier = notifier
}

func (s *Service) hasControlFailureNotifier() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.failureNotifier != nil
}

func controlFailureEventFromDecision(
	intent commandmodel.CommandIntent,
	status commandmodel.IntentStatus,
	reason string,
	detail string,
	occurredAt time.Time,
) (ControlFailureEvent, bool) {
	switch {
	case status == commandmodel.IntentOutcomeUnknown:
		return ControlFailureEvent{
			Kind:       string(commandmodel.IntentOutcomeUnknown),
			TenantID:   intent.TenantID,
			SiteID:     intent.SiteID,
			DeviceID:   intent.DeviceID,
			PointID:    intent.PointID,
			CommandID:  intent.ID,
			Capability: intent.Capability,
			ReasonCode: reason,
			Detail:     strings.TrimSpace(detail),
			OccurredAt: occurredAt,
		}, true
	case reason == commandmodel.ReasonPulseDeliveredPowerUnconfirmed:
		return ControlFailureEvent{
			Kind:       commandmodel.ReasonPulseDeliveredPowerUnconfirmed,
			TenantID:   intent.TenantID,
			SiteID:     intent.SiteID,
			DeviceID:   intent.DeviceID,
			PointID:    intent.PointID,
			CommandID:  intent.ID,
			Capability: intent.Capability,
			ReasonCode: reason,
			Detail:     strings.TrimSpace(detail),
			OccurredAt: occurredAt,
		}, true
	default:
		return ControlFailureEvent{}, false
	}
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
		priorityLocks:  make(map[string]*commandmodel.DevicePriorityLock),
	}
}

func (s *Service) Submit(request commandmodel.SubmitRequest) (SubmitResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	payloadHash, priorityTier, commandSource, err := validateAndHash(request)
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

	deviceIntents := s.deviceIntentsLocked(request.TenantID, request.DeviceID)
	activeStatuses := make([]commandmodel.IntentPriorityState, 0, len(deviceIntents))
	for _, existing := range deviceIntents {
		if IsBlockingEarlierIntentStatus(existing.Status) {
			activeStatuses = append(activeStatuses, commandmodel.IntentPriorityState{
				Status:       existing.Status,
				PriorityTier: existing.PriorityTier,
			})
		}
	}
	activeLock := s.activePriorityLockLocked(request.TenantID, request.DeviceID, now)
	if PriorityLockBlocking(priorityTier, activeLock, now) || ActiveHigherPriorityBlocking(priorityTier, activeStatuses) {
		return SubmitResult{}, ErrDevicePriorityLocked
	}
	if priorityTier == commandmodel.PrioritySafetyInterlock {
		for _, existing := range deviceIntents {
			if ShouldPreemptOnSafetyInterlock(priorityTier, existing.Status, existing.PriorityTier) {
				transition(existing, commandmodel.IntentCancelled, "PRIORITY_PREEMPTED", request.PrincipalID, now, request.Authorization.GrantID, request.Authorization.GrantID)
			}
		}
	}

	s.nextCommandID++
	commandID := fmt.Sprintf("018f3e00-0000-7000-8000-%012d", s.nextCommandID)
	s.deviceSequence[request.DeviceID]++

	capabilityRevision, _, _, _ := commandCapabilityProfile(request.Capability)
	preconditionMode, bypassReason := normalizedPrecondition(request)
	intent := &commandmodel.CommandIntent{
		ID:                    commandID,
		TenantID:              request.TenantID,
		SiteID:                request.SiteID,
		DeviceID:              request.DeviceID,
		PointID:               request.PointID,
		PrincipalID:           request.PrincipalID,
		IdempotencyKey:        request.IdempotencyKey,
		TraceID:               commandmodel.NormalizeTraceID(request.TraceID),
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
		PreconditionMode:      preconditionMode,
		BypassReason:          bypassReason,
		PriorityTier:          priorityTier,
		CommandSource:         commandSource,
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

// Cancel transitions an AWAITING_APPROVAL or QUEUED command to CANCELLED before dispatch starts.
func (s *Service) Cancel(request commandmodel.CancelRequest) (commandmodel.CommandIntent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	intent, ok := s.intents[request.CommandID]
	if !ok || intent.TenantID != request.TenantID {
		return commandmodel.CommandIntent{}, ErrCommandNotFound
	}
	now := s.now().UTC()
	decision, err := DecideCommandCancel(*intent, request, now)
	if err != nil {
		return commandmodel.CommandIntent{}, err
	}
	transition(intent, decision.IntentFinal, decision.Reason, request.Authorization.PrincipalID, now, request.Authorization.GrantID, request.Authorization.GrantID)
	return cloneIntent(*intent), nil
}

// CancelPending cancels all AWAITING_APPROVAL or QUEUED commands for a site (and optional device IDs)
// and, when AbortActiveSequences is set, cleanly fails ACKNOWLEDGED unit-sequence and emergency DO release commands.
func (s *Service) CancelPending(request commandmodel.CancelPendingRequest) ([]string, error) {
	if strings.TrimSpace(request.TenantID) == "" || strings.TrimSpace(request.SiteID) == "" || strings.TrimSpace(request.PrincipalID) == "" {
		return nil, ErrInvalidRequest
	}
	reason := strings.TrimSpace(request.Reason)
	if reason == "" {
		reason = "CONTROL_EXIT_CANCELLED"
	}
	causationID := strings.TrimSpace(request.CausationID)
	if causationID == "" {
		causationID = reason
	}
	var deviceFilter map[string]bool
	if len(request.DeviceIDs) > 0 {
		deviceFilter = make(map[string]bool, len(request.DeviceIDs))
		for _, id := range request.DeviceIDs {
			deviceFilter[id] = true
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now().UTC()
	var scoped []*commandmodel.CommandIntent
	for _, intent := range s.intents {
		if intent.TenantID != request.TenantID || intent.SiteID != request.SiteID {
			continue
		}
		if deviceFilter != nil && !deviceFilter[intent.DeviceID] {
			continue
		}
		scoped = append(scoped, intent)
	}
	sort.Slice(scoped, func(i, j int) bool {
		if scoped[i].DeviceCommandSequence != scoped[j].DeviceCommandSequence {
			return scoped[i].DeviceCommandSequence < scoped[j].DeviceCommandSequence
		}
		return scoped[i].ID < scoped[j].ID
	})

	var cancelledIDs []string
	for _, intent := range scoped {
		if intent.Status != commandmodel.IntentAwaitingApproval && intent.Status != commandmodel.IntentQueued {
			continue
		}
		transition(intent, commandmodel.IntentCancelled, reason, request.PrincipalID, now, causationID, causationID)
		cancelledIDs = append(cancelledIDs, intent.ID)
	}
	if !request.AbortActiveSequences {
		return cancelledIDs, nil
	}
	for _, intent := range scoped {
		if intent.Status != commandmodel.IntentDispatching {
			continue
		}
		for index := range intent.Attempts {
			attempt := &intent.Attempts[index]
			if attempt.ExecutionFence != intent.ActiveFence || attempt.Status != commandmodel.AttemptAcknowledged {
				continue
			}
			if !IsActiveAbortableSequence(intent.Capability, attempt.EdgeExecution) {
				break
			}
			attempt.Status = commandmodel.AttemptFailed
			attempt.Version++
			attempt.VerificationLeaseOwner = ""
			attempt.VerificationLeaseUntil = time.Time{}
			attempt.UpdatedAt = now
			transition(intent, commandmodel.IntentFailed, "ACTIVE_SEQUENCE_ABORTED", request.PrincipalID, now, causationID, causationID)
			cancelledIDs = append(cancelledIDs, intent.ID)
			break
		}
	}
	return cancelledIDs, nil
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
	activeLock := s.activePriorityLockLocked(intent.TenantID, intent.DeviceID, now)
	if PriorityLockBlocking(intent.PriorityTier, activeLock, now) {
		return commandmodel.DispatchEnvelope{}, ErrDevicePriorityLocked
	}
	peers := s.queuedPeerStatesLocked(intent.TenantID, intent.DeviceID, intent.ID)
	activeStatuses := make([]commandmodel.IntentPriorityState, 0, len(peers))
	for _, peer := range peers {
		activeStatuses = append(activeStatuses, commandmodel.IntentPriorityState{
			Status:       peer.Status,
			PriorityTier: peer.PriorityTier,
		})
	}
	if ActiveHigherPriorityBlocking(intent.PriorityTier, activeStatuses) {
		return commandmodel.DispatchEnvelope{}, ErrDevicePriorityLocked
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
		TraceID:               commandmodel.NormalizeTraceID(intent.TraceID),
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
	if attempt.Status != commandmodel.AttemptPrepared || intent.Status != commandmodel.IntentDispatching ||
		envelope.ExecutionFence != intent.ActiveFence || envelope.ExecutionFence != attempt.ExecutionFence {
		return ErrStaleFence
	}
	if envelope.PointID != intent.PointID || envelope.PayloadHash != intent.PayloadHash || envelope.PayloadHash != attempt.PayloadHash {
		return ErrInvalidRequest
	}

	switch result.Phase {
	case commandmodel.ConnectorPreSendRejected, commandmodel.ConnectorExecutionRejected, commandmodel.ConnectorRequestCommitted:
		if result.EdgeExecution != nil && !result.EdgeExecution.Valid() {
			return ErrUnsupportedResult
		}
	case commandmodel.ConnectorAcknowledged:
		if !result.Acknowledged || result.Verified || strings.TrimSpace(result.EvidenceID) == "" || result.EdgeExecution == nil || !result.EdgeExecution.ValidExecuted() {
			return ErrUnsupportedResult
		}
	default:
		return ErrUnsupportedResult
	}

	now := s.now().UTC()
	decision, err := DecideDispatchResolution(intent.Capability, result, now)
	if err != nil {
		return err
	}
	attempt.Version++
	attempt.UpdatedAt = now
	attempt.Status = decision.AttemptFinal
	if result.Phase == commandmodel.ConnectorAcknowledged {
		attempt.ConnectorEvidenceID = result.EvidenceID
		attempt.EdgeExecution = cloneEdgeExecutionEvidence(result.EdgeExecution)
		attempt.AcknowledgedAt = *decision.AcknowledgedAt
		attempt.VerificationDeadline = *decision.VerificationDeadline
	}
	transition(intent, decision.IntentFinal, decision.Reason, "command-dispatcher", now, attempt.ID, result.EvidenceID)
	if event, notify := controlFailureEventFromDecision(*intent, decision.IntentFinal, decision.Reason, result.FailureCode, now); notify {
		s.failureQueue.enqueue(s.failureNotifier, event)
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
			decision, err := DecideVerificationResolutionWithEdge(
				*intent,
				commandmodel.VerificationEnvelope{EdgeExecution: attempt.EdgeExecution},
				attempt.EdgeExecution,
				commandmodel.VerificationResult{Outcome: commandmodel.VerificationTimedOut},
				attempt.VerificationDeadline,
				now,
			)
			if err != nil {
				return commandmodel.VerificationEnvelope{}, err
			}
			attempt.Status = decision.AttemptFinal
			attempt.Version++
			attempt.UpdatedAt = now
			transition(intent, decision.IntentFinal, decision.Reason, "command-verifier", now, attempt.ID, attempt.ConnectorEvidenceID)
			if event, notify := controlFailureEventFromDecision(*intent, decision.IntentFinal, decision.Reason, "", now); notify {
				s.failureQueue.enqueue(s.failureNotifier, event)
			}
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
			EdgeExecution: cloneEdgeExecutionEvidence(attempt.EdgeExecution),
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
		attempt.VerificationLeaseUntil != envelope.LeaseUntil || !attempt.VerificationDeadline.Equal(envelope.VerificationDeadline) ||
		attempt.ConnectorEvidenceID != envelope.ConnectorEvidenceID {
		return ErrStaleFence
	}
	now := s.now().UTC()
	if envelope.EdgeExecution == nil && attempt.EdgeExecution != nil {
		envelope.EdgeExecution = cloneEdgeExecutionEvidence(attempt.EdgeExecution)
	}
	decision, err := DecideVerificationResolutionWithEdge(*intent, envelope, envelope.EdgeExecution, result, attempt.VerificationDeadline, now)
	if err != nil {
		return err
	}
	attempt.Version++
	attempt.UpdatedAt = now
	attempt.VerificationEvidenceID = result.EvidenceID
	attempt.VerificationLeaseOwner = ""
	attempt.VerificationLeaseUntil = time.Time{}
	attempt.Status = decision.AttemptFinal
	transition(intent, decision.IntentFinal, decision.Reason, "command-verifier", now, attempt.ID, result.EvidenceID)
	if event, notify := controlFailureEventFromDecision(*intent, decision.IntentFinal, decision.Reason, result.FailureCode, now); notify {
		s.failureQueue.enqueue(s.failureNotifier, event)
	}
	return nil
}

func validReportedState(intent commandmodel.CommandIntent, envelope commandmodel.VerificationEnvelope, reported commandmodel.ReportedStateEvidence) bool {
	expected, ok := commandmodel.ExpectedVerificationValue(intent.Capability, intent.Parameters, envelope.EdgeExecution)
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

func validateAndHash(request commandmodel.SubmitRequest) (string, commandmodel.PriorityTier, commandmodel.CommandSource, error) {
	if strings.TrimSpace(request.TenantID) == "" || strings.TrimSpace(request.SiteID) == "" ||
		strings.TrimSpace(request.DeviceID) == "" || strings.TrimSpace(request.PointID) == "" || strings.TrimSpace(request.PrincipalID) == "" ||
		strings.TrimSpace(request.IdempotencyKey) == "" || strings.TrimSpace(request.VerificationPointKey) == "" {
		return "", "", "", ErrInvalidRequest
	}
	priorityTier, commandSource, err := commandmodel.ResolveCommandPriority(request)
	if err != nil {
		return "", "", "", ErrInvalidRequest
	}
	profile, ok := commandmodel.CapabilityProfileFor(request.Capability)
	if !ok || !commandmodel.ParametersValid(request.Capability, request.Parameters) {
		return "", "", "", ErrCapabilityDenied
	}
	preconditionMode, bypassReason, err := validatePrecondition(request, profile)
	if err != nil {
		return "", "", "", err
	}
	parameterCanonical := ""
	if profile.ParameterKey != "" {
		value, _ := commandmodel.ParameterValue(request.Capability, request.Parameters)
		parameterCanonical = profile.ParameterKey + "=" + strconv.FormatFloat(value, 'f', 3, 64)
	}
	if preconditionMode == commandmodel.PreconditionRequireCurrentState {
		state := request.CurrentState
		if state.EvaluationAvailability != "AVAILABLE" || state.Presence != "ONLINE" ||
			state.Readiness != "CURRENT" || state.Quality != "GOOD" || state.BusinessRevision == 0 ||
			state.ObservedAt.IsZero() {
			return "", "", "", ErrCurrentStateUnsafe
		}
		if profile.ParameterKey != "" {
			value, _ := commandmodel.ParameterValue(request.Capability, request.Parameters)
			if state.CurrentValue == nil || absolute(value-*state.CurrentValue) > profile.MaximumDelta {
				return "", "", "", ErrCapabilityDenied
			}
		}
	}

	canonicalParts := []string{
		request.TenantID,
		request.SiteID,
		request.DeviceID,
		request.PointID,
		string(request.Capability),
		parameterCanonical,
		request.VerificationPointKey,
		profile.Revision,
		string(priorityTier),
		string(commandSource),
	}
	if preconditionMode == commandmodel.PreconditionEmergencyBypass {
		canonicalParts = append(canonicalParts, string(preconditionMode), bypassReason)
	}
	canonical := strings.Join(canonicalParts, "|")
	digest := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(digest[:]), priorityTier, commandSource, nil
}

func validatePrecondition(request commandmodel.SubmitRequest, profile commandmodel.CapabilityProfile) (commandmodel.PreconditionMode, string, error) {
	mode := request.PreconditionMode
	if mode == "" {
		mode = commandmodel.PreconditionRequireCurrentState
	}
	reason := strings.TrimSpace(request.BypassReason)
	switch mode {
	case commandmodel.PreconditionRequireCurrentState:
		return mode, "", nil
	case commandmodel.PreconditionEmergencyBypass:
		if !profile.SupportsEmergencyBypass {
			return "", "", ErrCapabilityDenied
		}
		if reason == "" {
			return "", "", ErrInvalidRequest
		}
		return mode, reason, nil
	default:
		return "", "", ErrInvalidRequest
	}
}

func normalizedPrecondition(request commandmodel.SubmitRequest) (commandmodel.PreconditionMode, string) {
	if request.PreconditionMode == commandmodel.PreconditionEmergencyBypass {
		return commandmodel.PreconditionEmergencyBypass, strings.TrimSpace(request.BypassReason)
	}
	return commandmodel.PreconditionRequireCurrentState, ""
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
	for index := range intent.Attempts {
		intent.Attempts[index].EdgeExecution = cloneEdgeExecutionEvidence(intent.Attempts[index].EdgeExecution)
	}
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

func cloneEdgeExecutionEvidence(evidence *commandmodel.EdgeExecutionEvidence) *commandmodel.EdgeExecutionEvidence {
	if evidence == nil {
		return nil
	}
	cloned := *evidence
	cloned.Requested = cloneScalarValue(evidence.Requested)
	if evidence.Effective != nil {
		value := cloneScalarValue(*evidence.Effective)
		cloned.Effective = &value
	}
	if evidence.Applied != nil {
		value := cloneScalarValue(*evidence.Applied)
		cloned.Applied = &value
	}
	cloned.Constraints = append([]commandmodel.EdgeConstraintEvidence(nil), evidence.Constraints...)
	return &cloned
}

func cloneScalarValue(value commandmodel.ScalarValue) commandmodel.ScalarValue {
	cloned := commandmodel.ScalarValue{}
	if value.Number != nil {
		number := *value.Number
		cloned.Number = &number
	}
	if value.Text != nil {
		text := *value.Text
		cloned.Text = &text
	}
	if value.Boolean != nil {
		boolean := *value.Boolean
		cloned.Boolean = &boolean
	}
	return cloned
}

func absolute(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
}

func priorityLockKey(tenantID, deviceID string) string {
	return tenantID + "\x00" + deviceID
}

func normalizeAndValidatePriorityLock(lock commandmodel.DevicePriorityLock, now time.Time) (commandmodel.DevicePriorityLock, error) {
	lock.TenantID = strings.TrimSpace(lock.TenantID)
	lock.SiteID = strings.TrimSpace(lock.SiteID)
	lock.DeviceID = strings.TrimSpace(lock.DeviceID)
	lock.LockedBy = strings.TrimSpace(lock.LockedBy)
	lock.Reason = strings.TrimSpace(lock.Reason)
	lock.LockedPriority = commandmodel.PriorityTier(strings.TrimSpace(string(lock.LockedPriority)))
	lock.CommandSource = commandmodel.CommandSource(strings.TrimSpace(string(lock.CommandSource)))
	if lock.TenantID == "" || lock.SiteID == "" || lock.DeviceID == "" ||
		lock.LockedBy == "" || lock.Reason == "" || !lock.LockedPriority.Valid() || !lock.LockedUntil.After(now) {
		return commandmodel.DevicePriorityLock{}, ErrInvalidRequest
	}
	if lock.CommandSource == "" {
		lock.CommandSource = commandmodel.DefaultCommandSourceForPriority(lock.LockedPriority)
	}
	if !commandmodel.PrioritySourceCompatible(lock.LockedPriority, lock.CommandSource) {
		return commandmodel.DevicePriorityLock{}, ErrInvalidRequest
	}
	lock.LockedUntil = lock.LockedUntil.UTC()
	return lock, nil
}

// AcquireDevicePriorityLock installs or refreshes a device-level priority lock at now.
func (s *Service) AcquireDevicePriorityLock(lock commandmodel.DevicePriorityLock) (commandmodel.DevicePriorityLock, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now().UTC()
	normalized, err := normalizeAndValidatePriorityLock(lock, now)
	if err != nil {
		return commandmodel.DevicePriorityLock{}, err
	}
	existing := s.activePriorityLockLocked(normalized.TenantID, normalized.DeviceID, now)
	if existing != nil && commandmodel.PriorityRank(existing.LockedPriority) > commandmodel.PriorityRank(normalized.LockedPriority) {
		return commandmodel.DevicePriorityLock{}, ErrDevicePriorityLocked
	}
	key := priorityLockKey(normalized.TenantID, normalized.DeviceID)
	if raw, ok := s.priorityLocks[key]; ok && !raw.CreatedAt.IsZero() {
		normalized.CreatedAt = raw.CreatedAt
	} else {
		normalized.CreatedAt = now
	}
	normalized.UpdatedAt = now
	stored := normalized
	s.priorityLocks[key] = &stored
	return normalized, nil
}

// ReleaseDevicePriorityLock clears any priority lock held on (tenantID, deviceID).
func (s *Service) ReleaseDevicePriorityLock(tenantID, deviceID string) error {
	tenantID = strings.TrimSpace(tenantID)
	deviceID = strings.TrimSpace(deviceID)
	if tenantID == "" || deviceID == "" {
		return ErrInvalidRequest
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.priorityLocks, priorityLockKey(tenantID, deviceID))
	return nil
}

// GetDevicePriorityLock returns the active priority lock on (tenantID, deviceID), or nil when none or expired.
func (s *Service) GetDevicePriorityLock(tenantID, deviceID string) (*commandmodel.DevicePriorityLock, error) {
	tenantID = strings.TrimSpace(tenantID)
	deviceID = strings.TrimSpace(deviceID)
	if tenantID == "" || deviceID == "" {
		return nil, ErrInvalidRequest
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.activePriorityLockLocked(tenantID, deviceID, s.now().UTC()), nil
}

// RejectPriorityLockedQueued transitions QUEUED commands invalidated by a higher-priority device lock to REJECTED.
func (s *Service) RejectPriorityLockedQueued(tenantID string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now().UTC()
	var queued []*commandmodel.CommandIntent
	for _, intent := range s.intents {
		if intent.TenantID == tenantID && intent.Status == commandmodel.IntentQueued {
			queued = append(queued, intent)
		}
	}
	sort.Slice(queued, func(i, j int) bool {
		if queued[i].DeviceCommandSequence != queued[j].DeviceCommandSequence {
			return queued[i].DeviceCommandSequence < queued[j].DeviceCommandSequence
		}
		return queued[i].ID < queued[j].ID
	})
	var rejected []string
	for _, intent := range queued {
		lock := s.activePriorityLockLocked(intent.TenantID, intent.DeviceID, now)
		if !PriorityLockBlocking(intent.PriorityTier, lock, now) {
			continue
		}
		transition(intent, commandmodel.IntentRejected, "DEVICE_PRIORITY_LOCKED", "command-service", now, lock.LockedBy, lock.LockedBy)
		rejected = append(rejected, intent.ID)
	}
	return rejected, nil
}

func (s *Service) activePriorityLockLocked(tenantID, deviceID string, now time.Time) *commandmodel.DevicePriorityLock {
	lock, ok := s.priorityLocks[priorityLockKey(tenantID, deviceID)]
	if !ok || lock == nil || !lock.LockedUntil.After(now) {
		return nil
	}
	cloned := *lock
	return &cloned
}

func (s *Service) deviceIntentsLocked(tenantID, deviceID string) []*commandmodel.CommandIntent {
	var matching []*commandmodel.CommandIntent
	for _, intent := range s.intents {
		if intent.TenantID == tenantID && intent.DeviceID == deviceID {
			matching = append(matching, intent)
		}
	}
	sort.Slice(matching, func(i, j int) bool {
		if matching[i].DeviceCommandSequence != matching[j].DeviceCommandSequence {
			return matching[i].DeviceCommandSequence < matching[j].DeviceCommandSequence
		}
		return matching[i].ID < matching[j].ID
	})
	return matching
}

func (s *Service) queuedPeerStatesLocked(tenantID, deviceID, excludeCommandID string) []QueuedPeerState {
	intents := s.deviceIntentsLocked(tenantID, deviceID)
	peers := make([]QueuedPeerState, 0, len(intents))
	for _, intent := range intents {
		if intent.ID == excludeCommandID {
			continue
		}
		peers = append(peers, QueuedPeerState{
			Status:                intent.Status,
			PriorityTier:          intent.PriorityTier,
			DeviceCommandSequence: intent.DeviceCommandSequence,
		})
	}
	return peers
}

func (s *Service) canClaimQueuedIntent(commandID string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	intent, ok := s.intents[commandID]
	if !ok {
		return false
	}
	activeLock := s.activePriorityLockLocked(intent.TenantID, intent.DeviceID, now.UTC())
	peers := s.queuedPeerStatesLocked(intent.TenantID, intent.DeviceID, intent.ID)
	return CanClaimQueuedIntentWithPriority(*intent, false, activeLock, peers, now.UTC())
}
