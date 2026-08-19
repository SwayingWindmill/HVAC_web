package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/telemetryauth"
	"github.com/quanlaihe/hvac-web/libs/telemetryhistorymodel"
	"github.com/quanlaihe/hvac-web/services/platform-gateway/pkg/s2telemetryapi"
)

const (
	internalDeviceHistoryPath          = "/internal/v1/telemetry/device-history"
	internalDeviceHistoryAggregatePath = "/internal/v1/telemetry/device-history:aggregate"
)

func (h *handler) QueryDeviceHistory(writer http.ResponseWriter, request *http.Request, input s2telemetryapi.DeviceHistoryRequest) {
	caller, ok := h.telemetryCaller(writer, request, true)
	if !ok {
		return
	}
	selection, failure := historySelection(input)
	if failure != nil {
		h.writeTelemetryFailure(writer, request, *failure)
		return
	}
	authorization, failure := h.authorizeTelemetry(request.Context(), request, caller, telemetryauth.ActionHistoryRead, []telemetryauth.Target{{DeviceID: selection.DeviceID, Keys: selection.Keys}})
	if failure != nil {
		h.writeTelemetryFailure(writer, request, *failure)
		return
	}
	if len(authorization.targets) != 1 {
		h.writeTelemetryFailure(writer, request, historyUnavailable("IAM returned an incomplete Device History resource scope."))
		return
	}
	authorizedTarget := authorization.targets[0]
	canonical, err := selection.Complete(authorizedTarget.TenantID, authorizedTarget.SiteID)
	if err != nil {
		h.writeTelemetryFailure(writer, request, historyUnavailable("IAM returned an incomplete Device History resource scope."))
		return
	}
	grant, failure := h.signHistoryQueryGrant(caller, authorization, canonical.TenantID, telemetryhistorymodel.DeviceHistoryAction, canonical.ScopeDigest)
	if failure != nil {
		h.writeTelemetryFailure(writer, request, *failure)
		return
	}
	raw, failure := h.executeHistoryQuery(request.Context(), request, canonical, grant, internalDeviceHistoryPath)
	if failure != nil {
		h.writeTelemetryFailure(writer, request, *failure)
		return
	}
	var response telemetryhistorymodel.DeviceHistoryResponse
	if decodeStrictTelemetryJSON(raw, &response) != nil || response.ValidateFor(canonical) != nil {
		h.writeTelemetryFailure(writer, request, telemetryFailure{http.StatusBadGateway, "TELEMETRY_HISTORY_RESPONSE_INVALID", "Device History response invalid", "Telemetry Query Service returned a response outside the product contract.", true})
		return
	}
	writer.Header().Set("Cache-Control", "private, no-store")
	writeJSON(writer, http.StatusOK, response)
}

func (h *handler) QueryDeviceHistoryAggregate(writer http.ResponseWriter, request *http.Request, input s2telemetryapi.DeviceHistoryAggregateRequest) {
	caller, ok := h.telemetryCaller(writer, request, true)
	if !ok {
		return
	}
	selection, failure := historyAggregateSelection(input)
	if failure != nil {
		h.writeTelemetryFailure(writer, request, *failure)
		return
	}
	authorization, failure := h.authorizeTelemetry(request.Context(), request, caller, telemetryauth.ActionHistoryRead, []telemetryauth.Target{{DeviceID: selection.DeviceID, Keys: selection.Keys}})
	if failure != nil {
		h.writeTelemetryFailure(writer, request, *failure)
		return
	}
	if len(authorization.targets) != 1 {
		h.writeTelemetryFailure(writer, request, historyUnavailable("IAM returned an incomplete Device History aggregate resource scope."))
		return
	}
	authorizedTarget := authorization.targets[0]
	canonical, err := selection.Complete(authorizedTarget.TenantID, authorizedTarget.SiteID)
	if err != nil {
		h.writeTelemetryFailure(writer, request, historyUnavailable("IAM returned an incomplete Device History aggregate resource scope."))
		return
	}
	grant, failure := h.signHistoryQueryGrant(caller, authorization, canonical.TenantID, telemetryhistorymodel.DeviceHistoryAggregateAction, canonical.ScopeDigest)
	if failure != nil {
		h.writeTelemetryFailure(writer, request, *failure)
		return
	}
	raw, failure := h.executeHistoryQuery(request.Context(), request, canonical, grant, internalDeviceHistoryAggregatePath)
	if failure != nil {
		h.writeTelemetryFailure(writer, request, *failure)
		return
	}
	var response telemetryhistorymodel.DeviceHistoryAggregateResponse
	if decodeStrictTelemetryJSON(raw, &response) != nil || response.ValidateFor(canonical) != nil {
		h.writeTelemetryFailure(writer, request, telemetryFailure{http.StatusBadGateway, "TELEMETRY_HISTORY_AGGREGATE_RESPONSE_INVALID", "Device History aggregate response invalid", "Telemetry Query Service returned an aggregate response outside the product contract.", true})
		return
	}
	writer.Header().Set("Cache-Control", "private, no-store")
	writeJSON(writer, http.StatusOK, response)
}

