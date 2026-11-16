package commandservice

import (
	"bytes"
	"context"
	"crypto"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandauth"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/workloadtls"
)

const (
	iamRegistryDecisionPath       = "/internal/v1/iam/decisions/registry"
	iamCommandDecisionPath        = "/internal/v1/iam/decisions/command"
	telemetryReportedStatePath    = "/internal/v1/commands/reported-state"
	registryDispatchContextPath   = "/internal/v1/registry/commands/dispatch-context"
	maximumUpstreamResponseBytes  = int64(4 << 20)
	maximumReportedStateBodyBytes = int64(128 << 10)
)

type HTTPClientsConfig struct {
	Now                  func() time.Time
	CommandServiceSPIFFE string
	DelegationSigner     crypto.Signer
	PolicyRevision       string

	IAMBaseURL    string
	IAMAudience   string
	IAMHTTPClient *http.Client

	CoreBaseURL    string
	CoreHTTPClient *http.Client

	TelemetryBaseURL    string
	TelemetryHTTPClient *http.Client

	CommandGrantPublicKey crypto.PublicKey

	Timeout time.Duration
}

type HTTPAdapters struct {
	now                  func() time.Time
	commandServiceSPIFFE string
	delegationSigner     crypto.Signer
	policyRevision       string

	iamBaseURL    string
	iamAudience   string
	iamHTTPClient *http.Client

	coreBaseURL    string
	coreHTTPClient *http.Client

	telemetryBaseURL    string
	telemetryHTTPClient *http.Client

	commandGrantPublicKey crypto.PublicKey
	timeout               time.Duration
}

