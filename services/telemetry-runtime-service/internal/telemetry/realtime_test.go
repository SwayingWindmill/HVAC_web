package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/services/telemetry-runtime-service/pkg/telemetryapi"
)

const (
	realtimeTestPrincipal = "018f2e00-2000-7000-8000-000000000001"
	realtimeTestTenant    = "018f2d00-0000-7000-8000-000000000001"
	realtimeTestOrg       = "018f2e00-1000-7000-8000-000000000003"
	realtimeTestDevice1   = "018f2e00-3000-7000-8000-000000000001"
	realtimeTestDevice2   = "018f2e00-3000-7000-8000-000000000002"
)

func TestRealtimeBootstrapIsAllOrNothingAndScopeBound(t *testing.T) {
	now := time.Date(2026, 7, 24, 15, 0, 0, 0, time.UTC)
	repository := NewMemoryRealtimeRepository()
	service := newRealtimeTestService(t, repository, &RecordingRealtimeTransport{}, &now)
	access := realtimeTestAccess()

	response, err := service.Bootstrap(context.Background(), access, telemetryapi.SubscriptionBootstrapRequest{Subscriptions: []telemetryapi.SubscriptionTargetRequest{
		{ClientSubscriptionId: "zone-a", DeviceId: realtimeTestDevice1, Keys: []telemetryapi.TelemetryKey{"temperature", "humidity"}},
		{ClientSubscriptionId: "zone-b", DeviceId: realtimeTestDevice2, Keys: []telemetryapi.TelemetryKey{}},
	}})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if response.TransportProtocol != "CENTRIFUGO_JSON_V1" || response.Endpoint != "wss://realtime.example.test/connection/websocket" || len(response.Subscriptions) != 2 {
		t.Fatalf("unexpected bootstrap response: %+v", response)
	}
	if response.Subscriptions[0].Channel == response.Subscriptions[1].Channel || response.Subscriptions[0].SubscriptionId == response.Subscriptions[1].SubscriptionId {
		t.Fatal("subscriptions must receive distinct opaque capabilities")
	}
	for _, descriptor := range response.Subscriptions {
		if strings.Contains(string(descriptor.Channel), realtimeTestDevice1) || strings.Contains(string(descriptor.Channel), realtimeTestDevice2) {
			t.Fatal("opaque channel leaked a Device identifier")
		}
	}

	invalidRepository := NewMemoryRealtimeRepository()
	invalidService := newRealtimeTestService(t, invalidRepository, &RecordingRealtimeTransport{}, &now)
	_, err = invalidService.Bootstrap(context.Background(), access, telemetryapi.SubscriptionBootstrapRequest{Subscriptions: []telemetryapi.SubscriptionTargetRequest{
		{ClientSubscriptionId: "valid", DeviceId: realtimeTestDevice1, Keys: []telemetryapi.TelemetryKey{"temperature"}},
		{ClientSubscriptionId: "invalid", DeviceId: realtimeTestDevice2, Keys: []telemetryapi.TelemetryKey{"bad key"}},
	}})
	if !errors.Is(err, ErrSubscriptionConflict) {
		t.Fatalf("expected all-or-nothing validation failure, got %v", err)
	}
	invalidRepository.mu.Lock()
	count := len(invalidRepository.subscriptions)
	invalidRepository.mu.Unlock()
	if count != 0 {
		t.Fatalf("invalid mixed bootstrap persisted %d partial subscriptions", count)
	}
}

