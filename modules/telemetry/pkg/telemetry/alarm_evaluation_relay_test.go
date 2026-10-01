package telemetry

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/modules/telemetry/pkg/telemetryapi"
)

func TestAlarmEvaluationRelayMarksOnlyAlarmDeliveryOnSuccess(t *testing.T) {
	now := time.Date(2026, 8, 28, 14, 0, 0, 0, time.UTC)
	repository := &alarmEvaluationRelayRepositoryStub{pending: []PendingPublication{alarmEvaluationPendingPublication(now)}}
	transport := &alarmEvaluationTransportStub{}
	relay, err := NewAlarmEvaluationRelay(AlarmEvaluationRelayConfig{
		Repository: repository, Transport: transport, WorkerID: "relay-a", LeaseDuration: 30 * time.Second, RetryDelay: 5 * time.Second,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	delivered, err := relay.RelayOnce(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if delivered != 1 || repository.deliveredEventID != repository.pending[0].EventID || repository.retryEventID != "" || transport.calls != 1 {
		t.Fatalf("unexpected Alarm relay success state: delivered=%d repository=%#v transport=%#v", delivered, repository, transport)
	}
}

func TestAlarmEvaluationRelaySchedulesRetryWithoutFallback(t *testing.T) {
	now := time.Date(2026, 8, 28, 14, 30, 0, 0, time.UTC)
	repository := &alarmEvaluationRelayRepositoryStub{pending: []PendingPublication{alarmEvaluationPendingPublication(now)}}
	transport := &alarmEvaluationTransportStub{err: errors.New("alarm unavailable")}
	relay, err := NewAlarmEvaluationRelay(AlarmEvaluationRelayConfig{
		Repository: repository, Transport: transport, WorkerID: "relay-b", LeaseDuration: 30 * time.Second, RetryDelay: 7 * time.Second,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	delivered, err := relay.RelayOnce(context.Background(), 10)
	if err == nil || delivered != 0 {
		t.Fatalf("Alarm transport failure was hidden: delivered=%d err=%v", delivered, err)
	}
	if repository.deliveredEventID != "" || repository.retryEventID != repository.pending[0].EventID || repository.retryAt != now.Add(7*time.Second) || repository.retryCode != "ALARM_EVALUATION_UNAVAILABLE" {
		t.Fatalf("Alarm relay failure did not remain retryable: %#v", repository)
	}
}

type alarmEvaluationRelayRepositoryStub struct {
	pending          []PendingPublication
	deliveredEventID string
	retryEventID     string
	retryAt          time.Time
	retryCode        string
}

func (repository *alarmEvaluationRelayRepositoryStub) ClaimPendingAlarmEvaluations(context.Context, string, int, time.Time, time.Duration) ([]PendingPublication, error) {
	return append([]PendingPublication(nil), repository.pending...), nil
}

func (repository *alarmEvaluationRelayRepositoryStub) MarkAlarmEvaluationDelivered(_ context.Context, eventID, _ string, _ time.Time) error {
	repository.deliveredEventID = eventID
	return nil
}

func (repository *alarmEvaluationRelayRepositoryStub) MarkAlarmEvaluationRetry(_ context.Context, eventID, _ string, availableAt time.Time, errorCode string) error {
	repository.retryEventID = eventID
	repository.retryAt = availableAt
	repository.retryCode = errorCode
	return nil
}

type alarmEvaluationTransportStub struct {
	calls int
	err   error
}

func (transport *alarmEvaluationTransportStub) EvaluateSnapshot(context.Context, PendingPublication) error {
	transport.calls++
	return transport.err
}

func alarmEvaluationPendingPublication(now time.Time) PendingPublication {
	const deviceID = "01910000-a000-7000-8000-000000000001"
	return PendingPublication{
		EventID: "01910000-a000-7000-8000-000000000002", DeviceID: deviceID, PreviousRevision: 0, Revision: 1, EvaluatedAt: now,
		Snapshot: telemetryapi.DeviceObservationSnapshot{
			SchemaVersion: 1, DeviceId: telemetryapi.UUIDv7(deviceID), TenantId: "01910000-a000-7000-8000-000000000003", SiteId: "01910000-a000-7000-8000-000000000004",
			BusinessRevision: 1, EvaluatedAt: telemetryapi.Instant(now.Format(time.RFC3339Nano)), EvaluationAvailability: telemetryapi.EvaluationAvailabilityAvailable,
		},
	}
}
