package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/cmd/energy-api/internal/platformapi"
	"github.com/quanlaihe/hvac-web/cmd/energy-api/internal/s2telemetryapi"
	"github.com/quanlaihe/hvac-web/libs/alarmauth"
	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/ownershipregistry"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/libs/sessionstore"
	"github.com/quanlaihe/hvac-web/libs/telemetryauth"
	"github.com/quanlaihe/hvac-web/modules/alarm/pkg/alarmservice"
)

func TestListSiteDataGapsJoinsReadingGapAlarmsWithAssetModelAndSnapshotTime(t *testing.T) {
	const (
		tenantID   = "01a14000-0000-7000-8000-000000000001"
		siteID     = "01a14000-1000-7000-8000-000000000001"
		spaceID    = "01a14000-2000-7000-8000-000000000001"
		chillerDID = "01a14000-3000-7000-8000-000000000004"
		meter2ID   = "01a14000-4000-7000-8000-000000000004"
		energyPtID = "01a14000-5000-7000-8000-000000040002"
		powerPtID  = "01a14000-5000-7000-8000-000000040001"
		relID      = "01a14000-6400-7000-8000-000000000004"
	)

	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	stamp := "2026-11-01T00:00:00.000Z"
	unitKWh := "kWh"
	unitKW := "kW"
	resetMode := "RESET_TO_ZERO"

	alarmStore, err := alarmservice.NewMemoryStore(nil)
	if err != nil {
		t.Fatal(err)
	}

	// Publish Songyaxuan meter-uid2 active_energy_total & power_total reading-gap alarms + one unrelated alarm.
	energyGap, err := alarmStore.Publish(context.Background(), tenantID, siteID, alarmservice.Publication{
		AlarmType:       "reading-gap/meter-uid2/active_energy_total",
		DeviceID:        dataGapsStringPtr(meter2ID),
		PointID:         dataGapsStringPtr(energyPtID),
		SourceType:      alarmmodel.SourceSiteRule,
		SourceReference: "alarm-rules:songyaxuan/reading-gap/meter-uid2/active_energy_total",
		RuleRevision:    "rev-1",
		Title:           "D 机组电表：正向有功总电能读数中断",
		Summary:         "D 机组电表正向有功总电能已超过 15 分钟没有新读数。",
		Severity:        alarmmodel.SeverityMajor,
		OccurredAt:      "2026-11-08T08:28:00Z",
		Evidence:        []alarmmodel.EvidenceReference{{Kind: "telemetry-snapshot", Reference: "snap:1", CapturedAt: "2026-11-08T08:28:00Z"}},
		ActorType:       "WORKLOAD",
		ActorID:         "alarm-evaluator",
		CorrelationID:   "corr-gap-energy",
	})
	if err != nil {
		t.Fatal(err)
	}
	powerGap, err := alarmStore.Publish(context.Background(), tenantID, siteID, alarmservice.Publication{
		AlarmType:       "reading-gap/meter-uid2/power_total",
		DeviceID:        dataGapsStringPtr(meter2ID),
		PointID:         dataGapsStringPtr(powerPtID),
		SourceType:      alarmmodel.SourceSiteRule,
		SourceReference: "alarm-rules:songyaxuan/reading-gap/meter-uid2/power_total",
		RuleRevision:    "rev-1",
		Title:           "D 机组电表：总有功功率读数中断",
		Summary:         "D 机组电表总有功功率已超过 15 分钟没有新读数。",
		Severity:        alarmmodel.SeverityMajor,
		OccurredAt:      "2026-11-08T08:28:00Z",
		Evidence:        []alarmmodel.EvidenceReference{{Kind: "telemetry-snapshot", Reference: "snap:2", CapturedAt: "2026-11-08T08:28:00Z"}},
		ActorType:       "WORKLOAD",
		ActorID:         "alarm-evaluator",
		CorrelationID:   "corr-gap-power",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := alarmStore.Publish(context.Background(), tenantID, siteID, alarmservice.Publication{
		AlarmType:       "readings-not-updated/meter-uid2",
		DeviceID:        dataGapsStringPtr(meter2ID),
		SourceType:      alarmmodel.SourceSiteRule,
		SourceReference: "alarm-rules:songyaxuan/readings-not-updated/meter-uid2",
		RuleRevision:    "rev-1",
		Title:           "D 机组电表读数长时间没有更新",
		Summary:         "非关键点位长时间没有更新。",
		Severity:        alarmmodel.SeverityWarning,
		OccurredAt:      "2026-11-01T00:30:00Z",
		Evidence:        []alarmmodel.EvidenceReference{{Kind: "telemetry-snapshot", Reference: "snap:3", CapturedAt: "2026-11-01T00:30:00Z"}},
		ActorType:       "WORKLOAD",
		ActorID:         "alarm-evaluator",
		CorrelationID:   "corr-non-critical",
	}); err != nil {
		t.Fatal(err)
	}

	assetModel := platformapi.SiteAssetModel{
		SchemaVersion: 2,
		TenantID:      tenantID,
		SiteID:        siteID,
		Spaces: []platformapi.Space{{
			ID: spaceID, TenantID: tenantID, SiteID: siteID, Code: "plant-room",
			DisplayName: "空调机房", SpaceType: "PLANT_ROOM", Status: "ACTIVE", Revision: 1, CreatedAt: stamp, UpdatedAt: stamp,
		}},
		Assets: []platformapi.Asset{{
			ID: chillerDID, TenantID: tenantID, SiteID: siteID, Code: "chiller-d",
			DisplayName: "D 机组", AssetType: "CHILLER", Status: "ACTIVE", Revision: 1, CreatedAt: stamp, UpdatedAt: stamp,
		}},
		Devices: []platformapi.Device{{
			ID: meter2ID, TenantID: tenantID, SiteID: siteID, Code: "meter-uid2",
			DisplayName: "D 机组电表（2 号，UID 2）", DeviceType: "HVAC_POWER_METER", Status: "ACTIVE", Revision: 1, CreatedAt: stamp, UpdatedAt: stamp,
		}},
		Sensors: []platformapi.Sensor{},
		TelemetryPoints: []platformapi.TelemetryPoint{
			{
				ID: energyPtID, TenantID: tenantID, SiteID: siteID, ReportingDeviceID: meter2ID,
				PointCode: "active_energy_total", SourceKey: "active_energy_total", DisplayName: "正向有功总电能",
				PointType: "COUNTER", ValueType: "NUMBER", Unit: &unitKWh, SampleIntervalMS: 300000, PublishIntervalMS: 300000, StaleAfterMS: 900000,
				CounterDecreaseMode: &resetMode, SourceMetadata: map[string]any{"protocol": "MODBUS_RTU"}, Status: "ACTIVE", Revision: 1, CreatedAt: stamp, UpdatedAt: stamp,
			},
			{
				ID: powerPtID, TenantID: tenantID, SiteID: siteID, ReportingDeviceID: meter2ID,
				PointCode: "power_total", SourceKey: "power_total", DisplayName: "总有功功率",
				PointType: "TELEMETRY", ValueType: "NUMBER", Unit: &unitKW, SampleIntervalMS: 300000, PublishIntervalMS: 300000, StaleAfterMS: 900000,
				SourceMetadata: map[string]any{"protocol": "MODBUS_RTU"}, Status: "ACTIVE", Revision: 1, CreatedAt: stamp, UpdatedAt: stamp,
			},
		},
		Relationships: []platformapi.AssetRelationship{{
			ID: relID, TenantID: tenantID, SiteID: siteID, FromType: "DEVICE", FromID: meter2ID, ToType: "ASSET", ToID: chillerDID,
			Role: "METER", Status: "ACTIVE", ValidFrom: stamp, Revision: 1, CreatedAt: stamp, UpdatedAt: stamp,
		}},
		Counts: platformapi.AssetModelCounts{Spaces: 1, Assets: 1, DeviceEndpoints: 1, PhysicalSensors: 0, Points: 2},
	}

	h, sessionID := newDataGapsTestHandler(t, now, tenantID, siteID, alarmStore, assetModel, siteTotalElectricitySources{
		SiteID:      siteID,
		Synthesized: true,
		SubMeters: []siteTotalSubMeterSource{
			{PointID: energyPtID, DeviceID: meter2ID, MeterName: "D 机组电表（2 号，UID 2）", Ordinal: 1},
		},
	}, "2026-11-08T08:13:00Z")

	req := httptest.NewRequest(http.MethodGet, "https://web.example.test/api/v1/sites/"+siteID+"/data-gaps", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionID})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", rec.Code, rec.Body.String())
	}

	var got platformapi.SiteDataGapList
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(got.Items) != 2 {
		t.Fatalf("items = %#v; want 2 critical-point gaps", got.Items)
	}

	first := got.Items[0]
	if first.AlarmID != energyGap.AlarmID ||
		first.DeviceID != meter2ID ||
		first.DeviceName != "D 机组电表" ||
		first.PointID != energyPtID ||
		first.PointCode != "active_energy_total" ||
		first.PointName != "正向有功总电能" ||
		first.Since != "2026-11-08T08:13:00Z" ||
		!first.ExcludesDeviceFromTotal ||
		!slices.Equal(first.AffectedFigures, []string{"空调用电", "综合能效", "估算电费"}) {
		t.Fatalf("first item = %#v", first)
	}

	second := got.Items[1]
	if second.AlarmID != powerGap.AlarmID ||
		second.PointCode != "power_total" ||
		second.DeviceName != "D 机组电表" ||
		second.Since != "2026-11-08T08:13:00Z" ||
		second.ExcludesDeviceFromTotal ||
		!slices.Equal(second.AffectedFigures, []string{"设备实时功率", "用电负荷"}) {
		t.Fatalf("second item = %#v", second)
	}
}

