package iam

import (
	"crypto"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandauth"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/libs/telemetryauth"
)

const (
	CurrentPrincipalPath       = "/internal/v1/principal/current"
	RegistryReadDecisionPath   = "/internal/v1/registry-read/decision"
	TelemetryDecisionPath      = "/internal/v1/telemetry/decision"
	CommandDecisionPath        = "/internal/v1/command/decision"
	registryAuthorizeAction    = "registry:authorize"
	telemetryAuthorizeAction   = "telemetry:authorize"
	commandAuthorizeAction     = "command:authorize"
	maximumDecisionRequestSize = 64 << 10
	maximumGrantStatusSize     = 16 << 10
)

type Config struct {
	AllowedWorkloadSPIFFE       string
	CoreWorkloadSPIFFE          string
	Audience                    string
	Logger                      *slog.Logger
	Observability               *observability.Runtime
	Now                         func() time.Time
	AuthorizationStore          AuthorizationStore
	RegistryGrantSigner         crypto.Signer
	RegistryGrantIssuer         string
	RegistryGrantAudience       string
	RegistryGrantLifetime       time.Duration
	NewRegistryGrantID          func() string
	RegistryAuditSink           RegistryDecisionAuditSink
	RegistryGrantStatus         RegistryGrantStatusStore
	TelemetryAuthorizationStore TelemetryAuthorizationStore
	TelemetryGrantSigner        crypto.Signer
	TelemetryGrantIssuer        string
	TelemetryGrantAudience      string
	TelemetryGrantLifetime      time.Duration
	NewTelemetryGrantID         func() string
	TelemetryAuditSink          TelemetryDecisionAuditSink
	TelemetryRuntimeSPIFFE      string
	TelemetryGrantStore         TelemetryGrantStore
	CommandAuthorizationStore   CommandAuthorizationStore
	CommandGrantSigner          crypto.Signer
	CommandGrantIssuer          string
	CommandGrantAudience        string
	CommandGrantLifetime        time.Duration
	NewCommandGrantID           func() string
}

type handler struct {
	allowedWorkloadSPIFFE       string
	coreWorkloadSPIFFE          string
	audience                    string
	logger                      *slog.Logger
	observability               *observability.Runtime
	now                         func() time.Time
	authorizationStore          AuthorizationStore
	registryGrantSigner         crypto.Signer
	registryGrantIssuer         string
	registryGrantAudience       string
	registryGrantLifetime       time.Duration
	newRegistryGrantID          func() string
	registryAuditSink           RegistryDecisionAuditSink
	registryGrantStatus         RegistryGrantStatusStore
	telemetryAuthorizationStore TelemetryAuthorizationStore
	telemetryGrantSigner        crypto.Signer
	telemetryGrantIssuer        string
	telemetryGrantAudience      string
	telemetryGrantLifetime      time.Duration
	newTelemetryGrantID         func() string
	telemetryAuditSink          TelemetryDecisionAuditSink
	telemetryRuntimeSPIFFE      string
	telemetryGrantStore         TelemetryGrantStore
	commandAuthorizationStore   CommandAuthorizationStore
	commandGrantSigner          crypto.Signer
	commandGrantIssuer          string
	commandGrantAudience        string
	commandGrantLifetime        time.Duration
	newCommandGrantID           func() string
}