func NewHTTPAdapters(config HTTPClientsConfig) (*HTTPAdapters, error) {
	spiffe := strings.TrimSpace(config.CommandServiceSPIFFE)
	if spiffe == "" {
		spiffe = "spiffe://hvac.local/command-service"
	}
	if !strings.HasPrefix(spiffe, "spiffe://") {
		return nil, errors.New("command service SPIFFE ID is invalid")
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	iamAudience := strings.TrimSpace(config.IAMAudience)
	if iamAudience == "" {
		iamAudience = "iam-service"
	}
	policyRevision := strings.TrimSpace(config.PolicyRevision)
	if policyRevision == "" {
		policyRevision = "policy-v1"
	}
	timeout := config.Timeout
	if timeout <= 0 || timeout > 30*time.Second {
		timeout = 5 * time.Second
	}
	return &HTTPAdapters{
		now:                   now,
		commandServiceSPIFFE:  spiffe,
		delegationSigner:      config.DelegationSigner,
		policyRevision:        policyRevision,
		iamBaseURL:            strings.TrimRight(strings.TrimSpace(config.IAMBaseURL), "/"),
		iamAudience:           iamAudience,
		iamHTTPClient:         config.IAMHTTPClient,
		coreBaseURL:           strings.TrimRight(strings.TrimSpace(config.CoreBaseURL), "/"),
		coreHTTPClient:        config.CoreHTTPClient,
		telemetryBaseURL:      strings.TrimRight(strings.TrimSpace(config.TelemetryBaseURL), "/"),
		telemetryHTTPClient:   config.TelemetryHTTPClient,
		commandGrantPublicKey: config.CommandGrantPublicKey,
		timeout:               timeout,
	}, nil
}

// WorkloadEnvConfig parameterizes Command Service outbound mTLS clients constructed from process env.
type WorkloadEnvConfig struct {
	CertificateFiles      workloadtls.CertificateFiles
	DelegationSigner      crypto.Signer
	DefaultServerCA       string
	CommandServiceSPIFFE  string
	PolicyRevision        string
	CommandGrantPublicKey crypto.PublicKey
	Timeout               time.Duration
	Now                   func() time.Time
}

// NewWorkloadAdaptersFromEnv builds HTTPAdapters wired from IAM_URL, CORE_URL, and TELEMETRY_RUNTIME_URL
// with shared mTLS workload credentials and per-upstream CA/ServerName overrides.
func NewWorkloadAdaptersFromEnv(config WorkloadEnvConfig) (*HTTPAdapters, error) {
	timeout := config.Timeout
	if timeout <= 0 || timeout > 30*time.Second {
		timeout = 5 * time.Second
	}
	buildClient := func(rawURL, specificCAEnv, serverNameEnv string) (string, *http.Client, error) {
		baseURL := strings.TrimRight(strings.TrimSpace(os.Getenv(rawURL)), "/")
		if baseURL == "" {
			return "", nil, nil
		}
		parsed, err := url.Parse(baseURL)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
			return "", nil, fmt.Errorf("%s must be a valid HTTPS origin", rawURL)
		}
		caPath := strings.TrimSpace(os.Getenv(specificCAEnv))
		if caPath == "" {
			caPath = strings.TrimSpace(config.DefaultServerCA)
		}
		if caPath == "" {
			return "", nil, fmt.Errorf("%s requires %s or default CA", rawURL, specificCAEnv)
		}
		serverName := strings.TrimSpace(os.Getenv(serverNameEnv))
		if serverName == "" {
			host := parsed.Hostname()
			if host == "" || host == "127.0.0.1" || host == "::1" {
				serverName = "localhost"
			} else {
				serverName = host
			}
		}
		certFiles := config.CertificateFiles
		client, err := workloadtls.NewHTTPClient(workloadtls.ClientConfig{
			CertificateFiles: &certFiles,
			ServerCAPath:     caPath,
			ServerName:       serverName,
			Timeout:          timeout,
		})
		if err != nil {
			return "", nil, fmt.Errorf("configure %s client: %w", rawURL, err)
		}
		if client.CheckRedirect == nil {
			client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		}
		return baseURL, client, nil
	}

	iamBaseURL, iamClient, err := buildClient("IAM_URL", "IAM_SERVER_CA", "IAM_SERVER_NAME")
	if err != nil {
		return nil, err
	}
	coreBaseURL, coreClient, err := buildClient("CORE_URL", "CORE_SERVER_CA", "CORE_SERVER_NAME")
	if err != nil {
		return nil, err
	}
	telemetryBaseURL, telemetryClient, err := buildClient("TELEMETRY_RUNTIME_URL", "TELEMETRY_RUNTIME_SERVER_CA", "TELEMETRY_RUNTIME_SERVER_NAME")
	if err != nil {
		return nil, err
	}

	iamAudience := strings.TrimSpace(os.Getenv("COMMAND_IAM_AUDIENCE"))
	if iamAudience == "" {
		iamAudience = "iam-service"
	}
	return NewHTTPAdapters(HTTPClientsConfig{
		Now:                   config.Now,
		CommandServiceSPIFFE:  config.CommandServiceSPIFFE,
		DelegationSigner:      config.DelegationSigner,
		PolicyRevision:        config.PolicyRevision,
		IAMBaseURL:            iamBaseURL,
		IAMAudience:           iamAudience,
		IAMHTTPClient:         iamClient,
		CoreBaseURL:           coreBaseURL,
		CoreHTTPClient:        coreClient,
		TelemetryBaseURL:      telemetryBaseURL,
		TelemetryHTTPClient:   telemetryClient,
		CommandGrantPublicKey: config.CommandGrantPublicKey,
		Timeout:               timeout,
	})
}

