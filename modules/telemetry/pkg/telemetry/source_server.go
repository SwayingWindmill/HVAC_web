package telemetry

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/limitpolicy"
)

const (
	InternalSourceObservationPath           = "/internal/v1/telemetry/sources/observations:accept"
	InternalHistoricalReplayObservationPath = "/internal/v1/telemetry/history-replay/observations:accept"
	InternalMQTTGatewayEvidencePath         = "/internal/v1/telemetry/sources/mqtt/gateway-evidence:accept"
	InternalMQTTPresenceEvidencePath        = "/internal/v1/telemetry/sources/mqtt/presence-evidence:accept"
	InternalMQTTRuntimeEventPath            = "/internal/v1/telemetry/sources/mqtt/events:accept"
	maximumSourceObservationSize            = 96 << 10
)

type sourcePositionRequest struct {
	Partition string `json:"partition"`
	Offset    int64  `json:"offset"`
	EventID   string `json:"eventId"`
}

// sourceObservationRequest is one value from Connectivity. Device and Point are what
// Connectivity resolved from Registry; either is absent when the source key is unregistered.
type sourceObservationRequest struct {
	SourceID           string                 `json:"sourceId"`
	SourcePath         string                 `json:"sourcePath"`
	ExternalEntityType string                 `json:"externalEntityType"`
	ExternalID         string                 `json:"externalId"`
	Device             *resolvedDeviceRequest `json:"device"`
	Point              *resolvedPointRequest  `json:"point"`
	TelemetryKey       string                 `json:"telemetryKey"`
	Value              json.RawMessage        `json:"value"`
	ValueType          string                 `json:"valueType"`
	Unit               *string                `json:"unit"`
	WireQuality        uint8                  `json:"wireQuality"`
	SampledAt          string                 `json:"sampledAt"`
	SourcePosition     sourcePositionRequest  `json:"sourcePosition"`
}

type resolvedDeviceRequest struct {
	TenantID string `json:"tenantId"`
	SiteID   string `json:"siteId"`
	DeviceID string `json:"deviceId"`
}

type resolvedPointRequest struct {
	PointID                string   `json:"pointId"`
	SensorID               *string  `json:"sensorId"`
	PointType              string   `json:"pointType"`
	ValueType              string   `json:"valueType"`
	Unit                   *string  `json:"unit"`
	CounterDecreaseMode    *string  `json:"counterDecreaseMode"`
	CounterRolloverModulus *float64 `json:"counterRolloverModulus"`
	PointRevision          int64    `json:"pointRevision"`
}

type historicalReplayObservationRequest struct {
	ReplayDatasetID string          `json:"replayDatasetId"`
	DeviceID        string          `json:"deviceId"`
	TelemetryKey    string          `json:"telemetryKey"`
	Value           json.RawMessage `json:"value"`
	ValueType       string          `json:"valueType"`
	Unit            *string         `json:"unit"`
	WireQuality     uint8           `json:"wireQuality"`
	SampledAt       string          `json:"sampledAt"`
	Offset          int64           `json:"offset"`
}

// historicalReplaySourceID is the source of every replayed observation; each dataset and
// Device replays in its own partition.
const historicalReplaySourceID = "history-replay"

