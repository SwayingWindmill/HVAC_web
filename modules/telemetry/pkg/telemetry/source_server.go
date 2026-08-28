package telemetry

import (
	"bytes"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/limitpolicy"
	"github.com/quanlaihe/hvac-web/modules/telemetry/pkg/telemetryapi"
)

const (
	InternalSourceObservationPath   = "/internal/v1/telemetry/sources/observations:accept"
	InternalSourceCoveragePath      = "/internal/v1/telemetry/sources/coverage:report"
	InternalMQTTGatewayEvidencePath  = "/internal/v1/telemetry/sources/mqtt/gateway-evidence:accept"
	InternalMQTTPresenceEvidencePath = "/internal/v1/telemetry/sources/mqtt/presence-evidence:accept"
	InternalMQTTRuntimeEventPath     = "/internal/v1/telemetry/sources/mqtt/events:accept"
	maximumSourceObservationSize     = 96 << 10
)

type SourceAuthenticator interface {
	AllowsSource(peerSPIFFE, integrationInstanceID string) bool
}

type StaticSourceAuthenticator struct {
	allowed map[string]map[string]struct{}
}

func NewStaticSourceAuthenticator(bindings map[string][]string) *StaticSourceAuthenticator {
	authenticator := &StaticSourceAuthenticator{allowed: make(map[string]map[string]struct{}, len(bindings))}
	for peer, integrations := range bindings {
		peer = strings.TrimSpace(peer)
		if peer == "" {
			continue
		}
		set := make(map[string]struct{}, len(integrations))
		for _, integration := range integrations {
			integration = strings.TrimSpace(integration)
			if integration != "" {
				set[integration] = struct{}{}
			}
		}
		if len(set) > 0 {
			authenticator.allowed[peer] = set
		}
	}
	return authenticator
}

