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

type commandReportedStateResponse struct {
	SchemaVersion          int       `json:"schemaVersion"`
	EvidenceID             string    `json:"evidenceId"`
	OrganizationID         string    `json:"organizationId"`
	SiteID                 string    `json:"siteId"`
	DeviceID               string    `json:"deviceId"`
	EvaluationAvailability string    `json:"evaluationAvailability"`
	Presence               string    `json:"presence"`
	Readiness              string    `json:"readiness"`
	Freshness              string    `json:"freshness"`
	Quality                string    `json:"quality"`
	BusinessRevision       uint64    `json:"businessRevision"`
	ReportedSetpointC      float64   `json:"reportedSetpointC"`
	ObservedAt             time.Time `json:"observedAt"`
	ReportedStateKey       string    `json:"reportedStateKey"`
}

type commandReportedStateEvidencePayload struct {
	SchemaVersion          int       `json:"schemaVersion"`
	OrganizationID         string    `json:"organizationId"`
	SiteID                 string    `json:"siteId"`
	DeviceID               string    `json:"deviceId"`
	EvaluationAvailability string    `json:"evaluationAvailability"`
	Presence               string    `json:"presence"`
	Readiness              string    `json:"readiness"`
	Freshness              string    `json:"freshness"`
	Quality                string    `json:"quality"`
	BusinessRevision       uint64    `json:"businessRevision"`
	ReportedSetpointC      float64   `json:"reportedSetpointC"`
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
	if !verified || h.allowedCommandVerifierSPIFFE == "" || peer != h.allowedCommandVerifierSPIFFE {
		writeProblem(writer, request, http.StatusUnauthorized, "TELEMETRY_WORKLOAD_IDENTITY_INVALID", "The calling workload identity is not trusted.", false)
		return
	}
	if h.store == nil || !uuidV7Pattern.MatchString(h.commandVerifierOrganizationID) || !uuidV7Pattern.MatchString(h.commandVerifierSiteID) ||
		!uuidV7Pattern.MatchString(h.commandVerifierDeviceID) || strings.TrimSpace(h.commandReportedStateKey) == "" {
		writeProblem(writer, request, http.StatusServiceUnavailable, "TELEMETRY_COMMAND_REPORTED_STATE_UNAVAILABLE", "Command reported state is not configured.", true)
		return
	}
	commit, err := h.store.EvaluateAndRead(request.Context(), telemetryauth.Target{
		DeviceID: h.commandVerifierDeviceID, Keys: []string{h.commandReportedStateKey},
	}, h.now().UTC())
	if errors.Is(err, ErrDeviceNotFound) {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "The configured telemetry resource was not found.", false)
		return
	}
	if err != nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "TELEMETRY_COMMAND_REPORTED_STATE_UNAVAILABLE", "Command reported state is temporarily unavailable.", true)
		return
	}
	response, err := h.commandReportedStateResponse(commit.Snapshot)
	if err != nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "TELEMETRY_COMMAND_REPORTED_STATE_INVALID", "Command reported state is not authoritative.", true)
		return
	}
	writeJSON(writer, http.StatusOK, response)
}

func (h *handler) commandReportedStateResponse(snapshot telemetryapi.DeviceObservationSnapshot) (commandReportedStateResponse, error) {
	if string(snapshot.OwningOrganizationId) != h.commandVerifierOrganizationID || string(snapshot.SiteId) != h.commandVerifierSiteID ||
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
	quality := string(telemetryapi.TelemetryQualitySuspect)
	reportedSetpoint := float64(0)
	matched := 0
	for _, value := range snapshot.Values {
		if value.Present != nil && string(value.Present.Key) == h.commandReportedStateKey {
			matched++
			if err := json.Unmarshal(value.Present.Value, &reportedSetpoint); err != nil {
				return commandReportedStateResponse{}, err
			}
			freshness = value.Present.Freshness
			quality = string(value.Present.Quality)
			observedAt, err = time.Parse(time.RFC3339Nano, string(value.Present.SampledAt))
			if err != nil {
				return commandReportedStateResponse{}, err
			}
		}
		if value.Missing != nil && string(value.Missing.Key) == h.commandReportedStateKey {
			matched++
			freshness = value.Missing.Freshness
		}
	}
	if matched != 1 {
		return commandReportedStateResponse{}, errors.New("command reported-state key is missing or duplicated")
	}
	payload := commandReportedStateEvidencePayload{
		SchemaVersion: 1, OrganizationID: h.commandVerifierOrganizationID, SiteID: h.commandVerifierSiteID, DeviceID: h.commandVerifierDeviceID,
		EvaluationAvailability: string(snapshot.EvaluationAvailability), Presence: presence, Readiness: string(snapshot.TelemetryReadiness),
		Freshness: freshness, Quality: quality, BusinessRevision: uint64(snapshot.BusinessRevision), ReportedSetpointC: reportedSetpoint,
		ObservedAt: observedAt.UTC(), ReportedStateKey: h.commandReportedStateKey,
	}
	canonical, err := json.Marshal(payload)
	if err != nil {
		return commandReportedStateResponse{}, err
	}
	digest := sha256.Sum256(canonical)
	return commandReportedStateResponse{
		SchemaVersion: payload.SchemaVersion, EvidenceID: "s2:sha256:" + hex.EncodeToString(digest[:]),
		OrganizationID: payload.OrganizationID, SiteID: payload.SiteID, DeviceID: payload.DeviceID,
		EvaluationAvailability: payload.EvaluationAvailability, Presence: payload.Presence, Readiness: payload.Readiness,
		Freshness: payload.Freshness, Quality: payload.Quality, BusinessRevision: payload.BusinessRevision,
		ReportedSetpointC: payload.ReportedSetpointC, ObservedAt: payload.ObservedAt, ReportedStateKey: payload.ReportedStateKey,
	}, nil
}
