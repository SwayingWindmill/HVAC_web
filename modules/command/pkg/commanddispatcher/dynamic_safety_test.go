package commanddispatcher

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
)

type fakeSafetyReader struct {
	results map[string]struct {
		evidenceID string
		evidence   commandmodel.ReportedStateEvidence
		err        error
	}
}

func (reader *fakeSafetyReader) ReadReportedState(_ context.Context, envelope commandmodel.VerificationEnvelope) (string, commandmodel.ReportedStateEvidence, error) {
	key := envelope.DeviceID + "|" + envelope.VerificationPointKey
	entry, ok := reader.results[key]
	if !ok {
		return "", commandmodel.ReportedStateEvidence{}, errors.New("unexpected reported-state lookup: " + key)
	}
	return entry.evidenceID, entry.evidence, entry.err
}

type fakeInterlockProvider struct {
	checks []commandmodel.InterlockCheck
	err    error
}

func (provider fakeInterlockProvider) ResolveCommandInterlocks(context.Context, commandmodel.DispatchEnvelope) ([]commandmodel.InterlockCheck, error) {
	return provider.checks, provider.err
}

func freshEvidence(deviceID, value string, now time.Time) commandmodel.ReportedStateEvidence {
	return commandmodel.ReportedStateEvidence{
		TenantID:               "tenant-1",
		SiteID:                 "site-1",
		DeviceID:               deviceID,
		EvaluationAvailability: "AVAILABLE",
		Presence:               "ONLINE",
		Readiness:              "CURRENT",
		Freshness:              "FRESH",
		Quality:                "GOOD",
		BusinessRevision:       7,
		ReportedValue:          commandmodel.TextScalar(value),
		ObservedAt:             now,
	}
}