func NewHandler(config Config) http.Handler {
	logger := config.Logger
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	telemetry := config.Observability
	if telemetry == nil {
		telemetry = observability.NewRuntime(observability.RuntimeConfig{Service: "iam-service"})
	}
	store := config.AuthorizationStore
	if store == nil {
		store = NewDenyAllAuthorizationStore("policy-unconfigured")
	}
	grantIssuer := config.RegistryGrantIssuer
	if grantIssuer == "" {
		grantIssuer = "spiffe://hvac.local/iam-service"
	}
	grantAudience := config.RegistryGrantAudience
	if grantAudience == "" {
		grantAudience = "platform-core-service"
	}
	grantLifetime := config.RegistryGrantLifetime
	if grantLifetime <= 0 || grantLifetime > registryauth.MaximumGrantLifetime {
		grantLifetime = registryauth.MaximumGrantLifetime
	}
	newGrantID := config.NewRegistryGrantID
	if newGrantID == nil {
		newGrantID = randomIdentifier
	}
	auditSink := config.RegistryAuditSink
	if auditSink == nil {
		auditSink = newLoggerRegistryDecisionAuditSink(logger)
	}
	telemetryStore := config.TelemetryAuthorizationStore
	if telemetryStore == nil {
		telemetryStore = newDenyAllTelemetryAuthorizationStore("telemetry-policy-unconfigured")
	}
	telemetrySigner := config.TelemetryGrantSigner
	if telemetrySigner == nil {
		telemetrySigner = config.RegistryGrantSigner
	}
	telemetryIssuer := config.TelemetryGrantIssuer
	if telemetryIssuer == "" {
		telemetryIssuer = grantIssuer
	}
	telemetryAudience := config.TelemetryGrantAudience
	if telemetryAudience == "" {
		telemetryAudience = "telemetry-runtime-service"
	}
	telemetryLifetime := config.TelemetryGrantLifetime
	if telemetryLifetime <= 0 || telemetryLifetime > telemetryauth.MaximumGrantLifetime {
		telemetryLifetime = telemetryauth.MaximumGrantLifetime
	}
	newTelemetryGrantID := config.NewTelemetryGrantID
	if newTelemetryGrantID == nil {
		newTelemetryGrantID = randomIdentifier
	}
	telemetryAuditSink := config.TelemetryAuditSink
	if telemetryAuditSink == nil {
		telemetryAuditSink = newLoggerTelemetryDecisionAuditSink(logger)
	}
	commandStore := config.CommandAuthorizationStore
	if commandStore == nil {
		commandStore = newDenyAllCommandAuthorizationStore("command-policy-unconfigured")
	}
	commandSigner := config.CommandGrantSigner
	if commandSigner == nil {
		commandSigner = config.RegistryGrantSigner
	}
	commandIssuer := config.CommandGrantIssuer
	if commandIssuer == "" {
		commandIssuer = grantIssuer
	}
	commandAudience := config.CommandGrantAudience
	if commandAudience == "" {
		commandAudience = "command-service"
	}
	commandLifetime := config.CommandGrantLifetime
	if commandLifetime <= 0 || commandLifetime > commandauth.MaximumGrantLifetime {
		commandLifetime = commandauth.MaximumGrantLifetime
	}
	newCommandGrantID := config.NewCommandGrantID
	if newCommandGrantID == nil {
		newCommandGrantID = randomIdentifier
	}
	return &handler{
		allowedWorkloadSPIFFE:       config.AllowedWorkloadSPIFFE,
		coreWorkloadSPIFFE:          config.CoreWorkloadSPIFFE,
		audience:                    config.Audience,
		logger:                      logger,
		observability:               telemetry,
		now:                         now,
		authorizationStore:          store,
		registryGrantSigner:         config.RegistryGrantSigner,
		registryGrantIssuer:         grantIssuer,
		registryGrantAudience:       grantAudience,
		registryGrantLifetime:       grantLifetime,
		newRegistryGrantID:          newGrantID,
		registryAuditSink:           auditSink,
		registryGrantStatus:         config.RegistryGrantStatus,
		telemetryAuthorizationStore: telemetryStore,
		telemetryGrantSigner:        telemetrySigner,
		telemetryGrantIssuer:        telemetryIssuer,
		telemetryGrantAudience:      telemetryAudience,
		telemetryGrantLifetime:      telemetryLifetime,
		newTelemetryGrantID:         newTelemetryGrantID,
		telemetryAuditSink:          telemetryAuditSink,
		telemetryRuntimeSPIFFE:      config.TelemetryRuntimeSPIFFE,
		telemetryGrantStore:         config.TelemetryGrantStore,
		commandAuthorizationStore:   commandStore,
		commandGrantSigner:          commandSigner,
		commandGrantIssuer:          commandIssuer,
		commandGrantAudience:        commandAudience,
		commandGrantLifetime:        commandLifetime,
		newCommandGrantID:           newCommandGrantID,
	}
}

