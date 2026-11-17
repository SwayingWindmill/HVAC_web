package telemetry

import (
	"errors"
	"math"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestRegistryPointSyncQualityValidation(t *testing.T) {
	validMin := 0.0
	validMax := 100.0
	badMin := 120.0
	nanVal := math.NaN()

	baseRequest := func(pt RegistryPointSync) RegistrySyncRequest {
		return RegistrySyncRequest{
			TenantID: "018f4a00-0000-7000-8000-000000000001",
			SiteID:   "018f4a00-1000-7000-8000-000000000001",
			Devices: []RegistryDeviceSync{{
				DeviceID:              "018f4a00-3000-7000-8000-000000000001",
				Status:                "ACTIVE",
				PresenceApplicability: "APPLICABLE",
				Points:                []RegistryPointSync{pt},
			}},
		}
	}

	if err := baseRequest(RegistryPointSync{
		PointID:               "018f4a00-4000-7000-8000-000000000001",
		TelemetryKey:          "chw_temp",
		PointType:             "TELEMETRY",
		ValueType:             "NUMBER",
		Status:                "ACTIVE",
		SampleIntervalSeconds: 5,
		FreshWithinSeconds:    60,
		MinimumNumber:         &validMin,
		MaximumNumber:         &validMax,
		QualityChecks:         map[string]any{"maxStep": 2.5, "maxStepWindowSeconds": 60, "flatlineSeconds": 1800},
	}).validate(); err != nil {
		t.Fatalf("expected valid quality sync request, got %v", err)
	}

	for _, badPt := range []RegistryPointSync{
		{
			PointID: "018f4a00-4000-7000-8000-000000000002", TelemetryKey: "bad_range", PointType: "TELEMETRY", ValueType: "NUMBER", Status: "ACTIVE",
			MinimumNumber: &badMin, MaximumNumber: &validMax,
		},
		{
			PointID: "018f4a00-4000-7000-8000-000000000003", TelemetryKey: "nan_min", PointType: "TELEMETRY", ValueType: "NUMBER", Status: "ACTIVE",
			MinimumNumber: &nanVal,
		},
		{
			PointID: "018f4a00-4000-7000-8000-000000000004", TelemetryKey: "bad_qc_type", PointType: "TELEMETRY", ValueType: "NUMBER", Status: "ACTIVE",
			QualityChecks: map[string]any{"maxStep": "not-a-number"},
		},
		{
			PointID: "018f4a00-4000-7000-8000-000000000005", TelemetryKey: "oversized_qc", PointType: "TELEMETRY", ValueType: "NUMBER", Status: "ACTIVE",
			QualityChecks: map[string]any{"note": strings.Repeat("x", 4500)},
		},
	} {
		if err := baseRequest(badPt).validate(); !errors.Is(err, ErrRegistrySyncInvalid) {
			t.Fatalf("expected ErrRegistrySyncInvalid for %+v, got %v", badPt, err)
		}
	}
}

func TestPostgresApplyRegistrySyncTemplateQualityAndOperatorPreservation(t *testing.T) {
	runtimeURL := os.Getenv("S2_TELEMETRY_TEST_DATABASE_URL")
	adminURL := os.Getenv("S2_TELEMETRY_ADMIN_DATABASE_URL")
	if runtimeURL == "" || adminURL == "" {
		t.Skip("S2_TELEMETRY_TEST_DATABASE_URL and S2_TELEMETRY_ADMIN_DATABASE_URL are required")
	}
	ctx := t.Context()

	admin, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()

	store, err := OpenPostgresStore(ctx, runtimeURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	tenantID := "018f4a00-0000-7000-8000-000000000001"
	siteID := "018f4a00-1000-7000-8000-000000000001"
	deviceID := "018f4a00-3000-7000-8000-000000000099"
	pointID := "018f4a00-4000-7000-8000-000000000099"

	cleanup := func() {
		for _, stmt := range []string{
			`DELETE FROM telemetry_runtime.telemetry_publication_outbox WHERE device_id = $1::uuid`,
			`DELETE FROM telemetry_runtime.device_observation_snapshots WHERE device_id = $1::uuid`,
			`DELETE FROM telemetry_runtime.device_presence WHERE device_id = $1::uuid`,
			`DELETE FROM telemetry_runtime.presence_signals WHERE device_id = $1::uuid`,
			`DELETE FROM telemetry_runtime.observation_coverage WHERE device_id = $1::uuid`,
			`DELETE FROM telemetry_runtime.freshness_policies WHERE device_id = $1::uuid`,
			`DELETE FROM telemetry_runtime.presence_policies WHERE device_id = $1::uuid`,
			`DELETE FROM telemetry_runtime.devices WHERE device_id = $1::uuid`,
		} {
			_, _ = admin.Exec(ctx, stmt, deviceID)
		}
	}
	cleanup()
	t.Cleanup(cleanup)

	tplMin := -5.0
	tplMax := 40.0

	makeReq := func(minNum, maxNum *float64, qc map[string]any) RegistrySyncRequest {
		return RegistrySyncRequest{
			TenantID: tenantID,
			SiteID:   siteID,
			Devices: []RegistryDeviceSync{{
				DeviceID:              deviceID,
				Status:                "ACTIVE",
				PresenceApplicability: "APPLICABLE",
				Points: []RegistryPointSync{{
					PointID:                       pointID,
					TelemetryKey:                  "chw_supply_temp",
					PointType:                     "TELEMETRY",
					ValueType:                     "NUMBER",
					Unit:                          "degC",
					Status:                        "ACTIVE",
					SampleIntervalSeconds:         5,
					ExpectedSampleIntervalSeconds: 5,
					FreshWithinSeconds:            60,
					MinimumNumber:                 minNum,
					MaximumNumber:                 maxNum,
					QualityChecks:                 qc,
				}},
			}},
		}
	}

	readPolicy := func() (int64, float64, float64, string) {
		var rev int64
		var minN, maxN float64
		var qc string
		if err := admin.QueryRow(ctx, `
SELECT policy_revision, minimum_number, maximum_number, quality_checks::text
FROM telemetry_runtime.freshness_policies
WHERE device_id = $1::uuid AND telemetry_key = 'chw_supply_temp'
`, deviceID).Scan(&rev, &minN, &maxN, &qc); err != nil {
			t.Fatalf("query freshness_policies: %v", err)
		}
		return rev, minN, maxN, qc
	}

	res1, err := store.ApplyRegistrySync(ctx, makeReq(&tplMin, &tplMax, map[string]any{"maxStep": 3.0, "maxStepWindowSeconds": 60}))
	if err != nil || res1.SyncedDevices != 1 {
		t.Fatalf("initial template quality sync failed: res=%+v err=%v", res1, err)
	}
	rev1, _, _, _ := readPolicy()
	if rev1 != 1 {
		t.Fatalf("expected policy_revision 1, got %d", rev1)
	}

	// Identical resync must not bump policy_revision.
	if _, err := store.ApplyRegistrySync(ctx, makeReq(&tplMin, &tplMax, map[string]any{"maxStep": 3.0, "maxStepWindowSeconds": 60})); err != nil {
		t.Fatalf("idempotent resync failed: %v", err)
	}
	rev2, _, _, _ := readPolicy()
	if rev2 != 1 {
		t.Fatalf("idempotent resync should keep policy_revision=1, got %d", rev2)
	}

	// Simulate operator override directly on freshness_policies, then send non-template sync (nil bounds & qualityChecks).
	if _, err := admin.Exec(ctx, `
UPDATE telemetry_runtime.freshness_policies
SET minimum_number = 2.0, quality_checks = '{"flatlineSeconds": 900}'::jsonb
WHERE device_id = $1::uuid AND telemetry_key = 'chw_supply_temp'
`, deviceID); err != nil {
		t.Fatalf("simulate operator override: %v", err)
	}

	if _, err := store.ApplyRegistrySync(ctx, makeReq(nil, nil, nil)); err != nil {
		t.Fatalf("nil quality sync failed: %v", err)
	}
	rev3, storedMin, storedMax, _ := readPolicy()
	if rev3 != 1 || storedMin != 2.0 || storedMax != 40.0 {
		t.Fatalf("nil quality sync must preserve operator overrides without bumping policy_revision: rev=%d min=%v max=%v", rev3, storedMin, storedMax)
	}

	// Template update with explicit new bounds updates the policy and bumps revision.
	newMin := -2.0
	if _, err := store.ApplyRegistrySync(ctx, makeReq(&newMin, &tplMax, map[string]any{"flatlineSeconds": 1200})); err != nil {
		t.Fatalf("explicit template quality update failed: %v", err)
	}
	rev4, updatedMin, _, _ := readPolicy()
	if rev4 != 2 || updatedMin != -2.0 {
		t.Fatalf("expected updated policy rev=2 min=-2.0, got rev=%d min=%v", rev4, updatedMin)
	}

	// Updating only minimumNumber above stored maximumNumber must clear conflicting maximum_number cleanly instead of hitting SQL CHECK 23514.
	highMin := 50.0
	if _, err := store.ApplyRegistrySync(ctx, makeReq(&highMin, nil, nil)); err != nil {
		t.Fatalf("single-bound minimum increase past old maximum failed: %v", err)
	}
	var shiftedMax *float64
	var rev5 int64
	if err := admin.QueryRow(ctx, `
SELECT policy_revision, maximum_number
FROM telemetry_runtime.freshness_policies
WHERE device_id = $1::uuid AND telemetry_key = 'chw_supply_temp'
`, deviceID).Scan(&rev5, &shiftedMax); err != nil {
		t.Fatalf("query shifted policy: %v", err)
	}
	if rev5 != 3 || shiftedMax != nil {
		t.Fatalf("expected rev=3 and cleared maximum_number=nil, got rev=%d max=%v", rev5, shiftedMax)
	}
}