func (h *handler) handleSourceObservation(writer http.ResponseWriter, request *http.Request) {
	peer, ok := h.trustedSourcePeer(writer, request, h.allowedSourceSPIFFE)
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
	if h.rateLimiter != nil && !h.rateLimiter.Allow(request.Context(), limitpolicy.DimensionTelemetryIngest, candidate.SourceID).Allowed {
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

func (h *handler) handleHistoricalReplayObservation(writer http.ResponseWriter, request *http.Request) {
	if _, ok := h.trustedSourcePeer(writer, request, h.allowedHistoricalReplaySPIFFE); !ok {
		return
	}
	if h.historicalObservationAcceptor == nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "TELEMETRY_HISTORY_REPLAY_UNAVAILABLE", "The telemetry Historical Replay path is temporarily unavailable.", true)
		return
	}
	var input historicalReplayObservationRequest
	if !decodeSourceRequest(writer, request, &input) {
		return
	}
	candidate, err := normalizeHistoricalReplayObservation(input, h.now().UTC())
	if err != nil {
		writeProblem(writer, request, http.StatusBadRequest, "TELEMETRY_HISTORY_REPLAY_REQUEST_INVALID", "The Historical Replay observation request is invalid.", false)
		return
	}
	if h.rateLimiter != nil && !h.rateLimiter.Allow(request.Context(), limitpolicy.DimensionTelemetryIngest, candidate.SourceID).Allowed {
		writeProblem(writer, request, http.StatusTooManyRequests, "TELEMETRY_INGEST_RATE_LIMITED", "The Historical Replay observation rate has been exceeded.", true)
		return
	}
	receipt, err := h.historicalObservationAcceptor.AcceptHistoricalObservation(request.Context(), candidate)
	if err != nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "TELEMETRY_HISTORY_REPLAY_UNAVAILABLE", "The telemetry Historical Replay path is temporarily unavailable.", true)
		return
	}
	h.metrics.observeDataQuality(receipt)
	writeJSON(writer, http.StatusOK, receipt)
}

func normalizeSourceObservation(input sourceObservationRequest, receivedAt time.Time) (ObservationCandidate, error) {
	sampledAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(input.SampledAt))
	if err != nil || receivedAt.IsZero() {
		return ObservationCandidate{}, errors.New("telemetry sampledAt is invalid")
	}
	sourcePath := safeSourcePath(input.SourcePath)
	if sourcePath == SourcePathHistoryReplay {
		return ObservationCandidate{}, errors.New("HISTORY_REPLAY requires the dedicated admission path")
	}
	candidate := ObservationCandidate{
		SourceID:           strings.TrimSpace(input.SourceID),
		SourcePath:         sourcePath,
		ExternalEntityType: strings.ToUpper(strings.TrimSpace(input.ExternalEntityType)),
		ExternalID:         strings.TrimSpace(input.ExternalID),
		TelemetryKey:       strings.TrimSpace(input.TelemetryKey),
		Value:              append(json.RawMessage(nil), input.Value...),
		ValueType:          strings.ToUpper(strings.TrimSpace(input.ValueType)),
		Unit:               cloneString(input.Unit),
		WireQuality:        input.WireQuality,
		SampledAt:          sampledAt.UTC(),
		ReceivedAt:         receivedAt.UTC(),
		Position: SourcePosition{
			Partition: strings.TrimSpace(input.SourcePosition.Partition),
			Offset:    input.SourcePosition.Offset,
			EventID:   strings.TrimSpace(input.SourcePosition.EventID),
		},
	}
	if device := input.Device; device != nil {
		candidate.Device = &ResolvedDevice{
			TenantID: strings.ToLower(strings.TrimSpace(device.TenantID)),
			SiteID:   strings.ToLower(strings.TrimSpace(device.SiteID)),
			DeviceID: strings.ToLower(strings.TrimSpace(device.DeviceID)),
		}
	}
	if point := input.Point; point != nil {
		candidate.Point = &ResolvedPoint{
			PointID: strings.ToLower(strings.TrimSpace(point.PointID)), SensorID: cloneString(point.SensorID),
			PointType: point.PointType, ValueType: point.ValueType, Unit: cloneString(point.Unit),
			CounterDecreaseMode: cloneString(point.CounterDecreaseMode), CounterRolloverModulus: point.CounterRolloverModulus,
			PointRevision: point.PointRevision,
		}
	}
	if err := validateObservationCandidate(candidate); err != nil {
		return ObservationCandidate{}, err
	}
	return candidate, nil
}

