package connectivity

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/modules/connectivity/pkg/packettap"
)

func NewDiagnosticsHandlerWithWaveform(
	store DiagnosticsStore,
	tap *packettap.Manager,
	authorize CredentialAuthorizer,
	bridge *DiagnosticsMQTTBridge,
) http.Handler {
	baseHandler := NewDiagnosticsHandler(store, tap, authorize)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if handled := serveWaveformDiagnosticsRoutes(w, r, store, authorize, bridge); handled {
			return
		}
		baseHandler.ServeHTTP(w, r)
	})
}

func serveWaveformDiagnosticsRoutes(
	w http.ResponseWriter,
	r *http.Request,
	store DiagnosticsStore,
	authorize CredentialAuthorizer,
	bridge *DiagnosticsMQTTBridge,
) bool {
	rest, ok := strings.CutPrefix(r.URL.Path, "/internal/v1/gateways/")
	if !ok {
		return false
	}
	parts := strings.Split(rest, "/")
	if len(parts) < 2 || len(parts) > 3 {
		return false
	}
	gatewayID := strings.TrimSpace(parts[0])
	section := parts[1]
	sub := ""
	if len(parts) == 3 {
		sub = parts[2]
	}

	isWaveformRoute := section == "waveform-rules" ||
		section == "waveform-captures" ||
		section == "waveform-captures:trigger" ||
		(section == "diagnostics" && (sub == "logs:pull" || sub == "host-metrics"))
	if !isWaveformRoute {
		return false
	}

	wStore, hasWStore := store.(WaveformDiagnosticsStore)
	if !hasWStore || store == nil {
		writeDiagnosticsErrorJSON(w, http.StatusServiceUnavailable, "WAVEFORM_DIAGNOSTICS_UNAVAILABLE", "waveform diagnostics store is unavailable")
		return true
	}

	requiredAction := registryauth.ActionDeviceRead
	if r.Method != http.MethodGet {
		requiredAction = registryauth.ActionDeviceWrite
	}
	if authorize == nil {
		writeDiagnosticsErrorJSON(w, http.StatusForbidden, "CREDENTIAL_FORBIDDEN", "diagnostics authorization unavailable")
		return true
	}
	claims, err := authorize(r, requiredAction)
	if err != nil {
		writeMappedDiagnosticsError(w, err)
		return true
	}

	switch {
	case len(parts) == 2 && section == "waveform-rules" && r.Method == http.MethodGet:
		items, err := wStore.ListGatewayWaveformRules(r.Context(), claims, gatewayID)
		if err != nil {
			writeMappedDiagnosticsError(w, err)
			return true
		}
		writeWaveformJSON(w, http.StatusOK, struct {
			GatewayID string               `json:"gatewayId"`
			Items     []WaveformRuleRecord `json:"items"`
		}{GatewayID: gatewayID, Items: items})
		return true

	case len(parts) == 2 && section == "waveform-rules" && r.Method == http.MethodPost:
		var input UpsertWaveformRuleInput
		if err := decodeWaveformBody(w, r, &input); err != nil {
			writeDiagnosticsErrorJSON(w, http.StatusBadRequest, "WAVEFORM_RULE_INVALID", err.Error())
			return true
		}
		rec, err := wStore.CreateGatewayWaveformRule(r.Context(), claims, gatewayID, input)
		if err != nil {
			writeMappedDiagnosticsError(w, err)
			return true
		}
		writeWaveformJSON(w, http.StatusCreated, rec)
		return true

	case len(parts) == 3 && section == "waveform-rules" && r.Method == http.MethodPut:
		var input UpsertWaveformRuleInput
		if err := decodeWaveformBody(w, r, &input); err != nil {
			writeDiagnosticsErrorJSON(w, http.StatusBadRequest, "WAVEFORM_RULE_INVALID", err.Error())
			return true
		}
		rec, err := wStore.UpdateGatewayWaveformRule(r.Context(), claims, gatewayID, sub, input)
		if err != nil {
			writeMappedDiagnosticsError(w, err)
			return true
		}
		writeWaveformJSON(w, http.StatusOK, rec)
		return true

	case len(parts) == 3 && section == "waveform-rules" && r.Method == http.MethodDelete:
		if err := wStore.DeleteGatewayWaveformRule(r.Context(), claims, gatewayID, sub); err != nil {
			writeMappedDiagnosticsError(w, err)
			return true
		}
		writeWaveformJSON(w, http.StatusOK, map[string]bool{"deleted": true})
		return true

	case len(parts) == 2 && section == "waveform-captures" && r.Method == http.MethodGet:
		limit := 20
		if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
			if n, convErr := strconv.Atoi(raw); convErr == nil {
				limit = n
			}
		}
		items, err := wStore.ListGatewayWaveformCaptures(r.Context(), claims, gatewayID, limit)
		if err != nil {
			writeMappedDiagnosticsError(w, err)
			return true
		}
		writeWaveformJSON(w, http.StatusOK, struct {
			GatewayID string                  `json:"gatewayId"`
			Items     []WaveformCaptureRecord `json:"items"`
		}{GatewayID: gatewayID, Items: items})
		return true

	case len(parts) == 3 && section == "waveform-captures" && r.Method == http.MethodGet:
		rec, err := wStore.GetGatewayWaveformCapture(r.Context(), claims, gatewayID, sub)
		if err != nil {
			writeMappedDiagnosticsError(w, err)
			return true
		}
		writeWaveformJSON(w, http.StatusOK, rec)
		return true

	case len(parts) == 2 && section == "waveform-captures:trigger" && r.Method == http.MethodPost:
		var input TriggerWaveformCaptureInput
		if err := decodeWaveformBody(w, r, &input); err != nil {
			writeDiagnosticsErrorJSON(w, http.StatusBadRequest, "WAVEFORM_TRIGGER_INVALID", err.Error())
			return true
		}
		rec, err := wStore.CreateManualWaveformCapture(r.Context(), claims, gatewayID, input)
		if err != nil {
			writeMappedDiagnosticsError(w, err)
			return true
		}
		if bridge != nil {
			_ = bridge.PublishTriggerWaveform(r.Context(), gatewayID, edgecontrol.DiagnosticsDownlinkPayload{
				RequestID:     rec.CaptureID,
				Action:        edgecontrol.DiagnosticsActionTriggerWaveform,
				CaptureID:     rec.CaptureID,
				RuleID:        derefString(rec.RuleID),
				DeviceCode:    input.DeviceCode,
				PointCode:     input.PointCode,
				PointCodes:    input.PointCodes,
				PreSeconds:    int(rec.TriggeredAt.Sub(rec.WindowStartAt).Seconds()),
				PostSeconds:   int(rec.WindowEndAt.Sub(rec.TriggeredAt).Seconds()),
				SampleRateHz:  rec.SampleRateHz,
				TriggerReason: rec.TriggerReason,
				RequestedBy:   rec.RequestedBy,
			})
		}
		_ = wStore.RecordDiagnosticsAudit(r.Context(), rec.TenantID, rec.SiteID, gatewayID, "WAVEFORM_TRIGGER", rec.RequestedBy, 0, 0, 0, 0, "SUCCEEDED", rec.TriggerReason)
		writeWaveformJSON(w, http.StatusCreated, rec)
		return true

	case len(parts) == 3 && section == "diagnostics" && sub == "logs:pull" && r.Method == http.MethodPost:
		tenantID, siteID, scopeErr := wStore.CheckGatewayScopeForDiagnostics(r.Context(), claims, gatewayID)
		if scopeErr != nil {
			writeMappedDiagnosticsError(w, scopeErr)
			return true
		}
		var input PullGatewayLogsInput
		if err := decodeWaveformOptionalBody(w, r, &input); err != nil {
			writeDiagnosticsErrorJSON(w, http.StatusBadRequest, "DIAGNOSTICS_LOG_REQUEST_INVALID", err.Error())
			return true
		}
		maxBytes, sinceMinutes := edgecontrol.ClampLogPullBounds(input.MaxBytes, input.SinceMinutes)
		reqID, _ := newUUIDv7(time.Now().UTC())
		if reqID == "" {
			reqID = "0194f000-8800-7000-8000-000000000001"
		}
		reply, pullErr := bridge.RequestLogBundle(r.Context(), gatewayID, edgecontrol.DiagnosticsDownlinkPayload{
			RequestID:    reqID,
			Action:       edgecontrol.DiagnosticsActionPullLogBundle,
			MaxBytes:     maxBytes,
			SinceMinutes: sinceMinutes,
			RequestedBy:  firstActorID(claims),
		})
		if pullErr != nil {
			_ = wStore.RecordDiagnosticsAudit(r.Context(), tenantID, siteID, gatewayID, "LOG_BUNDLE_PULL", firstActorID(claims), maxBytes, sinceMinutes, 0, 0, "FAILED", pullErr.Error())
			writeDiagnosticsErrorJSON(w, http.StatusServiceUnavailable, "GATEWAY_DIAGNOSTICS_OFFLINE", pullErr.Error())
			return true
		}
		cleanContent, serverRedactions, serverTrunc := edgecontrol.RedactSensitiveLogText(reply.Content, maxBytes)
		totalRedacted := reply.RedactedCount + serverRedactions
		truncated := reply.Truncated || serverTrunc
		_ = wStore.RecordDiagnosticsAudit(r.Context(), tenantID, siteID, gatewayID, "LOG_BUNDLE_PULL", firstActorID(claims), maxBytes, sinceMinutes, len(cleanContent), totalRedacted, "SUCCEEDED", "redacted process log pull")
		writeWaveformJSON(w, http.StatusOK, GatewayLogBundleResult{
			RequestID:     reqID,
			GatewayID:     gatewayID,
			Status:        "SUCCEEDED",
			Content:       cleanContent,
			ReturnedBytes: len(cleanContent),
			RedactedCount: totalRedacted,
			Truncated:     truncated,
			CollectedAt:   time.UnixMilli(reply.CollectedAtMs).UTC(),
		})
		return true

	case len(parts) == 3 && section == "diagnostics" && sub == "host-metrics" && r.Method == http.MethodGet:
		limit := 20
		if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
			if n, convErr := strconv.Atoi(raw); convErr == nil {
				limit = n
			}
		}
		summary, err := wStore.GetGatewayHostMetrics(r.Context(), claims, gatewayID, limit)
		if err != nil {
			writeMappedDiagnosticsError(w, err)
			return true
		}
		writeWaveformJSON(w, http.StatusOK, summary)
		return true
	}

	return false
}