func (h *handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	started := h.now()
	ctx := h.observability.Tracer.ExtractHTTP(request.Context(), request.Header)
	ctx, span := h.observability.Tracer.Start(ctx, "http.iam.request", observability.SpanKindServer, map[string]any{
		"http.request.method": request.Method, "http.route": safePath(request.URL.Path),
	})
	request = request.WithContext(ctx)
	writer.Header().Set("traceparent", observability.Traceparent(ctx))
	status := http.StatusOK
	defer func() {
		result := "ok"
		if status >= http.StatusBadRequest {
			result = "error"
			span.SetStatus("error", http.StatusText(status))
		} else {
			span.SetStatus("ok", "")
		}
		span.SetAttributes(map[string]any{"http.response.status_code": status})
		span.End()
		_ = h.observability.Metrics.AddCounter("s0_http_requests_total", "IAM HTTP requests.", map[string]string{"service": "iam-service", "route": safePath(request.URL.Path), "method": request.Method, "result": result}, 1)
		_ = h.observability.Metrics.ObserveHistogram("s0_http_request_duration_seconds", "IAM HTTP request latency.", map[string]string{"service": "iam-service", "route": safePath(request.URL.Path), "method": request.Method}, h.now().Sub(started).Seconds(), nil)
		h.logger.InfoContext(request.Context(), "iam_request",
			"method", request.Method,
			"path", safePath(request.URL.Path),
			"status", status,
			"duration_ms", h.now().Sub(started).Milliseconds(),
		)
	}()

	if request.URL.Path == RegistryGrantStatusPath {
		status = h.handleRegistryGrantStatusRoute(writer, request)
		return
	}
	if request.URL.Path == TelemetryGrantConsumePath || request.URL.Path == TelemetryRevocationPollPath {
		status = h.handleTelemetryRuntimeRoute(writer, request)
		return
	}

	expectedAction, knownRoute := expectedInboundAction(request.URL.Path)
	if !knownRoute {
		status = http.StatusNotFound
		writeProblem(writer, status, "IAM_ROUTE_NOT_FOUND", "The requested IAM route does not exist.")
		return
	}
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", http.MethodPost)
		status = http.StatusMethodNotAllowed
		writeProblem(writer, status, "IAM_METHOD_NOT_ALLOWED", "This IAM route only supports POST.")
		return
	}
	if hasForgedIdentityHeader(request.Header) {
		status = http.StatusBadRequest
		writeProblem(writer, status, "IAM_FORGED_IDENTITY_HEADER", "Caller-supplied identity or business-scope headers are not accepted.")
		return
	}

	peerCertificate, spiffeID, ok := peerIdentity(request)
	if !ok || spiffeID != h.allowedWorkloadSPIFFE {
		status = http.StatusUnauthorized
		writeProblem(writer, status, "IAM_WORKLOAD_IDENTITY_INVALID", "The calling workload identity is not trusted.")
		return
	}
	grant := request.Header.Get("X-Delegation-Grant")
	claims, err := identitycontext.VerifyDelegation(peerCertificate.PublicKey, grant)
	if err != nil {
		status = http.StatusUnauthorized
		writeProblem(writer, status, "IAM_DELEGATION_INVALID", "The delegated identity context is invalid.")
		return
	}
	scope := "session:" + claims.SessionID
	if err := identitycontext.ValidateDelegation(claims, h.now(), spiffeID, h.audience, expectedAction, scope); err != nil {
		status = http.StatusForbidden
		writeProblem(writer, status, "IAM_DELEGATION_REJECTED", "The delegated identity context is not authorized for this operation.")
		return
	}

	switch request.URL.Path {
	case CurrentPrincipalPath:
		status = h.handleCurrentPrincipal(writer, claims, spiffeID)
	case RegistryReadDecisionPath:
		status = h.handleRegistryReadDecision(writer, request, claims, spiffeID)
	case TelemetryDecisionPath:
		status = h.handleTelemetryDecision(writer, request, claims, spiffeID)
	case CommandDecisionPath:
		status = h.handleCommandDecision(writer, request, claims, spiffeID)
	}
}

