package controlconnector

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
)

const (
	defaultProviderTimeout       = 7 * time.Second
	defaultProviderResponseLimit = int64(256 << 10)
)

var (
	ErrMappingNotVerified  = errors.New("thingsboard command mapping is not locally verified")
	ErrTargetUnavailable   = errors.New("thingsboard target binding is unavailable")
	ErrEvidenceUnavailable = errors.New("connector evidence store is unavailable")
)

type MappingStatus string

const (
	MappingReference          MappingStatus = "REFERENCE"
	MappingLocalVerified      MappingStatus = "LOCAL_VERIFIED"
	MappingProductionVerified MappingStatus = "PRODUCTION_VERIFIED"
)

type Mapping struct {
	Capability         commandmodel.Capability
	CapabilityRevision string
	MappingRevision    string
	Status             MappingStatus
	Method             string
	Timeout            time.Duration
}

type Target struct {
	IntegrationID    string
	ExternalDeviceID string
	BindingRevision  string
}

type TargetResolver interface {
	ResolveThingsBoardTarget(context.Context, commandmodel.DispatchEnvelope) (Target, error)
}

type CredentialProvider interface {
	ProviderCredential(context.Context, Target) (string, error)
}

type PreparedEvidence = commandmodel.PreparedConnectorEvidence

type CompletedEvidence = commandmodel.CompletedConnectorEvidence

type EvidenceStore interface {
	Prepare(context.Context, PreparedEvidence) error
	Complete(context.Context, CompletedEvidence) error
}

type ThingsBoardConfig struct {
	BaseURL                 string
	HTTPClient              *http.Client
	TargetResolver          TargetResolver
	CredentialProvider      CredentialProvider
	EvidenceStore           EvidenceStore
	Mappings                []Mapping
	AllowLocalVerified      bool
	AllowProductionVerified bool
	Now                     func() time.Time
	MaxResponseBytes        int64
}

type ThingsBoard struct {
	baseURL                 string
	httpClient              *http.Client
	targetResolver          TargetResolver
	credentialProvider      CredentialProvider
	evidenceStore           EvidenceStore
	mappings                map[string]Mapping
	allowLocalVerified      bool
	allowProductionVerified bool
	now                     func() time.Time
	maxResponseBytes        int64

	mu               sync.Mutex
	results          map[string]record
	maxFenceByDevice map[string]uint64
}

type rpcRequest struct {
	Method  string         `json:"method"`
	Params  map[string]any `json:"params"`
	Timeout int64          `json:"timeout"`
}

type setpointReply struct {
	Success          bool     `json:"success"`
	AppliedSetpointC *float64 `json:"appliedSetpointC,omitempty"`
}

func NewThingsBoard(config ThingsBoardConfig) (*ThingsBoard, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("thingsboard base URL is invalid")
	}
	if config.TargetResolver == nil || config.CredentialProvider == nil || config.EvidenceStore == nil {
		return nil, errors.New("thingsboard connector dependencies are incomplete")
	}
	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultProviderTimeout}
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	maxResponseBytes := config.MaxResponseBytes
	if maxResponseBytes <= 0 || maxResponseBytes > 4<<20 {
		maxResponseBytes = defaultProviderResponseLimit
	}
	mappings := make(map[string]Mapping, len(config.Mappings))
	for _, mapping := range config.Mappings {
		if mapping.Capability == "" || mapping.CapabilityRevision == "" || mapping.MappingRevision == "" || mapping.Method == "" {
			return nil, errors.New("thingsboard command mapping is incomplete")
		}
		if mapping.Timeout <= 0 || mapping.Timeout > 30*time.Second {
			return nil, errors.New("thingsboard command mapping timeout is invalid")
		}
		key := mappingKey(mapping.Capability, mapping.CapabilityRevision)
		if _, duplicate := mappings[key]; duplicate {
			return nil, errors.New("thingsboard command mapping is duplicated")
		}
		mappings[key] = mapping
	}
	return &ThingsBoard{
		baseURL: baseURL, httpClient: httpClient, targetResolver: config.TargetResolver,
		credentialProvider: config.CredentialProvider, evidenceStore: config.EvidenceStore,
		mappings: mappings, allowLocalVerified: config.AllowLocalVerified,
		allowProductionVerified: config.AllowProductionVerified, now: now,
		maxResponseBytes: maxResponseBytes, results: make(map[string]record),
		maxFenceByDevice: make(map[string]uint64),
	}, nil
}