func TestListSiteDataGapsEmptyAndHiddenSite(t *testing.T) {
	const (
		tenantID     = "01a14000-0000-7000-8000-000000000001"
		siteID       = "01a14000-1000-7000-8000-000000000001"
		hiddenSiteID = "01a14000-1000-7000-8000-000000000099"
	)
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	alarmStore, err := alarmservice.NewMemoryStore(nil)
	if err != nil {
		t.Fatal(err)
	}
	assetModel := platformapi.SiteAssetModel{
		SchemaVersion: 2, TenantID: tenantID, SiteID: siteID,
		Spaces: []platformapi.Space{}, Assets: []platformapi.Asset{}, Devices: []platformapi.Device{},
		Sensors: []platformapi.Sensor{}, TelemetryPoints: []platformapi.TelemetryPoint{}, Relationships: []platformapi.AssetRelationship{},
	}
	h, sessionID := newDataGapsTestHandler(t, now, tenantID, siteID, alarmStore, assetModel, siteTotalElectricitySources{SiteID: siteID}, "")

	// Empty active reading-gap alarms -> 200 {"items": []}
	okReq := httptest.NewRequest(http.MethodGet, "https://web.example.test/api/v1/sites/"+siteID+"/data-gaps", nil)
	okReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionID})
	okRec := httptest.NewRecorder()
	h.ServeHTTP(okRec, okReq)
	if okRec.Code != http.StatusOK {
		t.Fatalf("empty status = %d; body = %s", okRec.Code, okRec.Body.String())
	}
	var list platformapi.SiteDataGapList
	if err := json.Unmarshal(okRec.Body.Bytes(), &list); err != nil || len(list.Items) != 0 {
		t.Fatalf("empty list = %#v err = %v", list, err)
	}

	// Hidden site -> 404 Problem Details RESOURCE_NOT_FOUND
	hiddenReq := httptest.NewRequest(http.MethodGet, "https://web.example.test/api/v1/sites/"+hiddenSiteID+"/data-gaps", nil)
	hiddenReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionID})
	hiddenRec := httptest.NewRecorder()
	h.ServeHTTP(hiddenRec, hiddenReq)
	if hiddenRec.Code != http.StatusNotFound {
		t.Fatalf("hidden site status = %d; body = %s", hiddenRec.Code, hiddenRec.Body.String())
	}
	var problem platformapi.ProblemDetails
	if err := json.Unmarshal(hiddenRec.Body.Bytes(), &problem); err != nil || problem.Code != "RESOURCE_NOT_FOUND" {
		t.Fatalf("hidden site problem = %#v err = %v", problem, err)
	}
}

