package optimization

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/intelligencemodel"
)

type memoryEvaluationStore struct {
	started        bool
	begun          []Evaluation
	completed      []Evaluation
	failed         []string
	recommendation PublishedRecommendation
	preparations   map[string]PreparationOutcome
	preparationErr error
}

func (store *memoryEvaluationStore) StartRun(context.Context, Request, time.Time) error {
	store.started = true
	return nil
}
func (store *memoryEvaluationStore) BeginPublication(_ context.Context, _ Request, _ Recommendation, evaluation Evaluation, _ time.Time) error {
	store.begun = append(store.begun, evaluation)
	return nil
}
func (store *memoryEvaluationStore) CompletePublication(_ context.Context, evaluation Evaluation, _ time.Time) error {
	store.completed = append(store.completed, evaluation)
	return nil
}
func (store *memoryEvaluationStore) FailRun(_ context.Context, _ Request, code string, _ time.Time) error {
	store.failed = append(store.failed, code)
	return nil
}
func (store *memoryEvaluationStore) ListStalePublications(context.Context, time.Time, int) ([]Evaluation, error) {
	return nil, nil
}
func (store *memoryEvaluationStore) LatestPreparation(_ context.Context, tenantID, siteID string) (PreparationOutcome, error) {
	if store.preparationErr != nil {
		return PreparationOutcome{}, store.preparationErr
	}
	if outcome, ok := store.preparations[tenantID+":"+siteID]; ok {
		return outcome, nil
	}
	return PreparationOutcome{}, ErrPreparationNotFound
}
func (store *memoryEvaluationStore) LatestRecommendation(context.Context, string, string) (PublishedRecommendation, error) {
	if store.recommendation.RunID == "" {
		return PublishedRecommendation{}, ErrOptimizationNotFound
	}
	return store.recommendation, nil
}

type captureEvaluationSink struct {
	evaluations []Evaluation
	err         error
}

func (sink *captureEvaluationSink) InsertEvaluation(_ context.Context, evaluation Evaluation) error {
	if sink.err != nil {
		return sink.err
	}
	sink.evaluations = append(sink.evaluations, evaluation)
	return nil
}
func (sink *captureEvaluationSink) HasEvaluation(_ context.Context, evaluationID string) (bool, error) {
	for _, evaluation := range sink.evaluations {
		if evaluation.EvaluationID == evaluationID {
			return true, nil
		}
	}
	return false, nil
}

func validRequest() Request {
	return Request{
		TenantID:               "01990000-3000-7000-8000-000000000001",
		SiteID:                 "01990000-5000-7000-8000-000000000001",
		SubjectType:            "SITE",
		SubjectID:              "01990000-5000-7000-8000-000000000001",
		OptimizationRunID:      "01990000-1950-7000-8000-000000000001",
		InputSnapshotID:        "01990000-1930-7000-8000-000000000001",
		InputChecksum:          strings.Repeat("a", 64),
		PolicyVersionID:        "01990000-1920-7000-8000-000000000001",
		TopologyVersionID:      "01990000-1300-7000-8000-000000000001",
		LoadForecastSnapshotID: "01990000-1890-7000-8000-000000000001",
		TariffVersionID:        "01990000-1420-7000-8000-000000000001",
		DeploymentRevisionID:   "01990000-1960-7000-8000-000000000001",
		Objective:              "COST",
		Horizon:                "DAY_AHEAD",
		HorizonMinutes:         1440,
		Granularity:            "15MIN",
		ValidFrom:              time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC),
		Baseline: HVACBaseline{
			DailyEnergyKWh: 2400, DailyCost: 360, SupplyTempC: 7, ZoneTempC: 23,
		},
		Constraints: HVACConstraints{
			SupplyTempMinC: 6, SupplyTempMaxC: 10, ZoneTempMinC: 21, ZoneTempMaxC: 25, MaxSupplyTempStep: 1,
		},
		ResponseModel: HVACResponseModel{
			DailyEnergyDeltaPerSupplyTempC: -180, ZoneTempDeltaPerSupplyTempC: 0.4,
			EnergyUncertaintyP90KWh: 60, ZoneTempUncertaintyP90C: 0.2,
		},
	}
}