func TestRealtimeCheckpointRecoveryAndRevocation(t *testing.T) {
	now := time.Date(2026, 7, 24, 15, 0, 0, 0, time.UTC)
	repository := NewMemoryRealtimeRepository()
	transport := &RecordingRealtimeTransport{}
	service := newRealtimeTestService(t, repository, transport, &now)
	access := realtimeTestAccess()
	request := telemetryapi.SubscriptionBootstrapRequest{Subscriptions: []telemetryapi.SubscriptionTargetRequest{
		{ClientSubscriptionId: "zone-a", DeviceId: realtimeTestDevice1, Keys: []telemetryapi.TelemetryKey{"temperature"}},
	}}
	bootstrap, err := service.Bootstrap(context.Background(), access, request)
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	subscriptionID := bootstrap.Subscriptions[0].SubscriptionId
	repository.SetCurrentRevision(realtimeTestDevice1, 5)

	checkpoint, err := service.Checkpoint(context.Background(), access, telemetryapi.RecoveryCursorCheckpointRequest{Checkpoints: []telemetryapi.RecoveryCursorCheckpoint{
		{SubscriptionId: subscriptionID, BusinessRevision: 5, TransportPosition: telemetryapi.TransportPosition{Epoch: "epoch-a", Offset: 42}},
	}})
	if err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	if len(checkpoint.Items) != 1 || checkpoint.Items[0].RecoveryCursor == "" {
		t.Fatalf("unexpected checkpoint response: %+v", checkpoint)
	}
	if _, err := service.Checkpoint(context.Background(), access, telemetryapi.RecoveryCursorCheckpointRequest{Checkpoints: []telemetryapi.RecoveryCursorCheckpoint{
		{SubscriptionId: subscriptionID, BusinessRevision: 6, TransportPosition: telemetryapi.TransportPosition{Epoch: "epoch-a", Offset: 43}},
	}}); !errors.Is(err, ErrRecoveryCursorRejected) {
		t.Fatalf("future revision checkpoint was not rejected: %v", err)
	}
	cursor := checkpoint.Items[0].RecoveryCursor
	request.Subscriptions[0].RecoveryCursor = &cursor
	recovered, err := service.Bootstrap(context.Background(), access, request)
	if err != nil {
		t.Fatalf("same-scope recovery bootstrap: %v", err)
	}
	if recovered.Subscriptions[0].RecoveryMode != "ATTEMPT_RECOVERY" || recovered.Subscriptions[0].TransportPosition == nil || recovered.Subscriptions[0].TransportPosition.Offset != 42 {
		t.Fatalf("recovery capability lost its transport checkpoint: %+v", recovered.Subscriptions[0])
	}

	crossTenant := access
	crossTenant.ActingOrganizationID = "018f2e00-1000-7000-8000-000000000004"
	if _, err := service.Bootstrap(context.Background(), crossTenant, request); !errors.Is(err, ErrRecoveryCursorRejected) {
		t.Fatalf("cross-tenant cursor reuse was not rejected: %v", err)
	}
	newSession := access
	newSession.SessionID = "session-2"
	if _, err := service.Bootstrap(context.Background(), newSession, request); !errors.Is(err, ErrRecoveryCursorRejected) {
		t.Fatalf("cross-session cursor reuse was not rejected: %v", err)
	}

	revoked, err := service.Revoke(context.Background(), access.PrincipalID, realtimeTestDevice1)
	if err != nil || revoked != 1 || len(transport.Unsubscribes) != 1 {
		t.Fatalf("revocation did not unsubscribe: count=%d transport=%+v err=%v", revoked, transport.Unsubscribes, err)
	}
	if _, err := service.AuthorizeSubscribe(context.Background(), access.PrincipalID, string(bootstrap.Subscriptions[0].Channel)); !errors.Is(err, ErrSubscriptionNotFound) {
		t.Fatalf("revoked channel remained subscribable: %v", err)
	}
	if _, err := service.Bootstrap(context.Background(), access, request); !errors.Is(err, ErrRecoveryCursorRejected) {
		t.Fatalf("revoked cursor reuse was not rejected: %v", err)
	}
}

