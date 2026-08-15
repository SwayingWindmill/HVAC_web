package gateway

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/analyticsmodel"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/observability"
)

const (
	PublicEnergySeriesPath             = "/api/v1/analytics/energy-series"
	internalEnergySeriesPath           = "/internal/v1/analytics/energy-series"
	analyticsDecisionPath              = "/internal/v1/analytics/decision"
	analyticsAuthorizeAction           = "analytics:authorize"
	defaultAnalyticsTimeout            = 8 * time.Second
	defaultAnalyticsAuthorizationLimit = int64(64 << 10)
	defaultAnalyticsResponseLimit      = int64(8 << 20)
	maximumAnalyticsRequestSize        = int64(64 << 10)
)

type AnalyticsConfig struct {
	QueryBaseURL     string
	QueryHTTPClient  *http.Client
	QueryAudience    string
	Timeout          time.Duration
	MaxResponseBytes int64
}

type analyticsController struct {
	queryBaseURL     string
	queryHTTPClient  *http.Client
	queryAudience    string
	timeout          time.Duration
	maxResponseBytes int64
}

type analyticsFailure struct {
	status    int
	code      string
	title     string
	detail    string
	retryable bool
}

func newAnalyticsController(config *AnalyticsConfig) *analyticsController {
	if config == nil {
		return nil
	}
	resolved := *config
	resolved.QueryBaseURL = strings.TrimRight(strings.TrimSpace(resolved.QueryBaseURL), "/")
	if resolved.QueryHTTPClient == nil {
		resolved.QueryHTTPClient = &http.Client{}
	}
	if resolved.QueryAudience == "" {
		resolved.QueryAudience = "telemetry-query-service"
	}
	if resolved.Timeout <= 0 || resolved.Timeout > 30*time.Second {
		resolved.Timeout = defaultAnalyticsTimeout
	}
	if resolved.MaxResponseBytes <= 0 || resolved.MaxResponseBytes > 16<<20 {
		resolved.MaxResponseBytes = defaultAnalyticsResponseLimit
	}
	return &analyticsController{
		queryBaseURL: resolved.QueryBaseURL, queryHTTPClient: resolved.QueryHTTPClient,
		queryAudience: resolved.QueryAudience, timeout: resolved.Timeout, maxResponseBytes: resolved.MaxResponseBytes,
	}
}

func (h *handler) QueryEnergySeries(writer http.ResponseWriter, request *http.Request) {
	session, ok := h.analyticsSession(writer, request)
	if !ok {
		return
	}
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, maximumAnalyticsRequestSize))
	decoder.DisallowUnknownFields()
	var query analyticsmodel.EnergySeriesQuery
	if decoder.Decode(&query) != nil || ensureAnalyticsJSONEOF(decoder) != nil {
		h.writeAnalyticsFailure(writer, request, analyticsFailure{http.StatusBadRequest, "ANALYTICS_REQUEST_INVALID", "Analytics request invalid", "The Energy Series request body is invalid.", false})
		return
	}
	if err := query.Validate(); err != nil {
		h.writeAnalyticsFailure(writer, request, analyticsFailure{http.StatusUnprocessableEntity, "ANALYTICS_QUERY_INVALID", "Analytics query invalid", "The Energy Series query exceeds the supported product boundary.", false})
		return
	}
	grant, failure := h.authorizeAnalytics(request.Context(), request, session, query)
	if failure != nil {
		h.writeAnalyticsFailure(writer, request, *failure)
		return
	}
	body, err := json.Marshal(query)
	if err != nil {
		h.writeAnalyticsFailure(writer, request, analyticsUnavailable("The Gateway could not encode the Energy Series query."))
		return
	}
	raw, failure := h.executeAnalyticsQuery(request.Context(), request, body, grant)
	if failure != nil {
		h.writeAnalyticsFailure(writer, request, *failure)
		return
	}
	var response analyticsmodel.EnergySeriesResponse
	decodeErr := decodeStrictAnalyticsJSON(raw, &response)
	var validationErr error
	if decodeErr == nil {
		validationErr = validateEnergySeriesResponse(response, query)
	}
	if decodeErr != nil || validationErr != nil {
		firstStart := ""
		lastEnd := ""
		if len(response.Points) > 0 {
			firstStart = response.Points[0].PeriodStart.UTC().Format(time.RFC3339Nano)
			lastEnd = response.Points[len(response.Points)-1].PeriodEnd.UTC().Format(time.RFC3339Nano)
		}
		h.logger.WarnContext(
			request.Context(),
			"analytics_response_contract_rejected",
			"decode_error", errorText(decodeErr),
			"validation_error", errorText(validationErr),
			"point_count", len(response.Points),
			"requested_granularity", query.Granularity,
			"actual_granularity", response.Metadata.ActualGranularity,
			"first_period_start", firstStart,
			"last_period_end", lastEnd,
		)
		h.writeAnalyticsFailure(writer, request, analyticsFailure{http.StatusBadGateway, "ANALYTICS_RESPONSE_INVALID", "Analytics response invalid", "Telemetry Query Service returned a response outside the product contract.", true})
		return
	}
	result := "complete"
	if response.Metadata.Partial {
		result = "partial"
	}
	_ = h.observability.Metrics.AddCounter("hvac_analytics_gateway_responses_total", "Gateway Energy Series responses.", map[string]string{"result": result, "granularity": string(query.Granularity)}, 1)
	writer.Header().Set("Cache-Control", "private, no-store")
	writeJSON(writer, http.StatusOK, response)
}