func (c *ThingsBoard) Execute(ctx context.Context, envelope commandmodel.DispatchEnvelope) (commandmodel.ConnectorResult, error) {
	key := fmt.Sprintf("%s|%d", envelope.AttemptID, envelope.ExecutionFence)
	c.mu.Lock()
	if existing, ok := c.results[key]; ok {
		if existing.PayloadHash != envelope.PayloadHash {
			c.mu.Unlock()
			return commandmodel.ConnectorResult{}, ErrPayloadMismatch
		}
		result := existing.Result
		c.mu.Unlock()
		return result, nil
	}
	if envelope.ExecutionFence < c.maxFenceByDevice[envelope.DeviceID] {
		c.mu.Unlock()
		return commandmodel.ConnectorResult{}, ErrOldFence
	}
	if envelope.ExecutionFence > c.maxFenceByDevice[envelope.DeviceID] {
		c.maxFenceByDevice[envelope.DeviceID] = envelope.ExecutionFence
	}
	c.mu.Unlock()

	mapping, ok := c.mappings[mappingKey(envelope.Capability, envelope.CapabilityRevision)]
	if !ok || !c.mappingAllowed(mapping.Status) {
		return commandmodel.ConnectorResult{}, ErrMappingNotVerified
	}
	target, err := c.targetResolver.ResolveThingsBoardTarget(ctx, envelope)
	if err != nil || strings.TrimSpace(target.ExternalDeviceID) == "" || strings.TrimSpace(target.IntegrationID) == "" || strings.TrimSpace(target.BindingRevision) == "" {
		return commandmodel.ConnectorResult{}, ErrTargetUnavailable
	}
	credential, err := c.credentialProvider.ProviderCredential(ctx, target)
	if err != nil || strings.TrimSpace(credential) == "" {
		return commandmodel.ConnectorResult{}, ErrTargetUnavailable
	}

	body, err := json.Marshal(rpcRequest{
		Method:  mapping.Method,
		Params:  map[string]any{"setpointC": envelope.SetpointC},
		Timeout: mapping.Timeout.Milliseconds(),
	})
	if err != nil {
		return commandmodel.ConnectorResult{}, err
	}
	endpoint := c.baseURL + "/api/rpc/twoway/" + url.PathEscape(target.ExternalDeviceID)
	prepared := PreparedEvidence{
		AttemptID: envelope.AttemptID, CommandID: envelope.CommandID,
		OrganizationID: envelope.OrganizationID, SiteID: envelope.SiteID, DeviceID: envelope.DeviceID,
		ExternalDeviceID: target.ExternalDeviceID, ExecutionFence: envelope.ExecutionFence,
		PayloadHash: envelope.PayloadHash, MappingRevision: mapping.MappingRevision,
		BindingRevision: target.BindingRevision, ProviderEndpoint: "/api/rpc/twoway/{deviceId}",
		ProviderMethod: mapping.Method, RequestSHA256: sha256Hex(body), PreparedAt: c.now().UTC(),
	}
	if err := c.evidenceStore.Prepare(ctx, prepared); err != nil {
		return commandmodel.ConnectorResult{}, ErrEvidenceUnavailable
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return c.completeBeforeWrite(ctx, key, envelope.PayloadHash, prepared, "REQUEST_CONSTRUCTION_FAILED")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-Authorization", providerAuthorization(credential))
	var requestWritten bool
	trace := &httptrace.ClientTrace{WroteRequest: func(info httptrace.WroteRequestInfo) {
		if info.Err == nil {
			requestWritten = true
		}
	}}
	request = request.WithContext(httptrace.WithClientTrace(request.Context(), trace))

	response, err := c.httpClient.Do(request)
	if err != nil {
		phase := commandmodel.ConnectorPreSendRejected
		failure := "THINGSBOARD_PRE_SEND_TRANSPORT_ERROR"
		if requestWritten {
			phase = commandmodel.ConnectorRequestCommitted
			failure = "THINGSBOARD_TRANSPORT_ERROR_AFTER_WRITE"
		}
		return c.complete(ctx, key, envelope.PayloadHash, CompletedEvidence{
			PreparedConnectorEvidence: prepared, RequestWritten: requestWritten, ConnectorPhase: phase,
			FailureCode: failure, CompletedAt: c.now().UTC(),
		})
	}
	defer response.Body.Close()
	responseBody, readErr := readBounded(response.Body, c.maxResponseBytes)
	completed := CompletedEvidence{
		PreparedConnectorEvidence: prepared, ProviderStatusCode: response.StatusCode,
		ResponseSHA256: sha256Hex(responseBody), RequestWritten: requestWritten,
		CompletedAt: c.now().UTC(),
	}
	if readErr != nil {
		completed.ConnectorPhase = commandmodel.ConnectorRequestCommitted
		completed.FailureCode = "THINGSBOARD_RESPONSE_UNREADABLE"
		return c.complete(ctx, key, envelope.PayloadHash, completed)
	}
	if response.StatusCode != http.StatusOK {
		completed.ConnectorPhase = commandmodel.ConnectorRequestCommitted
		if response.StatusCode == http.StatusGatewayTimeout {
			completed.FailureCode = "THINGSBOARD_RPC_TIMEOUT"
		} else {
			completed.FailureCode = fmt.Sprintf("THINGSBOARD_HTTP_%d", response.StatusCode)
		}
		return c.complete(ctx, key, envelope.PayloadHash, completed)
	}
	var reply setpointReply
	decoder := json.NewDecoder(bytes.NewReader(responseBody))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&reply) != nil || ensureEOF(decoder) != nil || !reply.Success {
		completed.ConnectorPhase = commandmodel.ConnectorRequestCommitted
		completed.FailureCode = "THINGSBOARD_ACK_INVALID"
		return c.complete(ctx, key, envelope.PayloadHash, completed)
	}
	completed.ConnectorPhase = commandmodel.ConnectorAcknowledged
	return c.complete(ctx, key, envelope.PayloadHash, completed)
}