func TestRealtimeRelayReusesRevisionAndEmitsEmptyUnselectedDelta(t *testing.T) {
	now := time.Date(2026, 7, 24, 15, 0, 0, 0, time.UTC)
	repository := NewMemoryRealtimeRepository()
	transport := &RecordingRealtimeTransport{}
	service := newRealtimeTestService(t, repository, transport, &now)
	access := realtimeTestAccess()
	bootstrap, err := service.Bootstrap(context.Background(), access, telemetryapi.SubscriptionBootstrapRequest{Subscriptions: []telemetryapi.SubscriptionTargetRequest{
		{ClientSubscriptionId: "temperature", DeviceId: realtimeTestDevice1, Keys: []telemetryapi.TelemetryKey{"temperature"}},
		{ClientSubscriptionId: "humidity", DeviceId: realtimeTestDevice1, Keys: []telemetryapi.TelemetryKey{"humidity"}},
	}})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	repository.AddPending(PendingPublication{
		EventID: "018f2e00-5000-7000-8000-000000000001", DeviceID: realtimeTestDevice1,
		PreviousRevision: 8, Revision: 9, EvaluatedAt: now,
		Snapshot: realtimeTestSnapshot(now, 9), ChangedKeys: []string{"temperature"},
	})
	published, err := service.RelayOnce(context.Background(), 10)
	if err != nil || published != 1 || len(transport.Publications) != 2 {
		t.Fatalf("relay result: published=%d transport=%d err=%v", published, len(transport.Publications), err)
	}
	if !repository.IsPublished("018f2e00-5000-7000-8000-000000000001") {
		t.Fatal("outbox intent was not marked published")
	}
	byChannel := map[string]DeviceObservationPublication{}
	for _, recorded := range transport.Publications {
		byChannel[recorded.Channel] = recorded.Publication
		encoded, err := json.Marshal(recorded.Publication)
		if err != nil {
			t.Fatalf("marshal publication: %v", err)
		}
		if strings.Contains(string(encoded), "transportPosition") || strings.Contains(string(encoded), "recoveryCursor") {
			t.Fatalf("publication leaked transport capability: %s", encoded)
		}
		if recorded.Publication.EventId != "018f2e00-5000-7000-8000-000000000001" || recorded.Publication.PreviousRevision != 8 || recorded.Publication.Revision != 9 {
			t.Fatalf("publication did not reuse outbox identity/revision: %+v", recorded.Publication)
		}
	}
	if len(byChannel[string(bootstrap.Subscriptions[0].Channel)].TelemetryChanges) != 1 {
		t.Fatal("selected changed key was not published")
	}
	if len(byChannel[string(bootstrap.Subscriptions[1].Channel)].TelemetryChanges) != 0 {
		t.Fatal("unselected-key revision must contain an empty telemetryChanges array")
	}

	retryRepository := NewMemoryRealtimeRepository()
	retryTransport := &RecordingRealtimeTransport{PublishError: errors.New("transport down")}
	retryService := newRealtimeTestService(t, retryRepository, retryTransport, &now)
	_, err = retryService.Bootstrap(context.Background(), access, telemetryapi.SubscriptionBootstrapRequest{Subscriptions: []telemetryapi.SubscriptionTargetRequest{
		{ClientSubscriptionId: "retry", DeviceId: realtimeTestDevice1, Keys: []telemetryapi.TelemetryKey{"temperature"}},
	}})
	if err != nil {
		t.Fatalf("retry bootstrap: %v", err)
	}
	intent := PendingPublication{EventID: "018f2e00-5000-7000-8000-000000000002", DeviceID: realtimeTestDevice1, PreviousRevision: 9, Revision: 10, EvaluatedAt: now, Snapshot: realtimeTestSnapshot(now, 10), ChangedKeys: []string{"temperature"}}
	retryRepository.AddPending(intent)
	if _, err := retryService.RelayOnce(context.Background(), 10); !errors.Is(err, ErrRealtimeUnavailable) {
		t.Fatalf("expected transport failure, got %v", err)
	}
	if retryRepository.IsPublished(intent.EventID) {
		t.Fatal("failed publication was acknowledged")
	}
	retryTransport.PublishError = nil
	if _, err := retryService.RelayOnce(context.Background(), 10); err != nil {
		t.Fatalf("retry relay: %v", err)
	}
	if len(retryTransport.Publications) != 1 || retryTransport.Publications[0].Publication.EventId != telemetryapi.UUIDv7(intent.EventID) || retryTransport.Publications[0].Publication.Revision != 10 {
		t.Fatalf("retry changed event identity/revision: %+v", retryTransport.Publications)
	}
}

