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

type fakeActuationInterlockProvider struct {
	checks  []commandmodel.InterlockCheck
	err     error
	mode    string
	modeErr error
}

func (provider fakeActuationInterlockProvider) ResolveCommandInterlocks(context.Context, commandmodel.DispatchEnvelope) ([]commandmodel.InterlockCheck, error) {
	return provider.checks, provider.err
}

func (provider fakeActuationInterlockProvider) ResolveCommandActuationMode(context.Context, commandmodel.DispatchEnvelope) (string, error) {
	return provider.mode, provider.modeErr
}

func TestVerifyBeforeDispatchActuationModeShutdownRules(t *testing.T) {
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	baseEnv := commandmodel.DispatchEnvelope{
		TenantID:           "tenant-1",
		SiteID:             "site-1",
		DeviceID:           "ctrl-chiller-a",
		PointID:            "point-stop",
		CommandID:          "cmd-stop-1",
		AttemptID:          "att-1",
		Capability:         commandmodel.CapabilityStop,
		CapabilityRevision: "capability:stop:v1",
		LeaseUntil:         now.Add(15 * time.Second),
	}

	staleChiller := freshEvidence("ctrl-chiller-a", "RUNNING", now)
	staleChiller.Freshness = "STALE"
	staleReader := &fakeSafetyReader{
		results: map[string]struct {
			evidenceID string
			evidence   commandmodel.ReportedStateEvidence
			err        error
		}{
			"ctrl-chiller-a|run_state": {evidenceID: "ev-chiller-stale", evidence: staleChiller},
			"ctrl-chwp-1|run_state":    {evidenceID: "ev-chwp", evidence: freshEvidence("ctrl-chwp-1", "RUNNING", now)},
		},
	}
	errorReader := &fakeSafetyReader{
		results: map[string]struct {
			evidenceID string
			evidence   commandmodel.ReportedStateEvidence
			err        error
		}{
			"ctrl-chiller-a|run_state": {err: errors.New("telemetry cache offline")},
		},
	}

	t.Run("emergency DO release STOP with 0 interlocks allows immediately even if reader would fail", func(t *testing.T) {
		verifier, err := NewAuthoritativeDispatchSafetyVerifierWithInterlocks(errorReader, DispatchSafetyStateKey, fakeActuationInterlockProvider{
			mode: " emergency_do_release ",
		})
		if err != nil {
			t.Fatal(err)
		}
		res, err := verifier.VerifyBeforeDispatch(context.Background(), baseEnv)
		if err != nil || !res.Safe || res.EvidenceID != "emergency-do-release:cmd-stop-1" {
			t.Fatalf("got res=%#v err=%v", res, err)
		}
	})

	t.Run("uninterlocked chiller pulse STOP bypasses stale and errored device telemetry", func(t *testing.T) {
		for _, reader := range []DispatchSafetyStateReader{staleReader, errorReader} {
			verifier, err := NewAuthoritativeDispatchSafetyVerifierWithInterlocks(reader, DispatchSafetyStateKey, fakeActuationInterlockProvider{
				mode: " pulse ",
			})
			if err != nil {
				t.Fatal(err)
			}
			res, err := verifier.VerifyBeforeDispatch(context.Background(), baseEnv)
			if err != nil || !res.Safe || res.EvidenceID != "shutdown-pulse-bypass:cmd-stop-1" {
				t.Fatalf("got res=%#v err=%v", res, err)
			}
		}
	})

	t.Run("uninterlocked chiller pulse STOP uses live telemetry when fresh", func(t *testing.T) {
		freshReader := &fakeSafetyReader{
			results: map[string]struct {
				evidenceID string
				evidence   commandmodel.ReportedStateEvidence
				err        error
			}{
				"ctrl-chiller-a|run_state": {evidenceID: "ev-chiller-fresh", evidence: freshEvidence("ctrl-chiller-a", "RUNNING", now)},
			},
		}
		verifier, err := NewAuthoritativeDispatchSafetyVerifierWithInterlocks(freshReader, DispatchSafetyStateKey, fakeActuationInterlockProvider{
			mode: "PULSE",
		})
		if err != nil {
			t.Fatal(err)
		}
		res, err := verifier.VerifyBeforeDispatch(context.Background(), baseEnv)
		if err != nil || !res.Safe || res.EvidenceID != "ev-chiller-fresh" {
			t.Fatalf("got res=%#v err=%v", res, err)
		}
	})

	t.Run("interlocked pump or tower STOP remains fail-closed even with PULSE or EMERGENCY_DO_RELEASE", func(t *testing.T) {
		for _, mode := range []string{"PULSE", "EMERGENCY_DO_RELEASE"} {
			verifier, err := NewAuthoritativeDispatchSafetyVerifierWithInterlocks(staleReader, DispatchSafetyStateKey, fakeActuationInterlockProvider{
				checks: []commandmodel.InterlockCheck{{DeviceID: "ctrl-chwp-1", Key: "run_state", Expected: "STOPPED"}},
				mode:   mode,
			})
			if err != nil {
				t.Fatal(err)
			}
			res, err := verifier.VerifyBeforeDispatch(context.Background(), baseEnv)
			if err != nil || res.Safe || res.FailureCode != "DISPATCH_SAFETY_STATE_NOT_FRESH" {
				t.Fatalf("mode=%s stale self telemetry was not blocked: res=%#v err=%v", mode, res, err)
			}
		}

		// Fresh self telemetry, but violated peer interlock MUST still block.
		freshChiller := &fakeSafetyReader{
			results: map[string]struct {
				evidenceID string
				evidence   commandmodel.ReportedStateEvidence
				err        error
			}{
				"ctrl-chiller-a|run_state": {evidenceID: "ev-chiller", evidence: freshEvidence("ctrl-chiller-a", "RUNNING", now)},
				"ctrl-chwp-1|run_state":    {evidenceID: "ev-chwp", evidence: freshEvidence("ctrl-chwp-1", "RUNNING", now)},
			},
		}
		pumpStopEnv := baseEnv
		pumpStopEnv.DeviceID = "ctrl-chwp-1"
		verifier, err := NewAuthoritativeDispatchSafetyVerifierWithInterlocks(freshChiller, DispatchSafetyStateKey, fakeActuationInterlockProvider{
			checks: []commandmodel.InterlockCheck{{DeviceID: "ctrl-chiller-a", Key: "run_state", Expected: "STOPPED"}},
			mode:   "PULSE",
		})
		if err != nil {
			t.Fatal(err)
		}
		res, err := verifier.VerifyBeforeDispatch(context.Background(), pumpStopEnv)
		if err != nil || res.Safe || res.FailureCode != "DISPATCH_SAFETY_INTERLOCK_VIOLATED" {
			t.Fatalf("expected peer interlock violation, got res=%#v err=%v", res, err)
		}
	})

	t.Run("non-STOP capabilities remain fail-closed even with 0 interlocks and PULSE or EMERGENCY_DO_RELEASE", func(t *testing.T) {
		nonStopCaps := []commandmodel.Capability{
			commandmodel.CapabilityStart,
			commandmodel.CapabilityStartUnitSequence,
			commandmodel.CapabilityStopUnitSequence,
			commandmodel.CapabilitySetTemperatureSetpoint,
			commandmodel.CapabilitySetFrequency,
			commandmodel.CapabilityResetFault,
		}
		for _, cap := range nonStopCaps {
			for _, mode := range []string{"PULSE", "EMERGENCY_DO_RELEASE"} {
				env := baseEnv
				env.Capability = cap
				verifier, err := NewAuthoritativeDispatchSafetyVerifierWithInterlocks(staleReader, DispatchSafetyStateKey, fakeActuationInterlockProvider{
					mode: mode,
				})
				if err != nil {
					t.Fatal(err)
				}
				res, err := verifier.VerifyBeforeDispatch(context.Background(), env)
				if err != nil || res.Safe || res.FailureCode != "DISPATCH_SAFETY_STATE_NOT_FRESH" {
					t.Fatalf("cap=%s mode=%s must remain fail-closed: res=%#v err=%v", cap, mode, res, err)
				}
			}
		}
	})

	t.Run("LEVEL STOP with 0 interlocks remains fail-closed on stale telemetry", func(t *testing.T) {
		verifier, err := NewAuthoritativeDispatchSafetyVerifierWithInterlocks(staleReader, DispatchSafetyStateKey, fakeActuationInterlockProvider{
			mode: "LEVEL",
		})
		if err != nil {
			t.Fatal(err)
		}
		res, err := verifier.VerifyBeforeDispatch(context.Background(), baseEnv)
		if err != nil || res.Safe || res.FailureCode != "DISPATCH_SAFETY_STATE_NOT_FRESH" {
			t.Fatalf("LEVEL STOP must remain fail-closed: res=%#v err=%v", res, err)
		}
	})

	t.Run("actuation mode lookup error fails closed", func(t *testing.T) {
		verifier, err := NewAuthoritativeDispatchSafetyVerifierWithInterlocks(staleReader, DispatchSafetyStateKey, fakeActuationInterlockProvider{
			modeErr: errors.New("registry read port error"),
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := verifier.VerifyBeforeDispatch(context.Background(), baseEnv); err == nil {
			t.Fatal("expected actuation mode lookup error")
		}
	})
}