func ParseSourceAuthenticatorJSON(raw string) (*StaticSourceAuthenticator, error) {
	var bindings map[string][]string
	decoder := json.NewDecoder(bytes.NewBufferString(strings.TrimSpace(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&bindings); err != nil || ensureJSONEOF(decoder) != nil || len(bindings) == 0 {
		return nil, errors.New("telemetry source bindings JSON is invalid")
	}
	for peer, integrations := range bindings {
		canonicalPeer := strings.TrimSpace(peer)
		parsed, err := url.Parse(canonicalPeer)
		if err != nil || parsed.Scheme != "spiffe" || parsed.Host == "" || parsed.Path == "" || parsed.Path == "/" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Opaque != "" || len(integrations) == 0 {
			return nil, errors.New("telemetry source workload binding is invalid")
		}
		seen := make(map[string]struct{}, len(integrations))
		for _, integration := range integrations {
			integration = strings.TrimSpace(integration)
			if !uuidV7Pattern.MatchString(integration) {
				return nil, errors.New("telemetry source integration binding is invalid")
			}
			if _, duplicate := seen[integration]; duplicate {
				return nil, errors.New("telemetry source integration binding is duplicated")
			}
			seen[integration] = struct{}{}
		}
	}
	authenticator := NewStaticSourceAuthenticator(bindings)
	if len(authenticator.allowed) != len(bindings) {
		return nil, errors.New("telemetry source bindings JSON is incomplete")
	}
	return authenticator, nil
}

func (authenticator *StaticSourceAuthenticator) AllowsSource(peerSPIFFE, integrationInstanceID string) bool {
	if authenticator == nil {
		return false
	}
	integrations := authenticator.allowed[strings.TrimSpace(peerSPIFFE)]
	_, allowed := integrations[strings.TrimSpace(integrationInstanceID)]
	return allowed
}

type sourcePositionRequest struct {
	Partition string `json:"partition"`
	Offset    int64  `json:"offset"`
	EventID   string `json:"eventId"`
}

type sourceObservationRequest struct {
	IntegrationInstanceID string                `json:"integrationInstanceId"`
	SourcePath            string                `json:"sourcePath"`
	ExternalEntityType    string                `json:"externalEntityType"`
	ExternalID            string                `json:"externalId"`
	TelemetryKey          string                `json:"telemetryKey"`
	Value                 json.RawMessage       `json:"value"`
	ValueType             string                `json:"valueType"`
	Unit                  *string               `json:"unit"`
	WireQuality           uint8                 `json:"wireQuality"`
	SampledAt             string                `json:"sampledAt"`
	SourcePosition        sourcePositionRequest `json:"sourcePosition"`
}

func (h *handler) handleSourceObservation(writer http.ResponseWriter, request *http.Request) {
	peer, ok := h.trustedSourcePeer(writer, request)
	if !ok {
		return
	}
	if h.observationAcceptor == nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "TELEMETRY_SOURCE_UNAVAILABLE", "The telemetry source acceptance path is temporarily unavailable.", true)
		return
	}
	var input sourceObservationRequest
	if !decodeSourceRequest(writer, request, &input) {
		return
	}
	candidate, err := normalizeSourceObservation(input, h.now().UTC())
	if err != nil {
		writeProblem(writer, request, http.StatusBadRequest, "TELEMETRY_SOURCE_REQUEST_INVALID", "The telemetry source request is invalid.", false)
		return
	}
	if !h.sourceAuthenticator.AllowsSource(peer, candidate.IntegrationInstanceID) {
		writeProblem(writer, request, http.StatusUnauthorized, "TELEMETRY_SOURCE_IDENTITY_INVALID", "The calling source workload identity is not trusted.", false)
		return
	}
	if h.rateLimiter != nil && !h.rateLimiter.Allow(request.Context(), limitpolicy.DimensionTelemetryIngest, candidate.IntegrationInstanceID).Allowed {
		writeProblem(writer, request, http.StatusTooManyRequests, "TELEMETRY_INGEST_RATE_LIMITED", "The telemetry source observation rate has been exceeded.", true)
		return
	}
	receipt, err := h.observationAcceptor.AcceptObservation(request.Context(), candidate)
	if err != nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "TELEMETRY_SOURCE_UNAVAILABLE", "The telemetry source acceptance path is temporarily unavailable.", true)
		return
	}
	sourceOutcome, sourceReason := "success", "none"
	if receipt.Status == ObservationQuarantined {
		sourceOutcome = "rejected"
		sourceReason = quarantineReasonFamily(receipt.QuarantineReason)
	}
	h.metrics.observeDataQuality(receipt)
	h.metrics.observeIngest(sourceOutcome, sourceReason)
	h.metrics.observeSourceLag(sourceDependency(peer), sourceOutcome, candidate.SampledAt, candidate.ReceivedAt)
	if receipt.Status == ObservationQuarantined {
		h.metrics.observeQuarantine(sourceReason)
	}
	writeJSON(writer, http.StatusOK, receipt)
}

func normalizeSourceObservation(input sourceObservationRequest, receivedAt time.Time) (ObservationCandidate, error) {
	sampledAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(input.SampledAt))
	if err != nil || receivedAt.IsZero() {
		return ObservationCandidate{}, errors.New("telemetry sampledAt is invalid")
	}
	candidate := ObservationCandidate{
		IntegrationInstanceID: strings.TrimSpace(input.IntegrationInstanceID),
		SourcePath:            safeSourcePath(input.SourcePath),
		ExternalEntityType:    strings.ToUpper(strings.TrimSpace(input.ExternalEntityType)),
		ExternalID:            strings.TrimSpace(input.ExternalID),
		TelemetryKey:          strings.TrimSpace(input.TelemetryKey),
		Value:                 append(json.RawMessage(nil), input.Value...),
		ValueType:             strings.ToUpper(strings.TrimSpace(input.ValueType)),
		Unit:                  cloneString(input.Unit),
		WireQuality:           input.WireQuality,
		SampledAt:             sampledAt.UTC(),
		ReceivedAt:            receivedAt.UTC(),
		Position: SourcePosition{
			Partition: strings.TrimSpace(input.SourcePosition.Partition),
			Offset:    input.SourcePosition.Offset,
			EventID:   strings.TrimSpace(input.SourcePosition.EventID),
		},
	}
	if err := validateObservationCandidate(candidate); err != nil {
		return ObservationCandidate{}, err
	}
	return candidate, nil
}

