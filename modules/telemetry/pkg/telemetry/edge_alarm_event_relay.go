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
	"github.com/quanlaihe/hvac-web/libs/uuidv7"
)

const InternalEdgeAlarmEventsPath = "/internal/v1/alarms/edge-events"

var (
	ErrEdgeAlarmEventRelayUnavailable = errors.New("edge alarm event relay unavailable")
	ErrPermanentEdgeAlarmEventFailure = errors.New("edge alarm event permanent failure")
)

type PendingEdgeAlarmEvent struct {
	MessageID      string
	TenantID       string
	SiteID         string
	GatewayID      string
	EventType      string
	SourceType     string
	SourceID       string
	EventTime      time.Time
	Severity       string
	SourceSequence int64
	Data           json.RawMessage
	ReceivedAt     time.Time
	AlarmAttempts  int
}

type EdgeAlarmEventRepository interface {
	ClaimPendingEdgeAlarmEvents(context.Context, string, int, time.Time, time.Duration) ([]PendingEdgeAlarmEvent, error)
	MarkEdgeAlarmEventDelivered(context.Context, string, string, time.Time) error
	MarkEdgeAlarmEventRetry(context.Context, string, string, time.Time, string) error
	MarkEdgeAlarmEventDeadLetter(context.Context, string, string, time.Time, string) error
}

// EdgeAlarmEventOutboxStateCounter optionally exposes total pending and dead-letter row counts.
type EdgeAlarmEventOutboxStateCounter interface {
	CountEdgeAlarmEventOutboxStates(ctx context.Context) (pending int64, deadLetter int64, err error)
}

type EdgeAlarmEventTransport interface {
	DeliverEdgeAlarmEvent(context.Context, PendingEdgeAlarmEvent) error
}

type EdgeAlarmEventRelayConfig struct {
	Repository      EdgeAlarmEventRepository
	Transport       EdgeAlarmEventTransport
	Metrics         *observability.Registry
	WorkerID        string
	LeaseDuration   time.Duration
	RetryDelay      time.Duration
	MaxAttempts     int
	DeliveryTimeout time.Duration
	Now             func() time.Time
}

type EdgeAlarmEventRelay struct {
	repository      EdgeAlarmEventRepository
	transport       EdgeAlarmEventTransport
	metrics         *PipelineMetrics
	workerID        string
	leaseDuration   time.Duration
	retryDelay      time.Duration
	maxAttempts     int
	deliveryTimeout time.Duration
	now             func() time.Time
}

