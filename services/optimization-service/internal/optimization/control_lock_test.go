package optimization

import (
	"errors"
	"testing"
	"time"
)

func TestControlLockBlockMessage(t *testing.T) {
	tests := []struct {
		name            string
		siteControlMode string
		hasAssetLock    bool
		wantBlocked     bool
		wantReason      PreparationReason
	}{
		{name: "empty defaults to unlocked", siteControlMode: "", hasAssetLock: false, wantBlocked: false},
		{name: "remote auto unlocked", siteControlMode: "REMOTE_AUTO", hasAssetLock: false, wantBlocked: false},
		{name: "manual override blocks", siteControlMode: "MANUAL_OVERRIDE", hasAssetLock: false, wantBlocked: true, wantReason: ReasonMaintenanceActive},
		{name: "safe shutdown exit blocks", siteControlMode: "SAFE_SHUTDOWN_EXIT", hasAssetLock: false, wantBlocked: true, wantReason: ReasonMaintenanceActive},
		{name: "asset control lock blocks remote auto", siteControlMode: "REMOTE_AUTO", hasAssetLock: true, wantBlocked: true, wantReason: ReasonMaintenanceActive},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			msg, blocked := controlLockBlockMessage(tc.siteControlMode, tc.hasAssetLock)
			if blocked != tc.wantBlocked {
				t.Fatalf("blocked=%v, want %v (msg=%q)", blocked, tc.wantBlocked, msg)
			}
			if !blocked {
				if msg != "" {
					t.Fatalf("expected empty message when unblocked, got %q", msg)
				}
				return
			}
			err := unavailable(ReasonMaintenanceActive, "%s", msg)
			if err == nil || err.Error() == "" {
				t.Fatalf("expected non-empty preparation unavailable error")
			}
			var typed *PreparationUnavailableError
			if !errors.As(err, &typed) || typed.Reason != tc.wantReason {
				t.Fatalf("reason=%v, want %s", err, tc.wantReason)
			}
		})
	}
}

func TestPreparerRejectsLockedSiteViaMaintenanceActive(t *testing.T) {
	at := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	store := &preparationStoreStub{
		err: unavailable(ReasonMaintenanceActive, "site control mode MANUAL_OVERRIDE suppresses automated optimization"),
	}
	preparer, err := NewPreparer(store, &authoritativeInputStub{state: testAuthoritativeState(at)}, func() time.Time { return at })
	if err != nil {
		t.Fatal(err)
	}
	_, err = preparer.Prepare(t.Context(), PreparationRequest{
		TenantID:    "01990000-3000-7000-8000-000000000001",
		SiteID:      "01990000-5000-7000-8000-000000000001",
		SubjectType: "SITE",
		SubjectID:   "01990000-5000-7000-8000-000000000001",
		Trigger:     TriggerManual,
	})
	var typed *PreparationUnavailableError
	if !errors.Is(err, ErrPreparationUnavailable) || !errors.As(err, &typed) || typed.Reason != ReasonMaintenanceActive {
		t.Fatalf("Prepare err=%v, want %v (%s)", err, ErrPreparationUnavailable, ReasonMaintenanceActive)
	}
}
