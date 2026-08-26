package commanddispatcher

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

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/modules/command/pkg/commandservice"
)

const maximumRuntimeResponseBytes = int64(512 << 10)

type RuntimeClientConfig struct {
	BaseURL    string
	HTTPClient *http.Client
	TenantID   string
	SiteID     string
	DeviceID   string
	DeviceIDs  []string
}

type RuntimeClient struct {
	baseURL    string
	httpClient *http.Client
	tenantID   string
	siteID     string
	deviceIDs  map[string]struct{}
}

type runtimeClaimRequest struct {
	LeaseOwner   string `json:"leaseOwner"`
	LeaseSeconds int64  `json:"leaseSeconds"`
}

type runtimeDispatchResolveRequest struct {
	Envelope commandmodel.DispatchEnvelope `json:"envelope"`
	Result   commandmodel.ConnectorResult  `json:"result"`
}

type runtimeVerificationResolveRequest struct {
	Envelope commandmodel.VerificationEnvelope `json:"envelope"`
	Result   commandmodel.VerificationResult   `json:"result"`
}

type runtimeClientProblem struct {
	Code      string `json:"code"`
	Retryable bool   `json:"retryable"`
}

func NewRuntimeClient(config RuntimeClientConfig) (*RuntimeClient, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return nil, errors.New("command runtime base URL must be an HTTPS service origin")
	}
	tenantID := strings.TrimSpace(config.TenantID)
	siteID := strings.TrimSpace(config.SiteID)
	deviceIDs := make(map[string]struct{}, len(config.DeviceIDs)+1)
	if deviceID := strings.TrimSpace(config.DeviceID); deviceID != "" {
		deviceIDs[deviceID] = struct{}{}
	}
	for _, rawDeviceID := range config.DeviceIDs {
		deviceID := strings.TrimSpace(rawDeviceID)
		if deviceID != "" {
			deviceIDs[deviceID] = struct{}{}
		}
	}
	if !commandmodel.IsUUIDv7(tenantID) || !commandmodel.IsUUIDv7(siteID) || len(deviceIDs) == 0 {
		return nil, errors.New("command runtime approved cohort is incomplete")
	}
	for deviceID := range deviceIDs {
		if !commandmodel.IsUUIDv7(deviceID) {
			return nil, errors.New("command runtime approved cohort is incomplete")
		}
	}
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &RuntimeClient{
		baseURL: baseURL, httpClient: client,
		tenantID: tenantID, siteID: siteID, deviceIDs: deviceIDs,
	}, nil
}

func (client *RuntimeClient) ClaimDispatch(ctx context.Context, tenantID, leaseOwner string, leaseFor time.Duration) (commandmodel.DispatchEnvelope, error) {
	if client == nil || tenantID != client.tenantID {
		return commandmodel.DispatchEnvelope{}, commandservice.ErrInvalidRequest
	}
	var envelope commandmodel.DispatchEnvelope
	status, err := client.post(ctx, commandservice.InternalDispatchClaimPath, runtimeClaimRequest{
		LeaseOwner: leaseOwner, LeaseSeconds: int64(leaseFor / time.Second),
	}, &envelope)
	if status == http.StatusNoContent && err == nil {
		return commandmodel.DispatchEnvelope{}, commandservice.ErrNoDispatchAvailable
	}
	if err == nil && !client.approvedScope(envelope.TenantID, envelope.SiteID, envelope.DeviceID) {
		return commandmodel.DispatchEnvelope{}, commandservice.ErrInvalidRequest
	}
	return envelope, err
}

func (client *RuntimeClient) ResolveDispatch(ctx context.Context, envelope commandmodel.DispatchEnvelope, result commandmodel.ConnectorResult) error {
	if client == nil || !client.approvedScope(envelope.TenantID, envelope.SiteID, envelope.DeviceID) {
		return commandservice.ErrInvalidRequest
	}
	_, err := client.post(ctx, commandservice.InternalDispatchResolvePath, runtimeDispatchResolveRequest{Envelope: envelope, Result: result}, nil)
	return err
}