func TestListSiteDataGapsFailsClosedWhenTotalElectricitySourcesFails(t *testing.T) {
	const (
		tenantID = "01a14000-0000-7000-8000-000000000001"
		siteID   = "01a14000-1000-7000-8000-000000000001"
		meter2ID = "01a14000-4000-7000-8000-000000000004"
		energyPt = "01a14000-5000-7000-8000-000000040002"
	)
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	stamp := "2026-11-01T00:00:00.000Z"
	unitKWh := "kWh"
	alarmStore, err := alarmservice.NewMemoryStore(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := alarmStore.Publish(context.Background(), tenantID, siteID, alarmservice.Publication{
		AlarmType:       "reading-gap/meter-uid2/active_energy_total",
		DeviceID:        dataGapsStringPtr(meter2ID),
		PointID:         dataGapsStringPtr(energyPt),
		SourceType:      alarmmodel.SourceSiteRule,
		SourceReference: "alarm-rules:songyaxuan/reading-gap/meter-uid2/active_energy_total",
		RuleRevision:    "rev-1",
		Title:           "D 机组电表：正向有功总电能读数中断",
		Summary:         "读数中断。",
		Severity:        alarmmodel.SeverityMajor,
		OccurredAt:      "2026-11-08T08:28:00Z",
		Evidence:        []alarmmodel.EvidenceReference{{Kind: "telemetry-snapshot", Reference: "snap:1", CapturedAt: "2026-11-08T08:28:00Z"}},
		ActorType:       "WORKLOAD",
		ActorID:         "alarm-evaluator",
		CorrelationID:   "corr-gap-energy",
	}); err != nil {
		t.Fatal(err)
	}
	assetModel := platformapi.SiteAssetModel{
		SchemaVersion: 2, TenantID: tenantID, SiteID: siteID,
		Spaces: []platformapi.Space{}, Assets: []platformapi.Asset{}, Sensors: []platformapi.Sensor{}, Relationships: []platformapi.AssetRelationship{},
		Devices: []platformapi.Device{{
			ID: meter2ID, TenantID: tenantID, SiteID: siteID, Code: "meter-uid2",
			DisplayName: "D 机组电表", DeviceType: "HVAC_POWER_METER", Status: "ACTIVE", Revision: 1, CreatedAt: stamp, UpdatedAt: stamp,
		}},
		TelemetryPoints: []platformapi.TelemetryPoint{{
			ID: energyPt, TenantID: tenantID, SiteID: siteID, ReportingDeviceID: meter2ID,
			PointCode: "active_energy_total", SourceKey: "active_energy_total", DisplayName: "正向有功总电能",
			PointType: "COUNTER", ValueType: "NUMBER", Unit: &unitKWh, SampleIntervalMS: 300000, PublishIntervalMS: 300000, StaleAfterMS: 900000,
			SourceMetadata: map[string]any{"protocol": "MODBUS_RTU"}, Status: "ACTIVE", Revision: 1, CreatedAt: stamp, UpdatedAt: stamp,
		}},
	}
	h, sessionID := newDataGapsTestHandler(t, now, tenantID, siteID, alarmStore, assetModel, siteTotalElectricitySources{SiteID: "FORCE_503"}, "")
	req := httptest.NewRequest(http.MethodGet, "https://web.example.test/api/v1/sites/"+siteID+"/data-gaps", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionID})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d; body = %s", rec.Code, rec.Body.String())
	}
}