func normalizeHistoricalReplayObservation(input historicalReplayObservationRequest, receivedAt time.Time) (ObservationCandidate, error) {
	datasetID := strings.ToLower(strings.TrimSpace(input.ReplayDatasetID))
	deviceID := strings.ToLower(strings.TrimSpace(input.DeviceID))
	if !uuidV7Pattern.MatchString(datasetID) || !uuidV7Pattern.MatchString(deviceID) || input.Offset < 0 || receivedAt.IsZero() {
		return ObservationCandidate{}, errors.New("Historical Replay identity is invalid")
	}
	sampledAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(input.SampledAt))
	if err != nil {
		return ObservationCandidate{}, errors.New("Historical Replay sampledAt is invalid")
	}
	deviceDigest := sha256.Sum256([]byte(deviceID))
	partition := "history-replay:" + datasetID + ":" + hex.EncodeToString(deviceDigest[:16])
	eventID, err := deterministicHistoricalReplayEventID(datasetID, partition, input.Offset)
	if err != nil {
		return ObservationCandidate{}, err
	}
	candidate := ObservationCandidate{
		SourceID:           historicalReplaySourceID,
		SourcePath:         SourcePathHistoryReplay,
		ExternalEntityType: "DEVICE",
		ExternalID:         deviceID,
		TelemetryKey:       strings.TrimSpace(input.TelemetryKey),
		Value:              append(json.RawMessage(nil), input.Value...),
		ValueType:          strings.ToUpper(strings.TrimSpace(input.ValueType)),
		Unit:               cloneString(input.Unit),
		WireQuality:        input.WireQuality,
		SampledAt:          sampledAt.UTC(),
		ReceivedAt:         receivedAt.UTC(),
		Position:           SourcePosition{Partition: partition, Offset: input.Offset, EventID: eventID},
	}
	if err := validateObservationCandidate(candidate); err != nil {
		return ObservationCandidate{}, err
	}
	return candidate, nil
}

func deterministicHistoricalReplayEventID(datasetID, partition string, offset int64) (string, error) {
	datasetBytes, err := hex.DecodeString(strings.ReplaceAll(datasetID, "-", ""))
	if err != nil || len(datasetBytes) != 16 {
		return "", errors.New("Historical Replay dataset ID is invalid")
	}
	digest := sha256.Sum256([]byte(datasetID + "\x00" + partition + "\x00" + strconv.FormatInt(offset, 10)))
	identifier := make([]byte, 16)
	copy(identifier[:6], datasetBytes[:6])
	copy(identifier[6:], digest[:10])
	identifier[6] = (identifier[6] & 0x0f) | 0x70
	identifier[8] = (identifier[8] & 0x3f) | 0x80
	raw := hex.EncodeToString(identifier)
	return raw[:8] + "-" + raw[8:12] + "-" + raw[12:16] + "-" + raw[16:20] + "-" + raw[20:], nil
}

type mqttGatewayEvidenceRequest struct {
	TenantID     string          `json:"tenantId"`
	SiteID       string          `json:"siteId"`
	GatewayID    string          `json:"gatewayId"`
	MessageID    string          `json:"messageId"`
	EvidenceType string          `json:"evidenceType"`
	ObservedAt   string          `json:"observedAt"`
	Sequence     int64           `json:"sequence"`
	Payload      json.RawMessage `json:"payload"`
}

