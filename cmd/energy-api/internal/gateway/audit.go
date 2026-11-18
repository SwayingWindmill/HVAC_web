package gateway

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/cmd/energy-api/internal/platformapi"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/sessionstore"
	"github.com/quanlaihe/hvac-web/modules/audit/pkg/auditserver"
)

const publicAuditSearchPath = "/api/v1/audit/search"

func (h *handler) GetSessionAuditEvent(writer http.ResponseWriter, request *http.Request, params platformapi.GetSessionAuditEventParams) {
	session, failure := h.identitySession(request)
	if failure != nil {
		writeIdentityFailure(writer, request, *failure)
		return
	}
	principal, failure := h.identity.fetchPrincipal(request.Context(), session)
	if failure != nil {
		writeIdentityFailure(writer, request, *failure)
		return
	}
	if !principal.Authorization.Has(identitycontext.CapabilityAuditRead) {
		writeIdentityFailure(writer, request, identityFailure{403, "AUDIT_QUERY_FORBIDDEN", "Audit query forbidden", "The authenticated principal cannot read Tenant audit records.", false})
		return
	}
	if h.identity.config.AuditURL == "" || h.identity.config.AuditHTTPClient == nil {
		writeIdentityFailure(writer, request, identityFailure{503, "AUDIT_LEDGER_UNAVAILABLE", "Audit Ledger unavailable", "The private Audit Ledger service is not configured.", true})
		return
	}
	now := h.identity.now()
	expiry := now.Add(h.identity.config.DelegationTTL)
	if expiry.After(session.ExpiresAt) {
		expiry = session.ExpiresAt
	}
	claims := identitycontext.DelegationClaims{
		Issuer:           h.identity.config.ExecutingWorkloadSPIFFE,
		Subject:          principal.Principal.Subject,
		SubjectIssuer:    principal.Principal.Issuer,
		DisplayName:      principal.Principal.DisplayName,
		Email:            principal.Principal.Email,
		Roles:            append([]string(nil), principal.Principal.Roles...),
		ExecutingService: h.identity.config.ExecutingWorkloadSPIFFE,
		Audience:         h.identity.config.AuditAudience,
		TenantID:         principal.Context.TenantID,
		Actions:          []string{"audit:read"},
		Scopes:           []string{"tenant:" + principal.Context.TenantID},
		PolicyRevision:   principal.Context.PolicyRevision,
		SessionID:        session.ID,
		IssuedAt:         now.Unix(),
		ExpiresAt:        expiry.Unix(),
		TokenID:          randomURLToken(16),
	}
	grant, err := identitycontext.SignDelegation(h.identity.config.DelegationSigner, claims)
	if err != nil {
		writeIdentityFailure(writer, request, identityFailure{503, "AUDIT_DELEGATION_SIGNING_FAILED", "Audit query unavailable", "The Gateway could not create the constrained Audit delegation.", true})
		return
	}
	endpoint := strings.TrimRight(h.identity.config.AuditURL, "/") + "/internal/v1/audit/session-events/" + url.PathEscape(params.MessageID)
	internalContext, span := observability.Start(request.Context(), "http.audit.query", observability.SpanKindClient, map[string]any{
		"http.request.method": http.MethodGet, "server.service": "audit-ledger-service", "rpc.operation": "audit.session_event.get",
	})
	defer span.End()
	internalRequest, _ := http.NewRequestWithContext(internalContext, http.MethodGet, endpoint, nil)
	internalRequest.Header.Set("Accept", "application/json, application/problem+json")
	internalRequest.Header.Set("X-Delegation-Grant", grant)
	observability.InjectHTTP(internalContext, internalRequest.Header)
	response, err := h.identity.config.AuditHTTPClient.Do(internalRequest)
	if err != nil {
		writeIdentityFailure(writer, request, identityFailure{503, "AUDIT_LEDGER_UNAVAILABLE", "Audit Ledger unavailable", "The private Audit Ledger service could not be reached.", true})
		return
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		writeIdentityFailure(writer, request, identityFailure{404, "AUDIT_RECORD_NOT_FOUND", "Audit record not found", "No audit record is visible for this Tenant and message ID.", false})
		return
	}
	if response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusUnauthorized {
		writeIdentityFailure(writer, request, identityFailure{403, "AUDIT_QUERY_FORBIDDEN", "Audit query forbidden", "The private Audit Ledger rejected the delegated query.", false})
		return
	}
	if response.StatusCode != http.StatusOK {
		writeIdentityFailure(writer, request, identityFailure{503, "AUDIT_QUERY_UNAVAILABLE", "Audit query unavailable", "The private Audit Ledger could not complete the query.", true})
		return
	}
	var record platformapi.AuditRecord
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&record); err != nil {
		writeIdentityFailure(writer, request, identityFailure{503, "AUDIT_RESPONSE_INVALID", "Audit response invalid", "The private Audit Ledger returned an invalid response.", true})
		return
	}
	if record.MessageID != params.MessageID || record.TenantID != session.TenantID {
		writeIdentityFailure(writer, request, identityFailure{503, "AUDIT_RESPONSE_INVALID", "Audit response invalid", "The private Audit Ledger response violated the Tenant boundary.", true})
		return
	}
	writeJSON(writer, http.StatusOK, record)
}