func (a *HTTPAdapters) AuthorizeRegistry(ctx context.Context, operator OperatorIdentity, action string) (RegistryDecisionResult, error) {
	if a.iamBaseURL == "" || a.iamHTTPClient == nil || a.delegationSigner == nil {
		return RegistryDecisionResult{}, &ControlError{
			HTTPStatus: http.StatusServiceUnavailable, Code: "IAM_UNAVAILABLE",
			Title: "Authorization unavailable", Detail: "IAM Service is not configured for Command Service.", Retryable: true,
		}
	}
	delegation, err := a.signOperatorDelegation(operator, "registry:authorize")
	if err != nil {
		return RegistryDecisionResult{}, &ControlError{
			HTTPStatus: http.StatusServiceUnavailable, Code: "IAM_UNAVAILABLE",
			Title: "Authorization unavailable", Detail: "Command Service failed to sign IAM delegation context.", Retryable: true,
		}
	}
	payload, err := json.Marshal(struct {
		TenantID       string `json:"tenantId"`
		Action         string `json:"action"`
		GrantPresenter string `json:"grantPresenter,omitempty"`
	}{
		TenantID:       operator.TenantID,
		Action:         action,
		GrantPresenter: a.commandServiceSPIFFE,
	})
	if err != nil {
		return RegistryDecisionResult{}, &ControlError{
			HTTPStatus: http.StatusServiceUnavailable, Code: "IAM_UNAVAILABLE",
			Title: "Authorization unavailable", Detail: "Registry authorization request could not be encoded.", Retryable: true,
		}
	}
	status, raw, err := a.doHTTP(ctx, a.iamHTTPClient, http.MethodPost, a.iamBaseURL+iamRegistryDecisionPath, payload, map[string]string{
		"Content-Type":       "application/json",
		"Accept":             "application/json, application/problem+json",
		"X-Delegation-Grant": delegation,
	})
	if err != nil || status != http.StatusOK {
		return RegistryDecisionResult{}, &ControlError{
			HTTPStatus: http.StatusServiceUnavailable, Code: "IAM_UNAVAILABLE",
			Title: "Authorization unavailable", Detail: "IAM Registry authorization is temporarily unavailable.", Retryable: true,
		}
	}
	var resp struct {
		Decision struct {
			Allowed        bool     `json:"allowed"`
			PrincipalID    string   `json:"principalId"`
			TenantID       string   `json:"tenantId"`
			AllowedSiteIDs []string `json:"allowedSiteIds"`
			ReasonCode     string   `json:"reasonCode"`
		} `json:"decision"`
		DelegationGrant string `json:"delegationGrant"`
	}
	if json.Unmarshal(raw, &resp) != nil || (resp.Decision.TenantID != "" && resp.Decision.TenantID != operator.TenantID) {
		return RegistryDecisionResult{}, &ControlError{
			HTTPStatus: http.StatusServiceUnavailable, Code: "IAM_UNAVAILABLE",
			Title: "Authorization unavailable", Detail: "IAM returned an invalid Registry decision.", Retryable: true,
		}
	}
	return RegistryDecisionResult{
		Allowed:        resp.Decision.Allowed,
		PrincipalID:    resp.Decision.PrincipalID,
		AllowedSiteIDs: append([]string(nil), resp.Decision.AllowedSiteIDs...),
		Grant:          resp.DelegationGrant,
		ReasonCode:     resp.Decision.ReasonCode,
	}, nil
}

func (a *HTTPAdapters) AuthorizeCommand(
	ctx context.Context,
	operator OperatorIdentity,
	purpose commandmodel.AuthorizationPurpose,
	siteID, deviceID string,
	capability commandmodel.Capability,
	capabilityRevision string,
) (string, commandauth.GrantClaims, error) {
	if a.iamBaseURL == "" || a.iamHTTPClient == nil || a.delegationSigner == nil {
		return "", commandauth.GrantClaims{}, &ControlError{
			HTTPStatus: http.StatusServiceUnavailable, Code: "IAM_UNAVAILABLE",
			Title: "Authorization unavailable", Detail: "IAM Service is not configured for Command authorization.", Retryable: true,
		}
	}
	delegation, err := a.signOperatorDelegation(operator, "command:authorize")
	if err != nil {
		return "", commandauth.GrantClaims{}, &ControlError{
			HTTPStatus: http.StatusServiceUnavailable, Code: "IAM_UNAVAILABLE",
			Title: "Authorization unavailable", Detail: "Command Service failed to sign IAM command delegation.", Retryable: true,
		}
	}
	payload, err := json.Marshal(commandauth.DecisionRequest{
		TenantID:           operator.TenantID,
		SiteID:             siteID,
		DeviceID:           deviceID,
		Capability:         capability,
		CapabilityRevision: capabilityRevision,
		Purpose:            purpose,
	})
	if err != nil {
		return "", commandauth.GrantClaims{}, &ControlError{
			HTTPStatus: http.StatusServiceUnavailable, Code: "IAM_UNAVAILABLE",
			Title: "Authorization unavailable", Detail: "Command authorization request could not be encoded.", Retryable: true,
		}
	}
	status, raw, err := a.doHTTP(ctx, a.iamHTTPClient, http.MethodPost, a.iamBaseURL+iamCommandDecisionPath, payload, map[string]string{
		"Content-Type":       "application/json",
		"Accept":             "application/json, application/problem+json",
		"X-Delegation-Grant": delegation,
	})
	if err != nil || status != http.StatusOK {
		return "", commandauth.GrantClaims{}, &ControlError{
			HTTPStatus: http.StatusServiceUnavailable, Code: "IAM_UNAVAILABLE",
			Title: "Authorization unavailable", Detail: "IAM Command authorization is temporarily unavailable.", Retryable: true,
		}
	}
	var resp commandauth.DecisionResponse
	if json.Unmarshal(raw, &resp) != nil {
		return "", commandauth.GrantClaims{}, &ControlError{
			HTTPStatus: http.StatusServiceUnavailable, Code: "IAM_UNAVAILABLE",
			Title: "Authorization unavailable", Detail: "IAM returned an invalid Command decision.", Retryable: true,
		}
	}
	if !resp.Decision.Allowed || strings.TrimSpace(resp.DelegationGrant) == "" {
		return "", commandauth.GrantClaims{}, &ControlError{
			HTTPStatus: http.StatusForbidden, Code: "COMMAND_AUTHORIZATION_DENIED",
			Title: "Command forbidden", Detail: "The authenticated Principal is not allowed to execute this Command.", Retryable: false,
		}
	}
	if a.commandGrantPublicKey == nil {
		return resp.DelegationGrant, commandauth.GrantClaims{}, nil
	}
	claims, err := commandauth.VerifyGrant(a.commandGrantPublicKey, resp.DelegationGrant)
	if err != nil {
		return "", commandauth.GrantClaims{}, &ControlError{
			HTTPStatus: http.StatusServiceUnavailable, Code: "COMMAND_UNAVAILABLE",
			Title: "Authorization unavailable", Detail: "IAM returned an unverifiable Command grant.", Retryable: true,
		}
	}
	return resp.DelegationGrant, claims, nil
}