func (h *handler) handleRegistryGrantStatusRoute(writer http.ResponseWriter, request *http.Request) int {
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", http.MethodPost)
		writeProblem(writer, http.StatusMethodNotAllowed, "IAM_METHOD_NOT_ALLOWED", "This IAM route only supports POST.")
		return http.StatusMethodNotAllowed
	}
	if hasForgedIdentityHeader(request.Header) {
		writeProblem(writer, http.StatusBadRequest, "IAM_FORGED_IDENTITY_HEADER", "Caller-supplied identity or business-scope headers are not accepted.")
		return http.StatusBadRequest
	}
	_, spiffeID, ok := peerIdentity(request)
	if !ok || h.coreWorkloadSPIFFE == "" || spiffeID != h.coreWorkloadSPIFFE {
		writeProblem(writer, http.StatusUnauthorized, "IAM_WORKLOAD_IDENTITY_INVALID", "The calling workload identity is not trusted.")
		return http.StatusUnauthorized
	}
	if h.registryGrantStatus == nil {
		writeProblem(writer, http.StatusServiceUnavailable, "IAM_GRANT_STATUS_UNAVAILABLE", "Registry grant status is unavailable.")
		return http.StatusServiceUnavailable
	}
	request.Body = http.MaxBytesReader(writer, request.Body, maximumGrantStatusSize)
	var input registryauth.GrantStatusRequest
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || ensureJSONEOF(decoder) != nil || input.Validate() != nil {
		writeProblem(writer, http.StatusBadRequest, "IAM_GRANT_STATUS_REQUEST_INVALID", "The Registry grant status request is invalid.")
		return http.StatusBadRequest
	}
	status, err := h.registryGrantStatus.LookupRegistryGrantStatus(request.Context(), input.ActingOrganizationID, input.TokenID)
	if err != nil {
		writeProblem(writer, http.StatusServiceUnavailable, "IAM_GRANT_STATUS_UNAVAILABLE", "Registry grant status is unavailable.")
		return http.StatusServiceUnavailable
	}
	writeJSON(writer, http.StatusOK, status)
	return http.StatusOK
}

func (h *handler) handleCurrentPrincipal(writer http.ResponseWriter, claims identitycontext.DelegationClaims, spiffeID string) int {
	response := identitycontext.InternalPrincipalResponse{
		Principal: identitycontext.UserPrincipal{
			Subject:     claims.Subject,
			Issuer:      claims.SubjectIssuer,
			DisplayName: claims.DisplayName,
			Email:       claims.Email,
			Roles:       append([]string(nil), claims.Roles...),
		},
		Context: identitycontext.PrincipalContext{
			InitiatingPrincipal: identitycontext.UserPrincipal{
				Subject:     claims.Subject,
				Issuer:      claims.SubjectIssuer,
				DisplayName: claims.DisplayName,
				Email:       claims.Email,
				Roles:       append([]string(nil), claims.Roles...),
			},
			ExecutingServicePrincipal: identitycontext.ServicePrincipal{
				Service:  "platform-gateway",
				SPIFFEID: spiffeID,
			},
			ActingOrganizationID: claims.ActingOrganizationID,
			Audience:             claims.Audience,
			PolicyRevision:       claims.PolicyRevision,
			DelegationExpiresAt:  time.Unix(claims.ExpiresAt, 0).UTC().Format(time.RFC3339),
		},
	}
	writeJSON(writer, http.StatusOK, response)
	return http.StatusOK
}