func historySelection(input s2telemetryapi.DeviceHistoryRequest) (telemetryhistorymodel.DeviceHistoryRequest, *telemetryFailure) {
	from, to, failure := parseHistoryRange(input.From, input.To)
	if failure != nil {
		return telemetryhistorymodel.DeviceHistoryRequest{}, failure
	}
	keys := telemetryHistoryKeys(input.Keys)
	selection := telemetryhistorymodel.DeviceHistoryRequest{
		DeviceID: string(input.DeviceId), Keys: keys, From: from, To: to, PageSize: input.PageSize,
	}
	if input.Cursor != nil {
		cursor := *input.Cursor
		selection.Cursor = &cursor
	}
	if err := selection.Validate(); err != nil {
		failure := telemetryFailure{http.StatusUnprocessableEntity, "TELEMETRY_HISTORY_QUERY_INVALID", "Device History query invalid", "The Device History query exceeds the supported product boundary.", false}
		return telemetryhistorymodel.DeviceHistoryRequest{}, &failure
	}
	return selection, nil
}

func historyAggregateSelection(input s2telemetryapi.DeviceHistoryAggregateRequest) (telemetryhistorymodel.DeviceHistoryAggregateRequest, *telemetryFailure) {
	from, to, failure := parseHistoryRange(input.From, input.To)
	if failure != nil {
		return telemetryhistorymodel.DeviceHistoryAggregateRequest{}, failure
	}
	selection := telemetryhistorymodel.DeviceHistoryAggregateRequest{
		DeviceID: string(input.DeviceId), Keys: telemetryHistoryKeys(input.Keys), From: from, To: to,
		Granularity: telemetryhistorymodel.AggregateGranularity(input.Granularity), Timezone: input.Timezone,
		QualityPolicy: telemetryhistorymodel.AggregateQualityPolicy(input.QualityPolicy),
	}
	if err := selection.Validate(); err != nil {
		failure := telemetryFailure{http.StatusUnprocessableEntity, "TELEMETRY_HISTORY_AGGREGATE_QUERY_INVALID", "Device History aggregate query invalid", "The Device History aggregate query exceeds the supported product boundary.", false}
		return telemetryhistorymodel.DeviceHistoryAggregateRequest{}, &failure
	}
	return selection, nil
}

func parseHistoryRange(fromValue, toValue s2telemetryapi.HistoryInstant) (time.Time, time.Time, *telemetryFailure) {
	from, err := time.Parse(time.RFC3339Nano, string(fromValue))
	if err != nil {
		failure := telemetryFailure{http.StatusUnprocessableEntity, "TELEMETRY_HISTORY_QUERY_INVALID", "Device History query invalid", "The Device History from timestamp is invalid.", false}
		return time.Time{}, time.Time{}, &failure
	}
	to, err := time.Parse(time.RFC3339Nano, string(toValue))
	if err != nil {
		failure := telemetryFailure{http.StatusUnprocessableEntity, "TELEMETRY_HISTORY_QUERY_INVALID", "Device History query invalid", "The Device History to timestamp is invalid.", false}
		return time.Time{}, time.Time{}, &failure
	}
	_, fromOffset := from.Zone()
	_, toOffset := to.Zone()
	if fromOffset != 0 || toOffset != 0 {
		failure := telemetryFailure{http.StatusUnprocessableEntity, "TELEMETRY_HISTORY_QUERY_INVALID", "Device History query invalid", "Device History timestamps must use UTC.", false}
		return time.Time{}, time.Time{}, &failure
	}
	return from.UTC(), to.UTC(), nil
}

func telemetryHistoryKeys(keys []s2telemetryapi.TelemetryKey) []string {
	plain := make([]string, len(keys))
	for index, key := range keys {
		plain[index] = string(key)
	}
	return plain
}

type historyScopeDigest func() (string, error)