func (h *handler) SearchAudit(writer http.ResponseWriter, request *http.Request) {
	session, failure := h.identitySession(request)
	if failure != nil {
		writeIdentityFailure(writer, request, *failure)
		return
	}
	principal, failure := h.identity.fetchPrincipal(request.Context(), session)
	if failure != nil {
		writeIdentityFailure(writer, request, *failure)
		return
	}
	if !principal.Authorization.Has(identitycontext.CapabilityAuditRead) {
		writeIdentityFailure(writer, request, identityFailure{403, "AUDIT_QUERY_FORBIDDEN", "Audit query forbidden", "The authenticated principal cannot search Tenant audit records.", false})
		return
	}
	if h.identity.config.AuditURL == "" || h.identity.config.AuditHTTPClient == nil {
		writeIdentityFailure(writer, request, identityFailure{503, "AUDIT_LEDGER_UNAVAILABLE", "Audit Ledger unavailable", "The private Audit Ledger service is not configured.", true})
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, 32<<10)
	payload, err := io.ReadAll(request.Body)
	if err != nil {
		writeIdentityFailure(writer, request, identityFailure{400, "AUDIT_SEARCH_INVALID", "Audit search invalid", "The audit search filter is invalid.", false})
		return
	}
	now := h.identity.now()
	expiry := now.Add(h.identity.config.DelegationTTL)
	if expiry.After(session.ExpiresAt) {
		expiry = session.ExpiresAt
	}
	claims := identitycontext.DelegationClaims{
		Issuer: h.identity.config.ExecutingWorkloadSPIFFE, Subject: principal.Principal.Subject,
		SubjectIssuer: principal.Principal.Issuer, DisplayName: principal.Principal.DisplayName, Email: principal.Principal.Email,
		Roles: append([]string(nil), principal.Principal.Roles...), ExecutingService: h.identity.config.ExecutingWorkloadSPIFFE,
		Audience: h.identity.config.AuditAudience, TenantID: principal.Context.TenantID, Actions: []string{"audit:read"},
		Scopes: []string{"tenant:" + principal.Context.TenantID}, PolicyRevision: principal.Context.PolicyRevision,
		SessionID: session.ID, IssuedAt: now.Unix(), ExpiresAt: expiry.Unix(), TokenID: randomURLToken(16),
	}
	grant, err := identitycontext.SignDelegation(h.identity.config.DelegationSigner, claims)
	if err != nil {
		writeIdentityFailure(writer, request, identityFailure{503, "AUDIT_DELEGATION_SIGNING_FAILED", "Audit query unavailable", "The Gateway could not create the constrained Audit delegation.", true})
		return
	}
	ctx, span := observability.Start(request.Context(), "http.audit.search", observability.SpanKindClient, map[string]any{
		"http.request.method": http.MethodPost, "server.service": "audit-ledger-service", "rpc.operation": "audit.search",
	})
	defer span.End()
	internalRequest, _ := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(h.identity.config.AuditURL, "/")+"/internal/v1/audit/search", bytes.NewReader(payload))
	internalRequest.Header.Set("Content-Type", "application/json")
	internalRequest.Header.Set("Accept", "application/json, application/problem+json")
	internalRequest.Header.Set("X-Delegation-Grant", grant)
	observability.InjectHTTP(ctx, internalRequest.Header)
	response, err := h.identity.config.AuditHTTPClient.Do(internalRequest)
	if err != nil {
		writeIdentityFailure(writer, request, identityFailure{503, "AUDIT_LEDGER_UNAVAILABLE", "Audit Ledger unavailable", "The private Audit Ledger service could not be reached.", true})
		return
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		writeIdentityFailure(writer, request, identityFailure{503, "AUDIT_RESPONSE_INVALID", "Audit response invalid", "The private Audit Ledger returned an invalid response.", true})
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Type", response.Header.Get("Content-Type"))
	writer.WriteHeader(response.StatusCode)
	_, _ = writer.Write(responseBody)
}