type sourceCoverageRequest struct {
	IntegrationInstanceID string  `json:"integrationInstanceId"`
	ExternalEntityType    string  `json:"externalEntityType"`
	ExternalID            string  `json:"externalId"`
	Available             bool    `json:"available"`
	ContinuousSince       *string `json:"continuousSince"`
	Reason                string  `json:"reason"`
	SourceRevision        int64   `json:"sourceRevision"`
}

func (h *handler) handleSourceCoverage(writer http.ResponseWriter, request *http.Request) {
	peer, ok := h.trustedSourcePeer(writer, request)
	if !ok {
		return
	}
	if h.coverageReporter == nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "TELEMETRY_SOURCE_UNAVAILABLE", "The telemetry source acceptance path is temporarily unavailable.", true)
		return
	}
	var input sourceCoverageRequest
	if !decodeSourceRequest(writer, request, &input) {
		return
	}
	report, err := normalizeSourceCoverage(input, h.now().UTC())
	if err != nil {
		writeProblem(writer, request, http.StatusBadRequest, "TELEMETRY_SOURCE_REQUEST_INVALID", "The telemetry source request is invalid.", false)
		return
	}
	if !h.sourceAuthenticator.AllowsSource(peer, report.IntegrationInstanceID) {
		writeProblem(writer, request, http.StatusUnauthorized, "TELEMETRY_SOURCE_IDENTITY_INVALID", "The calling source workload identity is not trusted.", false)
		return
	}
	receipt, err := h.coverageReporter.ReportCoverage(request.Context(), report)
	if err != nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "TELEMETRY_SOURCE_UNAVAILABLE", "The telemetry source acceptance path is temporarily unavailable.", true)
		return
	}
	if receipt.Status == "QUARANTINED" {
		receipt.DeviceID = ""
		h.metrics.observeQuarantine("scope")
	}
	writeJSON(writer, http.StatusOK, receipt)
}

type mqttGatewayEvidenceRequest struct {
	IntegrationInstanceID string          `json:"integrationInstanceId"`
	TenantID              string          `json:"tenantId"`
	SiteID                string          `json:"siteId"`
	GatewayID             string          `json:"gatewayId"`
	MessageID             string          `json:"messageId"`
	EvidenceType          string          `json:"evidenceType"`
	ObservedAt            string          `json:"observedAt"`
	Sequence              int64           `json:"sequence"`
	Payload               json.RawMessage `json:"payload"`
}

func (h *handler) handleMQTTGatewayEvidence(writer http.ResponseWriter, request *http.Request) {
	peer, ok := h.trustedSourcePeer(writer, request)
	if !ok {
		return
	}
	if h.mqttEvidenceAcceptor == nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "TELEMETRY_SOURCE_UNAVAILABLE", "The MQTT evidence acceptance path is temporarily unavailable.", true)
		return
	}
	var input mqttGatewayEvidenceRequest
	if !decodeSourceRequest(writer, request, &input) {
		return
	}
	integrationID := strings.TrimSpace(input.IntegrationInstanceID)
	if !h.sourceAuthenticator.AllowsSource(peer, integrationID) {
		writeProblem(writer, request, http.StatusUnauthorized, "TELEMETRY_SOURCE_IDENTITY_INVALID", "The calling source workload identity is not trusted.", false)
		return
	}
	observedAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(input.ObservedAt))
	if err != nil {
		writeProblem(writer, request, http.StatusBadRequest, "TELEMETRY_SOURCE_REQUEST_INVALID", "The MQTT evidence request is invalid.", false)
		return
	}
	evidence := GatewayEvidence{
		IntegrationInstanceID: integrationID, TenantID: strings.TrimSpace(input.TenantID), SiteID: strings.TrimSpace(input.SiteID),
		GatewayID: strings.TrimSpace(input.GatewayID), MessageID: strings.TrimSpace(input.MessageID), EvidenceType: strings.ToUpper(strings.TrimSpace(input.EvidenceType)),
		ObservedAt: observedAt.UTC(), ReceivedAt: h.now().UTC(), Sequence: input.Sequence, Payload: append(json.RawMessage(nil), input.Payload...),
	}
	if err = h.mqttEvidenceAcceptor.AcceptGatewayEvidence(request.Context(), evidence); err != nil {
		writeProblem(writer, request, http.StatusBadRequest, "TELEMETRY_SOURCE_REQUEST_INVALID", "The MQTT evidence request was rejected.", false)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"accepted": true, "messageId": evidence.MessageID})
}