func (h *handler) handleRegistryReadDecision(writer http.ResponseWriter, request *http.Request, inbound identitycontext.DelegationClaims, presenter string) int {
	request.Body = http.MaxBytesReader(writer, request.Body, maximumDecisionRequestSize)
	var decisionRequest registryauth.DecisionRequest
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decisionRequest); err != nil {
		writeProblem(writer, http.StatusBadRequest, "IAM_REGISTRY_DECISION_REQUEST_INVALID", "The Registry authorization request is invalid.")
		return http.StatusBadRequest
	}
	if err := decisionRequest.Validate(); err != nil {
		writeProblem(writer, http.StatusBadRequest, "IAM_REGISTRY_DECISION_REQUEST_INVALID", "The Registry authorization request is invalid.")
		return http.StatusBadRequest
	}
	if err := ensureJSONEOF(decoder); err != nil {
		writeProblem(writer, http.StatusBadRequest, "IAM_REGISTRY_DECISION_REQUEST_INVALID", "The Registry authorization request must contain one JSON object.")
		return http.StatusBadRequest
	}

	now := h.now()
	decision, err := evaluateRegistryAuthorization(request.Context(), h.authorizationStore, now, inbound.SubjectIssuer, inbound.Subject, decisionRequest)
	if err != nil {
		writeProblem(writer, http.StatusServiceUnavailable, "IAM_AUTHORIZATION_UNAVAILABLE", "The IAM authorization facts are unavailable.")
		return http.StatusServiceUnavailable
	}
	response := registryauth.DecisionResponse{Decision: decision}
	deliveryCode := "DECISION_DENIED"
	if decision.Allowed {
		if h.registryGrantSigner == nil {
			deliveryCode = "GRANT_SIGNER_UNAVAILABLE"
			if !h.recordRegistryDecision(request, decision, false, deliveryCode) {
				writeProblem(writer, http.StatusServiceUnavailable, "IAM_AUTHORIZATION_AUDIT_UNAVAILABLE", "The Registry authorization evidence could not be recorded.")
				return http.StatusServiceUnavailable
			}
			writeProblem(writer, http.StatusServiceUnavailable, "IAM_REGISTRY_GRANT_SIGNER_UNAVAILABLE", "The Registry delegation signer is unavailable.")
			return http.StatusServiceUnavailable
		}
		grantID := h.newRegistryGrantID()
		if grantID == "" {
			deliveryCode = "GRANT_ID_UNAVAILABLE"
			if !h.recordRegistryDecision(request, decision, false, deliveryCode) {
				writeProblem(writer, http.StatusServiceUnavailable, "IAM_AUTHORIZATION_AUDIT_UNAVAILABLE", "The Registry authorization evidence could not be recorded.")
				return http.StatusServiceUnavailable
			}
			writeProblem(writer, http.StatusServiceUnavailable, "IAM_REGISTRY_GRANT_ID_UNAVAILABLE", "The Registry delegation identifier is unavailable.")
			return http.StatusServiceUnavailable
		}
		grant, err := registryauth.SignGrant(h.registryGrantSigner, registryauth.GrantClaims{
			Issuer:                 h.registryGrantIssuer,
			Presenter:              presenter,
			Audience:               h.registryGrantAudience,
			PrincipalID:            decision.PrincipalID,
			SubjectIssuer:          decision.SubjectIssuer,
			Subject:                decision.Subject,
			ActingOrganizationID:   decision.ActingOrganizationID,
			AllowedOrganizationIDs: append([]string(nil), decision.AllowedOrganizationIDs...),
			AllowedSiteIDs:         append([]string(nil), decision.AllowedSiteIDs...),
			DeniedOrganizationIDs:  append([]string(nil), decision.DeniedOrganizationIDs...),
			DeniedSiteIDs:          append([]string(nil), decision.DeniedSiteIDs...),
			Actions:                append([]registryauth.Action(nil), decision.Actions...),
			PolicyRevision:         decision.PolicyRevision,
			DecisionReason:         decision.ReasonCode,
			SessionID:              inbound.SessionID,
			ParentTokenID:          inbound.TokenID,
			IssuedAt:               now.Unix(),
			ExpiresAt:              now.Add(h.registryGrantLifetime).Unix(),
			TokenID:                grantID,
			Transitive:             false,
		})
		if err != nil {
			deliveryCode = "GRANT_SIGNING_FAILED"
			if !h.recordRegistryDecision(request, decision, false, deliveryCode) {
				writeProblem(writer, http.StatusServiceUnavailable, "IAM_AUTHORIZATION_AUDIT_UNAVAILABLE", "The Registry authorization evidence could not be recorded.")
				return http.StatusServiceUnavailable
			}
			writeProblem(writer, http.StatusServiceUnavailable, "IAM_REGISTRY_GRANT_SIGNING_FAILED", "The Registry delegation could not be signed.")
			return http.StatusServiceUnavailable
		}
		response.DelegationGrant = grant
		deliveryCode = "GRANT_SIGNED"
	}
	if !h.recordRegistryDecision(request, decision, response.DelegationGrant != "", deliveryCode) {
		writeProblem(writer, http.StatusServiceUnavailable, "IAM_AUTHORIZATION_AUDIT_UNAVAILABLE", "The Registry authorization evidence could not be recorded.")
		return http.StatusServiceUnavailable
	}
	_ = h.observability.Metrics.AddCounter(
		"s1_iam_registry_authorization_decisions_total",
		"IAM Registry-read authorization decisions.",
		map[string]string{"result": decisionResult(decision.Allowed), "action": string(decisionRequest.Action), "reason": string(decision.ReasonCode), "delivery": deliveryCode},
		1,
	)
	writeJSON(writer, http.StatusOK, response)
	return http.StatusOK
}

