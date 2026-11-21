package telemetry

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
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

func TestHTTPAlarmEvaluationTransportClassifiesPermanent4xxVsRetryableStatuses(t *testing.T) {
	var statusCode int
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(statusCode)
	}))
	defer server.Close()

	transport, err := NewHTTPAlarmEvaluationTransport(HTTPAlarmEvaluationTransportConfig{
		Endpoint:   server.URL + "/internal/v1/alarm/evaluations/telemetry",
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	pub := alarmEvaluationPendingPublication(time.Date(2026, 8, 28, 15, 0, 0, 0, time.UTC))

	for _, permanentStatus := range []int{http.StatusBadRequest, http.StatusNotFound, http.StatusUnprocessableEntity} {
		statusCode = permanentStatus
		err := transport.EvaluateSnapshot(context.Background(), pub)
		if !errors.Is(err, ErrPermanentAlarmEvaluationFailure) {
			t.Fatalf("HTTP %d should return ErrPermanentAlarmEvaluationFailure, got %v", permanentStatus, err)
		}
	}

	for _, retryableStatus := range []int{http.StatusRequestTimeout, http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusServiceUnavailable} {
		statusCode = retryableStatus
		err := transport.EvaluateSnapshot(context.Background(), pub)
		if err == nil || errors.Is(err, ErrPermanentAlarmEvaluationFailure) {
			t.Fatalf("HTTP %d should remain retryable, got %v", retryableStatus, err)
		}
	}

	statusCode = http.StatusNoContent
	if err := transport.EvaluateSnapshot(context.Background(), pub); err != nil {
		t.Fatalf("HTTP 204 should succeed, got %v", err)
	}
}

func TestAlarmEvaluationRelayDeadLettersPermanentFailureAndUnblocksSubsequentPublications(t *testing.T) {
	now := time.Date(2026, 8, 28, 15, 30, 0, 0, time.UTC)
	poison := alarmEvaluationPendingPublication(now)
	healthy := alarmEvaluationPendingPublication(now)
	healthy.EventID = "01910000-a000-7000-8000-000000000009"
	healthy.PreviousRevision = 1
	healthy.Revision = 2
	healthy.Snapshot.BusinessRevision = 2

	repository := &alarmEvaluationRelayRepositoryStub{pending: []PendingPublication{poison, healthy}}
	transport := &alarmEvaluationTransportStub{
		fn: func(_ context.Context, publication PendingPublication) error {
			if publication.EventID == poison.EventID {
				return fmt.Errorf("%w: HTTP 422", ErrPermanentAlarmEvaluationFailure)
			}
			return nil
		},
	}
	relay, err := NewAlarmEvaluationRelay(AlarmEvaluationRelayConfig{
		Repository: repository, Transport: transport, WorkerID: "relay-c", LeaseDuration: 30 * time.Second, RetryDelay: 5 * time.Second,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	delivered, err := relay.RelayOnce(context.Background(), 10)
	if !errors.Is(err, ErrPermanentAlarmEvaluationFailure) {
		t.Fatalf("expected ErrPermanentAlarmEvaluationFailure, got %v", err)
	}
	if delivered != 1 {
		t.Fatalf("healthy publication behind poison item should still be delivered: delivered=%d", delivered)
	}
	if repository.deadLetterEventID != poison.EventID || repository.deadLetterCode != "ALARM_EVALUATION_PERMANENT_FAILURE" || !repository.deadLetterAt.Equal(now) {
		t.Fatalf("poison publication was not dead-lettered cleanly: %#v", repository)
	}
	if repository.retryEventID != "" {
		t.Fatalf("permanent failure should not be scheduled for retry: %#v", repository)
	}
	if repository.deliveredEventID != healthy.EventID {
		t.Fatalf("healthy publication was not marked delivered: %#v", repository)
	}
}

func TestAlarmEvaluationRelayExponentialBackoffAndMaxAttemptsDeadLetter(t *testing.T) {
	now := time.Date(2026, 8, 28, 16, 0, 0, 0, time.UTC)
	cases := []struct {
		alarmAttempts int
		wantDelay     time.Duration
	}{
		{alarmAttempts: 0, wantDelay: 5 * time.Second},
		{alarmAttempts: 1, wantDelay: 10 * time.Second},
		{alarmAttempts: 2, wantDelay: 20 * time.Second},
		{alarmAttempts: 7, wantDelay: 640 * time.Second},
		{alarmAttempts: 8, wantDelay: 15 * time.Minute},
		{alarmAttempts: 40, wantDelay: 15 * time.Minute},
	}
	for _, tc := range cases {
		pub := alarmEvaluationPendingPublication(now)
		pub.AlarmAttempts = tc.alarmAttempts
		repository := &alarmEvaluationRelayRepositoryStub{pending: []PendingPublication{pub}}
		transport := &alarmEvaluationTransportStub{err: errors.New("downstream 503")}
		relay, err := NewAlarmEvaluationRelay(AlarmEvaluationRelayConfig{
			Repository: repository, Transport: transport, WorkerID: "relay-d",
			LeaseDuration: 30 * time.Second, RetryDelay: 5 * time.Second, MaxAttempts: 50,
			Now: func() time.Time { return now },
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := relay.RelayOnce(context.Background(), 10); err == nil {
			t.Fatalf("expected error for attempts=%d", tc.alarmAttempts)
		}
		if got := repository.retryAt.Sub(now); got != tc.wantDelay {
			t.Fatalf("attempts=%d backoff=%s, want %s", tc.alarmAttempts, got, tc.wantDelay)
		}
	}

	// Default MaxAttempts is 10: attempt 10 (AlarmAttempts == 9) transitions to DEAD_LETTER.
	pubAtLimit := alarmEvaluationPendingPublication(now)
	pubAtLimit.AlarmAttempts = 9
	repoAtLimit := &alarmEvaluationRelayRepositoryStub{pending: []PendingPublication{pubAtLimit}}
	transportAtLimit := &alarmEvaluationTransportStub{err: errors.New("still down")}
	defaultRelay, err := NewAlarmEvaluationRelay(AlarmEvaluationRelayConfig{
		Repository: repoAtLimit, Transport: transportAtLimit, WorkerID: "relay-e",
		LeaseDuration: 30 * time.Second, RetryDelay: 5 * time.Second,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	delivered, err := defaultRelay.RelayOnce(context.Background(), 10)
	if delivered != 0 || !errors.Is(err, ErrPermanentAlarmEvaluationFailure) {
		t.Fatalf("exhausted item should return permanent failure: delivered=%d err=%v", delivered, err)
	}
	if repoAtLimit.deadLetterEventID != pubAtLimit.EventID || repoAtLimit.deadLetterCode != "ALARM_EVALUATION_MAX_ATTEMPTS_EXCEEDED" || repoAtLimit.retryEventID != "" {
		t.Fatalf("exhausted item did not move to DEAD_LETTER: %#v", repoAtLimit)
	}

	// Pre-exhausted row (AlarmAttempts >= MaxAttempts) is dead-lettered without calling transport.
	pubPreExhausted := alarmEvaluationPendingPublication(now)
	pubPreExhausted.AlarmAttempts = 3
	repoPreExhausted := &alarmEvaluationRelayRepositoryStub{pending: []PendingPublication{pubPreExhausted}}
	transportPreExhausted := &alarmEvaluationTransportStub{}
	customRelay, err := NewAlarmEvaluationRelay(AlarmEvaluationRelayConfig{
		Repository: repoPreExhausted, Transport: transportPreExhausted, WorkerID: "relay-f",
		LeaseDuration: 30 * time.Second, RetryDelay: 5 * time.Second, MaxAttempts: 3,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := customRelay.RelayOnce(context.Background(), 10); !errors.Is(err, ErrPermanentAlarmEvaluationFailure) {
		t.Fatalf("pre-exhausted item should dead-letter, got %v", err)
	}
	if transportPreExhausted.calls != 0 || repoPreExhausted.deadLetterEventID != pubPreExhausted.EventID {
		t.Fatalf("pre-exhausted item called transport=%d repo=%#v", transportPreExhausted.calls, repoPreExhausted)
	}
}

func TestAlarmEvaluationRelayAppliesSingleDeliveryTimeoutAndRespectsTighterDeadline(t *testing.T) {
	now := time.Date(2026, 8, 28, 17, 0, 0, 0, time.UTC)

	t.Run("enforces configured delivery timeout", func(t *testing.T) {
		repository := &alarmEvaluationRelayRepositoryStub{pending: []PendingPublication{alarmEvaluationPendingPublication(now)}}
		var hadDeadline bool
		var observedRemaining time.Duration
		transport := &alarmEvaluationTransportStub{
			fn: func(ctx context.Context, _ PendingPublication) error {
				deadline, ok := ctx.Deadline()
				hadDeadline = ok
				if ok {
					observedRemaining = time.Until(deadline)
				}
				<-ctx.Done()
				return ctx.Err()
			},
		}
		relay, err := NewAlarmEvaluationRelay(AlarmEvaluationRelayConfig{
			Repository: repository, Transport: transport, WorkerID: "relay-g",
			LeaseDuration: 30 * time.Second, RetryDelay: 5 * time.Second, DeliveryTimeout: 50 * time.Millisecond,
			Now: func() time.Time { return now },
		})
		if err != nil {
			t.Fatal(err)
		}
		delivered, err := relay.RelayOnce(context.Background(), 10)
		if !errors.Is(err, context.DeadlineExceeded) || delivered != 0 {
			t.Fatalf("expected deadline exceeded, got delivered=%d err=%v", delivered, err)
		}
		if !hadDeadline || observedRemaining > 60*time.Millisecond {
			t.Fatalf("expected ~50ms single-delivery deadline, got ok=%v remaining=%s", hadDeadline, observedRemaining)
		}
		if repository.retryEventID != repository.pending[0].EventID {
			t.Fatalf("timed-out delivery was not scheduled for retry: %#v", repository)
		}
	})

	t.Run("preserves tighter caller deadline", func(t *testing.T) {
		repository := &alarmEvaluationRelayRepositoryStub{pending: []PendingPublication{alarmEvaluationPendingPublication(now)}}
		var observedRemaining time.Duration
		transport := &alarmEvaluationTransportStub{
			fn: func(ctx context.Context, _ PendingPublication) error {
				if deadline, ok := ctx.Deadline(); ok {
					observedRemaining = time.Until(deadline)
				}
				return nil
			},
		}
		relay, err := NewAlarmEvaluationRelay(AlarmEvaluationRelayConfig{
			Repository: repository, Transport: transport, WorkerID: "relay-h",
			LeaseDuration: 30 * time.Second, RetryDelay: 5 * time.Second,
			Now: func() time.Time { return now },
		})
		if err != nil {
			t.Fatal(err)
		}
		parentCtx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
		defer cancel()
		if _, err := relay.RelayOnce(parentCtx, 10); err != nil {
			t.Fatal(err)
		}
		if observedRemaining <= 0 || observedRemaining > 60*time.Millisecond {
			t.Fatalf("expected tighter caller deadline <= 60ms, got %s", observedRemaining)
		}
	})
}

type alarmEvaluationRelayRepositoryStub struct {
	pending           []PendingPublication
	deliveredEventID  string
	retryEventID      string
	retryAt           time.Time
	retryCode         string
	deadLetterEventID string
	deadLetterAt      time.Time
	deadLetterCode    string
	pendingCount      int64
	deadLetterCount   int64
}

func (repository *alarmEvaluationRelayRepositoryStub) CountAlarmEvaluationOutboxStates(context.Context) (int64, int64, error) {
	return repository.pendingCount, repository.deadLetterCount, nil
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

func (repository *alarmEvaluationRelayRepositoryStub) MarkAlarmEvaluationDeadLetter(_ context.Context, eventID, _ string, deadLetteredAt time.Time, errorCode string) error {
	repository.deadLetterEventID = eventID
	repository.deadLetterAt = deadLetteredAt
	repository.deadLetterCode = errorCode
	return nil
}

type alarmEvaluationTransportStub struct {
	calls int
	err   error
	fn    func(context.Context, PendingPublication) error
}

func (transport *alarmEvaluationTransportStub) EvaluateSnapshot(ctx context.Context, publication PendingPublication) error {
	transport.calls++
	if transport.fn != nil {
		return transport.fn(ctx, publication)
	}
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

func TestAlarmEvaluationRelayInstrumentsDeadLetterAndOutboxDepthMetrics(t *testing.T) {
	now := time.Date(2026, 11, 19, 16, 0, 0, 0, time.UTC)
	invalidPub := alarmEvaluationPendingPublication(now)
	invalidPub.EventID = "01910000-a000-7000-8000-000000000011"
	invalidPub.Snapshot.BusinessRevision = 99

	permPub := alarmEvaluationPendingPublication(now)
	permPub.EventID = "01910000-a000-7000-8000-000000000012"

	exhaustedPub := alarmEvaluationPendingPublication(now)
	exhaustedPub.EventID = "01910000-a000-7000-8000-000000000013"
	exhaustedPub.AlarmAttempts = 2

	registry := observability.NewRegistry()
	repository := &alarmEvaluationRelayRepositoryStub{
		pending:         []PendingPublication{invalidPub, permPub, exhaustedPub},
		pendingCount:    4,
		deadLetterCount: 3,
	}
	transport := &alarmEvaluationTransportStub{
		fn: func(_ context.Context, pub PendingPublication) error {
			if pub.EventID == permPub.EventID {
				return fmt.Errorf("%w: HTTP 400", ErrPermanentAlarmEvaluationFailure)
			}
			return errors.New("temporary 503")
		},
	}
	relay, err := NewAlarmEvaluationRelay(AlarmEvaluationRelayConfig{
		Repository: repository, Transport: transport, Metrics: registry,
		WorkerID: "relay-metrics", LeaseDuration: 30 * time.Second, RetryDelay: 5 * time.Second, MaxAttempts: 2,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := relay.RelayOnce(context.Background(), 10); err == nil {
		t.Fatal("expected joined permanent errors")
	}

	rec := httptest.NewRecorder()
	registry.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rec.Body.String()
	for _, want := range []string{
		`hvac_alarm_evaluation_dead_letter_total{reason_family="scope"} 1`,
		`hvac_alarm_evaluation_dead_letter_total{reason_family="dependency"} 1`,
		`hvac_alarm_evaluation_dead_letter_total{reason_family="transport"} 1`,
		`hvac_alarm_evaluation_outbox_depth{state="pending"} 4`,
		`hvac_alarm_evaluation_outbox_depth{state="dead_letter"} 3`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing metric line %q in:\n%s", want, body)
		}
	}
}