func decodeWaveformBody(w http.ResponseWriter, r *http.Request, dst any) error {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(bytes.TrimSpace(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON data")
	}
	return nil
}

func decodeWaveformOptionalBody(w http.ResponseWriter, r *http.Request, dst any) error {
	if r.Body == nil {
		return nil
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16<<10))
	if err != nil {
		return err
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON data")
	}
	return nil
}

func writeMappedDiagnosticsError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrEnrollmentRejected), errors.Is(err, ErrWaveformInvalid):
		writeDiagnosticsErrorJSON(w, http.StatusBadRequest, "WAVEFORM_DIAGNOSTICS_INVALID", err.Error())
	case errors.Is(err, ErrCredentialForbidden):
		writeDiagnosticsErrorJSON(w, http.StatusForbidden, "CREDENTIAL_FORBIDDEN", "gateway diagnostics outside authorized scope")
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrWaveformNotFound):
		writeDiagnosticsErrorJSON(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "gateway diagnostics resource not found")
	case errors.Is(err, ErrDiagnosticsOffline):
		writeDiagnosticsErrorJSON(w, http.StatusServiceUnavailable, "GATEWAY_DIAGNOSTICS_OFFLINE", err.Error())
	default:
		writeDiagnosticsErrorJSON(w, http.StatusInternalServerError, "WAVEFORM_DIAGNOSTICS_FAILED", "gateway diagnostics failed")
	}
}

func writeWaveformJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