func (a *HTTPAdapters) GetAsset(ctx context.Context, operator OperatorIdentity, assetID string) (AssetDTO, error) {
	decision, err := a.AuthorizeRegistry(ctx, operator, ActionAssetRead)
	if err != nil {
		return AssetDTO{}, err
	}
	if !decision.Allowed || decision.Grant == "" {
		return AssetDTO{}, &ControlError{
			HTTPStatus: http.StatusNotFound, Code: "RESOURCE_NOT_FOUND",
			Title: "Resource not found", Detail: "The requested Asset was not found.",
		}
	}
	if a.coreBaseURL == "" || a.coreHTTPClient == nil {
		return AssetDTO{}, coreUnavailableError("Core Registry is not configured.")
	}
	status, raw, err := a.doHTTP(ctx, a.coreHTTPClient, http.MethodGet, a.coreBaseURL+"/internal/v1/registry/assets/"+url.PathEscape(assetID), nil, map[string]string{
		"Accept":             "application/json, application/problem+json",
		"X-Delegation-Grant": decision.Grant,
	})
	if err != nil {
		return AssetDTO{}, coreUnavailableError("Core Registry is temporarily unavailable.")
	}
	if status == http.StatusNotFound || status == http.StatusForbidden {
		return AssetDTO{}, &ControlError{
			HTTPStatus: http.StatusNotFound, Code: "RESOURCE_NOT_FOUND",
			Title: "Resource not found", Detail: "The requested Asset was not found.",
		}
	}
	if status != http.StatusOK {
		return AssetDTO{}, coreUnavailableError("Core Registry could not resolve the Asset.")
	}
	var asset AssetDTO
	if json.Unmarshal(raw, &asset) != nil || asset.ID != assetID || asset.TenantID != operator.TenantID || !containsString(decision.AllowedSiteIDs, asset.SiteID) {
		return AssetDTO{}, &ControlError{
			HTTPStatus: http.StatusNotFound, Code: "RESOURCE_NOT_FOUND",
			Title: "Resource not found", Detail: "The requested Asset was not found.",
		}
	}
	return asset, nil
}

func (a *HTTPAdapters) GetSiteAssetModel(ctx context.Context, operator OperatorIdentity, siteID string) (SiteAssetModelDTO, error) {
	decision, err := a.AuthorizeRegistry(ctx, operator, ActionAssetModelRead)
	if err != nil {
		return SiteAssetModelDTO{}, err
	}
	if !decision.Allowed || decision.Grant == "" || !containsString(decision.AllowedSiteIDs, siteID) {
		return SiteAssetModelDTO{}, &ControlError{
			HTTPStatus: http.StatusNotFound, Code: "RESOURCE_NOT_FOUND",
			Title: "Resource not found", Detail: "The requested Site was not found.",
		}
	}
	if a.coreBaseURL == "" || a.coreHTTPClient == nil {
		return SiteAssetModelDTO{}, coreUnavailableError("Core Registry is not configured.")
	}
	status, raw, err := a.doHTTP(ctx, a.coreHTTPClient, http.MethodGet, a.coreBaseURL+"/internal/v1/registry/sites/"+url.PathEscape(siteID)+"/asset-model", nil, map[string]string{
		"Accept":             "application/json, application/problem+json",
		"X-Delegation-Grant": decision.Grant,
	})
	if err != nil {
		return SiteAssetModelDTO{}, coreUnavailableError("Core Registry is temporarily unavailable.")
	}
	if status == http.StatusNotFound || status == http.StatusForbidden {
		return SiteAssetModelDTO{}, &ControlError{
			HTTPStatus: http.StatusNotFound, Code: "RESOURCE_NOT_FOUND",
			Title: "Resource not found", Detail: "The requested Site was not found.",
		}
	}
	if status != http.StatusOK {
		return SiteAssetModelDTO{}, coreUnavailableError("Core Registry could not read the Site asset model.")
	}
	var model SiteAssetModelDTO
	if json.Unmarshal(raw, &model) != nil || model.SiteID != siteID || model.TenantID != operator.TenantID {
		return SiteAssetModelDTO{}, coreUnavailableError("Core Registry returned an invalid Site asset model.")
	}
	return model, nil
}