// NewEdgeAlarmEventRelay constructs a bounded lease-based relay for MQTT edge rule alarm events.
func NewEdgeAlarmEventRelay(config EdgeAlarmEventRelayConfig) (*EdgeAlarmEventRelay, error) {
	if config.Repository == nil || config.Transport == nil || strings.TrimSpace(config.WorkerID) == "" || config.LeaseDuration <= 0 || config.RetryDelay <= 0 || config.MaxAttempts < 0 || config.DeliveryTimeout < 0 {
		return nil, errors.New("edge alarm event relay configuration is invalid")
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
	return &EdgeAlarmEventRelay{
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

// RelayOnce claims due edge rule alarm events in chronological order and delivers them to alarm-service.
func (relay *EdgeAlarmEventRelay) RelayOnce(ctx context.Context, limit int) (int, error) {
	if relay == nil || relay.repository == nil || relay.transport == nil {
		return 0, ErrEdgeAlarmEventRelayUnavailable
	}
	if limit <= 0 || limit > 256 {
		limit = 64
	}
	now := relay.now().UTC()
	pending, err := relay.repository.ClaimPendingEdgeAlarmEvents(ctx, relay.workerID, limit, now, relay.leaseDuration)
	if err != nil {
		return 0, fmt.Errorf("claim pending edge Alarm events: %w", err)
	}
	defer relay.refreshOutboxDepths(ctx)
	delivered := 0
	var deadLetterErrs []error
	for _, event := range pending {
		if !validPendingEdgeAlarmEvent(event) {
			if dlErr := relay.repository.MarkEdgeAlarmEventDeadLetter(ctx, event.MessageID, relay.workerID, now, "EDGE_ALARM_EVENT_PAYLOAD_INVALID"); dlErr != nil {
				return delivered, fmt.Errorf("dead-letter invalid edge Alarm event: %w", dlErr)
			}
			relay.metrics.AddEdgeAlarmEventDeadLetter("payload_invalid")
			deadLetterErrs = append(deadLetterErrs, fmt.Errorf("%w: message %s payload is inconsistent", ErrPermanentEdgeAlarmEventFailure, event.MessageID))
			continue
		}
		if event.AlarmAttempts >= relay.maxAttempts {
			if dlErr := relay.repository.MarkEdgeAlarmEventDeadLetter(ctx, event.MessageID, relay.workerID, now, "EDGE_ALARM_EVENT_MAX_ATTEMPTS_EXCEEDED"); dlErr != nil {
				return delivered, fmt.Errorf("dead-letter exhausted edge Alarm event: %w", dlErr)
			}
			relay.metrics.AddEdgeAlarmEventDeadLetter("max_attempts_exceeded")
			deadLetterErrs = append(deadLetterErrs, fmt.Errorf("%w: message %s reached max attempts (%d)", ErrPermanentEdgeAlarmEventFailure, event.MessageID, relay.maxAttempts))
			continue
		}
		if err := relay.deliverWithTimeout(ctx, event); err != nil {
			if errors.Is(err, ErrPermanentEdgeAlarmEventFailure) {
				if dlErr := relay.repository.MarkEdgeAlarmEventDeadLetter(ctx, event.MessageID, relay.workerID, now, "EDGE_ALARM_EVENT_PERMANENT_FAILURE"); dlErr != nil {
					return delivered, fmt.Errorf("dead-letter permanently failed edge Alarm event after %v: %w", err, dlErr)
				}
				relay.metrics.AddEdgeAlarmEventDeadLetter("permanent_failure")
				deadLetterErrs = append(deadLetterErrs, err)
				continue
			}
			nextAttempt := event.AlarmAttempts + 1
			if nextAttempt >= relay.maxAttempts {
				if dlErr := relay.repository.MarkEdgeAlarmEventDeadLetter(ctx, event.MessageID, relay.workerID, now, "EDGE_ALARM_EVENT_MAX_ATTEMPTS_EXCEEDED"); dlErr != nil {
					return delivered, fmt.Errorf("dead-letter edge Alarm event after max attempts (%v): %w", err, dlErr)
				}
				relay.metrics.AddEdgeAlarmEventDeadLetter("max_attempts_exceeded")
				deadLetterErrs = append(deadLetterErrs, fmt.Errorf("%w: %w", ErrPermanentEdgeAlarmEventFailure, err))
				continue
			}
			delay := alarmEvaluationRetryDelay(relay.retryDelay, nextAttempt)
			if retryErr := relay.repository.MarkEdgeAlarmEventRetry(ctx, event.MessageID, relay.workerID, now.Add(delay), "EDGE_ALARM_EVENT_UNAVAILABLE"); retryErr != nil {
				return delivered, fmt.Errorf("record edge Alarm event retry after %v: %w", err, retryErr)
			}
			return delivered, err
		}
		if err := relay.repository.MarkEdgeAlarmEventDelivered(ctx, event.MessageID, relay.workerID, now); err != nil {
			return delivered, fmt.Errorf("mark edge Alarm event delivered: %w", err)
		}
		delivered++
	}
	return delivered, errors.Join(deadLetterErrs...)
}

func (relay *EdgeAlarmEventRelay) refreshOutboxDepths(ctx context.Context) {
	if relay == nil || relay.metrics == nil || ctx.Err() != nil {
		return
	}
	counter, ok := relay.repository.(EdgeAlarmEventOutboxStateCounter)
	if !ok {
		return
	}
	depthCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	pending, deadLetter, err := counter.CountEdgeAlarmEventOutboxStates(depthCtx)
	if err == nil {
		relay.metrics.SetEdgeAlarmEventOutboxDepths(pending, deadLetter)
	}
}

func (relay *EdgeAlarmEventRelay) deliverWithTimeout(ctx context.Context, event PendingEdgeAlarmEvent) error {
	deliveryCtx, cancel := context.WithTimeout(ctx, relay.deliveryTimeout)
	defer cancel()
	return relay.transport.DeliverEdgeAlarmEvent(deliveryCtx, event)
}

func validPendingEdgeAlarmEvent(event PendingEdgeAlarmEvent) bool {
	return uuidv7.IsValid(strings.ToLower(strings.TrimSpace(event.MessageID))) &&
		uuidv7.IsValid(strings.ToLower(strings.TrimSpace(event.TenantID))) &&
		uuidv7.IsValid(strings.ToLower(strings.TrimSpace(event.SiteID))) &&
		strings.TrimSpace(event.GatewayID) != "" &&
		IsEdgeRuleAlarmEventType(event.EventType) &&
		strings.TrimSpace(event.SourceID) != "" &&
		event.SourceSequence >= 0 &&
		!event.EventTime.IsZero() &&
		!event.ReceivedAt.IsZero() &&
		json.Valid(event.Data)
}

// DeriveEdgeAlarmEventsURL derives the alarm-service edge-events ingestion endpoint
// from TELEMETRY_ALARM_EVALUATION_URL unless an explicit valid HTTPS override is configured.
func DeriveEdgeAlarmEventsURL(alarmEvalURL, override string) (string, error) {
	if trimmed := strings.TrimSpace(override); trimmed != "" {
		parsed, err := url.Parse(trimmed)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
			return "", errors.New("edge alarm events endpoint URL is invalid")
		}
		return parsed.String(), nil
	}
	parsed, err := url.Parse(strings.TrimSpace(alarmEvalURL))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("alarm evaluation endpoint URL is invalid")
	}
	parsed.Path = InternalEdgeAlarmEventsPath
	return parsed.String(), nil
}

type HTTPEdgeAlarmEventTransportConfig struct {
	Endpoint   string
	HTTPClient *http.Client
}

type HTTPEdgeAlarmEventTransport struct {
	endpoint string
	client   *http.Client
}

func NewHTTPEdgeAlarmEventTransport(config HTTPEdgeAlarmEventTransportConfig) (*HTTPEdgeAlarmEventTransport, error) {
	endpoint := strings.TrimSpace(config.Endpoint)
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" || config.HTTPClient == nil {
		return nil, errors.New("edge alarm event HTTP transport configuration is invalid")
	}
	return &HTTPEdgeAlarmEventTransport{endpoint: endpoint, client: config.HTTPClient}, nil
}

