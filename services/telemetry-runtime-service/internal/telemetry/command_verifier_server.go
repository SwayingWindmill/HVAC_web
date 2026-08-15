package telemetry

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/telemetryauth"
	"github.com/quanlaihe/hvac-web/services/telemetry-runtime-service/pkg/telemetryapi"
)

type commandReportedScalar struct {
	Number  *float64 `json:"number,omitempty"`
	Text    *string  `json:"text,omitempty"`
	Boolean *bool    `json:"boolean,omitempty"`
}

type commandReportedStateResponse struct {
	SchemaVersion          int       `json:"schemaVersion"`
	EvidenceID             string    `json:"evidenceId"`
	TenantID               string    `json:"tenantId"`
	SiteID                 string    `json:"siteId"`
	DeviceID               string    `json:"deviceId"`
	EvaluationAvailability string    `json:"evaluationAvailability"`
	Presence               string    `json:"presence"`
	Readiness              string    `json:"readiness"`
	Freshness              string    `json:"freshness"`
	Quality                string    `json:"quality"`
	BusinessRevision       uint64    `json:"businessRevision"`
	ReportedValue          commandReportedScalar `json:"reportedValue"`
	ObservedAt             time.Time `json:"observedAt"`
	ReportedStateKey       string    `json:"reportedStateKey"`
}

type commandReportedStateEvidencePayload struct {
	SchemaVersion          int       `json:"schemaVersion"`
	TenantID               string    `json:"tenantId"`
	SiteID                 string    `json:"siteId"`
	DeviceID               string    `json:"deviceId"`
	EvaluationAvailability string    `json:"evaluationAvailability"`
	Presence               string    `json:"presence"`
	Readiness              string    `json:"readiness"`
	Freshness              string    `json:"freshness"`
	Quality                string    `json:"quality"`
	BusinessRevision       uint64    `json:"businessRevision"`
	ReportedValue          commandReportedScalar `json:"reportedValue"`
	ObservedAt             time.Time `json:"observedAt"`
	ReportedStateKey       string    `json:"reportedStateKey"`
}

func (h *handler) handleCommandReportedState(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", http.MethodGet)
		writeProblem(writer, request, http.StatusMethodNotAllowed, "TELEMETRY_METHOD_NOT_ALLOWED", "This telemetry route only supports GET.", false)
		return
	}
	if hasForgedIdentityHeader(request.Header) {
		writeProblem(writer, request, http.StatusBadRequest, "TELEMETRY_FORGED_IDENTITY_HEADER", "Caller-supplied identity headers are not accepted.", false)
		return
	}
	peer, verified := verifiedPeerSPIFFE(request)
	allowedWorkload := peer == h.allowedCommandVerifierSPIFFE || peer == h.allowedCommandDispatcherSPIFFE
	if !verified || peer == "" || !allowedWorkload {
		writeProblem(writer, request, http.StatusUnauthorized, "TELEMETRY_WORKLOAD_IDENTITY_INVALID", "The calling workload identity is not trusted for authoritative command state.", false)
		return
	}
	if h.store == nil || !uuidV7Pattern.MatchString(h.commandVerifierTenantID) || !uuidV7Pattern.MatchString(h.commandVerifierSiteID) ||
		!uuidV7Pattern.MatchString(h.commandVerifierDeviceID) {
		writeProblem(writer, request, http.StatusServiceUnavailable, "TELEMETRY_COMMAND_REPORTED_STATE_UNAVAILABLE", "Command reported state is not configured.", true)
		return
	}
	query := request.URL.Query()
	keys, present := query["key"]
	if !present || len(query) != 1 || len(keys) != 1 || strings.TrimSpace(keys[0]) == "" || len(keys[0]) > 256 {
		writeProblem(writer, request, http.StatusBadRequest, "TELEMETRY_COMMAND_REPORTED_STATE_KEY_INVALID", "A single reported-state key is required.", false)
		return
	}
	reportedStateKey := strings.TrimSpace(keys[0])
	commit, err := h.store.EvaluateAndRead(request.Context(), telemetryauth.Target{
		DeviceID: h.commandVerifierDeviceID, Keys: []string{reportedStateKey},
	}, h.now().UTC())
	if errors.Is(err, ErrDeviceNotFound) {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "The configured telemetry resource was not found.", false)
		return
	}
	if err != nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "TELEMETRY_COMMAND_REPORTED_STATE_UNAVAILABLE", "Command reported state is temporarily unavailable.", true)
		return
	}
	response, err := h.commandReportedStateResponse(commit.Snapshot, reportedStateKey)
	if err != nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "TELEMETRY_COMMAND_REPORTED_STATE_INVALID", "Command reported state is not authoritative.", true)
		return
	}
	writeJSON(writer, http.StatusOK, response)
}