func (a *HTTPAdapters) GetSiteControlState(ctx context.Context, operator OperatorIdentity, siteID string) (SiteControlStateView, error) {
	decision, err := a.AuthorizeRegistry(ctx, operator, ActionSiteRead)
	if err != nil {
		return SiteControlStateView{}, err
	}
	if !decision.Allowed || decision.Grant == "" || !containsString(decision.AllowedSiteIDs, siteID) {
		return SiteControlStateView{}, &ControlError{
			HTTPStatus: http.StatusNotFound, Code: "RESOURCE_NOT_FOUND",
			Title: "Resource not found", Detail: "The requested Site was not found.",
		}
	}
	if a.coreBaseURL == "" || a.coreHTTPClient == nil {
		return SiteControlStateView{}, coreUnavailableError("Core Registry is not configured.")
	}
	status, raw, err := a.doHTTP(ctx, a.coreHTTPClient, http.MethodGet, a.coreBaseURL+"/internal/v1/registry/sites/"+url.PathEscape(siteID)+"/control-state", nil, map[string]string{
		"Accept":             "application/json, application/problem+json",
		"X-Delegation-Grant": decision.Grant,
	})
	if err != nil {
		return SiteControlStateView{}, coreUnavailableError("Core Registry is temporarily unavailable.")
	}
	if status == http.StatusNotFound || status == http.StatusForbidden {
		return SiteControlStateView{}, &ControlError{
			HTTPStatus: http.StatusNotFound, Code: "RESOURCE_NOT_FOUND",
			Title: "Resource not found", Detail: "The requested Site was not found.",
		}
	}
	if status != http.StatusOK {
		return SiteControlStateView{}, coreUnavailableError("Core Registry could not read the Site control state.")
	}
	var state SiteControlStateView
	if json.Unmarshal(raw, &state) != nil || state.SiteID != siteID || state.TenantID != operator.TenantID || !validSiteControlMode(state.ControlMode) {
		return SiteControlStateView{}, coreUnavailableError("Core Registry returned an invalid Site control state.")
	}
	if state.AssetLocks == nil {
		state.AssetLocks = []AssetControlLockView{}
	}
	return state, nil
}

func (a *HTTPAdapters) SaveSiteControlMode(
	ctx context.Context,
	operator OperatorIdentity,
	siteID, controlMode, reason string,
	meta ControlMutationMeta,
) (SiteControlStateView, error) {
	decision, err := a.AuthorizeRegistry(ctx, operator, ActionScheduleManage)
	if err != nil {
		return SiteControlStateView{}, err
	}
	if !decision.Allowed || decision.Grant == "" || !containsString(decision.AllowedSiteIDs, siteID) {
		return SiteControlStateView{}, &ControlError{
			HTTPStatus: http.StatusForbidden, Code: "CONTROL_EXIT_FORBIDDEN",
			Title: "Control exit forbidden", Detail: "The authenticated Principal is not allowed to change Site control mode.",
		}
	}
	if a.coreBaseURL == "" || a.coreHTTPClient == nil {
		return SiteControlStateView{}, coreUnavailableError("Core Registry is not configured.")
	}
	payload, err := json.Marshal(map[string]any{
		"controlMode": controlMode,
		"reason":      reason,
		"meta":        meta,
	})
	if err != nil {
		return SiteControlStateView{}, coreUnavailableError("Control mode payload could not be encoded.")
	}
	status, raw, err := a.doHTTP(ctx, a.coreHTTPClient, http.MethodPut, a.coreBaseURL+"/internal/v1/registry/sites/"+url.PathEscape(siteID)+"/control-mode", payload, map[string]string{
		"Content-Type":       "application/json",
		"Accept":             "application/json, application/problem+json",
		"X-Delegation-Grant": decision.Grant,
	})
	if err != nil {
		return SiteControlStateView{}, coreUnavailableError("Core Registry is temporarily unavailable.")
	}
	if status != http.StatusOK && status != http.StatusCreated {
		return SiteControlStateView{}, coreProblemError(status, raw)
	}
	var saved SiteControlStateView
	if json.Unmarshal(raw, &saved) != nil || saved.SiteID != siteID || saved.TenantID != operator.TenantID || !validSiteControlMode(saved.ControlMode) {
		return SiteControlStateView{}, coreUnavailableError("Core Registry returned an invalid Site control state.")
	}
	if saved.AssetLocks == nil {
		saved.AssetLocks = []AssetControlLockView{}
	}
	return saved, nil
}