func TestHVACRecommendationCarriesBenefitConstraintsRollbackAndVerification(t *testing.T) {
	request := validRequest()
	at := time.Date(2026, 8, 19, 16, 0, 0, 0, time.UTC)
	recommendation, err := (HVACRecommendationSolver{}).Recommend(t.Context(), request, at)
	if err != nil {
		t.Fatal(err)
	}
	if recommendation.Approval != intelligencemodel.RecommendationDraft || recommendation.CommandIntentID != "" {
		t.Fatalf("recommendation must remain non-executable draft: %#v", recommendation)
	}
	candidateSupply, ok := recommendation.Candidate["supplyTempC"].(float64)
	if !ok || candidateSupply != 8 {
		t.Fatalf("candidate=%#v", recommendation.Candidate)
	}
	if recommendation.ExpectedImpact["energySavingKWhPerDay"].(float64) <= 0 || recommendation.ExpectedImpact["costSavingPerDay"].(float64) <= 0 {
		t.Fatalf("expected impact=%#v", recommendation.ExpectedImpact)
	}
	if len(recommendation.Constraints) < 3 || len(recommendation.Uncertainty) == 0 || len(recommendation.Risk) == 0 || len(recommendation.RollbackPlan) == 0 || len(recommendation.VerificationPlan) == 0 {
		t.Fatalf("recommendation evidence is incomplete: %#v", recommendation)
	}
	if err := recommendation.CanCreateCommand(at.Add(time.Minute)); err == nil {
		t.Fatal("draft recommendation must never create a command")
	}
}

