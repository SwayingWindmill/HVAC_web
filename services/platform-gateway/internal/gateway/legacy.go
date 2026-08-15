package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/ownershipregistry"
	"github.com/quanlaihe/hvac-web/libs/sessionevent"
	"github.com/quanlaihe/hvac-web/services/platform-gateway/pkg/platformapi"
)

type LegacyConfig struct {
	BaseURL          string
	Audience         string
	HTTPClient       *http.Client
	Timeout          time.Duration
	FailureThreshold int
	OpenDuration     time.Duration
}

type legacyController struct {
	config LegacyConfig
	mu     sync.Mutex
	fails  int
	openTo time.Time
}

type legacyHealthEnvelope struct {
	Code int `json:"code"`
	Data struct {
		Status    string `json:"status"`
		Timestamp string `json:"timestamp"`
		Version   string `json:"version"`
	} `json:"data"`
}

type legacyFailure struct {
	status    int
	code      string
	title     string
	detail    string
	retryable bool
}

func newLegacyController(config *LegacyConfig) *legacyController {
	if config == nil {
		return nil
	}
	resolved := *config
	resolved.BaseURL = strings.TrimRight(resolved.BaseURL, "/")
	if resolved.Audience == "" {
		resolved.Audience = "legacy-hvac-backend"
	}
	if resolved.HTTPClient == nil {
		resolved.HTTPClient = &http.Client{}
	}
	if resolved.Timeout <= 0 {
		resolved.Timeout = 750 * time.Millisecond
	}
	if resolved.FailureThreshold <= 0 {
		resolved.FailureThreshold = 2
	}
	if resolved.OpenDuration <= 0 {
		resolved.OpenDuration = 5 * time.Second
	}
	if resolved.BaseURL == "" {
		panic("Legacy base URL is required")
	}
	return &legacyController{config: resolved}
}