type mqttPresenceEvidenceRequest struct {
	IntegrationInstanceID string `json:"integrationInstanceId"`
	ExternalEntityType    string `json:"externalEntityType"`
	ExternalID            string `json:"externalId"`
	SignalType            string `json:"signalType"`
	ObservedAt            string `json:"observedAt"`
	SourceEventID         string `json:"sourceEventId"`
}

func (h *handler) handleMQTTPresenceEvidence(writer http.ResponseWriter, request *http.Request) {
	peer, ok := h.trustedSourcePeer(writer, request)
	if !ok {
		return
	}
	if h.mqttEvidenceAcceptor == nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "TELEMETRY_SOURCE_UNAVAILABLE", "The MQTT Presence evidence path is temporarily unavailable.", true)
		return
	}
	var input mqttPresenceEvidenceRequest
	if !decodeSourceRequest(writer, request, &input) {
		return
	}
	integrationID := strings.TrimSpace(input.IntegrationInstanceID)
	if !h.sourceAuthenticator.AllowsSource(peer, integrationID) {
		writeProblem(writer, request, http.StatusUnauthorized, "TELEMETRY_SOURCE_IDENTITY_INVALID", "The calling source workload identity is not trusted.", false)
		return
	}
	observedAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(input.ObservedAt))
	if err != nil {
		writeProblem(writer, request, http.StatusBadRequest, "TELEMETRY_SOURCE_REQUEST_INVALID", "The MQTT Presence evidence request is invalid.", false)
		return
	}
	receipt, err := h.mqttEvidenceAcceptor.AcceptPresenceEvidence(request.Context(), DevicePresenceEvidence{
		IntegrationInstanceID: integrationID, ExternalEntityType: strings.ToUpper(strings.TrimSpace(input.ExternalEntityType)), ExternalID: strings.TrimSpace(input.ExternalID),
		SignalType: strings.ToUpper(strings.TrimSpace(input.SignalType)), ObservedAt: observedAt.UTC(), ReceivedAt: h.now().UTC(), SourceEventID: strings.TrimSpace(input.SourceEventID),
	})
	if err != nil {
		writeProblem(writer, request, http.StatusBadRequest, "TELEMETRY_SOURCE_REQUEST_INVALID", "The MQTT Presence evidence request was rejected.", false)
		return
	}
	writeJSON(writer, http.StatusOK, receipt)
}

type mqttRuntimeEventRequest struct {
	IntegrationInstanceID string          `json:"integrationInstanceId"`
	TenantID              string          `json:"tenantId"`
	SiteID                string          `json:"siteId"`
	GatewayID             string          `json:"gatewayId"`
	MessageID             string          `json:"messageId"`
	Sequence              int64           `json:"sequence"`
	EventType             string          `json:"eventType"`
	SourceType            string          `json:"sourceType"`
	SourceID              string          `json:"sourceId"`
	EventTime             string          `json:"eventTime"`
	Severity              string          `json:"severity"`
	Data                  json.RawMessage `json:"data"`
}