const (
	auditOutcomeSucceeded = "SUCCEEDED"
	auditOutcomeDenied    = "DENIED"
	auditOutcomeRejected  = "REJECTED"
	auditOutcomeFailed    = "FAILED"
)

type domainAuditInput struct {
	Category      string
	TargetID      string
	TargetLabel   string
	Action        string
	Outcome       string
	ClientKind    string
	ChangeSummary map[string]any
}

type domainAuditActor struct {
	TenantID          string
	Subject           string
	DelegationSubject string
	PrincipalID       string
	Issuer            string
	DisplayName       string
	Email             string
	Roles             []string
	SessionID         string
	ExpiresAt         time.Time
	ClientKind        string
}

func (h *handler) emitDomainAudit(
	ctx context.Context,
	request *http.Request,
	session bffSession,
	input domainAuditInput,
) {
	if h == nil || h.identity == nil {
		return
	}
	clientKind := input.ClientKind
	if clientKind == "" {
		switch session.Client {
		case sessionstore.ClientMiniProgram:
			clientKind = auditserver.ClientKindMiniProgram
		case sessionstore.ClientBrowser:
			clientKind = auditserver.ClientKindBrowser
		}
	}
	h.emitDomainAuditWithActor(ctx, request, domainAuditActor{
		TenantID:    session.TenantID,
		Subject:     session.Principal.Subject,
		Issuer:      firstNonEmpty(session.Principal.Issuer, h.identity.config.OIDCIssuer, "https://identity.local"),
		DisplayName: session.Principal.DisplayName,
		Email:       session.Principal.Email,
		Roles:       session.Principal.Roles,
		SessionID:   session.ID,
		ExpiresAt:   session.ExpiresAt,
		ClientKind:  clientKind,
	}, input)
}

func (h *handler) emitSystemDomainAudit(
	ctx context.Context,
	request *http.Request,
	tenantID, principalID, actorDisplay, issuer string,
	input domainAuditInput,
) {
	if h == nil || h.identity == nil {
		return
	}
	subject := firstNonEmpty(principalID, h.identity.config.ExecutingWorkloadSPIFFE)
	input.ClientKind = auditserver.ClientKindSystem
	h.emitDomainAuditWithActor(ctx, request, domainAuditActor{
		TenantID:          tenantID,
		Subject:           subject,
		DelegationSubject: h.identity.config.ExecutingWorkloadSPIFFE,
		PrincipalID:       principalID,
		Issuer:            firstNonEmpty(issuer, h.identity.config.OIDCIssuer, "workload:spiffe"),
		DisplayName:       actorDisplay,
		ClientKind:        auditserver.ClientKindSystem,
	}, input)
}

