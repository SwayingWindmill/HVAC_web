package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/quanlaihe/hvac-web/cmd/energy-api/internal/platformapi"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/modules/command/pkg/commandservice"
)

func TestGatewaySingleAndBatchOfflineQueueTTLAndCancelRoutes(t *testing.T) {
	fixture := newCommandGatewayFixture(t)

	// 1. Single command with deliveryMode=QUEUE_IF_OFFLINE & ttlSeconds succeeds when device telemetry is OFFLINE.
	fixture.setFeedbackObservation(&commandmodel.LivePointObservation{
		EvaluationAvailability: "AVAILABLE",
		Presence:               "OFFLINE",
		Readiness:              "CURRENT",
		Freshness:              "STALE",
		Quality:                "GOOD",
		BusinessRevision:       0,
		ObservedAt:             fixture.now,
	})
	singleBody := `{"assetId":"` + fixture.assetID + `","commandPointId":"` + fixture.commandPointID + `","parameters":{"setpointC":23.5},"deliveryMode":"QUEUE_IF_OFFLINE","ttlSeconds":300}`
	singleReq := httptest.NewRequest(http.MethodPost, publicCommandsPath, strings.NewReader(singleBody))
	fixture.authenticate(singleReq, true)
	singleReq.Header.Set("Content-Type", "application/json")
	singleReq.Header.Set("Idempotency-Key", "offline-single-cmd-1")
	singleRec := httptest.NewRecorder()
	fixture.handler.ServeHTTP(singleRec, singleReq)
	if singleRec.Code != http.StatusAccepted {
		t.Fatalf("offline single command status=%d body=%s", singleRec.Code, singleRec.Body.String())
	}
	var singleView commandView
	if err := json.Unmarshal(singleRec.Body.Bytes(), &singleView); err != nil {
		t.Fatal(err)
	}
	if singleView.DeliveryMode != commandmodel.DeliveryModeQueueIfOffline ||
		singleView.TTLSeconds == nil || *singleView.TTLSeconds != 300 ||
		singleView.ExpiresAt == nil || singleView.Status != commandmodel.IntentQueued {
		t.Fatalf("unexpected queued offline command view: %+v", singleView)
	}

	// Restore device-A online observation and seed device-B (SLEEPING/OFFLINE) under the same Site for grouped batch.
	valA := 23.0
	fixture.setFeedbackObservation(&commandmodel.LivePointObservation{
		EvaluationAvailability: "AVAILABLE",
		Presence:               "ONLINE",
		Readiness:              "CURRENT",
		Freshness:              "FRESH",
		Quality:                "GOOD",
		BusinessRevision:       17,
		CurrentValue:           &valA,
		ReportedValue:          commandmodel.NumberScalar(23.0),
		ObservedAt:             fixture.now,
	})
	deviceB := "018f3e00-3000-7000-8000-000000000002"
	assetB := "018f3e00-6000-7000-8000-000000000002"
	cmdPtB := "018f3e00-7000-7000-8000-000000000002"
	fbPtB := "018f3e00-8000-7000-8000-000000000003"
	spProfile, _ := commandmodel.CapabilityProfileFor(commandmodel.CapabilitySetTemperatureSetpoint)
	model := fixture.control.Twin.Models[fixture.siteID]
	model.Assets = append(model.Assets, commandservice.AssetDTO{
		ID: assetB, TenantID: fixture.tenantID, SiteID: fixture.siteID, Code: "chiller-b", DisplayName: "Chiller B", AssetType: "CHILLER", Status: "ACTIVE",
	})
	model.Devices = append(model.Devices, commandservice.DeviceDTO{
		ID: deviceB, TenantID: fixture.tenantID, SiteID: fixture.siteID, Code: "ctrl-b", Status: "ACTIVE",
	})
	model.TelemetryPoints = append(model.TelemetryPoints,
		commandservice.TelemetryPointDTO{
			ID: fbPtB, TenantID: fixture.tenantID, SiteID: fixture.siteID, ReportingDeviceID: deviceB,
			PointCode: "zone_temperature", PointType: "TELEMETRY", ValueType: "NUMBER", Status: "ACTIVE",
		},
		commandservice.TelemetryPointDTO{
			ID: cmdPtB, TenantID: fixture.tenantID, SiteID: fixture.siteID, ReportingDeviceID: deviceB,
			PointCode: "zone_temperature_setpoint_b", PointType: "COMMAND", ValueType: "NUMBER", Writable: true, Status: "ACTIVE",
			SourceMetadata: map[string]any{"capability": string(commandmodel.CapabilitySetTemperatureSetpoint), "capabilityRevision": spProfile.Revision, "feedbackPointKey": "zone_temperature"},
		},
	)
	model.Relationships = append(model.Relationships, commandservice.AssetRelationshipDTO{
		ID: "rel-b", TenantID: fixture.tenantID, SiteID: fixture.siteID, FromType: "POINT", FromID: cmdPtB, ToType: "ASSET", ToID: assetB, Role: "CONTROLS", Status: "ACTIVE", ValidFrom: fixture.now.Format("2006-01-02T15:04:05Z"),
	})
	fixture.control.Twin.Models[fixture.siteID] = model
	fixture.control.Twin.BoundDevices[assetB] = []string{deviceB}
	fixture.control.Twin.Assets[assetB] = commandservice.AssetDTO{
		ID: assetB, TenantID: fixture.tenantID, SiteID: fixture.siteID, Code: "chiller-b", DisplayName: "Chiller B", AssetType: "CHILLER", Status: "ACTIVE",
	}
	fixture.control.Live.Set(deviceB, "zone_temperature", &commandmodel.LivePointObservation{
		EvaluationAvailability: "AVAILABLE",
		Presence:               "SLEEPING",
		Readiness:              "CURRENT",
		Freshness:              "STALE",
		Quality:                "GOOD",
		BusinessRevision:       0,
		ObservedAt:             fixture.now,
	}, "s2:sha256:"+strings.Repeat("3", 64))

	batchesPath := "/api/v1/sites/" + fixture.siteID + "/command-batches"

	// 2. POST without CSRF fails 403.
	noCSRF := httptest.NewRequest(http.MethodPost, batchesPath, strings.NewReader(`{}`))
	fixture.authenticate(noCSRF, false)
	noCSRF.Header.Set("Content-Type", "application/json")
	noCSRF.Header.Set("Idempotency-Key", "batch-csrf-check-1")
	noCSRFRec := httptest.NewRecorder()
	fixture.handler.ServeHTTP(noCSRFRec, noCSRF)
	if noCSRFRec.Code != http.StatusForbidden {
		t.Fatalf("POST command-batches without CSRF status=%d want 403", noCSRFRec.Code)
	}

	// 3. Submit grouped batch targeting online device-A and sleeping device-B with QUEUE_IF_OFFLINE.
	batchBody := `{"capability":"SET_TEMPERATURE_SETPOINT","parameters":{"setpointC":24.0},"deliveryMode":"QUEUE_IF_OFFLINE","ttlSeconds":600,"reason":"夏季冷站群控调温","targets":[{"deviceId":"` + fixture.deviceID + `","pointId":"` + fixture.commandPointID + `"},{"deviceId":"` + deviceB + `","pointId":"` + cmdPtB + `"}]}`
	postReq := httptest.NewRequest(http.MethodPost, batchesPath, strings.NewReader(batchBody))
	fixture.authenticate(postReq, true)
	postReq.Header.Set("Content-Type", "application/json")
	postReq.Header.Set("Idempotency-Key", "batch-group-set-1")
	postRec := httptest.NewRecorder()
	fixture.handler.ServeHTTP(postRec, postReq)
	if postRec.Code != http.StatusAccepted {
		t.Fatalf("POST command-batches status=%d body=%s", postRec.Code, postRec.Body.String())
	}
	var batchView commandservice.CommandBatchView
	if err := json.Unmarshal(postRec.Body.Bytes(), &batchView); err != nil {
		t.Fatal(err)
	}
	if batchView.AcceptedCount != 2 || batchView.RejectedCount != 0 || len(batchView.Items) != 2 ||
		batchView.DeliveryMode != commandmodel.DeliveryModeQueueIfOffline {
		t.Fatalf("unexpected created batch view: %+v", batchView)
	}
	wantLoc := batchesPath + "/" + batchView.ID
	if postRec.Header().Get("Location") != wantLoc {
		t.Fatalf("Location=%q want %q", postRec.Header().Get("Location"), wantLoc)
	}

	// 4. List & detail routes return the persisted batch for authorized site.
	listReq := httptest.NewRequest(http.MethodGet, batchesPath+"?limit=10", nil)
	fixture.authenticate(listReq, false)
	listRec := httptest.NewRecorder()
	fixture.handler.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("GET command-batches status=%d body=%s", listRec.Code, listRec.Body.String())
	}
	var listView commandservice.CommandBatchListView
	if err := json.Unmarshal(listRec.Body.Bytes(), &listView); err != nil {
		t.Fatal(err)
	}
	if len(listView.Items) != 1 || listView.Items[0].ID != batchView.ID {
		t.Fatalf("unexpected list view: %+v", listView)
	}

	getReq := httptest.NewRequest(http.MethodGet, wantLoc, nil)
	fixture.authenticate(getReq, false)
	getRec := httptest.NewRecorder()
	fixture.handler.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET command-batch detail status=%d body=%s", getRec.Code, getRec.Body.String())
	}

	// 5. High-risk START batch rejects QUEUE_IF_OFFLINE (400) and requires valid step-up TOTP for FAIL_FAST.
	fixture.setCommandPointCapability(commandmodel.CapabilityStart, "", nil)
	badOfflineBody := `{"capability":"START","deliveryMode":"QUEUE_IF_OFFLINE","ttlSeconds":300,"reason":"批量启机","targets":[{"deviceId":"` + fixture.deviceID + `","pointId":"` + fixture.commandPointID + `"}]}`
	badOfflineReq := httptest.NewRequest(http.MethodPost, batchesPath, strings.NewReader(badOfflineBody))
	fixture.authenticate(badOfflineReq, true)
	badOfflineReq.Header.Set("Content-Type", "application/json")
	badOfflineReq.Header.Set("Idempotency-Key", "batch-highrisk-bad-offline")
	badOfflineRec := httptest.NewRecorder()
	fixture.handler.ServeHTTP(badOfflineRec, badOfflineReq)
	var offlineProblem platformapi.ProblemDetails
	if badOfflineRec.Code != http.StatusBadRequest || json.NewDecoder(badOfflineRec.Body).Decode(&offlineProblem) != nil || offlineProblem.Code != "COMMAND_REQUEST_INVALID" {
		t.Fatalf("expected 400 COMMAND_REQUEST_INVALID for START with QUEUE_IF_OFFLINE, got %d body=%s", badOfflineRec.Code, badOfflineRec.Body.String())
	}

	highRiskBody := `{"capability":"START","deliveryMode":"FAIL_FAST","ttlSeconds":300,"reason":"批量启机","targets":[{"deviceId":"` + fixture.deviceID + `","pointId":"` + fixture.commandPointID + `"}]}`
	missingTotpReq := httptest.NewRequest(http.MethodPost, batchesPath, strings.NewReader(highRiskBody))
	fixture.authenticate(missingTotpReq, true)
	missingTotpReq.Header.Set("Content-Type", "application/json")
	missingTotpReq.Header.Set("Idempotency-Key", "batch-highrisk-nototp")
	missingTotpRec := httptest.NewRecorder()
	fixture.handler.ServeHTTP(missingTotpRec, missingTotpReq)
	var stepProblem platformapi.ProblemDetails
	if missingTotpRec.Code != http.StatusPreconditionRequired || json.NewDecoder(missingTotpRec.Body).Decode(&stepProblem) != nil || stepProblem.Code != "STEP_UP_REQUIRED" {
		t.Fatalf("expected 428 STEP_UP_REQUIRED for START batch without TOTP, got %d body=%s", missingTotpRec.Code, missingTotpRec.Body.String())
	}

	highRiskAuthedBody := `{"capability":"START","deliveryMode":"FAIL_FAST","ttlSeconds":300,"reason":"批量启机","targets":[{"deviceId":"` + fixture.deviceID + `","pointId":"` + fixture.commandPointID + `"}],"verificationCode":"123456"}`
	totpReq := httptest.NewRequest(http.MethodPost, batchesPath, strings.NewReader(highRiskAuthedBody))
	fixture.authenticate(totpReq, true)
	totpReq.Header.Set("Content-Type", "application/json")
	totpReq.Header.Set("Idempotency-Key", "batch-highrisk-totp-ok")
	totpRec := httptest.NewRecorder()
	fixture.handler.ServeHTTP(totpRec, totpReq)
	if totpRec.Code != http.StatusAccepted {
		t.Fatalf("expected 202 Accepted for START batch with valid TOTP, got %d body=%s", totpRec.Code, totpRec.Body.String())
	}

	// 6. Cancel batch cancels remaining queued commands and marks batch CANCELLED.
	cancelReq := httptest.NewRequest(http.MethodPost, wantLoc+"/cancel", strings.NewReader(`{"reason":"取消测试"}`))
	fixture.authenticate(cancelReq, true)
	cancelReq.Header.Set("Content-Type", "application/json")
	cancelRec := httptest.NewRecorder()
	fixture.handler.ServeHTTP(cancelRec, cancelReq)
	if cancelRec.Code != http.StatusOK {
		t.Fatalf("POST command-batch cancel status=%d body=%s", cancelRec.Code, cancelRec.Body.String())
	}
	var cancelResp commandservice.CommandBatchCancelResponse
	if err := json.Unmarshal(cancelRec.Body.Bytes(), &cancelResp); err != nil {
		t.Fatal(err)
	}
	if cancelResp.Batch.Status != string(commandmodel.IntentCancelled) || len(cancelResp.CancelledCommandIDs) != 2 {
		t.Fatalf("unexpected batch cancel response: %+v", cancelResp)
	}
}