func (h *handler) handleMQTTRuntimeEvent(writer http.ResponseWriter, request *http.Request) {
	peer, ok := h.trustedSourcePeer(writer, request)
	if !ok {
		return
	}
	if h.mqttEvidenceAcceptor == nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "TELEMETRY_SOURCE_UNAVAILABLE", "The MQTT runtime event path is temporarily unavailable.", true)
		return
	}
	var input mqttRuntimeEventRequest
	if !decodeSourceRequest(writer, request, &input) {
		return
	}
	integrationID := strings.TrimSpace(input.IntegrationInstanceID)
	if !h.sourceAuthenticator.AllowsSource(peer, integrationID) {
		writeProblem(writer, request, http.StatusUnauthorized, "TELEMETRY_SOURCE_IDENTITY_INVALID", "The calling source workload identity is not trusted.", false)
		return
	}
	eventTime, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(input.EventTime))
	if err != nil {
		writeProblem(writer, request, http.StatusBadRequest, "TELEMETRY_SOURCE_REQUEST_INVALID", "The MQTT runtime event request is invalid.", false)
		return
	}
	evidence := RuntimeEventEvidence{
		IntegrationInstanceID: integrationID, TenantID: strings.TrimSpace(input.TenantID), SiteID: strings.TrimSpace(input.SiteID), GatewayID: strings.TrimSpace(input.GatewayID),
		MessageID: strings.TrimSpace(input.MessageID), Sequence: input.Sequence, EventType: strings.TrimSpace(input.EventType), SourceType: strings.TrimSpace(input.SourceType), SourceID: strings.TrimSpace(input.SourceID),
		EventTime: eventTime.UTC(), Severity: strings.ToUpper(strings.TrimSpace(input.Severity)), Data: append(json.RawMessage(nil), input.Data...), ReceivedAt: h.now().UTC(),
	}
	if err = h.mqttEvidenceAcceptor.AcceptRuntimeEvent(request.Context(), evidence); err != nil {
		writeProblem(writer, request, http.StatusBadRequest, "TELEMETRY_SOURCE_REQUEST_INVALID", "The MQTT runtime event request was rejected.", false)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"accepted": true, "messageId": evidence.MessageID})
}

func (h *handler) trustedSourcePeer(writer http.ResponseWriter, request *http.Request) (string, bool) {
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", http.MethodPost)
		writeProblem(writer, request, http.StatusMethodNotAllowed, "TELEMETRY_METHOD_NOT_ALLOWED", "This telemetry source route only supports POST.", false)
		return "", false
	}
	if hasForgedIdentityHeader(request.Header) {
		writeProblem(writer, request, http.StatusBadRequest, "TELEMETRY_FORGED_IDENTITY_HEADER", "Caller-supplied identity headers are not accepted.", false)
		return "", false
	}
	peer, ok := verifiedPeerSPIFFE(request)
	if !ok {
		writeProblem(writer, request, http.StatusUnauthorized, "TELEMETRY_SOURCE_IDENTITY_INVALID", "The calling source workload identity is not trusted.", false)
		return "", false
	}
	if h.sourceAuthenticator == nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "TELEMETRY_SOURCE_UNAVAILABLE", "The telemetry source acceptance path is temporarily unavailable.", true)
		return "", false
	}
	return peer, true
}

func decodeSourceRequest(writer http.ResponseWriter, request *http.Request, destination any) bool {
	mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(request.Header.Get("Content-Type")))
	if err != nil || mediaType != "application/json" {
		writeProblem(writer, request, http.StatusBadRequest, "TELEMETRY_SOURCE_REQUEST_INVALID", "The telemetry source request is invalid.", false)
		return false
	}
	request.Body = http.MaxBytesReader(writer, request.Body, maximumSourceObservationSize)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil || ensureJSONEOF(decoder) != nil {
		writeProblem(writer, request, http.StatusBadRequest, "TELEMETRY_SOURCE_REQUEST_INVALID", "The telemetry source request is invalid.", false)
		return false
	}
	return true
}

func normalizeSourceCoverage(input sourceCoverageRequest, reportedAt time.Time) (CoverageReport, error) {
	report := CoverageReport{
		IntegrationInstanceID: strings.TrimSpace(input.IntegrationInstanceID),
		ExternalEntityType:    strings.ToUpper(strings.TrimSpace(input.ExternalEntityType)),
		ExternalID:            strings.TrimSpace(input.ExternalID),
		Available:             input.Available,
		Reason:                telemetryapi.AvailabilityReasonCode(strings.ToUpper(strings.TrimSpace(input.Reason))),
		SourceRevision:        input.SourceRevision,
		ReportedAt:            reportedAt.UTC(),
	}
	if input.ContinuousSince != nil {
		value, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(*input.ContinuousSince))
		if err != nil {
			return CoverageReport{}, errors.New("telemetry coverage continuousSince is invalid")
		}
		value = value.UTC()
		report.ContinuousSince = &value
	}
	if err := validateCoverageReport(report); err != nil {
		return CoverageReport{}, err
	}
	return report, nil
}

var _ SourceAuthenticator = (*StaticSourceAuthenticator)(nil)
