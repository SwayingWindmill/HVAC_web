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

	"github.com/quanlaihe/hvac-web/services/telemetry-runtime-service/pkg/telemetryapi"
)

const (
	InternalThingsBoardObservationPath = "/internal/v1/telemetry/thingsboard/observations:accept"
	InternalThingsBoardCoveragePath    = "/internal/v1/telemetry/thingsboard/coverage:report"
	maximumSourceObservationSize       = 96 << 10
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

type thingsBoardObservationRequest struct {
	IntegrationInstanceID string                `json:"integrationInstanceId"`
	SourcePath            string                `json:"sourcePath"`
	ExternalEntityType    string                `json:"externalEntityType"`
	ExternalID            string                `json:"externalId"`
	TelemetryKey          string                `json:"telemetryKey"`
	Value                 json.RawMessage       `json:"value"`
	ValueType             string                `json:"valueType"`
	Unit                  *string               `json:"unit"`
	SampledAt             string                `json:"sampledAt"`
	SourcePosition        sourcePositionRequest `json:"sourcePosition"`
}

func (h *handler) handleThingsBoardObservation(writer http.ResponseWriter, request *http.Request) {
	peer, ok := h.trustedSourcePeer(writer, request)
	if !ok {
		return
	}
	if h.observationAcceptor == nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "TELEMETRY_SOURCE_UNAVAILABLE", "The telemetry source acceptance path is temporarily unavailable.", true)
		return
	}
	var input thingsBoardObservationRequest
	if !decodeSourceRequest(writer, request, &input) {
		return
	}
	candidate, err := normalizeThingsBoardObservation(input, h.now().UTC())
	if err != nil {
		writeProblem(writer, request, http.StatusBadRequest, "TELEMETRY_SOURCE_REQUEST_INVALID", "The telemetry source request is invalid.", false)
		return
	}
	if !h.sourceAuthenticator.AllowsSource(peer, candidate.IntegrationInstanceID) {
		writeProblem(writer, request, http.StatusUnauthorized, "TELEMETRY_SOURCE_IDENTITY_INVALID", "The calling source workload identity is not trusted.", false)
		return
	}
	receipt, err := h.observationAcceptor.AcceptObservation(request.Context(), candidate)
	if err != nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "TELEMETRY_SOURCE_UNAVAILABLE", "The telemetry source acceptance path is temporarily unavailable.", true)
		return
	}
	sourceOutcome := "success"
	if receipt.Status == ObservationQuarantined {
		sourceOutcome = "rejected"
	}
	h.metrics.observeSourceLag(candidate.SampledAt, candidate.ReceivedAt, sourceOutcome)
	if receipt.Status == ObservationQuarantined {
		h.metrics.observeQuarantine(quarantineReasonFamily(receipt.QuarantineReason))
	}
	writeJSON(writer, http.StatusOK, receipt)
}

func normalizeThingsBoardObservation(input thingsBoardObservationRequest, receivedAt time.Time) (ObservationCandidate, error) {
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

type thingsBoardCoverageRequest struct {
	IntegrationInstanceID string  `json:"integrationInstanceId"`
	ExternalEntityType    string  `json:"externalEntityType"`
	ExternalID            string  `json:"externalId"`
	Available             bool    `json:"available"`
	ContinuousSince       *string `json:"continuousSince"`
	Reason                string  `json:"reason"`
	SourceRevision        int64   `json:"sourceRevision"`
}

func (h *handler) handleThingsBoardCoverage(writer http.ResponseWriter, request *http.Request) {
	peer, ok := h.trustedSourcePeer(writer, request)
	if !ok {
		return
	}
	if h.coverageReporter == nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "TELEMETRY_SOURCE_UNAVAILABLE", "The telemetry source acceptance path is temporarily unavailable.", true)
		return
	}
	var input thingsBoardCoverageRequest
	if !decodeSourceRequest(writer, request, &input) {
		return
	}
	report, err := normalizeThingsBoardCoverage(input, h.now().UTC())
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

func normalizeThingsBoardCoverage(input thingsBoardCoverageRequest, reportedAt time.Time) (CoverageReport, error) {
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