func (h *handler) recordRegistryDecision(request *http.Request, decision registryauth.Decision, grantSigned bool, deliveryCode string) bool {
	action := registryauth.Action("")
	if len(decision.Actions) == 1 {
		action = decision.Actions[0]
	}
	err := h.registryAuditSink.RecordRegistryDecision(request.Context(), RegistryDecisionAudit{
		PrincipalID:            decision.PrincipalID,
		ActingOrganizationID:   decision.ActingOrganizationID,
		Action:                 action,
		Allowed:                decision.Allowed,
		AllowedOrganizationIDs: append([]string(nil), decision.AllowedOrganizationIDs...),
		AllowedSiteIDs:         append([]string(nil), decision.AllowedSiteIDs...),
		DeniedOrganizationIDs:  append([]string(nil), decision.DeniedOrganizationIDs...),
		DeniedSiteIDs:          append([]string(nil), decision.DeniedSiteIDs...),
		PolicyRevision:         decision.PolicyRevision,
		ReasonCode:             decision.ReasonCode,
		GrantSigned:            grantSigned,
		DeliveryCode:           deliveryCode,
		TraceID:                observability.TraceID(request.Context()),
		OccurredAt:             formatInstant(h.now()),
	})
	return err == nil
}

func expectedInboundAction(path string) (string, bool) {
	switch path {
	case CurrentPrincipalPath:
		return "principal:read", true
	case RegistryReadDecisionPath:
		return registryAuthorizeAction, true
	case TelemetryDecisionPath:
		return telemetryAuthorizeAction, true
	case CommandDecisionPath:
		return commandAuthorizeAction, true
	default:
		return "", false
	}
}

func peerIdentity(request *http.Request) (*x509CertificateView, string, bool) {
	if request.TLS == nil || len(request.TLS.PeerCertificates) == 0 || len(request.TLS.VerifiedChains) == 0 {
		return nil, "", false
	}
	certificate := request.TLS.PeerCertificates[0]
	if len(certificate.URIs) != 1 || certificate.URIs[0] == nil || !strings.HasPrefix(certificate.URIs[0].String(), "spiffe://") {
		return nil, "", false
	}
	return &x509CertificateView{PublicKey: certificate.PublicKey}, certificate.URIs[0].String(), true
}

type x509CertificateView struct {
	PublicKey any
}

func safePath(path string) string {
	switch path {
	case CurrentPrincipalPath, RegistryReadDecisionPath, TelemetryDecisionPath, RegistryGrantStatusPath, TelemetryGrantConsumePath, TelemetryRevocationPollPath:
		return path
	default:
		return "unmatched"
	}
}

func hasForgedIdentityHeader(header http.Header) bool {
	for name, values := range header {
		nonEmpty := false
		for _, value := range values {
			if value != "" {
				nonEmpty = true
				break
			}
		}
		if !nonEmpty {
			continue
		}
		lowerName := strings.ToLower(name)
		switch lowerName {
		case "x-principal", "x-roles", "x-role", "x-admin", "x-scope", "x-organization-id", "x-site-id":
			return true
		}
		if strings.HasPrefix(lowerName, "x-principal-") || strings.HasPrefix(lowerName, "x-organization-") || strings.HasPrefix(lowerName, "x-site-") {
			return true
		}
	}
	return false
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return io.ErrUnexpectedEOF
		}
		return err
	}
	return nil
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func writeProblem(writer http.ResponseWriter, status int, code, detail string) {
	writer.Header().Set("Content-Type", "application/problem+json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(map[string]any{
		"type":      "https://api.quanlaihe.com/problems/" + strings.ToLower(strings.ReplaceAll(code, "_", "-")),
		"title":     http.StatusText(status),
		"status":    status,
		"detail":    detail,
		"code":      code,
		"retryable": false,
	})
}

func decisionResult(allowed bool) string {
	if allowed {
		return "allow"
	}
	return "deny"
}

func randomIdentifier() string {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return ""
	}
	return hex.EncodeToString(buffer)
}