func (h *handler) signHistoryQueryGrant(caller telemetryCaller, authorization telemetryAuthorization, tenantID, action string, scopeDigest historyScopeDigest) (string, *telemetryFailure) {
	if h.identity == nil || h.analytics == nil || strings.TrimSpace(h.analytics.queryAudience) == "" || authorization.principalID == "" || authorization.policyRevision == "" {
		failure := historyUnavailable("Device History query authorization is not configured.")
		return "", &failure
	}
	scope, err := scopeDigest()
	if err != nil {
		failure := telemetryFailure{http.StatusUnprocessableEntity, "TELEMETRY_HISTORY_QUERY_INVALID", "Device History query invalid", "The Device History query exceeds the supported product boundary.", false}
		return "", &failure
	}
	now := h.identity.now().UTC()
	expiresAt := now.Add(h.identity.config.DelegationTTL)
	if caller.expiresAt.IsZero() || caller.expiresAt.Before(expiresAt) {
		expiresAt = caller.expiresAt
	}
	if !expiresAt.After(now) {
		failure := telemetryFailure{http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "Authentication required", "The authenticated caller context has expired.", false}
		return "", &failure
	}
	claims := identitycontext.DelegationClaims{
		Issuer: h.identity.config.ExecutingWorkloadSPIFFE, Subject: caller.principal.Subject, SubjectIssuer: caller.principal.Issuer,
		PrincipalID: authorization.principalID, DisplayName: caller.principal.DisplayName, Email: caller.principal.Email,
		Roles: append([]string(nil), caller.principal.Roles...), ExecutingService: h.identity.config.ExecutingWorkloadSPIFFE,
		Audience: h.analytics.queryAudience, TenantID: tenantID,
		Actions: []string{action}, Scopes: []string{scope}, PolicyRevision: authorization.policyRevision, SessionID: caller.contextID,
		IssuedAt: now.Unix(), ExpiresAt: expiresAt.Unix(), TokenID: randomURLToken(16),
	}
	grant, err := identitycontext.SignDelegation(h.identity.config.DelegationSigner, claims)
	if err != nil {
		failure := historyUnavailable("The Gateway could not sign the Device History query grant.")
		return "", &failure
	}
	return grant, nil
}

func (h *handler) executeHistoryQuery(ctx context.Context, publicRequest *http.Request, query any, grant, path string) ([]byte, *telemetryFailure) {
	if h.analytics == nil || h.analytics.queryBaseURL == "" || h.analytics.queryHTTPClient == nil {
		failure := historyUnavailable("Telemetry Query Service is not configured.")
		return nil, &failure
	}
	body, err := json.Marshal(query)
	if err != nil {
		failure := historyUnavailable("The Gateway could not encode the Device History query.")
		return nil, &failure
	}
	requestContext, cancel := context.WithTimeout(ctx, h.analytics.timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodPost, h.analytics.queryBaseURL+path, bytes.NewReader(body))
	if err != nil {
		failure := historyUnavailable("The Gateway could not construct the Device History query request.")
		return nil, &failure
	}
	request.Header.Set("Accept", "application/json, application/problem+json")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Delegation-Grant", grant)
	request.Header.Set("X-Request-ID", requestIDFromContext(publicRequest.Context()))
	observability.InjectHTTP(ctx, request.Header)
	response, err := h.analytics.queryHTTPClient.Do(request)
	if err != nil {
		if errors.Is(requestContext.Err(), context.DeadlineExceeded) {
			failure := telemetryFailure{http.StatusGatewayTimeout, "TELEMETRY_HISTORY_TIMEOUT", "Device History request timed out", "Telemetry Query Service did not respond within the bounded deadline.", true}
			return nil, &failure
		}
		failure := historyUnavailable("Telemetry Query Service is temporarily unavailable.")
		return nil, &failure
	}
	defer response.Body.Close()
	raw, err := readBoundedBody(response.Body, h.analytics.maxResponseBytes)
	if err != nil {
		failure := telemetryFailure{http.StatusBadGateway, "TELEMETRY_HISTORY_RESPONSE_INVALID", "Device History response invalid", "Telemetry Query Service returned an oversized or unreadable response.", true}
		return nil, &failure
	}
	switch response.StatusCode {
	case http.StatusOK:
		return raw, nil
	case http.StatusGatewayTimeout:
		failure := telemetryFailure{http.StatusGatewayTimeout, "TELEMETRY_HISTORY_TIMEOUT", "Device History request timed out", "Telemetry Query Service did not complete the request within its deadline.", true}
		return nil, &failure
	case http.StatusServiceUnavailable:
		failure := historyUnavailable("Telemetry Query Service is temporarily unavailable.")
		return nil, &failure
	default:
		failure := telemetryFailure{http.StatusBadGateway, "TELEMETRY_HISTORY_UPSTREAM_INVALID", "Device History upstream invalid", "Telemetry Query Service rejected an internally authorized request.", true}
		return nil, &failure
	}
}

func historyUnavailable(detail string) telemetryFailure {
	return telemetryFailure{http.StatusServiceUnavailable, "TELEMETRY_HISTORY_UNAVAILABLE", "Device History unavailable", detail, true}
}