func (controller *legacyController) callPlatformStatus(ctx context.Context, identity *identityController, session bffSession, decision ownershipregistry.Decision, requestID, traceparent string) (platformapi.PlatformStatusResponse, *legacyFailure) {
	if controller == nil || identity == nil {
		failure := legacyFailure{http.StatusServiceUnavailable, "LEGACY_NOT_CONFIGURED", "Legacy unavailable", "The selected Legacy route is not configured.", true}
		return platformapi.PlatformStatusResponse{}, &failure
	}
	if controller.isOpen(identity.now()) {
		failure := legacyFailure{http.StatusServiceUnavailable, "LEGACY_CIRCUIT_OPEN", "Legacy circuit open", "The Legacy route is temporarily unavailable.", true}
		return platformapi.PlatformStatusResponse{}, &failure
	}

	now := identity.now().UTC()
	expiresAt := now.Add(identity.config.DelegationTTL)
	if expiresAt.After(session.ExpiresAt) {
		expiresAt = session.ExpiresAt
	}
	claims := identitycontext.DelegationClaims{
		Issuer:               identity.config.ExecutingWorkloadSPIFFE,
		Subject:              session.Principal.Subject,
		SubjectIssuer:        session.Principal.Issuer,
		Roles:                []string{},
		ExecutingService:     identity.config.ExecutingWorkloadSPIFFE,
		Audience:             controller.config.Audience,
		TenantID:             session.TenantID,
		Actions:              []string{"legacy:platform-status:read"},
		Scopes:               []string{"tenant:" + session.TenantID},
		PolicyRevision:       identity.config.PolicyRevision,
		SessionID:            sessionevent.AuditAggregateID(session.ID),
		IssuedAt:             now.Unix(),
		ExpiresAt:            expiresAt.Unix(),
		TokenID:              randomURLToken(16),
	}
	grant, err := identitycontext.SignDelegation(identity.config.DelegationSigner, claims)
	if err != nil {
		failure := legacyFailure{http.StatusServiceUnavailable, "LEGACY_DELEGATION_FAILED", "Legacy delegation unavailable", "The Gateway could not create a restricted Legacy delegation.", true}
		return platformapi.PlatformStatusResponse{}, &failure
	}

	ctx, span := observability.Start(ctx, "http.legacy.platform_status", observability.SpanKindClient, map[string]any{
		"http.request.method": http.MethodGet, "server.service": "legacy-hvac-backend", "route.owner": decision.SelectedOwner,
	})
	defer span.End()
	requestContext, cancel := context.WithTimeout(ctx, controller.config.Timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodGet, controller.config.BaseURL+"/api/v1/health", nil)
	if err != nil {
		failure := legacyFailure{http.StatusServiceUnavailable, "LEGACY_REQUEST_INVALID", "Legacy unavailable", "The Legacy request could not be constructed.", true}
		return platformapi.PlatformStatusResponse{}, &failure
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-Delegation-Grant", grant)
	request.Header.Set("X-Route-Policy-Revision", formatRevision(decision.RegistryRevision))
	request.Header.Set("X-Request-ID", requestID)
	if traceparent != "" {
		request.Header.Set("traceparent", traceparent)
	}
	observability.InjectHTTP(requestContext, request.Header)

	response, err := controller.config.HTTPClient.Do(request)
	if err != nil {
		controller.recordFailure(identity.now())
		if errors.Is(requestContext.Err(), context.DeadlineExceeded) {
			failure := legacyFailure{http.StatusGatewayTimeout, "LEGACY_TIMEOUT", "Legacy timeout", "The Legacy route did not respond before the Gateway deadline.", true}
			return platformapi.PlatformStatusResponse{}, &failure
		}
		failure := legacyFailure{http.StatusServiceUnavailable, "LEGACY_UNAVAILABLE", "Legacy unavailable", "The private Legacy service could not be reached.", true}
		return platformapi.PlatformStatusResponse{}, &failure
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		controller.recordFailure(identity.now())
		failure := legacyFailure{http.StatusServiceUnavailable, "LEGACY_RESPONSE_REJECTED", "Legacy response rejected", "The private Legacy service rejected the restricted request.", true}
		return platformapi.PlatformStatusResponse{}, &failure
	}
	var envelope legacyHealthEnvelope
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1<<20))
	if err := decoder.Decode(&envelope); err != nil || envelope.Code != http.StatusOK || envelope.Data.Status != "UP" || envelope.Data.Version == "" || envelope.Data.Timestamp == "" {
		controller.recordFailure(identity.now())
		failure := legacyFailure{http.StatusServiceUnavailable, "LEGACY_RESPONSE_INVALID", "Legacy response invalid", "The Legacy response could not be normalized safely.", true}
		return platformapi.PlatformStatusResponse{}, &failure
	}
	if _, err := time.Parse(time.RFC3339Nano, envelope.Data.Timestamp); err != nil {
		controller.recordFailure(identity.now())
		failure := legacyFailure{http.StatusServiceUnavailable, "LEGACY_RESPONSE_INVALID", "Legacy response invalid", "The Legacy response timestamp is invalid.", true}
		return platformapi.PlatformStatusResponse{}, &failure
	}
	controller.recordSuccess()
	return platformapi.PlatformStatusResponse{
		Status:              "ok",
		Service:             "platform-status",
		Implementation:      "legacy",
		Version:             envelope.Data.Version,
		CheckedAt:           envelope.Data.Timestamp,
		RoutePolicyRevision: decision.RegistryRevision,
		RouteRevision:       decision.RouteRevision,
		CompatibilityMode:   decision.CompatibilityMode,
	}, nil
}

func (controller *legacyController) isOpen(now time.Time) bool {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	if controller.openTo.IsZero() || !now.Before(controller.openTo) {
		if !controller.openTo.IsZero() {
			controller.openTo = time.Time{}
			controller.fails = 0
		}
		return false
	}
	return true
}

func (controller *legacyController) recordFailure(now time.Time) {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	controller.fails++
	if controller.fails >= controller.config.FailureThreshold {
		controller.openTo = now.Add(controller.config.OpenDuration)
	}
}

func (controller *legacyController) recordSuccess() {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	controller.fails = 0
	controller.openTo = time.Time{}
}

func formatRevision(value int64) string {
	return strconv.FormatInt(value, 10)
}
