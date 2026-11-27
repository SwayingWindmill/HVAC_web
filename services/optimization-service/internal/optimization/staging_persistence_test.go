package optimization

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/planttwin"
)

func TestTwinOnlySiteWithoutPolicyDeploymentOrZoneTempPersistsStagingEvaluationIdempotently(t *testing.T) {
	const (
		tenantID   = "44444444-1000-7000-8000-000000000001"
		siteID     = "44444444-2000-7000-8000-000000000001"
		chillerAID = "44444444-3000-7000-8000-000000000001"
		chillerBID = "44444444-3000-7000-8000-000000000002"
		pumpAID    = "44444444-3000-7000-8000-000000000011"
		meterAID   = "44444444-4000-7000-8000-000000000001"
		meterBID   = "44444444-4000-7000-8000-000000000002"
		plcID      = "44444444-4000-7000-8000-000000000003"
		btuID      = "44444444-4000-7000-8000-000000000004"
		retiredID  = "44444444-2000-7000-8000-000000000099"
	)

	ratedCap := 572.0
	ratedChillerKW := 127.5
	ratedPumpKW := 18.5
	twin := planttwin.SiteTwin{
		Scope:           planttwin.SiteScope{TenantID: tenantID, SiteID: siteID},
		Timezone:        "Asia/Shanghai",
		SiteControlMode: planttwin.ControlModeRemoteAuto,
		Units: []planttwin.PlantUnit{
			{AssetID: chillerAID, AssetCode: "chiller-a", AssetName: "A 机组", AssetType: planttwin.AssetTypeChiller, RatedCapacityKW: &ratedCap, RatedPowerKW: &ratedChillerKW, LockMode: planttwin.LockModeUnlocked},
			{AssetID: chillerBID, AssetCode: "chiller-b", AssetName: "B 机组", AssetType: planttwin.AssetTypeChiller, RatedCapacityKW: &ratedCap, RatedPowerKW: &ratedChillerKW, LockMode: planttwin.LockModeUnlocked},
			{AssetID: pumpAID, AssetCode: "chwp-a", AssetName: "A 冷冻泵", AssetType: planttwin.AssetTypeChilledWaterPump, RatedPowerKW: &ratedPumpKW, LockMode: planttwin.LockModeUnlocked},
		},
		Devices: []planttwin.TwinDevice{
			{DeviceID: meterAID, DeviceCode: "amp-a", DeviceType: "POWER_METER", SourceKey: "amp-a"},
			{DeviceID: meterBID, DeviceCode: "amp-b", DeviceType: "POWER_METER", SourceKey: "amp-b"},
			{DeviceID: plcID, DeviceCode: "plant-plc", DeviceType: "CONTROLLER", SourceKey: "plant-plc"},
			{DeviceID: btuID, DeviceCode: "plant-btu", DeviceType: "BTU_METER", SourceKey: "plant-btu"},
		},
		PointBindings: []planttwin.PointBinding{
			{PointID: "pt-pa", DeviceID: meterAID, DeviceCode: "amp-a", DeviceSourceKey: "amp-a", PointCode: "p_kw", WireSourceKey: "p_kw", PointType: "TELEMETRY", Unit: "kW", SubjectType: planttwin.SubjectTypeAsset, AssetID: chillerAID, BindingRole: planttwin.BindingMeters, SemanticRole: planttwin.RoleActivePower},
			{PointID: "pt-pb", DeviceID: meterBID, DeviceCode: "amp-b", DeviceSourceKey: "amp-b", PointCode: "p_kw", WireSourceKey: "p_kw", PointType: "TELEMETRY", Unit: "kW", SubjectType: planttwin.SubjectTypeAsset, AssetID: chillerBID, BindingRole: planttwin.BindingMeters, SemanticRole: planttwin.RoleActivePower},
			{PointID: "pt-ra", DeviceID: plcID, DeviceCode: "plant-plc", DeviceSourceKey: "plant-plc", PointCode: "ch_a_run", WireSourceKey: "ch_a_run", PointType: "TELEMETRY", SubjectType: planttwin.SubjectTypeAsset, AssetID: chillerAID, BindingRole: planttwin.BindingDescribes, SemanticRole: planttwin.RoleRunState},
			{PointID: "pt-rb", DeviceID: plcID, DeviceCode: "plant-plc", DeviceSourceKey: "plant-plc", PointCode: "ch_b_run", WireSourceKey: "ch_b_run", PointType: "TELEMETRY", SubjectType: planttwin.SubjectTypeAsset, AssetID: chillerBID, BindingRole: planttwin.BindingDescribes, SemanticRole: planttwin.RoleRunState},
			{PointID: "pt-pump-a", DeviceID: plcID, DeviceCode: "plant-plc", DeviceSourceKey: "plant-plc", PointCode: "pump_a_run", WireSourceKey: "pump_a_run", PointType: "TELEMETRY", SubjectType: planttwin.SubjectTypeAsset, AssetID: pumpAID, BindingRole: planttwin.BindingDescribes, SemanticRole: planttwin.RoleRunState},
			{PointID: "pt-load", DeviceID: btuID, DeviceCode: "plant-btu", DeviceSourceKey: "plant-btu", PointCode: "chw_load", WireSourceKey: "chw_load", PointType: "TELEMETRY", Unit: "kW", SubjectType: planttwin.SubjectTypeSite, BindingRole: planttwin.BindingDescribes, SemanticRole: planttwin.RoleCoolingLoad},
			{PointID: "pt-sup", DeviceID: btuID, DeviceCode: "plant-btu", DeviceSourceKey: "plant-btu", PointCode: "hdr_sup_t", WireSourceKey: "hdr_sup_t", PointType: "TELEMETRY", Unit: "Cel", SubjectType: planttwin.SubjectTypeSite, BindingRole: planttwin.BindingDescribes, SemanticRole: planttwin.RoleSupplyWaterTemp},
		},
		HydraulicEdges: []planttwin.HydraulicEdge{
			{FromAssetID: pumpAID, ToAssetID: chillerAID, RelationshipRole: planttwin.RelationshipServes},
		},
	}
	loader, err := planttwin.NewStaticLoader(twin)
	if err != nil {
		t.Fatal(err)
	}

	evalAt := time.Date(2026, 11, 27, 5, 20, 0, 0, time.UTC)
	var siteListQueries atomic.Int32
	chServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		sql := string(body)
		w.Header().Set("Content-Type", "application/x-ndjson")
		switch {
		case strings.Contains(sql, "SELECT DISTINCT") && strings.Contains(sql, "FROM telemetry_history.observations"):
			siteListQueries.Add(1)
			rows := []string{
				fmt.Sprintf(`{"tenantId":%q,"siteId":%q}`, tenantID, siteID),
				fmt.Sprintf(`{"tenantId":%q,"siteId":%q}`, tenantID, retiredID),
			}
			_, _ = w.Write([]byte(strings.Join(rows, "\n") + "\n"))
		case strings.Contains(sql, "toStartOfFifteenMinutes"):
			// Return empty history; idle-auxiliary stop still evaluates cleanly without policy/deployment/room_temperature.
			return
		case strings.Contains(sql, "row_number() OVER"):
			tsStr := evalAt.Add(-30 * time.Second).Format(time.RFC3339Nano)
			latest := []string{
				fmt.Sprintf(`{"deviceId":%q,"sourceId":"plant-btu","telemetryKey":"chw_load","valueNumber":260,"valueString":null,"unit":"kW","quality":"GOOD","sampledAt":%q}`, btuID, tsStr),
				fmt.Sprintf(`{"deviceId":%q,"sourceId":"plant-btu","telemetryKey":"hdr_sup_t","valueNumber":7.0,"valueString":null,"unit":"Cel","quality":"GOOD","sampledAt":%q}`, btuID, tsStr),
				fmt.Sprintf(`{"deviceId":%q,"sourceId":"amp-a","telemetryKey":"p_kw","valueNumber":0.5,"valueString":null,"unit":"kW","quality":"GOOD","sampledAt":%q}`, meterAID, tsStr),
				fmt.Sprintf(`{"deviceId":%q,"sourceId":"amp-b","telemetryKey":"p_kw","valueNumber":58.0,"valueString":null,"unit":"kW","quality":"GOOD","sampledAt":%q}`, meterBID, tsStr),
				fmt.Sprintf(`{"deviceId":%q,"sourceId":"plant-plc","telemetryKey":"ch_a_run","valueNumber":null,"valueString":"STOPPED","unit":"","quality":"GOOD","sampledAt":%q}`, plcID, tsStr),
				fmt.Sprintf(`{"deviceId":%q,"sourceId":"plant-plc","telemetryKey":"ch_b_run","valueNumber":null,"valueString":"RUNNING","unit":"","quality":"GOOD","sampledAt":%q}`, plcID, tsStr),
				fmt.Sprintf(`{"deviceId":%q,"sourceId":"plant-plc","telemetryKey":"pump_a_run","valueNumber":null,"valueString":"RUNNING","unit":"","quality":"GOOD","sampledAt":%q}`, plcID, tsStr),
			}
			_, _ = w.Write([]byte(strings.Join(latest, "\n") + "\n"))
		default:
			var hist []string
			for _, mins := range []int{30, 20, 10, 2} {
				tsStr := evalAt.Add(-time.Duration(mins) * time.Minute).Format(time.RFC3339Nano)
				hist = append(hist,
					fmt.Sprintf(`{"deviceId":%q,"sourceId":"plant-plc","telemetryKey":"ch_a_run","valueNumber":null,"valueString":"STOPPED","unit":"","quality":"GOOD","sampledAt":%q}`, plcID, tsStr),
					fmt.Sprintf(`{"deviceId":%q,"sourceId":"plant-plc","telemetryKey":"ch_b_run","valueNumber":null,"valueString":"RUNNING","unit":"","quality":"GOOD","sampledAt":%q}`, plcID, tsStr),
					fmt.Sprintf(`{"deviceId":%q,"sourceId":"plant-btu","telemetryKey":"chw_load","valueNumber":260,"valueString":null,"unit":"kW","quality":"GOOD","sampledAt":%q}`, btuID, tsStr),
				)
			}
			_, _ = w.Write([]byte(strings.Join(hist, "\n") + "\n"))
		}
	}))
	defer chServer.Close()

	reader, err := NewClickHouseInputReader(InputReaderConfig{
		BaseURL: chServer.URL,
		Energy:  &energySeriesStub{},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Store has NO policies, NO deployments, NO scheduled preparations.
	store := &memoryEvaluationStore{}
	sink := &captureEvaluationSink{}
	now := evalAt
	service, err := NewDefaultService(store, sink, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	service.WithStaging(&StagingEvaluator{
		History:    reader,
		Snapshot:   reader,
		TwinLoader: loader,
		Policy:     DefaultStagingPolicy(),
	})

	// First sweep in hour 05:00Z persists 1 STAGING_ONLY evaluation and skips the retired site cleanly.
	summary1, err := service.EvaluateAndPersistAllSiteStaging(t.Context(), evalAt)
	if err != nil {
		t.Fatalf("EvaluateAndPersistAllSiteStaging failed: %v", err)
	}
	if siteListQueries.Load() != 1 || summary1.Sites != 2 || summary1.Persisted != 1 || summary1.Skipped != 1 || summary1.Deduplicated != 0 || summary1.Failed != 0 {
		t.Fatalf("unexpected first sweep summary: %+v", summary1)
	}
	if len(sink.evaluations) != 1 {
		t.Fatalf("expected 1 persisted evaluation, got %d", len(sink.evaluations))
	}

	evalRow := sink.evaluations[0]
	if evalRow.TenantID != tenantID || evalRow.SiteID != siteID ||
		evalRow.Quality != StagingEvaluationQuality ||
		evalRow.Objective != StagingEvaluationObjective ||
		evalRow.SolverOutcome != stagingOutcomeRecommended ||
		evalRow.PolicyVersionID != stagingZeroProvenanceUUID ||
		evalRow.TopologyVersionID != stagingZeroProvenanceUUID ||
		evalRow.LoadForecastSnapshotID != stagingZeroProvenanceUUID ||
		evalRow.TariffVersionID != stagingZeroProvenanceUUID ||
		evalRow.PVForecastSnapshotID != nil {
		t.Fatalf("unexpected persisted STAGING_ONLY evaluation metadata: %+v", evalRow)
	}
	if !uuidPattern.MatchString(evalRow.EvaluationID) || !uuidPattern.MatchString(evalRow.OptimizationRunID) ||
		!uuidPattern.MatchString(evalRow.RecommendationID) || !uuidPattern.MatchString(evalRow.InputSnapshotID) {
		t.Fatalf("persisted UUID fields must match UUID format: %+v", evalRow)
	}

	var payload StagingEvaluationPayload
	if err := json.Unmarshal([]byte(evalRow.EvaluationJSON), &payload); err != nil {
		t.Fatalf("decode EvaluationJSON: %v", err)
	}
	if payload.Mode != StagingEvaluationMode || payload.Reason != StagingEvaluationMode ||
		payload.ConfirmationMode != ConfirmationModeManual || !payload.ReadOnly ||
		payload.Solver != StagingEvaluationSolverName ||
		!payload.BucketAt.Equal(time.Date(2026, 11, 27, 5, 0, 0, 0, time.UTC)) {
		t.Fatalf("unexpected STAGING_ONLY payload envelope: %+v", payload)
	}
	if len(payload.Recommendations) != 1 || payload.Recommendations[0].Kind != RecommendationKindIdleAuxiliaryStop ||
		payload.Recommendations[0].ConfirmationMode != ConfirmationModeManual ||
		payload.Recommendations[0].TargetAssetID != pumpAID {
		t.Fatalf("expected IDLE_AUXILIARY_STOP MANUAL_CONFIRM recommendation, got %+v", payload.Recommendations)
	}
	if strings.Contains(evalRow.EvaluationJSON, "commandIntentId") || strings.Contains(evalRow.EvaluationJSON, "supplyTempC") {
		t.Fatalf("STAGING_ONLY evaluation must never emit command intents or control setpoints: %s", evalRow.EvaluationJSON)
	}

	// Second call inside the same hour bucket (05:45Z) must deduplicate without inserting another row.
	secondAt := evalAt.Add(25 * time.Minute)
	summary2, err := service.EvaluateAndPersistAllSiteStaging(t.Context(), secondAt)
	if err != nil {
		t.Fatalf("second sweep failed: %v", err)
	}
	if summary2.Sites != 2 || summary2.Persisted != 0 || summary2.Deduplicated != 1 || summary2.Skipped != 1 || len(sink.evaluations) != 1 {
		t.Fatalf("expected hourly deduplication on repeat call, got summary=%+v rows=%d", summary2, len(sink.evaluations))
	}

	// Next hour bucket (06:05Z) persists a new evaluation row with distinct deterministic IDs.
	nextHour := time.Date(2026, 11, 27, 6, 5, 0, 0, time.UTC)
	evalAt = nextHour
	summary3, err := service.EvaluateAndPersistAllSiteStaging(t.Context(), nextHour)
	if err != nil {
		t.Fatalf("next-hour sweep failed: %v", err)
	}
	if summary3.Persisted != 1 || summary3.Deduplicated != 0 || len(sink.evaluations) != 2 {
		t.Fatalf("expected second row in next hour bucket, got summary=%+v rows=%d", summary3, len(sink.evaluations))
	}
	if sink.evaluations[0].EvaluationID == sink.evaluations[1].EvaluationID ||
		sink.evaluations[0].OptimizationRunID == sink.evaluations[1].OptimizationRunID {
		t.Fatalf("distinct hours must produce distinct EvaluationID and OptimizationRunID")
	}
}

func TestClickHouseSinkPersistsAndDedupesStagingOnlyEvaluation(t *testing.T) {
	var insertCount atomic.Int32
	var hasChecks atomic.Int32
	var storedJSON string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		if strings.HasPrefix(q, "SELECT count()") {
			hasChecks.Add(1)
			if insertCount.Load() > 0 {
				_, _ = io.WriteString(w, "1\n")
				return
			}
			_, _ = io.WriteString(w, "0\n")
			return
		}
		if strings.HasPrefix(q, "INSERT INTO analytics.optimization_evaluations") {
			insertCount.Add(1)
			raw, _ := io.ReadAll(r.Body)
			storedJSON = string(raw)
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()

	sink, err := NewClickHouseSink(server.URL, "optimization_service_writer", "", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	const (
		tenantID   = "55555555-1000-7000-8000-000000000001"
		siteID     = "55555555-2000-7000-8000-000000000001"
		chillerAID = "55555555-3000-7000-8000-000000000001"
	)
	twin := planttwin.SiteTwin{
		Scope:           planttwin.SiteScope{TenantID: tenantID, SiteID: siteID},
		Timezone:        "Asia/Shanghai",
		SiteControlMode: planttwin.ControlModeRemoteAuto,
		Units: []planttwin.PlantUnit{
			{AssetID: chillerAID, AssetCode: "chiller-a", AssetName: "A 机组", AssetType: planttwin.AssetTypeChiller, LockMode: planttwin.LockModeUnlocked},
		},
	}
	loader, err := planttwin.NewStaticLoader(twin)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := NewClickHouseInputReader(InputReaderConfig{BaseURL: server.URL, Energy: &energySeriesStub{}})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewDefaultService(&memoryEvaluationStore{}, sink, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	service.WithStaging(&StagingEvaluator{
		History:    reader,
		Snapshot:   reader,
		TwinLoader: loader,
		Policy:     DefaultStagingPolicy(),
	})

	at := time.Date(2026, 11, 27, 7, 15, 0, 0, time.UTC)
	persisted, err := service.PersistStagingEvaluation(t.Context(), tenantID, siteID, at)
	if err != nil || !persisted {
		t.Fatalf("first PersistStagingEvaluation persisted=%t err=%v", persisted, err)
	}
	persistedAgain, err := service.PersistStagingEvaluation(t.Context(), tenantID, siteID, at.Add(15*time.Minute))
	if err != nil || persistedAgain {
		t.Fatalf("second PersistStagingEvaluation persisted=%t err=%v, want false", persistedAgain, err)
	}
	if hasChecks.Load() != 2 || insertCount.Load() != 1 {
		t.Fatalf("expected 2 HasEvaluation checks and 1 ClickHouse INSERT, got checks=%d inserts=%d", hasChecks.Load(), insertCount.Load())
	}
	if !strings.Contains(storedJSON, `"quality":"STAGING_ONLY"`) || !strings.Contains(storedJSON, `"objective":"STAGING"`) {
		t.Fatalf("unexpected stored JSONEachRow row: %s", storedJSON)
	}
}

func TestCompositeStagingSiteListerDeduplicatesAndHandlesErrors(t *testing.T) {
	siteA := StagingSiteScope{TenantID: "66666666-1000-7000-8000-000000000001", SiteID: "66666666-2000-7000-8000-000000000001"}
	siteB := StagingSiteScope{TenantID: "66666666-1000-7000-8000-000000000001", SiteID: "66666666-2000-7000-8000-000000000002"}

	lister1 := staticSiteLister{sites: []StagingSiteScope{siteB, siteA, {TenantID: "invalid", SiteID: "bad"}}}
	lister2 := staticSiteLister{sites: []StagingSiteScope{{TenantID: strings.ToUpper(siteA.TenantID), SiteID: strings.ToUpper(siteA.SiteID)}}}
	listerErr := staticSiteLister{err: errors.New("pg temporary timeout")}

	combined := NewCompositeStagingSiteLister(lister1, lister2, listerErr)
	got, err := combined.ListStagingSites(t.Context(), time.Now().UTC())
	if err != nil {
		t.Fatalf("expected composite lister to succeed when at least one source succeeds, got %v", err)
	}
	if len(got) != 2 || got[0] != siteA || got[1] != siteB {
		t.Fatalf("unexpected deduplicated sorted sites: %+v", got)
	}

	onlyFailing := NewCompositeStagingSiteLister(listerErr)
	if _, err := onlyFailing.ListStagingSites(t.Context(), time.Now().UTC()); err == nil {
		t.Fatal("expected error when all listers fail")
	}
}

type staticSiteLister struct {
	sites []StagingSiteScope
	err   error
}

func (s staticSiteLister) ListStagingSites(context.Context, time.Time) ([]StagingSiteScope, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.sites, nil
}