func (h *handler) analyticsSession(writer http.ResponseWriter, request *http.Request) (bffSession, bool) {
	session, ok := routeSessionFromContext(request.Context())
	if !ok {
		var failure *identityFailure
		session, failure = h.identitySession(request)
		if failure != nil {
			writeIdentityFailure(writer, request, *failure)
			return bffSession{}, false
		}
	}
	if h.identity == nil {
		h.writeAnalyticsFailure(writer, request, analyticsUnavailable("Session validation is unavailable."))
		return bffSession{}, false
	}
	csrf := request.Header.Get("X-CSRF-Token")
	if csrf == "" {
		h.writeAnalyticsFailure(writer, request, analyticsFailure{http.StatusForbidden, "CSRF_REQUIRED", "CSRF token required", "A CSRF token is required for this Analytics request.", false})
		return bffSession{}, false
	}
	if request.Header.Get("Origin") != h.identity.config.PublicOrigin || subtle.ConstantTimeCompare([]byte(csrf), []byte(session.CSRFToken)) != 1 {
		h.writeAnalyticsFailure(writer, request, analyticsFailure{http.StatusForbidden, "CSRF_INVALID", "CSRF token invalid", "The request Origin or CSRF token is invalid.", false})
		return bffSession{}, false
	}
	return session, true
}

func (h *handler) authorizeAnalytics(ctx context.Context, publicRequest *http.Request, session bffSession, query analyticsmodel.EnergySeriesQuery) (string, *analyticsFailure) {
	presenterSPIFFE := ""
	if h.identity != nil {
		presenterSPIFFE = h.identity.config.ExecutingWorkloadSPIFFE
	}
	return h.authorizeAnalyticsForPresenter(ctx, publicRequest, session, query, presenterSPIFFE)
}