func (h *handler) commandReportedStateResponse(snapshot telemetryapi.DeviceObservationSnapshot, reportedStateKey string) (commandReportedStateResponse, error) {
	if string(snapshot.TenantId) != h.commandVerifierTenantID || string(snapshot.SiteId) != h.commandVerifierSiteID ||
		string(snapshot.DeviceId) != h.commandVerifierDeviceID || snapshot.BusinessRevision < 0 {
		return commandReportedStateResponse{}, errors.New("command reported-state scope mismatch")
	}
	observedAt, err := time.Parse(time.RFC3339Nano, string(snapshot.EvaluatedAt))
	if err != nil {
		return commandReportedStateResponse{}, err
	}
	presence := string(telemetryapi.DevicePresenceStateUnknown)
	if snapshot.Presence.CurrentState != nil {
		presence = string(*snapshot.Presence.CurrentState)
	}
	freshness := string(telemetryapi.TelemetryFreshnessMissing)
	quality := string(telemetryapi.TelemetryQualityInvalid)
	reportedValue := commandReportedScalar{}
	matched := 0
	for _, value := range snapshot.Values {
		if value.Present != nil && string(value.Present.Key) == reportedStateKey {
			matched++
			switch value.Present.ValueType {
			case "NUMBER":
				var numeric float64
				if err := json.Unmarshal(value.Present.Value, &numeric); err != nil {
					return commandReportedStateResponse{}, err
				}
				reportedValue.Number = &numeric
			case "STRING":
				var text string
				if err := json.Unmarshal(value.Present.Value, &text); err != nil {
					return commandReportedStateResponse{}, err
				}
				reportedValue.Text = &text
			case "BOOLEAN":
				var boolean bool
				if err := json.Unmarshal(value.Present.Value, &boolean); err != nil {
					return commandReportedStateResponse{}, err
				}
				reportedValue.Boolean = &boolean
			default:
				return commandReportedStateResponse{}, errors.New("command reported-state value type is unsupported")
			}
			freshness = value.Present.Freshness
			quality = string(value.Present.Quality)
			observedAt, err = time.Parse(time.RFC3339Nano, string(value.Present.SampledAt))
			if err != nil {
				return commandReportedStateResponse{}, err
			}
		}
		if value.Missing != nil && string(value.Missing.Key) == reportedStateKey {
			matched++
			freshness = value.Missing.Freshness
		}
	}
	if matched != 1 {
		return commandReportedStateResponse{}, errors.New("command reported-state key is missing or duplicated")
	}
	payload := commandReportedStateEvidencePayload{
		SchemaVersion: 1, TenantID: h.commandVerifierTenantID, SiteID: h.commandVerifierSiteID, DeviceID: h.commandVerifierDeviceID,
		EvaluationAvailability: string(snapshot.EvaluationAvailability), Presence: presence, Readiness: string(snapshot.TelemetryReadiness),
		Freshness: freshness, Quality: quality, BusinessRevision: uint64(snapshot.BusinessRevision), ReportedValue: reportedValue,
		ObservedAt: observedAt.UTC(), ReportedStateKey: reportedStateKey,
	}
	canonical, err := json.Marshal(payload)
	if err != nil {
		return commandReportedStateResponse{}, err
	}
	digest := sha256.Sum256(canonical)
	return commandReportedStateResponse{
		SchemaVersion: payload.SchemaVersion, EvidenceID: "s2:sha256:" + hex.EncodeToString(digest[:]),
		TenantID: payload.TenantID, SiteID: payload.SiteID, DeviceID: payload.DeviceID,
		EvaluationAvailability: payload.EvaluationAvailability, Presence: payload.Presence, Readiness: payload.Readiness,
		Freshness: payload.Freshness, Quality: payload.Quality, BusinessRevision: payload.BusinessRevision,
		ReportedValue: payload.ReportedValue, ObservedAt: payload.ObservedAt, ReportedStateKey: payload.ReportedStateKey,
	}, nil
}