func TestVerifyBeforeDispatchWithInterlocks(t *testing.T) {
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	envelope := commandmodel.DispatchEnvelope{
		TenantID:           "tenant-1",
		SiteID:             "site-1",
		DeviceID:           "ctrl-chiller-a",
		PointID:            "point-start",
		CommandID:          "cmd-1",
		AttemptID:          "att-1",
		Capability:         commandmodel.CapabilityStart,
		CapabilityRevision: "capability:start:v1",
		LeaseUntil:         now.Add(15 * time.Second),
	}

	t.Run("all interlocks satisfied passes safety", func(t *testing.T) {
		reader := &fakeSafetyReader{
			results: map[string]struct {
				evidenceID string
				evidence   commandmodel.ReportedStateEvidence
				err        error
			}{
				"ctrl-chiller-a|run_state": {evidenceID: "ev-chiller", evidence: freshEvidence("ctrl-chiller-a", "STOPPED", now)},
				"ctrl-chwp-1|run_state":    {evidenceID: "ev-chwp", evidence: freshEvidence("ctrl-chwp-1", "RUNNING", now)},
				"ctrl-cwp-1|run_state":     {evidenceID: "ev-cwp", evidence: freshEvidence("ctrl-cwp-1", "RUNNING", now)},
				"ctrl-tower-1|run_state":   {evidenceID: "ev-tower", evidence: freshEvidence("ctrl-tower-1", "RUNNING", now)},
			},
		}
		verifier, err := NewAuthoritativeDispatchSafetyVerifierWithInterlocks(reader, DispatchSafetyStateKey, fakeInterlockProvider{
			checks: []commandmodel.InterlockCheck{
				{DeviceID: "ctrl-chwp-1", Key: "run_state", Expected: "RUNNING"},
				{DeviceID: "ctrl-cwp-1", Key: "run_state", Expected: "RUNNING"},
				{DeviceID: "ctrl-tower-1", Key: "run_state", Expected: "RUNNING"},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		res, err := verifier.VerifyBeforeDispatch(context.Background(), envelope)
		if err != nil || !res.Safe || res.FailureCode != "" {
			t.Fatalf("got res=%#v err=%v", res, err)
		}
	})

	t.Run("chiller start blocked when pump is stopped", func(t *testing.T) {
		reader := &fakeSafetyReader{
			results: map[string]struct {
				evidenceID string
				evidence   commandmodel.ReportedStateEvidence
				err        error
			}{
				"ctrl-chiller-a|run_state": {evidenceID: "ev-chiller", evidence: freshEvidence("ctrl-chiller-a", "STOPPED", now)},
				"ctrl-chwp-1|run_state":    {evidenceID: "ev-chwp", evidence: freshEvidence("ctrl-chwp-1", "STOPPED", now)},
			},
		}
		verifier, err := NewAuthoritativeDispatchSafetyVerifierWithInterlocks(reader, DispatchSafetyStateKey, fakeInterlockProvider{
			checks: []commandmodel.InterlockCheck{{DeviceID: "ctrl-chwp-1", Key: "run_state", Expected: "RUNNING"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		res, err := verifier.VerifyBeforeDispatch(context.Background(), envelope)
		if err != nil || res.Safe || res.FailureCode != "DISPATCH_SAFETY_INTERLOCK_VIOLATED" || res.EvidenceID != "ev-chwp" {
			t.Fatalf("got res=%#v err=%v", res, err)
		}
	})

	t.Run("pump stop blocked when chiller is running", func(t *testing.T) {
		pumpEnv := envelope
		pumpEnv.DeviceID = "ctrl-chwp-1"
		pumpEnv.Capability = commandmodel.CapabilityStop
		reader := &fakeSafetyReader{
			results: map[string]struct {
				evidenceID string
				evidence   commandmodel.ReportedStateEvidence
				err        error
			}{
				"ctrl-chwp-1|run_state":    {evidenceID: "ev-chwp", evidence: freshEvidence("ctrl-chwp-1", "RUNNING", now)},
				"ctrl-chiller-a|run_state": {evidenceID: "ev-chiller", evidence: freshEvidence("ctrl-chiller-a", "RUNNING", now)},
			},
		}
		verifier, err := NewAuthoritativeDispatchSafetyVerifierWithInterlocks(reader, DispatchSafetyStateKey, fakeInterlockProvider{
			checks: []commandmodel.InterlockCheck{{DeviceID: "ctrl-chiller-a", Key: "run_state", Expected: "STOPPED"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		res, err := verifier.VerifyBeforeDispatch(context.Background(), pumpEnv)
		if err != nil || res.Safe || res.FailureCode != "DISPATCH_SAFETY_INTERLOCK_VIOLATED" || res.EvidenceID != "ev-chiller" {
			t.Fatalf("got res=%#v err=%v", res, err)
		}
	})

	t.Run("stale peer returns interlock state unavailable", func(t *testing.T) {
		stalePeer := freshEvidence("ctrl-chwp-1", "RUNNING", now)
		stalePeer.Freshness = "STALE"
		reader := &fakeSafetyReader{
			results: map[string]struct {
				evidenceID string
				evidence   commandmodel.ReportedStateEvidence
				err        error
			}{
				"ctrl-chiller-a|run_state": {evidenceID: "ev-chiller", evidence: freshEvidence("ctrl-chiller-a", "STOPPED", now)},
				"ctrl-chwp-1|run_state":    {evidenceID: "ev-chwp-stale", evidence: stalePeer},
			},
		}
		verifier, err := NewAuthoritativeDispatchSafetyVerifierWithInterlocks(reader, DispatchSafetyStateKey, fakeInterlockProvider{
			checks: []commandmodel.InterlockCheck{{DeviceID: "ctrl-chwp-1", Key: "run_state", Expected: "RUNNING"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		res, err := verifier.VerifyBeforeDispatch(context.Background(), envelope)
		if err != nil || res.Safe || res.FailureCode != "DISPATCH_SAFETY_INTERLOCK_STATE_UNAVAILABLE" || res.EvidenceID != "ev-chwp-stale" {
			t.Fatalf("got res=%#v err=%v", res, err)
		}
	})

	t.Run("interlock provider error bubbles up for retry", func(t *testing.T) {
		reader := &fakeSafetyReader{
			results: map[string]struct {
				evidenceID string
				evidence   commandmodel.ReportedStateEvidence
				err        error
			}{
				"ctrl-chiller-a|run_state": {evidenceID: "ev-chiller", evidence: freshEvidence("ctrl-chiller-a", "STOPPED", now)},
			},
		}
		verifier, err := NewAuthoritativeDispatchSafetyVerifierWithInterlocks(reader, DispatchSafetyStateKey, fakeInterlockProvider{
			err: errors.New("registry read port timeout"),
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := verifier.VerifyBeforeDispatch(context.Background(), envelope); err == nil {
			t.Fatal("expected error from interlock provider")
		}
	})
}