func (h *handler) authorizeAnalyticsForPresenter(
	ctx context.Context,
	publicRequest *http.Request,
	session bffSession,
	query analyticsmodel.EnergySeriesQuery,
	presenterSPIFFE string,
) (string, *analyticsFailure) {
	if h.identity == nil || h.analytics == nil || h.analytics.queryAudience == "" || strings.TrimSpace(presenterSPIFFE) == "" {
		failure := analyticsUnavailable("Analytics authorization is not configured.")
		return "", &failure
	}
	now := h.identity.now().UTC()
	expiresAt := now.Add(h.identity.config.DelegationTTL)
	if expiresAt.After(session.ExpiresAt) {
		expiresAt = session.ExpiresAt
	}
	if !expiresAt.After(now) {
		failure := analyticsFailure{http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "Authentication required", "The authenticated Session has expired.", false}
		return "", &failure
	}
	parent := identitycontext.DelegationClaims{
		Issuer: h.identity.config.ExecutingWorkloadSPIFFE, Subject: session.Principal.Subject, SubjectIssuer: session.Principal.Issuer,
		DisplayName: session.Principal.DisplayName, Email: session.Principal.Email, Roles: append([]string(nil), session.Principal.Roles...),
		ExecutingService: h.identity.config.ExecutingWorkloadSPIFFE, Audience: h.identity.config.IAMAudience,
		TenantID: session.TenantID, Actions: []string{analyticsAuthorizeAction}, Scopes: []string{"session:" + session.ID},
		PolicyRevision: h.identity.config.PolicyRevision, SessionID: session.ID, IssuedAt: now.Unix(), ExpiresAt: expiresAt.Unix(), TokenID: randomURLToken(16),
	}
	delegation, err := identitycontext.SignDelegation(h.identity.config.DelegationSigner, parent)
	if err != nil {
		failure := analyticsUnavailable("The Gateway could not sign the Analytics authorization request.")
		return "", &failure
	}
	decisionBody, err := json.Marshal(analyticsmodel.AuthorizationDecisionRequest{
		TenantID: session.TenantID, SiteID: query.SiteID, Action: analyticsmodel.EnergySeriesAction,
	})
	if err != nil {
		failure := analyticsUnavailable("The Gateway could not encode the Analytics authorization request.")
		return "", &failure
	}
	authorizationContext, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(authorizationContext, http.MethodPost, strings.TrimRight(h.identity.config.IAMURL, "/")+analyticsDecisionPath, bytes.NewReader(decisionBody))
	if err != nil {
		failure := analyticsUnavailable("The Gateway could not construct the Analytics authorization request.")
		return "", &failure
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, application/problem+json")
	request.Header.Set("X-Delegation-Grant", delegation)
	request.Header.Set("X-Request-ID", requestIDFromContext(publicRequest.Context()))
	observability.InjectHTTP(ctx, request.Header)
	response, err := h.identity.config.IAMHTTPClient.Do(request)
	if err != nil {
		failure := analyticsUnavailable("IAM Analytics authorization is temporarily unavailable.")
		return "", &failure
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		if response.StatusCode >= http.StatusInternalServerError {
			failure := analyticsUnavailable("IAM Analytics authorization is temporarily unavailable.")
			return "", &failure
		}
		failure := analyticsFailure{http.StatusBadGateway, "ANALYTICS_AUTHORIZATION_INVALID", "Analytics authorization invalid", "IAM rejected an internally signed Analytics authorization request.", true}
		return "", &failure
	}
	raw, err := readBoundedBody(response.Body, defaultAnalyticsAuthorizationLimit)
	if err != nil {
		failure := analyticsUnavailable("IAM returned an oversized or unreadable Analytics decision.")
		return "", &failure
	}
	var result analyticsmodel.AuthorizationDecisionResponse
	if decodeStrictAnalyticsJSON(raw, &result) != nil || !validateAnalyticsDecision(result.Decision, session, query) {
		failure := analyticsUnavailable("IAM returned an Analytics decision outside the authenticated request boundary.")
		return "", &failure
	}
	if !result.Decision.Allowed {
		failure := analyticsFailure{http.StatusForbidden, "ANALYTICS_ACCESS_DENIED", "Analytics access denied", "The requested Site is not authorized for Energy Analytics.", false}
		return "", &failure
	}
	scope, err := query.ScopeDigest()
	if err != nil {
		failure := analyticsFailure{http.StatusUnprocessableEntity, "ANALYTICS_QUERY_INVALID", "Analytics query invalid", "The Energy Series query exceeds the supported product boundary.", false}
		return "", &failure
	}
	claims := identitycontext.DelegationClaims{
		Issuer: h.identity.config.ExecutingWorkloadSPIFFE, Subject: session.Principal.Subject, SubjectIssuer: session.Principal.Issuer,
		PrincipalID: result.Decision.PrincipalID,
		DisplayName: session.Principal.DisplayName, Email: session.Principal.Email, Roles: append([]string(nil), session.Principal.Roles...),
		ExecutingService: presenterSPIFFE, Audience: h.analytics.queryAudience,
		TenantID: query.TenantID, Actions: []string{analyticsmodel.EnergySeriesAction}, Scopes: []string{scope},
		PolicyRevision: result.Decision.PolicyRevision, SessionID: session.ID, IssuedAt: now.Unix(), ExpiresAt: expiresAt.Unix(), TokenID: randomURLToken(16),
	}
	grant, err := identitycontext.SignDelegation(h.identity.config.DelegationSigner, claims)
	if err != nil {
		failure := analyticsUnavailable("The Gateway could not sign the Energy Series delegation grant.")
		return "", &failure
	}
	return grant, nil
}

func validateAnalyticsDecision(decision analyticsmodel.AuthorizationDecision, session bffSession, query analyticsmodel.EnergySeriesQuery) bool {
	if decision.PrincipalID == "" || decision.Subject != session.Principal.Subject || decision.SubjectIssuer != session.Principal.Issuer ||
		decision.TenantID != session.TenantID || decision.TenantID != query.TenantID || decision.SiteID != query.SiteID || decision.Action != analyticsmodel.EnergySeriesAction ||
		strings.TrimSpace(decision.PolicyRevision) == "" {
		return false
	}
	if decision.Allowed {
		switch decision.ReasonCode {
		case analyticsmodel.AuthorizationReasonAllowSiteBinding:
		default:
			return false
		}
	} else {
		switch decision.ReasonCode {
		case analyticsmodel.AuthorizationReasonDenyExplicit, analyticsmodel.AuthorizationReasonDenyPrincipal, analyticsmodel.AuthorizationReasonDenyMembership, analyticsmodel.AuthorizationReasonDenyAction:
		default:
			return false
		}
	}
	_, err := time.Parse(time.RFC3339Nano, decision.DecidedAt)
	return err == nil
}

func (h *handler) executeAnalyticsQuery(ctx context.Context, publicRequest *http.Request, body []byte, grant string) ([]byte, *analyticsFailure) {
	if h.analytics == nil || h.analytics.queryBaseURL == "" || h.analytics.queryHTTPClient == nil {
		failure := analyticsUnavailable("Telemetry Query Service is not configured.")
		return nil, &failure
	}
	requestContext, cancel := context.WithTimeout(ctx, h.analytics.timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodPost, h.analytics.queryBaseURL+internalEnergySeriesPath, bytes.NewReader(body))
	if err != nil {
		failure := analyticsUnavailable("The Gateway could not construct the Telemetry Query request.")
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
			failure := analyticsFailure{http.StatusGatewayTimeout, "ANALYTICS_TIMEOUT", "Analytics request timed out", "Telemetry Query Service did not respond within the bounded deadline.", true}
			return nil, &failure
		}
		failure := analyticsUnavailable("Telemetry Query Service is temporarily unavailable.")
		return nil, &failure
	}
	defer response.Body.Close()
	raw, err := readBoundedBody(response.Body, h.analytics.maxResponseBytes)
	if err != nil {
		failure := analyticsFailure{http.StatusBadGateway, "ANALYTICS_RESPONSE_INVALID", "Analytics response invalid", "Telemetry Query Service returned an oversized or unreadable response.", true}
		return nil, &failure
	}
	if response.StatusCode == http.StatusOK {
		return raw, nil
	}
	if response.StatusCode == http.StatusGatewayTimeout {
		failure := analyticsFailure{http.StatusGatewayTimeout, "ANALYTICS_TIMEOUT", "Analytics request timed out", "Telemetry Query Service did not complete the request within its deadline.", true}
		return nil, &failure
	}
	if response.StatusCode == http.StatusServiceUnavailable {
		failure := analyticsUnavailable("Telemetry Query Service is temporarily unavailable.")
		return nil, &failure
	}
	failure := analyticsFailure{http.StatusBadGateway, "ANALYTICS_UPSTREAM_INVALID", "Analytics upstream invalid", "Telemetry Query Service rejected an internally authorized request.", true}
	return nil, &failure
}

