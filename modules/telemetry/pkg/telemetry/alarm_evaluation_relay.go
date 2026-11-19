package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
)

const (
	DefaultAlarmEvaluationMaxAttempts     = 10
	DefaultAlarmEvaluationDeliveryTimeout = 10 * time.Second
	MaxAlarmEvaluationRetryDelay          = 15 * time.Minute
)

var (
	ErrAlarmEvaluationRelayUnavailable   = errors.New("alarm evaluation relay unavailable")
	ErrPermanentAlarmEvaluationFailure   = errors.New("alarm evaluation permanent failure")
	ErrAlarmEvaluationDeadLetterNotFound = errors.New("alarm evaluation dead letter not found")
)

type AlarmEvaluationRepository interface {
	ClaimPendingAlarmEvaluations(context.Context, string, int, time.Time, time.Duration) ([]PendingPublication, error)
	MarkAlarmEvaluationDelivered(context.Context, string, string, time.Time) error
	MarkAlarmEvaluationRetry(context.Context, string, string, time.Time, string) error
	MarkAlarmEvaluationDeadLetter(context.Context, string, string, time.Time, string) error
}

// AlarmEvaluationOutboxStateCounter optionally exposes total pending and dead-letter row counts.
type AlarmEvaluationOutboxStateCounter interface {
	CountAlarmEvaluationOutboxStates(ctx context.Context) (pending int64, deadLetter int64, err error)
}

type AlarmEvaluationTransport interface {
	EvaluateSnapshot(context.Context, PendingPublication) error
}

type AlarmEvaluationRelayConfig struct {
	Repository      AlarmEvaluationRepository
	Transport       AlarmEvaluationTransport
	Metrics         *observability.Registry
	WorkerID        string
	LeaseDuration   time.Duration
	RetryDelay      time.Duration
	MaxAttempts     int
	DeliveryTimeout time.Duration
	Now             func() time.Time
}

type AlarmEvaluationRelay struct {
	repository      AlarmEvaluationRepository
	transport       AlarmEvaluationTransport
	metrics         *PipelineMetrics
	workerID        string
	leaseDuration   time.Duration
	retryDelay      time.Duration
	maxAttempts     int
	deliveryTimeout time.Duration
	now             func() time.Time
}