func TestHVACRecommendationHonorsComfortUncertainty(t *testing.T) {
	request := validRequest()
	request.Baseline.ZoneTempC = 24.5
	request.ResponseModel.ZoneTempUncertaintyP90C = 0.3
	recommendation, err := (HVACRecommendationSolver{}).Recommend(t.Context(), request, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if recommendation.Candidate["supplyTempC"].(float64) != 7 {
		t.Fatalf("solver selected a candidate whose P90 comfort bound should be rejected: %#v", recommendation.Candidate)
	}
}

func TestOptimizationRejectsInvalidFrozenModelInputs(t *testing.T) {
	for name, mutate := range map[string]func(*Request){
		"wrong subject":         func(request *Request) { request.SubjectID = "01990000-5000-7000-8000-000000000002" },
		"missing checksum":      func(request *Request) { request.InputChecksum = "" },
		"unsupported objective": func(request *Request) { request.Objective = "CARBON" },
		"wrong horizon":         func(request *Request) { request.HorizonMinutes = 720 },
		"invalid baseline":      func(request *Request) { request.Baseline.DailyEnergyKWh = 0 },
		"invalid coefficient":   func(request *Request) { request.ResponseModel.ZoneTempDeltaPerSupplyTempC = math.NaN() },
	} {
		t.Run(name, func(t *testing.T) {
			request := validRequest()
			mutate(&request)
			if err := request.Validate(); err == nil {
				t.Fatal("expected optimization request to fail")
			}
		})
	}
}

func TestOptimizationServicePublishesRecommendationNotDispatchPlan(t *testing.T) {
	store := &memoryEvaluationStore{}
	sink := &captureEvaluationSink{}
	at := time.Date(2026, 8, 19, 16, 0, 0, 0, time.UTC)
	service, err := NewDefaultService(store, sink, func() time.Time { return at })
	if err != nil {
		t.Fatal(err)
	}
	recommendation, err := service.Optimize(t.Context(), validRequest())
	if err != nil {
		t.Fatal(err)
	}
	if !store.started || len(store.begun) != 1 || len(store.completed) != 1 || len(sink.evaluations) != 1 {
		t.Fatalf("publication state started=%v begun=%d completed=%d evaluations=%d", store.started, len(store.begun), len(store.completed), len(sink.evaluations))
	}
	if store.completed[0].RecommendationID != recommendation.ID || store.completed[0].ConstraintCount != uint32(len(recommendation.Constraints)) {
		t.Fatalf("evaluation lineage=%#v recommendation=%#v", store.completed[0], recommendation)
	}
}

func TestOptimizationClickHouseFailureIsExplicit(t *testing.T) {
	store := &memoryEvaluationStore{}
	sink := &captureEvaluationSink{err: errors.New("clickhouse unavailable")}
	service, _ := NewDefaultService(store, sink, time.Now)
	if _, err := service.Optimize(t.Context(), validRequest()); err == nil {
		t.Fatal("expected evaluation persistence failure")
	}
	if len(store.failed) != 1 || store.failed[0] != "CLICKHOUSE_WRITE_FAILED" {
		t.Fatalf("failure evidence=%v", store.failed)
	}
}

type fakePreparationReadDB struct {
	visible          bool
	policyCount      int
	constraints      json.RawMessage
	activePointKeys  map[string]bool
	activeDeployment bool
	job              *scheduledPreparationJob
	checkedKeys      []string
}

func (db *fakePreparationReadDB) checkSiteVisible(context.Context, string, string) (bool, error) {
	return db.visible, nil
}

func (db *fakePreparationReadDB) readEffectiveHVACPolicy(context.Context, string, time.Time) (json.RawMessage, int, error) {
	return db.constraints, db.policyCount, nil
}

func (db *fakePreparationReadDB) hasAllActiveTelemetryPoints(_ context.Context, _, _ string, keys []string) (bool, error) {
	db.checkedKeys = append([]string(nil), keys...)
	if len(keys) == 0 {
		return false, nil
	}
	for _, key := range keys {
		if !db.activePointKeys[key] {
			return false, nil
		}
	}
	return true, nil
}

func (db *fakePreparationReadDB) hasActiveOptimizationDeployment(context.Context, string, string) (bool, error) {
	return db.activeDeployment, nil
}

func (db *fakePreparationReadDB) readLatestScheduledJob(context.Context, string, string) (scheduledPreparationJob, bool, error) {
	if db.job == nil {
		return scheduledPreparationJob{}, false, nil
	}
	return *db.job, true, nil
}

func TestServiceLatestPreparationForwardsOutcomeAndReadiness(t *testing.T) {
	tenantID := "01990000-3000-7000-8000-000000000001"
	siteUnscheduled := "01990000-5000-7000-8000-000000000001"
	siteFailed := "01990000-5000-7000-8000-000000000002"
	siteHidden := "01990000-5000-7000-8000-000000000099"
	trigger := TriggerSchedule
	reason := string(ReasonZoneTempMissing)
	fired := time.Date(2026, 11, 15, 10, 20, 0, 0, time.UTC)
	calibratedAt := "2026-11-12T08:00:00Z"

	store := &memoryEvaluationStore{
		preparations: map[string]PreparationOutcome{
			tenantID + ":" + siteUnscheduled: {
				Status:              PreparationStatusNotScheduled,
				ResponseCoefficient: PreparationResponseCoefficient{Status: ResponseCoefficientNotCalibrated},
				TenantStrategy:      PreparationTenantStrategy{Status: TenantStrategyPending},
			},
			tenantID + ":" + siteFailed: {
				Trigger:             &trigger,
				Status:              PreparationStatusFailed,
				Reason:              &reason,
				FiredAt:             &fired,
				ResponseCoefficient: PreparationResponseCoefficient{Status: ResponseCoefficientCalibrated, CalibratedAt: &calibratedAt},
				TenantStrategy:      PreparationTenantStrategy{Status: TenantStrategyConfirmed},
			},
		},
	}
	service, err := NewDefaultService(store, &captureEvaluationSink{}, time.Now)
	if err != nil {
		t.Fatal(err)
	}

	gotUnscheduled, err := service.LatestPreparation(t.Context(), tenantID, siteUnscheduled)
	if err != nil {
		t.Fatalf("unexpected error for visible unscheduled site: %v", err)
	}
	if gotUnscheduled.Status != PreparationStatusNotScheduled || gotUnscheduled.Trigger != nil || gotUnscheduled.FiredAt != nil ||
		gotUnscheduled.ResponseCoefficient.Status != ResponseCoefficientNotCalibrated || gotUnscheduled.ResponseCoefficient.CalibratedAt != nil ||
		gotUnscheduled.TenantStrategy.Status != TenantStrategyPending {
		t.Fatalf("unexpected unscheduled outcome: %#v", gotUnscheduled)
	}

	gotFailed, err := service.LatestPreparation(t.Context(), tenantID, siteFailed)
	if err != nil {
		t.Fatalf("unexpected error for failed scheduled site: %v", err)
	}
	if gotFailed.Status != PreparationStatusFailed || gotFailed.Reason == nil || *gotFailed.Reason != reason ||
		gotFailed.ResponseCoefficient.Status != ResponseCoefficientCalibrated || gotFailed.ResponseCoefficient.CalibratedAt == nil || *gotFailed.ResponseCoefficient.CalibratedAt != calibratedAt ||
		gotFailed.TenantStrategy.Status != TenantStrategyConfirmed {
		t.Fatalf("unexpected failed outcome: %#v", gotFailed)
	}

	if _, err := service.LatestPreparation(t.Context(), tenantID, siteHidden); !errors.Is(err, ErrPreparationNotFound) {
		t.Fatalf("expected ErrPreparationNotFound for invisible site, got %v", err)
	}
}

func TestResolveLatestPreparationTruthDerivation(t *testing.T) {
	tenantID := "018f3d00-0000-7000-8000-000000000001"
	siteID := "01a14000-1000-7000-8000-000000000001"
	otherSiteID := "01a14000-1000-7000-8000-000000000002"
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	fired := time.Date(2026, 11, 16, 9, 20, 0, 0, time.UTC)
	completed := fired.Add(3 * time.Second)
	reason := string(ReasonZoneTempMissing)

	handSetPolicy := json.RawMessage(`{
		"comfort":{"zoneTempMinC":22,"zoneTempMaxC":26},
		"safety":{"supplyTempMinC":5,"supplyTempMaxC":8,"maxSupplyTempStepC":1},
		"inputMapping":{"supplyTemperatureKey":"supply_water_temperature","zoneTemperatureKey":"room_temperature"},
		"maintenanceConstraints":{"outOfService":[]},
		"manualLocks":{"resources":[]},
		"responseModel":{"dailyEnergyDeltaKWhPerSupplyTempC":-60,"zoneTempDeltaCPerSupplyTempC":0.3,"energyUncertaintyP90KWh":20,"zoneTempUncertaintyP90C":0.2}
	}`)

	t.Run("invisible_or_cross_tenant_site_returns_not_found", func(t *testing.T) {
		db := &fakePreparationReadDB{visible: false, policyCount: 1, constraints: handSetPolicy}
		if _, err := resolveLatestPreparation(t.Context(), db, tenantID, siteID, now); !errors.Is(err, ErrPreparationNotFound) {
			t.Fatalf("err=%v, want ErrPreparationNotFound", err)
		}
	})

	t.Run("visible_unscheduled_songyaxuan_shape_returns_not_scheduled_and_reasons", func(t *testing.T) {
		db := &fakePreparationReadDB{
			visible:          true,
			policyCount:      1,
			constraints:      handSetPolicy,
			activePointKeys:  map[string]bool{"t18": true, "t53": true, "t54": true},
			activeDeployment: false,
		}
		got, err := resolveLatestPreparation(t.Context(), db, tenantID, siteID, now)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Status != PreparationStatusNotScheduled || got.Trigger != nil || got.Reason != nil || got.FiredAt != nil || got.CompletedAt != nil {
			t.Fatalf("unexpected schedule fields: %#v", got)
		}
		if got.ResponseCoefficient.Status != ResponseCoefficientNotCalibrated || got.ResponseCoefficient.CalibratedAt != nil {
			t.Fatalf("unexpected responseCoefficient: %#v", got.ResponseCoefficient)
		}
		if got.TenantStrategy.Status != TenantStrategyPending {
			t.Fatalf("unexpected tenantStrategy: %#v", got.TenantStrategy)
		}
	})

	t.Run("failed_scheduled_firing_includes_readiness_status", func(t *testing.T) {
		db := &fakePreparationReadDB{
			visible:          true,
			policyCount:      1,
			constraints:      handSetPolicy,
			activePointKeys:  map[string]bool{"supply_water_temperature": true, "room_temperature": true},
			activeDeployment: true,
			job: &scheduledPreparationJob{
				state:       "FAILED",
				errorCode:   &reason,
				firedAt:     fired,
				completedAt: &completed,
			},
		}
		got, err := resolveLatestPreparation(t.Context(), db, tenantID, siteID, now)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Trigger == nil || *got.Trigger != TriggerSchedule || got.Status != PreparationStatusFailed || got.Reason == nil || *got.Reason != reason {
			t.Fatalf("unexpected failed outcome: %#v", got)
		}
		if got.FiredAt == nil || !got.FiredAt.Equal(fired) || got.CompletedAt == nil || !got.CompletedAt.Equal(completed) {
			t.Fatalf("unexpected timestamps: %#v", got)
		}
		if got.ResponseCoefficient.Status != ResponseCoefficientNotCalibrated || got.ResponseCoefficient.CalibratedAt != nil {
			t.Fatalf("hand-set policy must remain NOT_CALIBRATED: %#v", got.ResponseCoefficient)
		}
		if got.TenantStrategy.Status != TenantStrategyConfirmed {
			t.Fatalf("all points and active deployment present must yield CONFIRMED: %#v", got.TenantStrategy)
		}
	})

	t.Run("tenant_strategy_requires_all_three_conditions", func(t *testing.T) {
		multiZonePolicy := json.RawMessage(`{
			"inputMapping":{"supplyTemperatureKey":"btu_meter.supply_water_temperature","zoneTemperatureKey":"zone.t53","zoneTemperatureKeys":["zone.t53","zone.t54"]},
			"responseModel":{"dailyEnergyDeltaKWhPerSupplyTempC":-60,"zoneTempDeltaCPerSupplyTempC":0.3,"energyUncertaintyP90KWh":20,"zoneTempUncertaintyP90C":0.2,"source":"SITE_DATA","calibratedAt":"2026-11-14T12:00:00Z"}
		}`)

		allOK := &fakePreparationReadDB{
			visible:          true,
			policyCount:      1,
			constraints:      multiZonePolicy,
			activePointKeys:  map[string]bool{"btu_meter.supply_water_temperature": true, "zone.t53": true, "zone.t54": true},
			activeDeployment: true,
		}
		got, err := resolveLatestPreparation(t.Context(), allOK, tenantID, siteID, now)
		if err != nil {
			t.Fatal(err)
		}
		if got.ResponseCoefficient.Status != ResponseCoefficientCalibrated || got.ResponseCoefficient.CalibratedAt == nil || *got.ResponseCoefficient.CalibratedAt != "2026-11-14T12:00:00Z" {
			t.Fatalf("expected CALIBRATED with timestamp, got %#v", got.ResponseCoefficient)
		}
		if got.TenantStrategy.Status != TenantStrategyConfirmed {
			t.Fatalf("expected CONFIRMED when all multi-zone keys and deployment match, got %#v", got.TenantStrategy)
		}
		if len(allOK.checkedKeys) != 3 {
			t.Fatalf("expected 3 deduplicated keys checked, got %v", allOK.checkedKeys)
		}

		// Missing one zone key -> PENDING.
		missingOneZone := &fakePreparationReadDB{
			visible:          true,
			policyCount:      1,
			constraints:      multiZonePolicy,
			activePointKeys:  map[string]bool{"btu_meter.supply_water_temperature": true, "zone.t53": true},
			activeDeployment: true,
		}
		if got, err := resolveLatestPreparation(t.Context(), missingOneZone, tenantID, siteID, now); err != nil || got.TenantStrategy.Status != TenantStrategyPending {
			t.Fatalf("missing zone point must yield PENDING, got %#v err=%v", got, err)
		}

		// Missing deployment binding -> PENDING.
		missingDeployment := &fakePreparationReadDB{
			visible:          true,
			policyCount:      1,
			constraints:      multiZonePolicy,
			activePointKeys:  map[string]bool{"btu_meter.supply_water_temperature": true, "zone.t53": true, "zone.t54": true},
			activeDeployment: false,
		}
		if got, err := resolveLatestPreparation(t.Context(), missingDeployment, tenantID, siteID, now); err != nil || got.TenantStrategy.Status != TenantStrategyPending {
			t.Fatalf("missing deployment must yield PENDING, got %#v err=%v", got, err)
		}

		// Ambiguous or missing policy (policyCount != 1) -> NOT_CALIBRATED and PENDING.
		for _, count := range []int{0, 2} {
			ambiguous := &fakePreparationReadDB{
				visible:          true,
				policyCount:      count,
				constraints:      multiZonePolicy,
				activePointKeys:  map[string]bool{"btu_meter.supply_water_temperature": true, "zone.t53": true, "zone.t54": true},
				activeDeployment: true,
			}
			got, err := resolveLatestPreparation(t.Context(), ambiguous, tenantID, siteID, now)
			if err != nil || got.ResponseCoefficient.Status != ResponseCoefficientNotCalibrated || got.ResponseCoefficient.CalibratedAt != nil || got.TenantStrategy.Status != TenantStrategyPending {
				t.Fatalf("policyCount=%d: got %#v err=%v", count, got, err)
			}
		}

		// Empty inputMapping -> PENDING.
		emptyMapping := &fakePreparationReadDB{
			visible:          true,
			policyCount:      1,
			constraints:      json.RawMessage(`{"inputMapping":{"supplyTemperatureKey":"","zoneTemperatureKey":"zone.t53"},"responseModel":{"source":"CALIBRATED"}}`),
			activePointKeys:  map[string]bool{"zone.t53": true},
			activeDeployment: true,
		}
		if got, err := resolveLatestPreparation(t.Context(), emptyMapping, tenantID, siteID, now); err != nil || got.TenantStrategy.Status != TenantStrategyPending {
			t.Fatalf("empty supplyTemperatureKey must yield PENDING, got %#v err=%v", got, err)
		}
	})

	t.Run("response_coefficient_calibration_variants", func(t *testing.T) {
		cases := []struct {
			name   string
			raw    string
			siteID string
			want   string
			wantAt string
		}{
			{
				name:   "manual_source_ignores_date",
				raw:    `{"responseModel":{"source":"MANUAL","calibratedAt":"2026-11-10"}}`,
				siteID: siteID,
				want:   ResponseCoefficientNotCalibrated,
			},
			{
				name:   "calibrated_source_without_date_has_nil_calibrated_at",
				raw:    `{"responseModel":{"source":"CALIBRATED","calibratedAt":"   "}}`,
				siteID: siteID,
				want:   ResponseCoefficientCalibrated,
			},
			{
				name:   "per_site_calibration_matches_target_site_only",
				raw:    `{"responseModel":{"source":"MANUAL"},"siteCalibrations":{"` + siteID + `":{"status":"CALIBRATED","calibratedAt":"2026-11-15"}}}`,
				siteID: siteID,
				want:   ResponseCoefficientCalibrated,
				wantAt: "2026-11-15",
			},
			{
				name:   "per_site_calibration_does_not_leak_to_other_site",
				raw:    `{"responseModel":{"source":"MANUAL"},"siteCalibrations":{"` + siteID + `":{"status":"CALIBRATED","calibratedAt":"2026-11-15"}}}`,
				siteID: otherSiteID,
				want:   ResponseCoefficientNotCalibrated,
			},
			{
				name:   "per_site_not_calibrated_overrides_policy_source",
				raw:    `{"responseModel":{"source":"SITE_DATA","calibratedAt":"2026-11-10"},"siteCalibrations":{"` + siteID + `":{"status":"NOT_CALIBRATED"}}}`,
				siteID: siteID,
				want:   ResponseCoefficientNotCalibrated,
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				var parsed preparationPolicyConstraints
				if err := json.Unmarshal([]byte(tc.raw), &parsed); err != nil {
					t.Fatal(err)
				}
				got := deriveResponseCoefficient(tc.siteID, parsed)
				if got.Status != tc.want {
					t.Fatalf("status=%q, want %q", got.Status, tc.want)
				}
				gotAt := ""
				if got.CalibratedAt != nil {
					gotAt = *got.CalibratedAt
				}
				if gotAt != tc.wantAt {
					t.Fatalf("calibratedAt=%q, want %q", gotAt, tc.wantAt)
				}
			})
		}
	})
}