func (a *HTTPAdapters) SaveAssetControlLock(
	ctx context.Context,
	operator OperatorIdentity,
	assetID, siteID, lockMode, reason string,
	meta ControlMutationMeta,
) (AssetControlLockView, error) {
	decision, err := a.AuthorizeRegistry(ctx, operator, ActionScheduleManage)
	if err != nil {
		return AssetControlLockView{}, err
	}
	if !decision.Allowed || decision.Grant == "" || !containsString(decision.AllowedSiteIDs, siteID) {
		return AssetControlLockView{}, &ControlError{
			HTTPStatus: http.StatusForbidden, Code: "CONTROL_EXIT_FORBIDDEN",
			Title: "Control exit forbidden", Detail: "The authenticated Principal is not allowed to lock or unlock Assets on this Site.",
		}
	}
	if a.coreBaseURL == "" || a.coreHTTPClient == nil {
		return AssetControlLockView{}, coreUnavailableError("Core Registry is not configured.")
	}
	payload, err := json.Marshal(map[string]any{
		"lockMode": lockMode,
		"reason":   reason,
		"meta":     meta,
	})
	if err != nil {
		return AssetControlLockView{}, coreUnavailableError("Asset control lock payload could not be encoded.")
	}
	status, raw, err := a.doHTTP(ctx, a.coreHTTPClient, http.MethodPut, a.coreBaseURL+"/internal/v1/registry/assets/"+url.PathEscape(assetID)+"/control-lock", payload, map[string]string{
		"Content-Type":       "application/json",
		"Accept":             "application/json, application/problem+json",
		"X-Delegation-Grant": decision.Grant,
	})
	if err != nil {
		return AssetControlLockView{}, coreUnavailableError("Core Registry is temporarily unavailable.")
	}
	if status != http.StatusOK && status != http.StatusCreated {
		return AssetControlLockView{}, coreProblemError(status, raw)
	}
	var saved AssetControlLockView
	if json.Unmarshal(raw, &saved) != nil || saved.AssetID != assetID || saved.SiteID != siteID {
		return AssetControlLockView{}, coreUnavailableError("Core Registry returned an invalid Asset control lock.")
	}
	return saved, nil
}

func (a *HTTPAdapters) GetDispatchContext(ctx context.Context, tenantID, siteID, deviceID, pointID string) (ControlDispatchContext, error) {
	if a.coreBaseURL == "" || a.coreHTTPClient == nil {
		return ControlDispatchContext{}, errors.New("core registry is not configured for dispatch context")
	}
	endpoint := a.coreBaseURL + registryDispatchContextPath + "?" + url.Values{
		"tenantId": {tenantID},
		"siteId":   {siteID},
		"deviceId": {deviceID},
		"pointId":  {pointID},
	}.Encode()
	status, raw, err := a.doHTTP(ctx, a.coreHTTPClient, http.MethodGet, endpoint, nil, map[string]string{
		"Accept": "application/json, application/problem+json",
	})
	if err != nil {
		return ControlDispatchContext{}, fmt.Errorf("dispatch context request failed: %w", err)
	}
	if status != http.StatusOK {
		return ControlDispatchContext{}, fmt.Errorf("core registry dispatch context returned HTTP %d", status)
	}
	var out ControlDispatchContext
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&out); err != nil || ensureCommandJSONEOF(decoder) != nil {
		return ControlDispatchContext{}, errors.New("core registry dispatch context payload is invalid")
	}
	if out.TenantID != tenantID || out.SiteID != siteID || out.DeviceID != deviceID || out.PointID != pointID {
		return ControlDispatchContext{}, errors.New("core registry dispatch context scope mismatch")
	}
	if out.AssetLocks == nil {
		out.AssetLocks = []AssetControlLockView{}
	}
	if out.Interlocks == nil {
		out.Interlocks = []commandmodel.InterlockCheck{}
	}
	return out, nil
}