func (transport *HTTPEdgeAlarmEventTransport) DeliverEdgeAlarmEvent(ctx context.Context, event PendingEdgeAlarmEvent) error {
	if transport == nil || transport.client == nil {
		return ErrEdgeAlarmEventRelayUnavailable
	}
	payload := struct {
		SchemaVersion  int             `json:"schemaVersion"`
		MessageID      string          `json:"messageId"`
		TenantID       string          `json:"tenantId"`
		SiteID         string          `json:"siteId"`
		GatewayID      string          `json:"gatewayId"`
		EventType      string          `json:"eventType"`
		SourceType     string          `json:"sourceType"`
		SourceID       string          `json:"sourceId"`
		EventTime      string          `json:"eventTime"`
		Severity       string          `json:"severity"`
		SourceSequence int64           `json:"sourceSequence"`
		ReceivedAt     string          `json:"receivedAt"`
		Data           json.RawMessage `json:"data"`
	}{
		SchemaVersion:  1,
		MessageID:      event.MessageID,
		TenantID:       event.TenantID,
		SiteID:         event.SiteID,
		GatewayID:      event.GatewayID,
		EventType:      event.EventType,
		SourceType:     event.SourceType,
		SourceID:       event.SourceID,
		EventTime:      event.EventTime.UTC().Format(time.RFC3339Nano),
		Severity:       event.Severity,
		SourceSequence: event.SourceSequence,
		ReceivedAt:     event.ReceivedAt.UTC().Format(time.RFC3339Nano),
		Data:           event.Data,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode edge Alarm event request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, transport.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build edge Alarm event request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := transport.client.Do(request)
	if err != nil {
		return fmt.Errorf("send edge Alarm event request: %w", err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
	if response.StatusCode >= http.StatusBadRequest && response.StatusCode < http.StatusInternalServerError &&
		response.StatusCode != http.StatusRequestTimeout && response.StatusCode != http.StatusTooManyRequests {
		return fmt.Errorf("%w: HTTP %d", ErrPermanentEdgeAlarmEventFailure, response.StatusCode)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("edge alarm event endpoint returned HTTP %d", response.StatusCode)
	}
	return nil
}
