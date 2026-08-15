package telemetry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/telemetryauth"
	"github.com/quanlaihe/hvac-web/services/telemetry-runtime-service/pkg/telemetryapi"
)

const commandVerifierSPIFFE = "spiffe://hvac.local/command-verifier"

type commandReportedStateStore struct {
	calls int
	now   time.Time
}

func (store *commandReportedStateStore) EvaluateAndRead(_ context.Context, target telemetryauth.Target, evaluatedAt time.Time) (SnapshotCommit, error) {
	store.calls++
	store.now = evaluatedAt
	state := telemetryapi.DevicePresenceStateOnline
	sampledAt := telemetryapi.Instant(evaluatedAt.Add(time.Second).Format(time.RFC3339Nano))
	return SnapshotCommit{Snapshot: telemetryapi.DeviceObservationSnapshot{
		SchemaVersion: 1,
		TenantId: telemetryapi.UUIDv7(tenantA), SiteId: telemetryapi.UUIDv7(siteA), DeviceId: telemetryapi.UUIDv7(deviceA),
		BusinessRevision: 9, EvaluatedAt: telemetryapi.Instant(evaluatedAt.Format(time.RFC3339Nano)),
		EvaluationAvailability: telemetryapi.EvaluationAvailabilityAvailable,
		Presence:               telemetryapi.PresenceSnapshot{Applicability: telemetryapi.PresenceApplicabilityApplicable, CurrentState: &state},
		TelemetryReadiness:     telemetryapi.TelemetryReadinessCurrent,
		Values: []telemetryapi.TelemetryKeyState{{Present: &telemetryapi.TelemetryPresentState{
			Key: "zone.temperature_setpoint", State: "PRESENT", Value: json.RawMessage(`22.5`), ValueType: "NUMBER",
			SampledAt: sampledAt, ReceivedAt: sampledAt, Freshness: "FRESH", Quality: telemetryapi.TelemetryQualityGood,
		}}},
	}}, nil
}

func TestCommandReportedStateReturnsExactConfiguredCohort(t *testing.T) {
	now := time.Date(2026, 7, 26, 1, 2, 3, 0, time.UTC)
	store := &commandReportedStateStore{}
	handler := NewHandler(ServerConfig{
		Store: store, AllowedCommandVerifierSPIFFE: commandVerifierSPIFFE,
		CommandVerifierTenantID: tenantA, CommandVerifierSiteID: siteA, CommandVerifierDeviceID: deviceA,
		Now: func() time.Time { return now },
	})
	request := httptest.NewRequest(http.MethodGet, InternalCommandReportedStatePath+"?key=zone.temperature_setpoint", nil)
	request.TLS = verifiedTLSState(commandVerifierSPIFFE)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response commandReportedStateResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.TenantID != tenantA || response.SiteID != siteA || response.DeviceID != deviceA ||
		response.ReportedStateKey != "zone.temperature_setpoint" || response.ReportedValue.Number == nil || *response.ReportedValue.Number != 22.5 ||
		response.BusinessRevision != 9 || response.Presence != "ONLINE" || response.Freshness != "FRESH" ||
		response.Quality != "GOOD" || response.EvidenceID == "" || store.calls != 1 {
		t.Fatalf("response=%#v calls=%d", response, store.calls)
	}
}

func TestCommandReportedStateRejectsOtherWorkload(t *testing.T) {
	store := &commandReportedStateStore{}
	handler := NewHandler(ServerConfig{
		Store: store, AllowedCommandVerifierSPIFFE: commandVerifierSPIFFE,
		CommandVerifierTenantID: tenantA, CommandVerifierSiteID: siteA, CommandVerifierDeviceID: deviceA,
		Now: time.Now,
	})
	request := httptest.NewRequest(http.MethodGet, InternalCommandReportedStatePath+"?key=zone.temperature_setpoint", nil)
	request.TLS = verifiedTLSState(gatewaySPIFFE)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized || store.calls != 0 {
		t.Fatalf("status=%d calls=%d body=%s", recorder.Code, store.calls, recorder.Body.String())
	}
}
