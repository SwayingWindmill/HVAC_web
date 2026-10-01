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

type DispatchSafetyStateReader interface {
	ReadReportedState(context.Context, commandmodel.VerificationEnvelope) (string, commandmodel.ReportedStateEvidence, error)
}

type authoritativeDispatchSafetyVerifier struct {
	reader DispatchSafetyStateReader
	key    string
}

type mappedDispatchSafetyVerifier struct {
	reader      DispatchSafetyStateReader
	keyByDevice map[string]string
}

func NewAuthoritativeDispatchSafetyVerifier(reader DispatchSafetyStateReader, reportedStateKey string) (DispatchSafetyVerifier, error) {
	if reader == nil || strings.TrimSpace(reportedStateKey) == "" {
		return nil, errors.New("dispatch safety verifier requires an authoritative state reader and key")
	}
	return &authoritativeDispatchSafetyVerifier{reader: reader, key: strings.TrimSpace(reportedStateKey)}, nil
}

func NewMappedDispatchSafetyVerifier(reader DispatchSafetyStateReader, keyByDevice map[string]string) (DispatchSafetyVerifier, error) {
	if reader == nil || len(keyByDevice) == 0 {
		return nil, errors.New("dispatch safety verifier requires authoritative state keys")
	}
	resolved := make(map[string]string, len(keyByDevice))
	for deviceID, key := range keyByDevice {
		deviceID = strings.TrimSpace(deviceID)
		key = strings.TrimSpace(key)
		if deviceID == "" || key == "" {
			return nil, errors.New("dispatch safety verifier contains an invalid device key")
		}
		resolved[deviceID] = key
	}
	return &mappedDispatchSafetyVerifier{reader: reader, keyByDevice: resolved}, nil
}

func (verifier *authoritativeDispatchSafetyVerifier) VerifyBeforeDispatch(ctx context.Context, envelope commandmodel.DispatchEnvelope) (DispatchSafetyResult, error) {
	if verifier == nil || verifier.reader == nil || strings.TrimSpace(verifier.key) == "" {
		return DispatchSafetyResult{}, errors.New("dispatch safety verifier is unavailable")
	}
	return verifyBeforeDispatchWithKey(ctx, verifier.reader, envelope, verifier.key)
}

func (verifier *mappedDispatchSafetyVerifier) VerifyBeforeDispatch(ctx context.Context, envelope commandmodel.DispatchEnvelope) (DispatchSafetyResult, error) {
	if verifier == nil || verifier.reader == nil {
		return DispatchSafetyResult{}, errors.New("dispatch safety verifier is unavailable")
	}
	key := strings.TrimSpace(verifier.keyByDevice[envelope.DeviceID])
	if key == "" {
		return DispatchSafetyResult{}, errors.New("dispatch safety verifier has no state key for device")
	}
	return verifyBeforeDispatchWithKey(ctx, verifier.reader, envelope, key)
}

func verifyBeforeDispatchWithKey(ctx context.Context, reader DispatchSafetyStateReader, envelope commandmodel.DispatchEnvelope, key string) (DispatchSafetyResult, error) {
	evidenceID, evidence, err := reader.ReadReportedState(ctx, commandmodel.VerificationEnvelope{
		CommandID: envelope.CommandID, AttemptID: envelope.AttemptID,
		TenantID: envelope.TenantID, SiteID: envelope.SiteID, DeviceID: envelope.DeviceID,
		PointID: envelope.PointID, Capability: envelope.Capability, CapabilityRevision: envelope.CapabilityRevision,
		Parameters: envelope.Parameters, VerificationPointKey: key, PayloadHash: envelope.PayloadHash,
		ExecutionFence: envelope.ExecutionFence, LeaseOwner: envelope.LeaseOwner, LeaseUntil: envelope.LeaseUntil,
	})
	if err != nil {
		return DispatchSafetyResult{}, err
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
	return result, nil
}
