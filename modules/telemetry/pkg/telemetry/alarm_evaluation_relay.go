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
)

var ErrAlarmEvaluationRelayUnavailable = errors.New("alarm evaluation relay unavailable")

type AlarmEvaluationRepository interface {
	ClaimPendingAlarmEvaluations(context.Context, string, int, time.Time, time.Duration) ([]PendingPublication, error)
	MarkAlarmEvaluationDelivered(context.Context, string, string, time.Time) error
	MarkAlarmEvaluationRetry(context.Context, string, string, time.Time, string) error
}

type AlarmEvaluationTransport interface {
	EvaluateSnapshot(context.Context, PendingPublication) error
}

type AlarmEvaluationRelayConfig struct {
	Repository    AlarmEvaluationRepository
	Transport     AlarmEvaluationTransport
	WorkerID      string
	LeaseDuration time.Duration
	RetryDelay    time.Duration
	Now           func() time.Time
}

type AlarmEvaluationRelay struct {
	repository    AlarmEvaluationRepository
	transport     AlarmEvaluationTransport
	workerID      string
	leaseDuration time.Duration
	retryDelay    time.Duration
	now           func() time.Time
}

func NewAlarmEvaluationRelay(config AlarmEvaluationRelayConfig) (*AlarmEvaluationRelay, error) {
	if config.Repository == nil || config.Transport == nil || strings.TrimSpace(config.WorkerID) == "" || config.LeaseDuration <= 0 || config.RetryDelay <= 0 {
		return nil, errors.New("alarm evaluation relay configuration is invalid")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &AlarmEvaluationRelay{
		repository: config.Repository, transport: config.Transport, workerID: strings.TrimSpace(config.WorkerID),
		leaseDuration: config.LeaseDuration, retryDelay: config.RetryDelay, now: config.Now,
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
	delivered := 0
	for _, publication := range pending {
		if publication.Revision < 1 || publication.Revision != publication.PreviousRevision+1 || string(publication.Snapshot.DeviceId) != publication.DeviceID || int64(publication.Snapshot.BusinessRevision) != publication.Revision {
			return delivered, ErrAlarmEvaluationRelayUnavailable
		}
		if err := relay.transport.EvaluateSnapshot(ctx, publication); err != nil {
			if retryErr := relay.repository.MarkAlarmEvaluationRetry(ctx, publication.EventID, relay.workerID, now.Add(relay.retryDelay), "ALARM_EVALUATION_UNAVAILABLE"); retryErr != nil {
				return delivered, fmt.Errorf("record Alarm evaluation retry after %v: %w", err, retryErr)
			}
			return delivered, err
		}
		if err := relay.repository.MarkAlarmEvaluationDelivered(ctx, publication.EventID, relay.workerID, now); err != nil {
			return delivered, fmt.Errorf("mark Alarm evaluation delivered: %w", err)
		}
		delivered++
	}
	return delivered, nil
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
		return nil, errors.New("Alarm evaluation HTTP transport configuration is invalid")
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
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("Alarm evaluation endpoint returned HTTP %d", response.StatusCode)
	}
	return nil
}
