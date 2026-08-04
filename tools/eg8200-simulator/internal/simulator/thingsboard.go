package simulator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const maximumThingsBoardBodyBytes = int64(256 << 10)

type ThingsBoardClient struct {
	baseURL     string
	httpClient  *http.Client
	accessToken map[string]string
}

type RPCCommand struct {
	ID       int64
	DeviceID string
	Method   string
	Params   map[string]float64
}

type rpcEnvelope struct {
	ID     *int64          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

func NewThingsBoardClient(baseURL string, accessToken map[string]string, httpClient *http.Client) (*ThingsBoardClient, error) {
	parsed, err := url.Parse(strings.TrimRight(strings.TrimSpace(baseURL), "/"))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return nil, errors.New("ThingsBoard base URL is invalid")
	}
	if len(accessToken) == 0 {
		return nil, errors.New("ThingsBoard access tokens are required")
	}
	tokens := make(map[string]string, len(accessToken))
	for deviceID, token := range accessToken {
		deviceID = strings.TrimSpace(deviceID)
		token = strings.TrimSpace(token)
		if deviceID == "" || token == "" {
			return nil, errors.New("ThingsBoard access token binding is incomplete")
		}
		tokens[deviceID] = token
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &ThingsBoardClient{baseURL: strings.TrimRight(parsed.String(), "/"), httpClient: httpClient, accessToken: tokens}, nil
}

func (client *ThingsBoardClient) PublishSnapshot(ctx context.Context, snapshot Snapshot) error {
	for deviceID, telemetry := range snapshot.Devices {
		if err := client.PublishTelemetry(ctx, deviceID, snapshot.ObservedAt, telemetry); err != nil {
			return err
		}
	}
	return nil
}

func (client *ThingsBoardClient) PublishMeasurements(ctx context.Context, measurements []Measurement) error {
	for _, measurement := range measurements {
		if err := client.PublishTelemetry(
			ctx,
			measurement.DeviceID,
			measurement.ObservedAt,
			DeviceTelemetry{measurement.SourceKey: measurement.Value},
		); err != nil {
			return err
		}
	}
	return nil
}

func (client *ThingsBoardClient) PublishTelemetry(ctx context.Context, deviceID string, observedAt time.Time, telemetry DeviceTelemetry) error {
	token, ok := client.accessToken[deviceID]
	if !ok {
		return fmt.Errorf("ThingsBoard access token is not configured for %s", deviceID)
	}
	body, err := json.Marshal(map[string]any{
		"ts":     observedAt.UTC().UnixMilli(),
		"values": telemetry,
	})
	if err != nil {
		return fmt.Errorf("encode ThingsBoard telemetry for %s: %w", deviceID, err)
	}
	endpoint := client.baseURL + "/api/v1/" + url.PathEscape(token) + "/telemetry"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create ThingsBoard telemetry request for %s: %w", deviceID, err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("publish ThingsBoard telemetry for %s: provider request failed", deviceID)
	}
	defer response.Body.Close()
	_, readErr := readLimitedBody(response.Body, maximumThingsBoardBodyBytes)
	if readErr != nil {
		return fmt.Errorf("read ThingsBoard telemetry response for %s: %w", deviceID, readErr)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("ThingsBoard telemetry for %s returned %d", deviceID, response.StatusCode)
	}
	return nil
}

func (client *ThingsBoardClient) PollRPC(ctx context.Context, deviceID string, timeout time.Duration) (*RPCCommand, error) {
	token, ok := client.accessToken[deviceID]
	if !ok {
		return nil, fmt.Errorf("ThingsBoard access token is not configured for %s", deviceID)
	}
	if timeout < time.Second || timeout > 30*time.Second {
		return nil, errors.New("ThingsBoard RPC poll timeout must be between 1s and 30s")
	}
	endpoint := client.baseURL + "/api/v1/" + url.PathEscape(token) + "/rpc?timeout=" + strconv.FormatInt(timeout.Milliseconds(), 10)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create ThingsBoard RPC poll request for %s: %w", deviceID, err)
	}
	response, err := client.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("poll ThingsBoard RPC for %s: provider request failed", deviceID)
	}
	defer response.Body.Close()
	responseBody, readErr := readLimitedBody(response.Body, maximumThingsBoardBodyBytes)
	if readErr != nil {
		return nil, fmt.Errorf("read ThingsBoard RPC response for %s: %w", deviceID, readErr)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("ThingsBoard RPC poll for %s returned %d", deviceID, response.StatusCode)
	}
	if len(bytes.TrimSpace(responseBody)) == 0 {
		return nil, nil
	}
	command, err := decodeRPCCommand(deviceID, responseBody)
	if err != nil {
		return nil, err
	}
	return &command, nil
}

func (client *ThingsBoardClient) ReplyRPC(ctx context.Context, deviceID string, rpcID int64, result CommandResult) error {
	token, ok := client.accessToken[deviceID]
	if !ok {
		return fmt.Errorf("ThingsBoard access token is not configured for %s", deviceID)
	}
	if rpcID < 0 {
		return errors.New("ThingsBoard RPC id must not be negative")
	}
	body, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("encode ThingsBoard RPC reply for %s: %w", deviceID, err)
	}
	endpoint := client.baseURL + "/api/v1/" + url.PathEscape(token) + "/rpc/" + strconv.FormatInt(rpcID, 10)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create ThingsBoard RPC reply request for %s: %w", deviceID, err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("reply ThingsBoard RPC for %s: provider request failed", deviceID)
	}
	defer response.Body.Close()
	_, readErr := readLimitedBody(response.Body, maximumThingsBoardBodyBytes)
	if readErr != nil {
		return fmt.Errorf("read ThingsBoard RPC reply response for %s: %w", deviceID, readErr)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("ThingsBoard RPC reply for %s returned %d", deviceID, response.StatusCode)
	}
	return nil
}

func decodeRPCCommand(deviceID string, body []byte) (RPCCommand, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var envelope rpcEnvelope
	if err := decoder.Decode(&envelope); err != nil {
		return RPCCommand{}, fmt.Errorf("decode ThingsBoard RPC for %s: %w", deviceID, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return RPCCommand{}, fmt.Errorf("ThingsBoard RPC for %s contains trailing JSON", deviceID)
	}
	if envelope.ID == nil || *envelope.ID < 0 || strings.TrimSpace(envelope.Method) == "" {
		return RPCCommand{}, fmt.Errorf("ThingsBoard RPC for %s is incomplete", deviceID)
	}
	params := map[string]float64{}
	trimmed := bytes.TrimSpace(envelope.Params)
	if len(trimmed) != 0 && !bytes.Equal(trimmed, []byte("null")) {
		paramsDecoder := json.NewDecoder(bytes.NewReader(trimmed))
		paramsDecoder.DisallowUnknownFields()
		if err := paramsDecoder.Decode(&params); err != nil {
			return RPCCommand{}, fmt.Errorf("decode ThingsBoard RPC params for %s: %w", deviceID, err)
		}
		if err := paramsDecoder.Decode(&trailing); !errors.Is(err, io.EOF) {
			return RPCCommand{}, fmt.Errorf("ThingsBoard RPC params for %s contain trailing JSON", deviceID)
		}
	}
	return RPCCommand{ID: *envelope.ID, DeviceID: deviceID, Method: strings.TrimSpace(envelope.Method), Params: params}, nil
}

func readLimitedBody(reader io.Reader, maximum int64) ([]byte, error) {
	limited := &io.LimitedReader{R: reader, N: maximum + 1}
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maximum {
		return nil, errors.New("response body exceeds size limit")
	}
	return body, nil
}