func newDataGapsTestHandler(
	t *testing.T,
	now time.Time,
	tenantID, siteID string,
	alarmStore alarmservice.Store,
	assetModel platformapi.SiteAssetModel,
	totalSources siteTotalElectricitySources,
	snapshotSampledAt string,
) (http.Handler, string) {
	t.Helper()
	signer := commandTestSigner(t)
	owner := signedAlarmOwner{t: t, signer: &signer.PublicKey, store: newDirectAlarmAdapter(alarmStore)}
	ownership, err := ownershipregistry.Parse(readRepositoryFile(t, "contracts", "ownership", "route-ownership.v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	sessions := sessionstore.NewMemoryStore()

	iamClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		parent, err := identitycontext.VerifyDelegation(&signer.PublicKey, request.Header.Get("X-Delegation-Grant"))
		if err != nil {
			t.Fatalf("invalid Gateway delegation: %v", err)
		}
		switch request.URL.Path {
		case "/internal/v1/registry/decision":
			var input registryauth.DecisionRequest
			if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
				t.Fatal(err)
			}
			decision := registryauth.Decision{
				Allowed: true, PrincipalID: "01a14000-9000-7000-8000-000000000001",
				SubjectIssuer: parent.SubjectIssuer, Subject: parent.Subject, TenantID: tenantID,
				AllowedSiteIDs: []string{siteID}, Actions: []registryauth.Action{input.Action},
				PolicyRevision: "registry-read:1/iam:1", ReasonCode: registryauth.ReasonAllowSiteRole,
				DecidedAt: now.Format(time.RFC3339Nano),
			}
			claims := registryauth.GrantClaims{
				Issuer: "spiffe://hvac.local/iam-service", Presenter: "spiffe://hvac.local/platform-gateway",
				Audience: "platform-core-service", PrincipalID: decision.PrincipalID,
				SubjectIssuer: parent.SubjectIssuer, Subject: parent.Subject, TenantID: tenantID,
				AllowedSiteIDs: []string{siteID}, Actions: []registryauth.Action{input.Action},
				PolicyRevision: decision.PolicyRevision, DecisionReason: registryauth.ReasonAllowSiteRole,
				SessionID: parent.SessionID, ParentTokenID: parent.TokenID,
				IssuedAt: now.Unix(), ExpiresAt: now.Add(30 * time.Second).Unix(), TokenID: randomURLToken(16),
			}
			return telemetryJSONResponse(http.StatusOK, registryauth.DecisionResponse{
				Decision: decision, DelegationGrant: commandUnsignedRegistryGrant(claims),
			}), nil
		case alarmDecisionPath:
			var input alarmauth.DecisionRequest
			if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
				t.Fatal(err)
			}
			return telemetryJSONResponse(http.StatusOK, alarmauth.DecisionResponse{Decision: alarmauth.Decision{
				Allowed: input.SiteID == siteID, PrincipalID: "01a14000-9000-7000-8000-000000000001",
				SubjectIssuer: parent.SubjectIssuer, Subject: parent.Subject, TenantID: input.TenantID,
				SiteID: input.SiteID, AlarmID: input.AlarmID, Action: input.Action,
				PolicyRevision: "alarm-policy-1", ReasonCode: alarmauth.ReasonAllowExactScope,
				DecidedAt: now.Format(time.RFC3339Nano),
			}}), nil
		case telemetryDecisionPath:
			var input telemetryauth.DecisionRequest
			if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
				t.Fatal(err)
			}
			canonical, err := telemetryauth.CanonicalTargets(input.Targets)
			if err != nil {
				t.Fatal(err)
			}
			scopeDigest, _ := telemetryauth.ScopeDigest(input.Action, input.TenantID, canonical)
			authorized := make([]telemetryauth.AuthorizedTarget, 0, len(canonical))
			keyCount := 0
			for _, target := range canonical {
				keyCount += len(target.Keys)
				authorized = append(authorized, telemetryauth.AuthorizedTarget{
					TenantID: tenantID, SiteID: siteID, DeviceID: target.DeviceID, Keys: target.Keys,
				})
			}
			claims := telemetryauth.GrantClaims{
				Issuer: "spiffe://hvac.local/iam-service", Presenter: "spiffe://hvac.local/platform-gateway",
				Audience: "telemetry-runtime-service", PrincipalID: "01a14000-9000-7000-8000-000000000001",
				SubjectIssuer: parent.SubjectIssuer, Subject: parent.Subject, TenantID: tenantID,
				ActorChain: []telemetryauth.Actor{{Service: "platform-gateway", SPIFFEID: "spiffe://hvac.local/platform-gateway"}},
				Action: input.Action, ScopeDigest: scopeDigest, TargetCount: len(canonical), KeyCount: keyCount,
				PolicyRevision: "telemetry:1", SessionID: parent.SessionID, ParentTokenID: parent.TokenID,
				RequestID: request.Header.Get("X-Request-ID"), TraceID: traceIDFromTraceparent(request.Header.Get("Traceparent")),
				Route: telemetryPublicRoute(input.Action),
				IssuedAt: now.Unix(), ExpiresAt: now.Add(30 * time.Second).Unix(), TokenID: randomURLToken(16),
			}
			return telemetryJSONResponse(http.StatusOK, telemetryauth.DecisionResponse{
				Decision: telemetryauth.Decision{
					Allowed: true, PrincipalID: claims.PrincipalID, SubjectIssuer: parent.SubjectIssuer,
					Subject: parent.Subject, TenantID: tenantID, Action: input.Action,
					ScopeDigest: scopeDigest, Targets: authorized,
					PolicyRevision: claims.PolicyRevision, ReasonCode: telemetryauth.ReasonAllowExactScope,
					DecidedAt: now.Format(time.RFC3339Nano),
				},
				DelegationGrant: unsignedTelemetryGrant(claims),
			}), nil
		default:
			t.Fatalf("unexpected IAM path %s", request.URL.Path)
			return nil, nil
		}
	})}

	registryClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/internal/v1/registry/sites/" + siteID + "/asset-model":
			return telemetryJSONResponse(http.StatusOK, assetModel), nil
		case "/internal/v1/registry/sites/" + siteID + "/total-electricity-sources":
			if totalSources.SiteID == "FORCE_503" {
				return telemetryJSONResponse(http.StatusServiceUnavailable, map[string]any{
					"code": "REGISTRY_UNAVAILABLE", "title": "Registry unavailable", "detail": "down", "retryable": true,
				}), nil
			}
			return telemetryJSONResponse(http.StatusOK, totalSources), nil
		default:
			return telemetryJSONResponse(http.StatusNotFound, map[string]any{"code": "RESOURCE_NOT_FOUND"}), nil
		}
	})}

	var telemetryCfg *TelemetryConfig
	if snapshotSampledAt != "" {
		telemetryCfg = &TelemetryConfig{
			RuntimeBaseURL:  "https://telemetry.example.test",
			RuntimeAudience: "telemetry-runtime-service",
			Timeout:         time.Second,
			RuntimeHTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				var batchReq s2telemetryapi.BatchGetObservationSnapshotsRequest
				if err := json.NewDecoder(request.Body).Decode(&batchReq); err != nil {
					t.Fatal(err)
				}
				items := make([]s2telemetryapi.BatchObservationResult, 0, len(batchReq.Requests))
				for _, target := range batchReq.Requests {
					values := make([]s2telemetryapi.TelemetryKeyState, 0, len(target.Keys))
					for _, key := range target.Keys {
						values = append(values, s2telemetryapi.TelemetryKeyState{
							Present: &s2telemetryapi.TelemetryPresentState{
								Key:            key,
								State:          "PRESENT",
								Value:          json.RawMessage(`12345.6`),
								ValueType:      "NUMBER",
								SampledAt:      s2telemetryapi.Instant(snapshotSampledAt),
								ReceivedAt:     s2telemetryapi.Instant(snapshotSampledAt),
								Freshness:      "STALE",
								Quality:        s2telemetryapi.TelemetryQualityStale,
								QualityReasons: []s2telemetryapi.QualityReasonCode{},
								PolicyRevision: 1,
							},
						})
					}
					items = append(items, s2telemetryapi.BatchObservationResult{
						Success: &s2telemetryapi.BatchObservationSuccess{
							RequestId: target.RequestId,
							DeviceId:  target.DeviceId,
							Status:    "OK",
							Snapshot: s2telemetryapi.DeviceObservationSnapshot{
								SchemaVersion:          1,
								DeviceId:               target.DeviceId,
								TenantId:               s2telemetryapi.UUIDv7(tenantID),
								SiteId:                 s2telemetryapi.UUIDv7(siteID),
								BusinessRevision:       1,
								EvaluatedAt:            s2telemetryapi.Instant(now.Format(time.RFC3339)),
								EvaluationAvailability: s2telemetryapi.EvaluationAvailabilityAvailable,
								AvailabilityReasons:    []s2telemetryapi.AvailabilityReasonCode{},
								Presence:               s2telemetryapi.PresenceSnapshot{Applicability: s2telemetryapi.PresenceApplicabilityApplicable},
								TelemetryReadiness:     s2telemetryapi.TelemetryReadinessDegraded,
								Values:                 values,
							},
						},
					})
				}
				return telemetryJSONResponse(http.StatusOK, s2telemetryapi.BatchGetObservationSnapshotsResponse{
					SchemaVersion: 1,
					Items:         items,
				}), nil
			})},
		}
	}

	configured := NewHandler(Config{
		Now:          func() time.Time { return now },
		RouteManager: ownershipregistry.NewManager(ownership, ownershipregistry.NewMemoryAuditSink(), func() time.Time { return now }),
		Identity: &IdentityConfig{
			OIDCIssuer: "https://issuer.example.test", OIDCClientID: "client",
			OIDCRedirectURI: "https://web.example.test/api/v1/auth/callback",
			PublicOrigin: "https://web.example.test", IAMURL: "https://iam.example.test", IAMAudience: "iam-service",
			ExecutingWorkloadSPIFFE: "spiffe://hvac.local/platform-gateway", PolicyRevision: "identity-policy-1",
			DelegationSigner: signer, TokenEncryptionKey: make([]byte, 32), SessionStore: sessions,
			SessionTTL: time.Hour, DelegationTTL: 30 * time.Second, IAMHTTPClient: iamClient,
		},
		Registry: &RegistryConfig{
			CoreBaseURL: "https://registry.example.test", CoreHTTPClient: registryClient, CoreTimeout: time.Second,
		},
		Alarm:     &AlarmConfig{Operations: owner, BackendAudience: "alarm-service", Timeout: time.Second},
		Telemetry: telemetryCfg,
	}).(*handler)

	ciphertext, err := configured.identity.encryptBytes([]byte("data-gaps-csrf"))
	if err != nil {
		t.Fatal(err)
	}
	created, err := sessions.CreateSession(context.Background(), sessionstore.Session{
		Client: sessionstore.ClientBrowser, ID: "data-gaps-session",
		Principal: identitycontext.UserPrincipal{Subject: "viewer-1", Issuer: "https://issuer.example.test", Roles: []string{"viewer"}},
		TenantID:  tenantID, CSRFTokenCiphertext: ciphertext, ExpiresAt: now.Add(time.Hour),
	}, sessionstore.MutationContext{
		Action: "SESSION_CREATED", Result: "SUCCEEDED", PolicyRevision: "identity-policy-1", CorrelationID: "data-gaps",
		TraceID: strings.Repeat("a", 32), Traceparent: "00-" + strings.Repeat("a", 32) + "-" + strings.Repeat("b", 16) + "-01",
		ExecutingService: "platform-gateway", ExecutingSPIFFEID: "spiffe://hvac.local/platform-gateway", OccurredAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return configured, created.ID
}

func dataGapsStringPtr(value string) *string {
	return &value
}