func (c *ThingsBoard) mappingAllowed(status MappingStatus) bool {
	switch status {
	case MappingLocalVerified:
		return c.allowLocalVerified
	case MappingProductionVerified:
		return c.allowProductionVerified
	default:
		return false
	}
}

func (c *ThingsBoard) completeBeforeWrite(ctx context.Context, key, payloadHash string, prepared PreparedEvidence, failure string) (commandmodel.ConnectorResult, error) {
	return c.complete(ctx, key, payloadHash, CompletedEvidence{
		PreparedConnectorEvidence: prepared, ConnectorPhase: commandmodel.ConnectorPreSendRejected,
		FailureCode: failure, CompletedAt: c.now().UTC(),
	})
}

func (c *ThingsBoard) complete(ctx context.Context, key, payloadHash string, evidence CompletedEvidence) (commandmodel.ConnectorResult, error) {
	result := commandmodel.ConnectorResult{
		Phase: evidence.ConnectorPhase, FailureCode: evidence.FailureCode,
		EvidenceID:   "thingsboard:" + evidence.AttemptID + ":" + fmt.Sprint(evidence.ExecutionFence),
		Acknowledged: evidence.ConnectorPhase == commandmodel.ConnectorAcknowledged,
		Verified:     false,
	}
	if err := c.evidenceStore.Complete(ctx, evidence); err != nil {
		if evidence.ConnectorPhase == commandmodel.ConnectorPreSendRejected {
			return commandmodel.ConnectorResult{}, ErrEvidenceUnavailable
		}
		result.Phase = commandmodel.ConnectorRequestCommitted
		result.Acknowledged = false
		result.FailureCode = "CONNECTOR_EVIDENCE_COMPLETION_FAILED"
	}
	c.mu.Lock()
	c.results[key] = record{PayloadHash: payloadHash, Result: result}
	c.mu.Unlock()
	return result, nil
}

func mappingKey(capability commandmodel.Capability, revision string) string {
	return string(capability) + "\x00" + revision
}

func providerAuthorization(credential string) string {
	return "Bear" + "er " + credential
}

func sha256Hex(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func readBounded(reader io.Reader, maximum int64) ([]byte, error) {
	limited := &io.LimitedReader{R: reader, N: maximum + 1}
	value, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(value)) > maximum {
		return nil, errors.New("provider response is too large")
	}
	return value, nil
}

func ensureEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("unexpected trailing JSON")
		}
		return err
	}
	return nil
}
