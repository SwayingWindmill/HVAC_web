package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	maximumCentrifugoResponseSize    = 64 << 10
	maximumCentrifugoPublicationSize = 64 << 10
)

type CentrifugoTransportConfig struct {
	BaseURL    string
	APIKey     string
	HTTPClient *http.Client
}

type CentrifugoTransport struct {
	baseURL string
	apiKey  string
	client  *http.Client
}

type centrifugoAPIResponse struct {
	Result json.RawMessage     `json:"result"`
	Error  *centrifugoAPIError `json:"error"`
}

type centrifugoAPIError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func NewCentrifugoTransport(config CentrifugoTransportConfig) (*CentrifugoTransport, error) {
	parsed, err := url.Parse(strings.TrimSpace(config.BaseURL))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && !(parsed.Scheme == "http" && isLoopbackHost(parsed.Hostname()))) {
		return nil, errors.New("Centrifugo server API must use HTTPS or loopback HTTP")
	}
	if strings.TrimSpace(config.APIKey) == "" || config.HTTPClient == nil {
		return nil, errors.New("Centrifugo server API configuration is incomplete")
	}
	return &CentrifugoTransport{
		baseURL: strings.TrimRight(parsed.String(), "/"),
		apiKey:  strings.TrimSpace(config.APIKey),
		client:  config.HTTPClient,
	}, nil
}

func (transport *CentrifugoTransport) Publish(ctx context.Context, channel string, publication DeviceObservationPublication) error {
	if transport == nil || transport.client == nil || strings.TrimSpace(channel) == "" {
		return ErrRealtimeUnavailable
	}
	encoded, err := json.Marshal(publication)
	if err != nil || len(encoded) == 0 || len(encoded) > maximumCentrifugoPublicationSize {
		return ErrRealtimeUnavailable
	}
	return transport.call(ctx, "/api/publish", map[string]any{
		"channel": channel,
		"data":    publication,
	})
}

func (transport *CentrifugoTransport) Unsubscribe(ctx context.Context, principalID, channel string) error {
	if transport == nil || transport.client == nil || strings.TrimSpace(principalID) == "" || strings.TrimSpace(channel) == "" {
		return ErrRealtimeUnavailable
	}
	return transport.call(ctx, "/api/unsubscribe", map[string]any{
		"user":    principalID,
		"channel": channel,
		"code":    2501,
		"reason":  "scope revoked",
	})
}

func (transport *CentrifugoTransport) call(ctx context.Context, path string, input any) error {
	payload, err := json.Marshal(input)
	if err != nil {
		return ErrRealtimeUnavailable
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, transport.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return ErrRealtimeUnavailable
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-API-Key", transport.apiKey)
	response, err := transport.client.Do(request)
	if err != nil {
		return ErrRealtimeUnavailable
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maximumCentrifugoResponseSize+1))
	if err != nil || len(body) > maximumCentrifugoResponseSize || response.StatusCode != http.StatusOK {
		return ErrRealtimeUnavailable
	}
	var decoded centrifugoAPIResponse
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil || ensureJSONEOF(decoder) != nil || decoded.Error != nil || len(decoded.Result) == 0 {
		return ErrRealtimeUnavailable
	}
	return nil
}

type RecordingRealtimeTransport struct {
	Publications     []RecordedPublication
	Unsubscribes     []RecordedUnsubscribe
	PublishError     error
	UnsubscribeError error
}

type RecordedPublication struct {
	Channel     string
	Publication DeviceObservationPublication
}

type RecordedUnsubscribe struct {
	PrincipalID string
	Channel     string
}

func (transport *RecordingRealtimeTransport) Publish(_ context.Context, channel string, publication DeviceObservationPublication) error {
	if transport.PublishError != nil {
		return transport.PublishError
	}
	transport.Publications = append(transport.Publications, RecordedPublication{Channel: channel, Publication: publication})
	return nil
}

func (transport *RecordingRealtimeTransport) Unsubscribe(_ context.Context, principalID, channel string) error {
	if transport.UnsubscribeError != nil {
		return transport.UnsubscribeError
	}
	transport.Unsubscribes = append(transport.Unsubscribes, RecordedUnsubscribe{PrincipalID: principalID, Channel: channel})
	return nil
}

func NewBoundedCentrifugoHTTPClient(transport http.RoundTripper) *http.Client {
	if transport == nil {
		transport = http.DefaultTransport
	}
	return &http.Client{
		Timeout:       3 * time.Second,
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

var _ RealtimeTransport = (*CentrifugoTransport)(nil)
var _ RealtimeTransport = (*RecordingRealtimeTransport)(nil)
