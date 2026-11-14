package commanddispatcher

import (
	"context"
	"errors"
	"strings"

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
)

type DispatchSafetyResult struct {
	Safe        bool
	EvidenceID  string
	FailureCode string
}

type DispatchSafetyVerifier interface {
	VerifyBeforeDispatch(context.Context, commandmodel.DispatchEnvelope) (DispatchSafetyResult, error)
}

// DispatchSafetyStateKey is the Point Code every controllable Device reports its running
// state with; dispatch requires it to be current before anything is sent.
const DispatchSafetyStateKey = "run_state"

type DispatchSafetyStateReader interface {
	ReadReportedState(context.Context, commandmodel.VerificationEnvelope) (string, commandmodel.ReportedStateEvidence, error)
}

type InterlockRulesProvider interface {
	ResolveCommandInterlocks(context.Context, commandmodel.DispatchEnvelope) ([]commandmodel.InterlockCheck, error)
}

type authoritativeDispatchSafetyVerifier struct {
	reader     DispatchSafetyStateReader
	key        string
	interlocks InterlockRulesProvider
}

func NewAuthoritativeDispatchSafetyVerifier(reader DispatchSafetyStateReader, reportedStateKey string) (DispatchSafetyVerifier, error) {
	return NewAuthoritativeDispatchSafetyVerifierWithInterlocks(reader, reportedStateKey, nil)
}

func NewAuthoritativeDispatchSafetyVerifierWithInterlocks(reader DispatchSafetyStateReader, reportedStateKey string, interlocks InterlockRulesProvider) (DispatchSafetyVerifier, error) {
	if reader == nil || strings.TrimSpace(reportedStateKey) == "" {
		return nil, errors.New("dispatch safety verifier requires an authoritative state reader and key")
	}
	return &authoritativeDispatchSafetyVerifier{
		reader:     reader,
		key:        strings.TrimSpace(reportedStateKey),
		interlocks: interlocks,
	}, nil
}

func (verifier *authoritativeDispatchSafetyVerifier) VerifyBeforeDispatch(ctx context.Context, envelope commandmodel.DispatchEnvelope) (DispatchSafetyResult, error) {
	if verifier == nil || verifier.reader == nil || strings.TrimSpace(verifier.key) == "" {
		return DispatchSafetyResult{}, errors.New("dispatch safety verifier is unavailable")
	}
	result, _, err := verifyBeforeDispatchWithKey(ctx, verifier.reader, envelope, verifier.key)
	if err != nil || !result.Safe || verifier.interlocks == nil {
		return result, err
	}
	checks, err := verifier.interlocks.ResolveCommandInterlocks(ctx, envelope)
	if err != nil {
		return DispatchSafetyResult{}, err
	}
	for _, check := range checks {
		peerEnvelope := envelope
		peerEnvelope.DeviceID = check.DeviceID
		peerResult, peerEvidence, peerErr := verifyBeforeDispatchWithKey(ctx, verifier.reader, peerEnvelope, check.Key)
		if peerErr != nil {
			return DispatchSafetyResult{}, peerErr
		}
		if !peerResult.Safe {
			return DispatchSafetyResult{
				EvidenceID:  peerResult.EvidenceID,
				FailureCode: "DISPATCH_SAFETY_INTERLOCK_STATE_UNAVAILABLE",
			}, nil
		}
		if !commandmodel.ScalarMatches(peerEvidence.ReportedValue, commandmodel.TextScalar(check.Expected), 0) {
			return DispatchSafetyResult{
				EvidenceID:  peerResult.EvidenceID,
				FailureCode: "DISPATCH_SAFETY_INTERLOCK_VIOLATED",
			}, nil
		}
	}
	return result, nil
}

func verifyBeforeDispatchWithKey(ctx context.Context, reader DispatchSafetyStateReader, envelope commandmodel.DispatchEnvelope, key string) (DispatchSafetyResult, commandmodel.ReportedStateEvidence, error) {
	evidenceID, evidence, err := reader.ReadReportedState(ctx, commandmodel.VerificationEnvelope{
		CommandID: envelope.CommandID, AttemptID: envelope.AttemptID,
		TenantID: envelope.TenantID, SiteID: envelope.SiteID, DeviceID: envelope.DeviceID,
		PointID: envelope.PointID, Capability: envelope.Capability, CapabilityRevision: envelope.CapabilityRevision,
		Parameters: envelope.Parameters, VerificationPointKey: key, PayloadHash: envelope.PayloadHash,
		ExecutionFence: envelope.ExecutionFence, LeaseOwner: envelope.LeaseOwner, LeaseUntil: envelope.LeaseUntil,
	})
	if err != nil {
		return DispatchSafetyResult{}, commandmodel.ReportedStateEvidence{}, err
	}
	result := DispatchSafetyResult{EvidenceID: evidenceID}
	switch {
	case evidence.EvaluationAvailability != "AVAILABLE":
		result.FailureCode = "DISPATCH_SAFETY_STATE_UNAVAILABLE"
	case evidence.Presence != "ONLINE":
		result.FailureCode = "DISPATCH_SAFETY_DEVICE_NOT_ONLINE"
	case evidence.Readiness != "CURRENT":
		result.FailureCode = "DISPATCH_SAFETY_STATE_NOT_CURRENT"
	case evidence.Freshness != "FRESH":
		result.FailureCode = "DISPATCH_SAFETY_STATE_NOT_FRESH"
	case evidence.Quality != "GOOD":
		result.FailureCode = "DISPATCH_SAFETY_STATE_QUALITY_UNSAFE"
	case evidence.ObservedAt.IsZero():
		result.FailureCode = "DISPATCH_SAFETY_STATE_TIMESTAMP_MISSING"
	default:
		result.Safe = true
	}
	return result, evidence, nil
}