func (h *handler) emitDomainAuditWithActor(
	ctx context.Context,
	request *http.Request,
	actor domainAuditActor,
	input domainAuditInput,
) {
	if h == nil || h.identity == nil ||
		h.identity.config.AuditURL == "" ||
		h.identity.config.AuditHTTPClient == nil ||
		h.identity.config.DelegationSigner == nil {
		return
	}
	if actor.TenantID == "" || actor.Subject == "" || input.Category == "" || input.Action == "" {
		return
	}
	now := h.identity.now().UTC()
	messageID := randomAuditUUID()
	traceID := observability.TraceID(ctx)
	if !isLowerHex32(traceID) {
		traceID = randomAuditHex(16)
	}
	correlationID := firstNonEmpty(requestIDFromContext(ctx), messageID)
	policyRevision := firstNonEmpty(h.identity.config.PolicyRevision, "policy-v1")
	event := auditserver.DomainEvent{
		SchemaVersion:     auditserver.DomainEventSchemaVersion,
		MessageID:         messageID,
		TenantID:          actor.TenantID,
		Category:          input.Category,
		TargetID:          truncateAuditString(firstNonEmpty(input.TargetID, actor.TenantID), 256),
		AggregateVersion:  1,
		OccurredAt:        now,
		InitiatingSubject: actor.Subject,
		InitiatingIssuer:  actor.Issuer,
		ExecutingService:  "platform-gateway",
		ExecutingSPIFFEID: h.identity.config.ExecutingWorkloadSPIFFE,
		Action:            input.Action,
		Outcome:           firstNonEmpty(input.Outcome, auditOutcomeSucceeded),
		PolicyRevision:    policyRevision,
		CorrelationID:     correlationID,
		CausationID:       messageID,
		TraceID:           traceID,
		Traceparent:       observability.Traceparent(ctx),
		ClientIP:          extractClientIP(request),
		ClientKind:        actor.ClientKind,
		ActorDisplay:      truncateAuditString(strings.TrimSpace(actor.DisplayName), 256),
		TargetLabel:       truncateAuditString(strings.TrimSpace(input.TargetLabel), 256),
		ChangeSummary:     encodeRedactedChangeSummary(input.ChangeSummary),
	}
	payload, err := json.Marshal(event)
	if err != nil {
		h.warnAuditFailure(ctx, "domain_audit_emit_failed", input.Category, input.Action, "AUDIT_EVENT_ENCODE_FAILED", 0)
		return
	}
	ttl := h.identity.config.DelegationTTL
	if ttl < 5*time.Second {
		ttl = 5 * time.Second
	}
	expiry := now.Add(ttl)
	if !actor.ExpiresAt.IsZero() && actor.ExpiresAt.After(now) && expiry.After(actor.ExpiresAt) {
		expiry = actor.ExpiresAt
	}
	claims := identitycontext.DelegationClaims{
		Issuer:           h.identity.config.ExecutingWorkloadSPIFFE,
		Subject:          firstNonEmpty(actor.DelegationSubject, actor.Subject),
		SubjectIssuer:    event.InitiatingIssuer,
		PrincipalID:      actor.PrincipalID,
		DisplayName:      actor.DisplayName,
		Email:            actor.Email,
		Roles:            append([]string(nil), actor.Roles...),
		ExecutingService: h.identity.config.ExecutingWorkloadSPIFFE,
		Audience:         firstNonEmpty(h.identity.config.AuditAudience, "audit-ledger-service"),
		TenantID:         actor.TenantID,
		Actions:          []string{"audit:write"},
		Scopes:           []string{"tenant:" + actor.TenantID},
		PolicyRevision:   policyRevision,
		SessionID:        actor.SessionID,
		IssuedAt:         now.Unix(),
		ExpiresAt:        expiry.Unix(),
		TokenID:          randomURLToken(16),
	}
	grant, err := identitycontext.SignDelegation(h.identity.config.DelegationSigner, claims)
	if err != nil {
		h.warnAuditFailure(ctx, "domain_audit_emit_failed", input.Category, input.Action, "AUDIT_DELEGATION_SIGNING_FAILED", 0)
		return
	}
	auditCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	auditCtx, span := observability.Start(auditCtx, "http.audit.domain_write", observability.SpanKindClient, map[string]any{
		"http.request.method": http.MethodPost, "server.service": "audit-ledger-service", "rpc.operation": "audit.domain_write",
	})
	defer span.End()
	endpoint := strings.TrimRight(h.identity.config.AuditURL, "/") + auditserver.DomainAuditPath
	internalRequest, err := http.NewRequestWithContext(auditCtx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		h.warnAuditFailure(auditCtx, "domain_audit_emit_failed", input.Category, input.Action, "AUDIT_REQUEST_INVALID", 0)
		return
	}
	internalRequest.Header.Set("Content-Type", "application/json")
	internalRequest.Header.Set("Accept", "application/json, application/problem+json")
	internalRequest.Header.Set("Idempotency-Key", messageID)
	internalRequest.Header.Set("X-Delegation-Grant", grant)
	observability.InjectHTTP(auditCtx, internalRequest.Header)
	response, err := h.identity.config.AuditHTTPClient.Do(internalRequest)
	if err != nil {
		h.warnAuditFailure(auditCtx, "domain_audit_emit_failed", input.Category, input.Action, "AUDIT_LEDGER_UNAVAILABLE", 0)
		return
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		h.warnAuditFailure(auditCtx, "domain_audit_emit_rejected", input.Category, input.Action, "", response.StatusCode)
	}
}