func (h *handler) handleMQTTGatewayEvidence(writer http.ResponseWriter, request *http.Request) {
	if _, ok := h.trustedSourcePeer(writer, request, h.allowedSourceSPIFFE); !ok {
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
	observedAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(input.ObservedAt))
	if err != nil {
		writeProblem(writer, request, http.StatusBadRequest, "TELEMETRY_SOURCE_REQUEST_INVALID", "The MQTT evidence request is invalid.", false)
		return
	}
	evidence := GatewayEvidence{
		TenantID: strings.TrimSpace(input.TenantID), SiteID: strings.TrimSpace(input.SiteID),
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
	DeviceID      string `json:"deviceId"`
	SignalType    string `json:"signalType"`
	ObservedAt    string `json:"observedAt"`
	SourceEventID string `json:"sourceEventId"`
}

func (h *handler) handleMQTTPresenceEvidence(writer http.ResponseWriter, request *http.Request) {
	if _, ok := h.trustedSourcePeer(writer, request, h.allowedSourceSPIFFE); !ok {
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
	observedAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(input.ObservedAt))
	if err != nil {
		writeProblem(writer, request, http.StatusBadRequest, "TELEMETRY_SOURCE_REQUEST_INVALID", "The MQTT Presence evidence request is invalid.", false)
		return
	}
	receipt, err := h.mqttEvidenceAcceptor.AcceptPresenceEvidence(request.Context(), DevicePresenceEvidence{
		DeviceID:   strings.ToLower(strings.TrimSpace(input.DeviceID)),
		SignalType: strings.ToUpper(strings.TrimSpace(input.SignalType)), ObservedAt: observedAt.UTC(), ReceivedAt: h.now().UTC(), SourceEventID: strings.TrimSpace(input.SourceEventID),
	})
	if err != nil {
		writeProblem(writer, request, http.StatusBadRequest, "TELEMETRY_SOURCE_REQUEST_INVALID", "The MQTT Presence evidence request was rejected.", false)
		return
	}
	writeJSON(writer, http.StatusOK, receipt)
}

type mqttRuntimeEventRequest struct {
	TenantID   string          `json:"tenantId"`
	SiteID     string          `json:"siteId"`
	GatewayID  string          `json:"gatewayId"`
	MessageID  string          `json:"messageId"`
	Sequence   int64           `json:"sequence"`
	EventType  string          `json:"eventType"`
	SourceType string          `json:"sourceType"`
	SourceID   string          `json:"sourceId"`
	EventTime  string          `json:"eventTime"`
	Severity   string          `json:"severity"`
	Data       json.RawMessage `json:"data"`
}

func (h *handler) handleMQTTRuntimeEvent(writer http.ResponseWriter, request *http.Request) {
	if _, ok := h.trustedSourcePeer(writer, request, h.allowedSourceSPIFFE); !ok {
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
	eventTime, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(input.EventTime))
	if err != nil {
		writeProblem(writer, request, http.StatusBadRequest, "TELEMETRY_SOURCE_REQUEST_INVALID", "The MQTT runtime event request is invalid.", false)
		return
	}
	evidence := RuntimeEventEvidence{
		TenantID: strings.TrimSpace(input.TenantID), SiteID: strings.TrimSpace(input.SiteID), GatewayID: strings.TrimSpace(input.GatewayID),
		MessageID: strings.TrimSpace(input.MessageID), Sequence: input.Sequence, EventType: strings.TrimSpace(input.EventType), SourceType: strings.TrimSpace(input.SourceType), SourceID: strings.TrimSpace(input.SourceID),
		EventTime: eventTime.UTC(), Severity: strings.ToUpper(strings.TrimSpace(input.Severity)), Data: append(json.RawMessage(nil), input.Data...), ReceivedAt: h.now().UTC(),
	}
	if err = h.mqttEvidenceAcceptor.AcceptRuntimeEvent(request.Context(), evidence); err != nil {
		writeProblem(writer, request, http.StatusBadRequest, "TELEMETRY_SOURCE_REQUEST_INVALID", "The MQTT runtime event request was rejected.", false)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"accepted": true, "messageId": evidence.MessageID})
}

// trustedSourcePeer admits a POST only from the one workload identity allowed on the route.
func (h *handler) trustedSourcePeer(writer http.ResponseWriter, request *http.Request, allowedSPIFFE string) (string, bool) {
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
	if !ok || peer != allowedSPIFFE {
		writeProblem(writer, request, http.StatusUnauthorized, "TELEMETRY_SOURCE_IDENTITY_INVALID", "The calling source workload identity is not trusted.", false)
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
