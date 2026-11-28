package optimization

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

	"github.com/quanlaihe/hvac-web/libs/clickhousehttp"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/workloadtls"
)

const (
	InternalControlEventsPath = "/internal/v1/alarms/control-events"

	ControlEventSetpointTrackingDeviation = "SETPOINT_TRACKING_DEVIATION"
	ControlEventAutoControlRollbackFailed = "AUTO_CONTROL_ROLLBACK_FAILED"

	defaultControlEventTimeout = 3 * time.Second
)

// ControlEvent mirrors alarmservice.ControlEventRequest for schemaVersion 1 internal alarm events.
type ControlEvent struct {
	SchemaVersion int     `json:"schemaVersion"`
	TenantID      string  `json:"tenantId"`
	SiteID        string  `json:"siteId"`
	DeviceID      *string `json:"deviceId,omitempty"`
	PointID       *string `json:"pointId,omitempty"`
	CommandID     string  `json:"commandId"`
	EventKind     string  `json:"eventKind"`
	Capability    string  `json:"capability,omitempty"`
	Title         string  `json:"title,omitempty"`
	Summary       string  `json:"summary,omitempty"`
	Severity      string  `json:"severity,omitempty"`
	ReasonCode    string  `json:"reasonCode,omitempty"`
	Detail        string  `json:"detail,omitempty"`
	OccurredAt    string  `json:"occurredAt,omitempty"`
	ActorType     string  `json:"actorType,omitempty"`
	ActorID       string  `json:"actorId,omitempty"`
	CorrelationID string  `json:"correlationId,omitempty"`
}

type ControlEventPublisher interface {
	PublishControlEvent(ctx context.Context, event ControlEvent) error
}

type ControlEventClientConfig struct {
	CertificatePath string
	PrivateKeyPath  string
	CAPath          string
	AlarmURL        string
	ServerName      string
	Timeout         time.Duration
	HTTPClient      *http.Client
}

type HTTPControlEventPublisher struct {
	endpoint   string
	httpClient *http.Client
	timeout    time.Duration
}

func NewHTTPControlEventPublisher(config ControlEventClientConfig) (*HTTPControlEventPublisher, error) {
	rawURL := strings.TrimSpace(config.AlarmURL)
	if rawURL == "" {
		return nil, errors.New("control event publisher requires AlarmURL")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" || parsed.Hostname() == "" {
		return nil, fmt.Errorf("invalid AlarmURL %q", rawURL)
	}
	cleanPath := strings.TrimRight(parsed.Path, "/")
	if cleanPath == "" {
		parsed.Path = InternalControlEventsPath
	} else if !strings.HasSuffix(cleanPath, InternalControlEventsPath) {
		parsed.Path = cleanPath + InternalControlEventsPath
	}

	timeout := config.Timeout
	if timeout <= 0 {
		timeout = defaultControlEventTimeout
	}
	httpClient := config.HTTPClient
	if httpClient == nil {
		if parsed.Scheme == "https" && strings.TrimSpace(config.CAPath) != "" {
			var certs *workloadtls.CertificateFiles
			certPath := strings.TrimSpace(config.CertificatePath)
			keyPath := strings.TrimSpace(config.PrivateKeyPath)
			if certPath != "" && keyPath != "" {
				certs = &workloadtls.CertificateFiles{
					CertificatePath: certPath,
					PrivateKeyPath:  keyPath,
				}
			}
			serverName := strings.TrimSpace(config.ServerName)
			if serverName == "" {
				serverName = parsed.Hostname()
			}
			httpClient, err = workloadtls.NewHTTPClient(workloadtls.ClientConfig{
				CertificateFiles: certs,
				ServerCAPath:     config.CAPath,
				ServerName:       serverName,
				Timeout:          timeout,
			})
			if err != nil {
				return nil, fmt.Errorf("create alarm control-event mTLS client: %w", err)
			}
		} else {
			httpClient = clickhousehttp.NewPooledHTTPClient(clickhousehttp.ServiceTransportConfig(timeout))
		}
	}

	return &HTTPControlEventPublisher{
		endpoint:   parsed.String(),
		httpClient: httpClient,
		timeout:    timeout,
	}, nil
}

func (publisher *HTTPControlEventPublisher) PublishControlEvent(ctx context.Context, event ControlEvent) error {
	if publisher == nil || publisher.httpClient == nil {
		return errors.New("control event publisher is not configured")
	}
	if event.SchemaVersion == 0 {
		event.SchemaVersion = 1
	}
	event.TenantID = strings.TrimSpace(event.TenantID)
	event.SiteID = strings.TrimSpace(event.SiteID)
	event.CommandID = strings.TrimSpace(event.CommandID)
	event.EventKind = strings.ToUpper(strings.TrimSpace(event.EventKind))
	if event.TenantID == "" || event.SiteID == "" || event.CommandID == "" || event.EventKind == "" {
		return errors.New("control event requires tenantId, siteId, commandId and eventKind")
	}
	if strings.TrimSpace(event.OccurredAt) == "" {
		event.OccurredAt = time.Now().UTC().Format(time.RFC3339Nano)
	}

	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("encode control event: %w", err)
	}
	timeout := publisher.timeout
	if timeout <= 0 {
		timeout = defaultControlEventTimeout
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, publisher.endpoint, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build control event request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, application/problem+json")
	observability.InjectHTTP(reqCtx, req.Header)

	resp, err := publisher.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("post control event to alarm-service: %w", err)
	}
	defer clickhousehttp.DrainAndClose(resp.Body)
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if readErr != nil {
		return fmt.Errorf("read control event response: %w", readErr)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("alarm control event endpoint returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}