func (h *handler) warnAuditFailure(ctx context.Context, msg, category, action, errorCode string, status int) {
	if h == nil || h.logger == nil {
		return
	}
	if status > 0 {
		h.logger.WarnContext(ctx, msg, "category", category, "action", action, "status", status)
		return
	}
	h.logger.WarnContext(ctx, msg, "category", category, "action", action, "error_code", errorCode)
}

func auditOutcomeForStatus(status int) string {
	switch {
	case status >= http.StatusOK && status < http.StatusMultipleChoices:
		return auditOutcomeSucceeded
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return auditOutcomeDenied
	case status >= http.StatusBadRequest && status < http.StatusInternalServerError:
		return auditOutcomeRejected
	default:
		return auditOutcomeFailed
	}
}

func extractClientIP(request *http.Request) string {
	if request == nil {
		return ""
	}
	remoteAddr, ok := parseNetIP(request.RemoteAddr)
	if !ok {
		return ""
	}
	if !isTrustedProxyAddr(remoteAddr) {
		return remoteAddr.String()
	}
	xffValues := request.Header.Values("X-Forwarded-For")
	if len(xffValues) == 0 {
		return remoteAddr.String()
	}
	xff := strings.TrimSpace(strings.Join(xffValues, ","))
	if xff == "" {
		return remoteAddr.String()
	}
	parts := strings.Split(xff, ",")
	for index := len(parts) - 1; index >= 0; index-- {
		candidate, valid := parseNetIP(parts[index])
		if !valid {
			return remoteAddr.String()
		}
		if !isTrustedProxyAddr(candidate) || index == 0 {
			return candidate.String()
		}
	}
	return remoteAddr.String()
}

func parseNetIP(raw string) (netip.Addr, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return netip.Addr{}, false
	}
	if host, _, err := net.SplitHostPort(trimmed); err == nil {
		trimmed = host
	}
	addr, err := netip.ParseAddr(trimmed)
	if err != nil || !addr.IsValid() {
		return netip.Addr{}, false
	}
	return addr.Unmap().WithZone(""), true
}

func isTrustedProxyAddr(addr netip.Addr) bool {
	return addr.IsLoopback() || addr.IsLinkLocalUnicast() || addr.IsPrivate()
}

func encodeRedactedChangeSummary(summary map[string]any) json.RawMessage {
	if len(summary) == 0 {
		return nil
	}
	sanitized := redactAuditMap(summary)
	if len(sanitized) == 0 {
		return nil
	}
	encoded, err := json.Marshal(sanitized)
	if err != nil || len(encoded) == 0 || len(encoded) > 16*1024 {
		return nil
	}
	return json.RawMessage(encoded)
}