func TestEvaluateRecoveryFallsBackOnUncertainty(t *testing.T) {
	tests := []struct {
		name     string
		evidence RecoveryEvidence
		want     RecoveryDisposition
	}{
		{"contiguous with duplicate", RecoveryEvidence{WasRecovering: true, Recovered: true, ExpectedEpoch: "a", RecoveredEpoch: "a", AppliedRevision: 5, PublicationRevisions: []int64{5, 6, 7}}, RecoveryAttemptTransport},
		{"gap", RecoveryEvidence{WasRecovering: true, Recovered: true, ExpectedEpoch: "a", RecoveredEpoch: "a", AppliedRevision: 5, PublicationRevisions: []int64{7}}, RecoveryLoadSnapshot},
		{"epoch reset", RecoveryEvidence{WasRecovering: true, Recovered: true, ExpectedEpoch: "a", RecoveredEpoch: "b", AppliedRevision: 5, PublicationRevisions: []int64{6}}, RecoveryLoadSnapshot},
		{"history overflow", RecoveryEvidence{WasRecovering: true, Recovered: false, ExpectedEpoch: "a", RecoveredEpoch: "a", AppliedRevision: 5}, RecoveryLoadSnapshot},
		{"fresh connection", RecoveryEvidence{}, RecoveryLoadSnapshot},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := EvaluateRecovery(test.evidence); got != test.want {
				t.Fatalf("got %s want %s", got, test.want)
			}
		})
	}
}

func newRealtimeTestService(t *testing.T, repository RealtimeRepository, transport RealtimeTransport, now *time.Time) *RealtimeService {
	t.Helper()
	sequence := 0
	service, err := NewRealtimeService(RealtimeConfig{
		Repository: repository, Transport: transport,
		PublicEndpoint:         "wss://realtime.example.test/connection/websocket",
		CapabilityHMACKey:      []byte("0123456789abcdef0123456789abcdef"),
		ConnectionTokenHMACKey: []byte("abcdef0123456789abcdef0123456789"),
		Now:                    func() time.Time { return *now },
		NewOpaqueID: func() (string, error) {
			sequence++
			return strings.Repeat(string(rune('A'+sequence-1)), 32), nil
		},
	})
	if err != nil {
		t.Fatalf("new realtime service: %v", err)
	}
	return service
}

func realtimeTestAccess() AccessContext {
	return AccessContext{PrincipalID: realtimeTestPrincipal, Subject: "subject-1", SubjectIssuer: "https://issuer.example", SessionID: "session-1", ActingOrganizationID: realtimeTestOrg, PolicyRevision: "policy-7"}
}

func realtimeTestSnapshot(now time.Time, revision int64) telemetryapi.DeviceObservationSnapshot {
	display := telemetryapi.DeviceDisplayStateOnline
	presence := telemetryapi.DevicePresenceStateOnline
	policy := telemetryapi.PolicyRevision(3)
	unitC := "Cel"
	unitPercent := "%"
	return telemetryapi.DeviceObservationSnapshot{
		SchemaVersion: 1, DeviceId: realtimeTestDevice1,
		TenantId: realtimeTestTenant, SiteId: "018f2e00-4000-7000-8000-000000000001",
		BusinessRevision: telemetryapi.BusinessRevision(revision), EvaluatedAt: instant(now),
		EvaluationAvailability: telemetryapi.EvaluationAvailabilityAvailable,
		AvailabilityReasons:    []telemetryapi.AvailabilityReasonCode{},
		Presence:               telemetryapi.PresenceSnapshot{Applicability: telemetryapi.PresenceApplicabilityApplicable, CurrentState: &presence, PolicyRevision: &policy},
		TelemetryReadiness:     telemetryapi.TelemetryReadinessCurrent, DisplayState: &display,
		Values: []telemetryapi.TelemetryKeyState{
			{Present: &telemetryapi.TelemetryPresentState{Key: "temperature", State: "PRESENT", Value: json.RawMessage(`21.5`), ValueType: "NUMBER", Unit: &unitC, SampledAt: instant(now), ReceivedAt: instant(now), Freshness: "FRESH", Quality: telemetryapi.TelemetryQualityGood, QualityReasons: []telemetryapi.QualityReasonCode{}, PolicyRevision: 3}},
			{Present: &telemetryapi.TelemetryPresentState{Key: "humidity", State: "PRESENT", Value: json.RawMessage(`45`), ValueType: "NUMBER", Unit: &unitPercent, SampledAt: instant(now), ReceivedAt: instant(now), Freshness: "FRESH", Quality: telemetryapi.TelemetryQualityGood, QualityReasons: []telemetryapi.QualityReasonCode{}, PolicyRevision: 3}},
		},
	}
}
