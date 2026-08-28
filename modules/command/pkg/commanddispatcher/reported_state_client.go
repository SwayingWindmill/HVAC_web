package commanddispatcher

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
)

const (
	internalCommandReportedStatePath  = "/internal/v1/commands/reported-state"
	maximumReportedStateResponseBytes = int64(128 << 10)
)

type ReportedStateClientConfig struct {
	BaseURL    string
	HTTPClient *http.Client
	TenantID   string
	SiteID     string
	DeviceID   string
	DeviceIDs  []string
}

type ReportedStateClient struct {
	baseURL    string
	httpClient *http.Client
	tenantID   string
	siteID     string
	deviceIDs  map[string]struct{}
}

type reportedStateResponse struct {
	SchemaVersion          int                      `json:"schemaVersion"`
	EvidenceID             string                   `json:"evidenceId"`
	TenantID               string                   `json:"tenantId"`
	SiteID                 string                   `json:"siteId"`
	DeviceID               string                   `json:"deviceId"`
	EvaluationAvailability string                   `json:"evaluationAvailability"`
	Presence               string                   `json:"presence"`
	Readiness              string                   `json:"readiness"`
	Freshness              string                   `json:"freshness"`
	Quality                string                   `json:"quality"`
	BusinessRevision       uint64                   `json:"businessRevision"`
	ReportedValue          commandmodel.ScalarValue `json:"reportedValue"`
	ObservedAt             time.Time                `json:"observedAt"`
	ReportedStateKey       string                   `json:"reportedStateKey"`
}

func NewReportedStateClient(config ReportedStateClientConfig) (*ReportedStateClient, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return nil, errors.New("S2 reported-state base URL must be an HTTPS service origin")
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
	if tenantID == "" || siteID == "" || len(deviceIDs) == 0 {
		return nil, errors.New("S2 reported-state cohort configuration is incomplete")
	}
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &ReportedStateClient{
		baseURL: baseURL, httpClient: client,
		tenantID: tenantID, siteID: siteID, deviceIDs: deviceIDs,
	}, nil
}

func (client *ReportedStateClient) ReadReportedState(ctx context.Context, envelope commandmodel.VerificationEnvelope) (string, commandmodel.ReportedStateEvidence, error) {
	approvedDevice := false
	if client != nil {
		_, approvedDevice = client.deviceIDs[envelope.DeviceID]
	}
	if client == nil || envelope.TenantID != client.tenantID || envelope.SiteID != client.siteID || !approvedDevice ||
		strings.TrimSpace(envelope.CommandID) == "" || strings.TrimSpace(envelope.AttemptID) == "" || envelope.ExecutionFence == 0 ||
		strings.TrimSpace(envelope.VerificationPointKey) == "" {
		return "", commandmodel.ReportedStateEvidence{}, errors.New("verification envelope is outside the approved cohort")
	}
	endpoint := client.baseURL + internalCommandReportedStatePath + "?deviceId=" + url.QueryEscape(envelope.DeviceID) + "&key=" + url.QueryEscape(envelope.VerificationPointKey)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", commandmodel.ReportedStateEvidence{}, fmt.Errorf("construct S2 reported-state request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	response, err := client.httpClient.Do(request)
	if err != nil {
		return "", commandmodel.ReportedStateEvidence{}, fmt.Errorf("S2 reported-state request failed: %w", err)
	}
	defer response.Body.Close()
	body, err := readReportedStateBody(response.Body)
	if err != nil {
		return "", commandmodel.ReportedStateEvidence{}, err
	}
	if response.StatusCode != http.StatusOK {
		return "", commandmodel.ReportedStateEvidence{}, fmt.Errorf("S2 reported-state returned HTTP %d", response.StatusCode)
	}
	var result reportedStateResponse
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil || ensureReportedStateEOF(decoder) != nil {
		return "", commandmodel.ReportedStateEvidence{}, errors.New("S2 reported-state response is invalid")
	}
	if result.SchemaVersion != 1 || result.TenantID != client.tenantID || result.SiteID != client.siteID || result.DeviceID != envelope.DeviceID ||
		result.ReportedStateKey != envelope.VerificationPointKey || !validS2EvidenceID(result.EvidenceID) || result.ObservedAt.IsZero() {
		return "", commandmodel.ReportedStateEvidence{}, errors.New("S2 reported-state response is outside the approved cohort")
	}
	return result.EvidenceID, commandmodel.ReportedStateEvidence{
		TenantID: result.TenantID, SiteID: result.SiteID, DeviceID: result.DeviceID,
		EvaluationAvailability: result.EvaluationAvailability, Presence: result.Presence, Readiness: result.Readiness,
		Freshness: result.Freshness, Quality: result.Quality, BusinessRevision: result.BusinessRevision,
		ReportedValue: result.ReportedValue, ObservedAt: result.ObservedAt.UTC(),
	}, nil
}

func validS2EvidenceID(value string) bool {
	const prefix = "s2:sha256:"
	if !strings.HasPrefix(value, prefix) || len(value) != len(prefix)+64 {
		return false
	}
	digest := strings.TrimPrefix(value, prefix)
	if digest != strings.ToLower(digest) {
		return false
	}
	_, err := hex.DecodeString(digest)
	return err == nil
}

func readReportedStateBody(reader io.Reader) ([]byte, error) {
	limited := &io.LimitedReader{R: reader, N: maximumReportedStateResponseBytes + 1}
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read S2 reported-state response: %w", err)
	}
	if int64(len(body)) > maximumReportedStateResponseBytes {
		return nil, errors.New("S2 reported-state response exceeds size limit")
	}
	return body, nil
}

func ensureReportedStateEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("unexpected trailing JSON")
		}
		return err
	}
	return nil
}