func (a *HTTPAdapters) ReadLivePoint(
	ctx context.Context,
	tenantID, siteID, deviceID, pointKey string,
) (*commandmodel.LivePointObservation, string, error) {
	if a.telemetryBaseURL == "" || a.telemetryHTTPClient == nil {
		return nil, "", &ControlError{
			HTTPStatus: http.StatusServiceUnavailable, Code: "TELEMETRY_UNAVAILABLE",
			Title: "Telemetry unavailable", Detail: "Telemetry Runtime is not configured.", Retryable: true,
		}
	}
	if strings.TrimSpace(deviceID) == "" || strings.TrimSpace(pointKey) == "" {
		return nil, "", errors.New("live point deviceId and pointKey are required")
	}
	endpoint := a.telemetryBaseURL + telemetryReportedStatePath + "?deviceId=" + url.QueryEscape(deviceID) + "&key=" + url.QueryEscape(pointKey)
	status, raw, err := a.doHTTPLimit(ctx, a.telemetryHTTPClient, http.MethodGet, endpoint, nil, map[string]string{
		"Accept": "application/json, application/problem+json",
	}, maximumReportedStateBodyBytes)
	if err != nil {
		return nil, "", &ControlError{
			HTTPStatus: http.StatusServiceUnavailable, Code: "TELEMETRY_UNAVAILABLE",
			Title: "Telemetry unavailable", Detail: "Telemetry Runtime is temporarily unavailable.", Retryable: true,
		}
	}
	if status == http.StatusNotFound {
		return nil, "", nil
	}
	if status != http.StatusOK {
		return nil, "", &ControlError{
			HTTPStatus: http.StatusServiceUnavailable, Code: "TELEMETRY_UNAVAILABLE",
			Title: "Telemetry unavailable", Detail: fmt.Sprintf("Telemetry Runtime returned HTTP %d.", status), Retryable: true,
		}
	}
	var resp struct {
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
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&resp); err != nil || ensureCommandJSONEOF(decoder) != nil {
		return nil, "", &ControlError{
			HTTPStatus: http.StatusServiceUnavailable, Code: "TELEMETRY_UNAVAILABLE",
			Title: "Telemetry unavailable", Detail: "Telemetry Runtime returned an invalid reported-state payload.", Retryable: true,
		}
	}
	if resp.SchemaVersion != 1 || resp.TenantID != tenantID || resp.SiteID != siteID || resp.DeviceID != deviceID ||
		resp.ReportedStateKey != pointKey || !validS2EvidenceID(resp.EvidenceID) || resp.ObservedAt.IsZero() {
		return nil, "", &ControlError{
			HTTPStatus: http.StatusServiceUnavailable, Code: "TELEMETRY_UNAVAILABLE",
			Title: "Telemetry unavailable", Detail: "Telemetry Runtime reported-state response failed scope validation.", Retryable: true,
		}
	}
	obs := &commandmodel.LivePointObservation{
		EvaluationAvailability: resp.EvaluationAvailability,
		Presence:               resp.Presence,
		Readiness:              resp.Readiness,
		Freshness:              resp.Freshness,
		Quality:                resp.Quality,
		BusinessRevision:       resp.BusinessRevision,
		ReportedValue:          resp.ReportedValue,
		ObservedAt:             resp.ObservedAt.UTC(),
	}
	if resp.ReportedValue.Number != nil {
		v := *resp.ReportedValue.Number
		obs.CurrentValue = &v
	}
	return obs, resp.EvidenceID, nil
}