func validateEnergySeriesResponse(response analyticsmodel.EnergySeriesResponse, query analyticsmodel.EnergySeriesQuery) error {
	metadata := response.Metadata
	if response.SchemaVersion != 1 {
		return fmt.Errorf("schema version %d is unsupported", response.SchemaVersion)
	}
	if response.Points == nil {
		return errors.New("points must be an explicit array")
	}
	if metadata.RequestedGranularity != query.Granularity {
		return fmt.Errorf("requested granularity %q does not match query %q", metadata.RequestedGranularity, query.Granularity)
	}
	if metadata.ActualGranularity != query.Granularity {
		return fmt.Errorf("actual granularity %q does not match query %q", metadata.ActualGranularity, query.Granularity)
	}
	if strings.TrimSpace(metadata.DatasetRevision) == "" {
		return errors.New("dataset revision is empty")
	}
	if metadata.QualitySummary.Valid < 0 || metadata.QualitySummary.Suspect < 0 || metadata.QualitySummary.Invalid < 0 {
		return errors.New("quality summary contains a negative count")
	}
	previousEnd := time.Time{}
	for index, point := range response.Points {
		if point.PeriodStart.IsZero() || point.PeriodEnd.IsZero() {
			return fmt.Errorf("point %d has a zero period boundary", index)
		}
		if !point.PeriodStart.Before(point.PeriodEnd) {
			return fmt.Errorf("point %d period is not increasing", index)
		}
		if !point.PeriodEnd.After(query.From) || !point.PeriodStart.Before(query.To) {
			return fmt.Errorf("point %d is outside the requested range", index)
		}
		if point.EnergyKWh < 0 || math.IsNaN(point.EnergyKWh) || math.IsInf(point.EnergyKWh, 0) {
			return fmt.Errorf("point %d energy is not a finite nonnegative value", index)
		}
		if !previousEnd.IsZero() && point.PeriodStart.Before(previousEnd) {
			return fmt.Errorf("point %d overlaps the previous point", index)
		}
		previousEnd = point.PeriodEnd
	}
	return nil
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func decodeStrictAnalyticsJSON(payload []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return ensureAnalyticsJSONEOF(decoder)
}

func ensureAnalyticsJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("unexpected trailing JSON")
		}
		return err
	}
	return nil
}

func analyticsUnavailable(detail string) analyticsFailure {
	return analyticsFailure{http.StatusServiceUnavailable, "ANALYTICS_UNAVAILABLE", "Analytics unavailable", detail, true}
}

func (h *handler) writeAnalyticsFailure(writer http.ResponseWriter, request *http.Request, failure analyticsFailure) {
	writeProblem(writer, request, failure.status, failure.code, failure.title, failure.detail, failure.retryable, nil)
}