func redactAuditMap(input map[string]any) map[string]any {
	out := make(map[string]any, len(input))
	for key, value := range input {
		if isSensitiveAuditValue(key, value) {
			out[key] = "[REDACTED]"
			continue
		}
		if sanitized, keep := redactAuditValue(value); keep {
			out[key] = sanitized
		}
	}
	return out
}

func redactAuditStringMap(input map[string]string) map[string]any {
	out := make(map[string]any, len(input))
	for key, value := range input {
		if isSensitiveAuditValue(key, value) {
			out[key] = "[REDACTED]"
			continue
		}
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		out[key] = truncateAuditString(trimmed, 256)
	}
	return out
}

func redactAuditValue(value any) (any, bool) {
	switch typed := value.(type) {
	case nil:
		return nil, false
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return nil, false
		}
		return truncateAuditString(trimmed, 256), true
	case map[string]any:
		nested := redactAuditMap(typed)
		if len(nested) == 0 {
			return nil, false
		}
		return nested, true
	case map[string]string:
		nested := redactAuditStringMap(typed)
		if len(nested) == 0 {
			return nil, false
		}
		return nested, true
	case []any:
		outSlice := make([]any, 0, len(typed))
		for _, item := range typed {
			if sanitized, keep := redactAuditValue(item); keep {
				outSlice = append(outSlice, sanitized)
			}
		}
		if len(outSlice) == 0 {
			return nil, false
		}
		return outSlice, true
	case []map[string]any:
		outSlice := make([]any, 0, len(typed))
		for _, item := range typed {
			if nested := redactAuditMap(item); len(nested) > 0 {
				outSlice = append(outSlice, nested)
			}
		}
		if len(outSlice) == 0 {
			return nil, false
		}
		return outSlice, true
	case []string:
		outSlice := make([]string, 0, len(typed))
		for _, item := range typed {
			if trimmed := strings.TrimSpace(item); trimmed != "" {
				outSlice = append(outSlice, truncateAuditString(trimmed, 256))
			}
		}
		if len(outSlice) == 0 {
			return nil, false
		}
		return outSlice, true
	default:
		return typed, true
	}
}

func isSensitiveAuditValue(key string, value any) bool {
	lower := strings.ToLower(strings.TrimSpace(key))
	if lower == "passwordchanged" {
		_, isBool := value.(bool)
		return !isBool
	}
	return isSensitiveAuditKey(lower)
}

func isSensitiveAuditKey(lower string) bool {
	if lower == "credential" || lower == "credentials" || lower == "pin" {
		return true
	}
	for _, needle := range []string{
		"password",
		"secret",
		"token",
		"verificationcode",
		"enrollmentcode",
		"totpcode",
		"otp",
		"hash",
		"privatekey",
		"passphrase",
		"apikey",
	} {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

func problemCodeFromRaw(raw []byte) string {
	var problem struct {
		Code string `json:"code"`
	}
	if json.Unmarshal(raw, &problem) == nil {
		return strings.TrimSpace(problem.Code)
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func truncateAuditString(value string, maxRunes int) string {
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	return string(runes[:maxRunes])
}

func isLowerHex32(value string) bool {
	if len(value) != 32 {
		return false
	}
	for index := 0; index < len(value); index++ {
		ch := value[index]
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return false
		}
	}
	return true
}

func randomAuditHex(byteCount int) string {
	buf := make([]byte, byteCount)
	if _, err := rand.Read(buf); err != nil {
		return strings.Repeat("0", byteCount*2)
	}
	return hex.EncodeToString(buf)
}

func randomAuditUUID() string {
	buf := make([]byte, 16)
	_, _ = rand.Read(buf)
	buf[6] = (buf[6] & 0x0f) | 0x40
	buf[8] = (buf[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", buf[0:4], buf[4:6], buf[6:8], buf[8:10], buf[10:16])
}