func (a *HTTPAdapters) signOperatorDelegation(operator OperatorIdentity, action string) (string, error) {
	now := a.now().UTC()
	expires := now.Add(30 * time.Second)
	if !operator.ExpiresAt.IsZero() && expires.After(operator.ExpiresAt.UTC()) {
		expires = operator.ExpiresAt.UTC()
	}
	policyRev := operator.PolicyRevision
	if strings.TrimSpace(policyRev) == "" {
		policyRev = a.policyRevision
	}
	tokenID, err := newUUID(now)
	if err != nil {
		return "", err
	}
	claims := identitycontext.DelegationClaims{
		Issuer:           a.commandServiceSPIFFE,
		Subject:          operator.Subject,
		SubjectIssuer:    operator.SubjectIssuer,
		DisplayName:      operator.DisplayName,
		Email:            operator.Email,
		Roles:            append([]string(nil), operator.Roles...),
		ExecutingService: a.commandServiceSPIFFE,
		Audience:         a.iamAudience,
		TenantID:         operator.TenantID,
		Actions:          []string{action},
		Scopes:           []string{"session:" + operator.SessionID},
		PolicyRevision:   policyRev,
		SessionID:        operator.SessionID,
		IssuedAt:         now.Unix(),
		ExpiresAt:        expires.Unix(),
		TokenID:          tokenID,
	}
	return identitycontext.SignDelegation(a.delegationSigner, claims)
}

func (a *HTTPAdapters) doHTTP(
	ctx context.Context,
	client *http.Client,
	method, targetURL string,
	body []byte,
	headers map[string]string,
) (int, []byte, error) {
	return a.doHTTPLimit(ctx, client, method, targetURL, body, headers, maximumUpstreamResponseBytes)
}

func (a *HTTPAdapters) doHTTPLimit(
	ctx context.Context,
	client *http.Client,
	method, targetURL string,
	body []byte,
	headers map[string]string,
	maxBytes int64,
) (int, []byte, error) {
	reqCtx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(reqCtx, method, targetURL, bodyReader)
	if err != nil {
		return 0, nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	observability.InjectHTTP(reqCtx, req.Header)
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	limited := &io.LimitedReader{R: resp.Body, N: maxBytes + 1}
	raw, err := io.ReadAll(limited)
	if err != nil {
		return 0, nil, err
	}
	if int64(len(raw)) > maxBytes {
		return 0, nil, errors.New("upstream response exceeded size limit")
	}
	return resp.StatusCode, raw, nil
}

func coreUnavailableError(detail string) *ControlError {
	return &ControlError{
		HTTPStatus: http.StatusServiceUnavailable,
		Code:       "REGISTRY_UNAVAILABLE",
		Title:      "Registry unavailable",
		Detail:     detail,
		Retryable:  true,
	}
}

func coreProblemError(status int, raw []byte) *ControlError {
	var upstream struct {
		Code      string `json:"code"`
		Title     string `json:"title"`
		Detail    string `json:"detail"`
		Retryable bool   `json:"retryable"`
	}
	if json.Unmarshal(raw, &upstream) == nil && upstream.Code != "" {
		detail := upstream.Detail
		if detail == "" {
			detail = "Core Registry rejected the control state mutation."
		}
		title := upstream.Title
		if title == "" {
			title = http.StatusText(status)
		}
		return &ControlError{
			HTTPStatus: status,
			Code:       upstream.Code,
			Title:      title,
			Detail:     detail,
			Retryable:  upstream.Retryable,
		}
	}
	switch status {
	case http.StatusBadRequest:
		return &ControlError{HTTPStatus: http.StatusBadRequest, Code: "CONTROL_EXIT_REQUEST_INVALID", Title: "Control exit request invalid", Detail: "The control state mutation is invalid."}
	case http.StatusForbidden:
		return &ControlError{HTTPStatus: http.StatusForbidden, Code: "CONTROL_EXIT_FORBIDDEN", Title: "Control exit forbidden", Detail: "The authenticated Principal is not allowed to update control state."}
	case http.StatusNotFound:
		return &ControlError{HTTPStatus: http.StatusNotFound, Code: "RESOURCE_NOT_FOUND", Title: "Resource not found", Detail: "The requested Site or Asset was not found."}
	case http.StatusConflict:
		return &ControlError{HTTPStatus: http.StatusConflict, Code: "REGISTRY_REVISION_CONFLICT", Title: "Control state conflict", Detail: "The expected control state revision is stale or conflicts with another request."}
	default:
		return coreUnavailableError("Core Registry is temporarily unavailable.")
	}
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

func validSiteControlMode(mode string) bool {
	switch mode {
	case SiteControlModeRemoteAuto, SiteControlModeManualOverride, SiteControlModeSafeShutdownExit:
		return true
	default:
		return false
	}
}

func containsString(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}