func NewAlarmEvaluationRelay(config AlarmEvaluationRelayConfig) (*AlarmEvaluationRelay, error) {
	if config.Repository == nil || config.Transport == nil || strings.TrimSpace(config.WorkerID) == "" || config.LeaseDuration <= 0 || config.RetryDelay <= 0 || config.MaxAttempts < 0 || config.DeliveryTimeout < 0 {
		return nil, errors.New("alarm evaluation relay configuration is invalid")
	}
	maxAttempts := config.MaxAttempts
	if maxAttempts == 0 {
		maxAttempts = DefaultAlarmEvaluationMaxAttempts
	}
	deliveryTimeout := config.DeliveryTimeout
	if deliveryTimeout == 0 {
		deliveryTimeout = DefaultAlarmEvaluationDeliveryTimeout
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &AlarmEvaluationRelay{
		repository:      config.Repository,
		transport:       config.Transport,
		metrics:         NewPipelineMetrics(config.Metrics),
		workerID:        strings.TrimSpace(config.WorkerID),
		leaseDuration:   config.LeaseDuration,
		retryDelay:      config.RetryDelay,
		maxAttempts:     maxAttempts,
		deliveryTimeout: deliveryTimeout,
		now:             config.Now,
	}, nil
}

func (relay *AlarmEvaluationRelay) RelayOnce(ctx context.Context, limit int) (int, error) {
	if relay == nil || relay.repository == nil || relay.transport == nil {
		return 0, ErrAlarmEvaluationRelayUnavailable
	}
	if limit <= 0 || limit > 256 {
		limit = 64
	}
	now := relay.now().UTC()
	pending, err := relay.repository.ClaimPendingAlarmEvaluations(ctx, relay.workerID, limit, now, relay.leaseDuration)
	if err != nil {
		return 0, fmt.Errorf("claim Alarm evaluation publications: %w", err)
	}
	defer relay.refreshOutboxDepths(ctx)
	delivered := 0
	var deadLetterErrs []error
	for _, publication := range pending {
		if !validAlarmEvaluationPublication(publication) {
			if dlErr := relay.repository.MarkAlarmEvaluationDeadLetter(ctx, publication.EventID, relay.workerID, now, "ALARM_EVALUATION_PAYLOAD_INVALID"); dlErr != nil {
				return delivered, fmt.Errorf("dead-letter invalid Alarm evaluation publication: %w", dlErr)
			}
			relay.metrics.AddAlarmEvaluationDeadLetter("payload_invalid")
			deadLetterErrs = append(deadLetterErrs, fmt.Errorf("%w: publication %s payload is inconsistent", ErrPermanentAlarmEvaluationFailure, publication.EventID))
			continue
		}
		if publication.AlarmAttempts >= relay.maxAttempts {
			if dlErr := relay.repository.MarkAlarmEvaluationDeadLetter(ctx, publication.EventID, relay.workerID, now, "ALARM_EVALUATION_MAX_ATTEMPTS_EXCEEDED"); dlErr != nil {
				return delivered, fmt.Errorf("dead-letter exhausted Alarm evaluation publication: %w", dlErr)
			}
			relay.metrics.AddAlarmEvaluationDeadLetter("max_attempts_exceeded")
			deadLetterErrs = append(deadLetterErrs, fmt.Errorf("%w: publication %s reached max attempts (%d)", ErrPermanentAlarmEvaluationFailure, publication.EventID, relay.maxAttempts))
			continue
		}
		if err := relay.evaluateWithTimeout(ctx, publication); err != nil {
			if errors.Is(err, ErrPermanentAlarmEvaluationFailure) {
				if dlErr := relay.repository.MarkAlarmEvaluationDeadLetter(ctx, publication.EventID, relay.workerID, now, "ALARM_EVALUATION_PERMANENT_FAILURE"); dlErr != nil {
					return delivered, fmt.Errorf("dead-letter permanently failed Alarm evaluation after %v: %w", err, dlErr)
				}
				relay.metrics.AddAlarmEvaluationDeadLetter("permanent_failure")
				deadLetterErrs = append(deadLetterErrs, err)
				continue
			}
			nextAttempt := publication.AlarmAttempts + 1
			if nextAttempt >= relay.maxAttempts {
				if dlErr := relay.repository.MarkAlarmEvaluationDeadLetter(ctx, publication.EventID, relay.workerID, now, "ALARM_EVALUATION_MAX_ATTEMPTS_EXCEEDED"); dlErr != nil {
					return delivered, fmt.Errorf("dead-letter Alarm evaluation after max attempts (%v): %w", err, dlErr)
				}
				relay.metrics.AddAlarmEvaluationDeadLetter("max_attempts_exceeded")
				deadLetterErrs = append(deadLetterErrs, fmt.Errorf("%w: %w", ErrPermanentAlarmEvaluationFailure, err))
				continue
			}
			delay := alarmEvaluationRetryDelay(relay.retryDelay, nextAttempt)
			if retryErr := relay.repository.MarkAlarmEvaluationRetry(ctx, publication.EventID, relay.workerID, now.Add(delay), "ALARM_EVALUATION_UNAVAILABLE"); retryErr != nil {
				return delivered, fmt.Errorf("record Alarm evaluation retry after %v: %w", err, retryErr)
			}
			return delivered, err
		}
		if err := relay.repository.MarkAlarmEvaluationDelivered(ctx, publication.EventID, relay.workerID, now); err != nil {
			return delivered, fmt.Errorf("mark Alarm evaluation delivered: %w", err)
		}
		delivered++
	}
	return delivered, errors.Join(deadLetterErrs...)
}

func (relay *AlarmEvaluationRelay) refreshOutboxDepths(ctx context.Context) {
	if relay == nil || relay.metrics == nil || ctx.Err() != nil {
		return
	}
	counter, ok := relay.repository.(AlarmEvaluationOutboxStateCounter)
	if !ok {
		return
	}
	depthCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	pending, deadLetter, err := counter.CountAlarmEvaluationOutboxStates(depthCtx)
	if err == nil {
		relay.metrics.SetAlarmEvaluationOutboxDepths(pending, deadLetter)
	}
}

func (relay *AlarmEvaluationRelay) evaluateWithTimeout(ctx context.Context, publication PendingPublication) error {
	deliveryCtx, cancel := context.WithTimeout(ctx, relay.deliveryTimeout)
	defer cancel()
	return relay.transport.EvaluateSnapshot(deliveryCtx, publication)
}

func validAlarmEvaluationPublication(publication PendingPublication) bool {
	return publication.Revision >= 1 &&
		publication.Revision == publication.PreviousRevision+1 &&
		string(publication.Snapshot.DeviceId) == publication.DeviceID &&
		int64(publication.Snapshot.BusinessRevision) == publication.Revision
}

func alarmEvaluationRetryDelay(base time.Duration, attempt int) time.Duration {
	if base <= 0 {
		return 0
	}
	if attempt <= 1 {
		if base > MaxAlarmEvaluationRetryDelay {
			return MaxAlarmEvaluationRetryDelay
		}
		return base
	}
	delay := base
	for i := 1; i < attempt; i++ {
		if delay >= MaxAlarmEvaluationRetryDelay/2 {
			return MaxAlarmEvaluationRetryDelay
		}
		delay *= 2
	}
	if delay > MaxAlarmEvaluationRetryDelay {
		return MaxAlarmEvaluationRetryDelay
	}
	return delay
}

type HTTPAlarmEvaluationTransportConfig struct {
	Endpoint   string
	HTTPClient *http.Client
}

type HTTPAlarmEvaluationTransport struct {
	endpoint string
	client   *http.Client
}

func NewHTTPAlarmEvaluationTransport(config HTTPAlarmEvaluationTransportConfig) (*HTTPAlarmEvaluationTransport, error) {
	endpoint := strings.TrimSpace(config.Endpoint)
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" || config.HTTPClient == nil {
		return nil, errors.New("alarm evaluation HTTP transport configuration is invalid")
	}
	return &HTTPAlarmEvaluationTransport{endpoint: endpoint, client: config.HTTPClient}, nil
}

func (transport *HTTPAlarmEvaluationTransport) EvaluateSnapshot(ctx context.Context, publication PendingPublication) error {
	if transport == nil || transport.client == nil {
		return ErrAlarmEvaluationRelayUnavailable
	}
	body, err := json.Marshal(struct {
		SchemaVersion int    `json:"schemaVersion"`
		EventID       string `json:"eventId"`
		Snapshot      any    `json:"snapshot"`
	}{SchemaVersion: 1, EventID: publication.EventID, Snapshot: publication.Snapshot})
	if err != nil {
		return fmt.Errorf("encode Alarm evaluation request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, transport.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build Alarm evaluation request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := transport.client.Do(request)
	if err != nil {
		return fmt.Errorf("send Alarm evaluation request: %w", err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
	if response.StatusCode >= http.StatusBadRequest && response.StatusCode < http.StatusInternalServerError &&
		response.StatusCode != http.StatusRequestTimeout && response.StatusCode != http.StatusTooManyRequests {
		return fmt.Errorf("%w: HTTP %d", ErrPermanentAlarmEvaluationFailure, response.StatusCode)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("alarm evaluation endpoint returned HTTP %d", response.StatusCode)
	}
	return nil
}