func (client *RuntimeClient) ClaimVerification(ctx context.Context, tenantID, leaseOwner string, leaseFor time.Duration) (commandmodel.VerificationEnvelope, error) {
	if client == nil || tenantID != client.tenantID {
		return commandmodel.VerificationEnvelope{}, commandservice.ErrInvalidRequest
	}
	var envelope commandmodel.VerificationEnvelope
	status, err := client.post(ctx, commandservice.InternalVerificationClaimPath, runtimeClaimRequest{
		LeaseOwner: leaseOwner, LeaseSeconds: int64(leaseFor / time.Second),
	}, &envelope)
	if status == http.StatusNoContent && err == nil {
		return commandmodel.VerificationEnvelope{}, commandservice.ErrVerificationNotAvailable
	}
	if err == nil && !client.approvedScope(envelope.TenantID, envelope.SiteID, envelope.DeviceID) {
		return commandmodel.VerificationEnvelope{}, commandservice.ErrInvalidRequest
	}
	return envelope, err
}

func (client *RuntimeClient) ResolveVerification(ctx context.Context, envelope commandmodel.VerificationEnvelope, result commandmodel.VerificationResult) error {
	if client == nil || !client.approvedScope(envelope.TenantID, envelope.SiteID, envelope.DeviceID) {
		return commandservice.ErrInvalidRequest
	}
	_, err := client.post(ctx, commandservice.InternalVerificationResolvePath, runtimeVerificationResolveRequest{Envelope: envelope, Result: result}, nil)
	return err
}

func (client *RuntimeClient) Prepare(ctx context.Context, evidence commandmodel.PreparedConnectorEvidence) error {
	if client == nil || !client.approvedScope(evidence.TenantID, evidence.SiteID, evidence.DeviceID) {
		return commandservice.ErrInvalidRequest
	}
	_, err := client.post(ctx, commandservice.InternalConnectorPreparePath, evidence, nil)
	return err
}

func (client *RuntimeClient) Complete(ctx context.Context, evidence commandmodel.CompletedConnectorEvidence) error {
	if client == nil || !client.approvedScope(evidence.TenantID, evidence.SiteID, evidence.DeviceID) {
		return commandservice.ErrInvalidRequest
	}
	_, err := client.post(ctx, commandservice.InternalConnectorCompletePath, evidence, nil)
	return err
}

func (client *RuntimeClient) approvedScope(tenantID, siteID, deviceID string) bool {
	if client == nil || tenantID != client.tenantID || siteID != client.siteID {
		return false
	}
	_, ok := client.deviceIDs[deviceID]
	return ok
}

func (client *RuntimeClient) post(ctx context.Context, path string, input, output any) (int, error) {
	body, err := json.Marshal(input)
	if err != nil {
		return 0, fmt.Errorf("marshal command runtime request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("construct command runtime request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := client.httpClient.Do(request)
	if err != nil {
		return 0, fmt.Errorf("command runtime request failed: %w", err)
	}
	defer response.Body.Close()
	responseBody, err := readRuntimeResponse(response.Body)
	if err != nil {
		return response.StatusCode, err
	}
	if response.StatusCode == http.StatusNoContent {
		return response.StatusCode, nil
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		if output == nil {
			return response.StatusCode, nil
		}
		decoder := json.NewDecoder(bytes.NewReader(responseBody))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(output); err != nil || ensureRuntimeClientEOF(decoder) != nil {
			return response.StatusCode, errors.New("command runtime response is invalid")
		}
		return response.StatusCode, nil
	}
	var problem runtimeClientProblem
	decoder := json.NewDecoder(bytes.NewReader(responseBody))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&problem) != nil || ensureRuntimeClientEOF(decoder) != nil {
		return response.StatusCode, fmt.Errorf("command runtime returned HTTP %d", response.StatusCode)
	}
	switch problem.Code {
	case "COMMAND_RUNTIME_REQUEST_INVALID":
		return response.StatusCode, commandservice.ErrInvalidRequest
	case "COMMAND_RUNTIME_COMMAND_NOT_FOUND":
		return response.StatusCode, commandservice.ErrCommandNotFound
	case "COMMAND_RUNTIME_STALE_FENCE":
		return response.StatusCode, commandservice.ErrStaleFence
	default:
		return response.StatusCode, fmt.Errorf("command runtime error %s retryable=%t", problem.Code, problem.Retryable)
	}
}

func readRuntimeResponse(reader io.Reader) ([]byte, error) {
	limited := &io.LimitedReader{R: reader, N: maximumRuntimeResponseBytes + 1}
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read command runtime response: %w", err)
	}
	if int64(len(body)) > maximumRuntimeResponseBytes {
		return nil, errors.New("command runtime response exceeds size limit")
	}
	return body, nil
}

func ensureRuntimeClientEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("unexpected trailing JSON")
		}
		return err
	}
	return nil
}
