package connectivity

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

func makeTestGatewayCertHeader(t *testing.T, gwID string) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: gwID},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
	}, &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: gwID},
	}, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return url.QueryEscape(string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})))
}

func sampleValidClusterPolicy() *edgecontrol.ClusterControlPolicy {
	p := edgecontrol.DefaultClusterControlPolicy()
	p.Enabled = true
	p.MinRunningChillers = 1
	p.MaxRunningChillers = 3
	p.StageUpLoadPct = 88
	p.StageDownLoadPct = 35
	p.StageDwellSeconds = 240
	p.DefaultSupplySetpointC = 7.2
	return &p
}

func sampleValidEnergySchedule() *edgecontrol.EdgeEnergySchedule {
	spPreCool := 6.0
	spPeak := 8.5
	pumpMin := 28.0
	pumpMax := 45.0
	towerMin := 35.0
	towerMax := 90.0
	maxChillers := 2
	targetLoad := 420.0
	base := time.Date(2026, 11, 27, 0, 0, 0, 0, time.UTC)
	return &edgecontrol.EdgeEnergySchedule{
		ScheduleID:                "sched-songyaxuan-chiller-plant",
		Version:                   3,
		IssuedAt:                  base.Add(-30 * time.Minute),
		ValidFrom:                 base,
		ValidUntil:                base.Add(24 * time.Hour),
		LeaseExpiresAt:            base.Add(12 * time.Hour),
		ContinueOfflineWhileValid: true,
		Slots: []edgecontrol.EnergyScheduleSlot{
			{
				SlotID:              "peak-shave",
				StartMinute:         480,
				EndMinute:           720,
				Mode:                edgecontrol.ScheduleModePeakShave,
				SupplyTempSetpointC: &spPeak,
				TowerFanSpeedMinPct: &towerMin,
				TowerFanSpeedMaxPct: &towerMax,
				MaxRunningChillers:  &maxChillers,
				TargetCoolingLoadKW: &targetLoad,
			},
			{
				SlotID:              "precool",
				StartMinute:         360,
				EndMinute:           480,
				Mode:                edgecontrol.ScheduleModePreCool,
				SupplyTempSetpointC: &spPreCool,
				PumpFrequencyMinHz:  &pumpMin,
				PumpFrequencyMaxHz:  &pumpMax,
			},
		},
	}
}

func TestConfigShadowClusterPolicyAndEnergyScheduleValidation(t *testing.T) {
	t.Parallel()

	cluster := sampleValidClusterPolicy()
	cleanedCluster, err := validateClusterPolicy(cluster)
	if err != nil || cleanedCluster == nil || cleanedCluster == cluster {
		t.Fatalf("validateClusterPolicy() = %+v err=%v", cleanedCluster, err)
	}

	badClusterCases := []struct {
		name   string
		mutate func(*edgecontrol.ClusterControlPolicy)
	}{
		{"minRunning > maxRunning", func(p *edgecontrol.ClusterControlPolicy) { p.MinRunningChillers = 4; p.MaxRunningChillers = 2 }},
		{"stageUpLoadPct <= 50", func(p *edgecontrol.ClusterControlPolicy) { p.StageUpLoadPct = 50 }},
		{"stageDownLoadPct >= 80", func(p *edgecontrol.ClusterControlPolicy) { p.StageDownLoadPct = 80; p.StageUpLoadPct = 95 }},
		{"hysteresis < 10", func(p *edgecontrol.ClusterControlPolicy) { p.StageUpLoadPct = 70; p.StageDownLoadPct = 62 }},
		{"stageDwellSeconds > 3600", func(p *edgecontrol.ClusterControlPolicy) { p.StageDwellSeconds = 3601 }},
		{"defaultSupplySetpointC NaN", func(p *edgecontrol.ClusterControlPolicy) { p.DefaultSupplySetpointC = math.NaN() }},
		{"pump bounds inverted", func(p *edgecontrol.ClusterControlPolicy) { p.MinPumpFrequencyHz = 40; p.MaxPumpFrequencyHz = 30 }},
		{"tower bounds out of range", func(p *edgecontrol.ClusterControlPolicy) { p.MinTowerFanSpeedPct = 10 }},
	}
	for _, tc := range badClusterCases {
		t.Run("cluster_"+tc.name, func(t *testing.T) {
			p := sampleValidClusterPolicy()
			tc.mutate(p)
			if _, err := validateClusterPolicy(p); !errors.Is(err, ErrConfigShadowInvalid) {
				t.Fatalf("%s: expected ErrConfigShadowInvalid, got %v", tc.name, err)
			}
		})
	}

	sched := sampleValidEnergySchedule()
	// Verify unsorted input slots are sorted canonically by validateEnergySchedule and trimmed.
	sched.ScheduleID = "  " + sched.ScheduleID + "  "
	sched.Slots[0].SlotID = "  " + sched.Slots[0].SlotID + "  "
	cleanedSched, err := validateEnergySchedule(sched)
	if err != nil || cleanedSched == nil || cleanedSched == sched {
		t.Fatalf("validateEnergySchedule() = %+v err=%v", cleanedSched, err)
	}
	if cleanedSched.ScheduleID != "sched-songyaxuan-chiller-plant" ||
		len(cleanedSched.Slots) != 2 ||
		cleanedSched.Slots[0].SlotID != "precool" ||
		cleanedSched.Slots[1].SlotID != "peak-shave" {
		t.Fatalf("expected trimmed & canonically sorted schedule slots, got %+v", cleanedSched)
	}

	badSchedCases := []struct {
		name   string
		mutate func(*edgecontrol.EdgeEnergySchedule)
	}{
		{"empty scheduleId", func(s *edgecontrol.EdgeEnergySchedule) { s.ScheduleID = "   " }},
		{"version < 1", func(s *edgecontrol.EdgeEnergySchedule) { s.Version = 0 }},
		{"validFrom >= validUntil", func(s *edgecontrol.EdgeEnergySchedule) { s.ValidUntil = s.ValidFrom }},
		{"empty slots", func(s *edgecontrol.EdgeEnergySchedule) { s.Slots = nil }},
		{"too many slots", func(s *edgecontrol.EdgeEnergySchedule) {
			slots := make([]edgecontrol.EnergyScheduleSlot, 289)
			for i := range slots {
				slots[i] = edgecontrol.EnergyScheduleSlot{SlotID: "s", StartMinute: 0, EndMinute: 1, Mode: edgecontrol.ScheduleModeEco}
			}
			s.Slots = slots
		}},
		{"duplicate slotId", func(s *edgecontrol.EdgeEnergySchedule) { s.Slots[1].SlotID = s.Slots[0].SlotID }},
		{"unsupported mode", func(s *edgecontrol.EdgeEnergySchedule) { s.Slots[0].Mode = "TURBO" }},
		{"overlapping slots", func(s *edgecontrol.EdgeEnergySchedule) { s.Slots[0].StartMinute = 420 }},
		{"setpoint out of range", func(s *edgecontrol.EdgeEnergySchedule) {
			bad := 3.5
			s.Slots[0].SupplyTempSetpointC = &bad
		}},
		{"pump bounds inverted", func(s *edgecontrol.EdgeEnergySchedule) {
			lo, hi := 45.0, 30.0
			s.Slots[0].PumpFrequencyMinHz = &lo
			s.Slots[0].PumpFrequencyMaxHz = &hi
		}},
		{"negative target load", func(s *edgecontrol.EdgeEnergySchedule) {
			bad := -1.0
			s.Slots[0].TargetCoolingLoadKW = &bad
		}},
	}
	for _, tc := range badSchedCases {
		t.Run("schedule_"+tc.name, func(t *testing.T) {
			s := sampleValidEnergySchedule()
			tc.mutate(s)
			if _, err := validateEnergySchedule(s); !errors.Is(err, ErrConfigShadowInvalid) {
				t.Fatalf("%s: expected ErrConfigShadowInvalid, got %v", tc.name, err)
			}
		})
	}
}

func TestConfigShadowClusterAndScheduleTriStateJSONAndSizeBounds(t *testing.T) {
	t.Parallel()

	// Tri-state omitted vs explicit null vs object vs unknown fields.
	var omitted UpdateDesiredConfigInput
	if err := json.Unmarshal([]byte(`{"reason":"omit"}`), &omitted); err != nil ||
		omitted.ClusterPolicy.IsSet() || !omitted.ClusterPolicy.IsZero() ||
		omitted.EnergySchedule.IsSet() || !omitted.EnergySchedule.IsZero() {
		t.Fatalf("expected omitted fields, got cluster=%+v schedule=%+v err=%v", omitted.ClusterPolicy, omitted.EnergySchedule, err)
	}
	omittedBytes, err := json.Marshal(omitted)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(omittedBytes), "clusterPolicy") || strings.Contains(string(omittedBytes), "energySchedule") {
		t.Fatalf("omitted keys must not marshal, got %s", string(omittedBytes))
	}

	var cleared UpdateDesiredConfigInput
	if err := json.Unmarshal([]byte(`{"clusterPolicy":null,"energySchedule":null,"reason":"clear"}`), &cleared); err != nil ||
		!cleared.ClusterPolicy.IsSet() || cleared.ClusterPolicy.Value != nil || cleared.ClusterPolicy.IsZero() ||
		!cleared.EnergySchedule.IsSet() || cleared.EnergySchedule.Value != nil || cleared.EnergySchedule.IsZero() {
		t.Fatalf("expected explicit null tri-state, got cluster=%+v schedule=%+v err=%v", cleared.ClusterPolicy, cleared.EnergySchedule, err)
	}
	clearedBytes, err := json.Marshal(cleared)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(clearedBytes), `"clusterPolicy":null`) || !strings.Contains(string(clearedBytes), `"energySchedule":null`) {
		t.Fatalf("explicit null must marshal null, got %s", string(clearedBytes))
	}

	cluster := sampleValidClusterPolicy()
	sched := sampleValidEnergySchedule()
	setBytes, err := json.Marshal(map[string]any{
		"clusterPolicy":  cluster,
		"energySchedule": sched,
		"reason":         "set both",
	})
	if err != nil {
		t.Fatal(err)
	}
	var replaced UpdateDesiredConfigInput
	if err := json.Unmarshal(setBytes, &replaced); err != nil ||
		!replaced.ClusterPolicy.IsSet() || replaced.ClusterPolicy.Value == nil || !replaced.ClusterPolicy.Value.Enabled ||
		!replaced.EnergySchedule.IsSet() || replaced.EnergySchedule.Value == nil || replaced.EnergySchedule.Value.ScheduleID != sched.ScheduleID {
		t.Fatalf("expected decoded clusterPolicy and energySchedule, got %+v err=%v", replaced, err)
	}

	var unknownCluster UpdateDesiredConfigInput
	if err := json.Unmarshal([]byte(`{"clusterPolicy":{"enabled":true,"extra":1},"reason":"bad"}`), &unknownCluster); err == nil {
		t.Fatal("expected unknown field inside clusterPolicy to be rejected")
	}
	var unknownSchedule UpdateDesiredConfigInput
	if err := json.Unmarshal([]byte(`{"energySchedule":{"scheduleId":"s","version":1,"extra":1},"reason":"bad"}`), &unknownSchedule); err == nil {
		t.Fatal("expected unknown field inside energySchedule to be rejected")
	}
	var unknownSlot UpdateDesiredConfigInput
	if err := json.Unmarshal([]byte(`{"energySchedule":{"scheduleId":"s","version":1,"issuedAt":"2026-11-27T00:00:00Z","validFrom":"2026-11-27T00:00:00Z","validUntil":"2026-11-28T00:00:00Z","continueOfflineWhileValid":true,"slots":[{"slotId":"a","startMinute":0,"endMinute":60,"mode":"NORMAL","bogus":1}]},"reason":"bad"}`), &unknownSlot); err == nil {
		t.Fatal("expected unknown field inside energySchedule.slots[0] to be rejected")
	}

	// normalizeAndValidateDesiredShadow encodes clusterPolicy & energySchedule without nil placeholders,
	// and decodeShadowConfigPayload decodes them cleanly back into Go structs.
	validated, err := normalizeAndValidateDesiredShadow(nil, nil, nil, nil, nil, nil, nil, cluster, sched)
	if err != nil {
		t.Fatalf("normalizeAndValidateDesiredShadow failed: %v", err)
	}
	if validated.encoded.clusterPolicyArg == nil || validated.encoded.energyScheduleArg == nil {
		t.Fatalf("expected encoded clusterPolicyArg and energyScheduleArg, got %+v", validated.encoded)
	}
	var (
		outMappings []GatewayPointMapping
		outPolicies []GatewaySamplingPolicy
		outUnits    map[string]ConcentratorUnitShadow
		outDrivers  []GatewayModbusDriverConfig
		outAutonomy *GatewayAutonomyPolicy
		outRules    *edgecontrol.EdgeRuleBundle
		outModels   []edgecontrol.EdgeModelBundle
		outCluster  *edgecontrol.ClusterControlPolicy
		outSchedule *edgecontrol.EdgeEnergySchedule
	)
	if err := decodeShadowConfigPayload(
		validated.encoded.mappingsJSON,
		validated.encoded.policiesJSON,
		validated.encoded.unitsJSON,
		validated.encoded.driversJSON,
		nil,
		nil,
		nil,
		nil,
		[]byte(*validated.encoded.clusterPolicyArg),
		[]byte(*validated.encoded.energyScheduleArg),
		&outMappings,
		&outPolicies,
		&outUnits,
		&outDrivers,
		&outAutonomy,
		&outRules,
		&outModels,
		nil,
		&outCluster,
		&outSchedule,
	); err != nil {
		t.Fatalf("decodeShadowConfigPayload failed: %v", err)
	}
	if outCluster == nil || !clusterPoliciesEqual(outCluster, cluster) {
		t.Fatalf("decoded clusterPolicy mismatch: got %+v want %+v", outCluster, cluster)
	}
	if outSchedule == nil || !energySchedulesEqual(outSchedule, sched) {
		t.Fatalf("decoded energySchedule mismatch: got %+v want %+v", outSchedule, sched)
	}

	// JSONB text size bound enforcement for energySchedule.
	oversized := sampleValidEnergySchedule()
	oversized.ScheduleID = strings.Repeat("x", maxEnergyScheduleJSONBytes)
	if _, err := marshalDesiredShadowSections(nil, nil, nil, nil, nil, nil, nil, nil, oversized); !errors.Is(err, ErrConfigShadowInvalid) {
		t.Fatalf("expected ErrConfigShadowInvalid for oversized energySchedule, got %v", err)
	}
}

func TestConfigShadowClusterAndScheduleCanonicalHashDriftAndReconcile(t *testing.T) {
	t.Parallel()

	cluster := sampleValidClusterPolicy()
	schedA := sampleValidEnergySchedule()

	// schedB has reversed slot order and +08:00 timezone offset representing exact same instants.
	locPlus8 := time.FixedZone("CST", 8*3600)
	schedB := sampleValidEnergySchedule()
	schedB.IssuedAt = schedB.IssuedAt.In(locPlus8)
	schedB.ValidFrom = schedB.ValidFrom.In(locPlus8)
	schedB.ValidUntil = schedB.ValidUntil.In(locPlus8)
	schedB.LeaseExpiresAt = schedB.LeaseExpiresAt.In(locPlus8)
	schedB.Slots = []edgecontrol.EnergyScheduleSlot{schedB.Slots[1], schedB.Slots[0]}

	desiredA := &GatewayDesiredConfig{
		DesiredRevision: 4,
		ClusterPolicy:   cluster,
		EnergySchedule:  schedA,
	}
	desiredB := &GatewayDesiredConfig{
		DesiredRevision: 4,
		ClusterPolicy:   cluster.Clone(),
		EnergySchedule:  schedB,
	}
	hashA, err := ComputeConfigShadowDigest(desiredA)
	if err != nil {
		t.Fatal(err)
	}
	hashB, err := ComputeConfigShadowDigest(desiredB)
	if err != nil {
		t.Fatal(err)
	}
	if hashA == "" || hashA != hashB {
		t.Fatalf("expected identical canonical shadow hash across slot order / timezone offset, got %q vs %q", hashA, hashB)
	}

	mutatedDesired := &GatewayDesiredConfig{
		DesiredRevision: 5,
		ClusterPolicy:   cluster.Clone(),
		EnergySchedule:  schedA.Clone(),
	}
	mutatedDesired.ClusterPolicy.StageUpLoadPct = 90
	hashMutatedCluster, err := ComputeConfigShadowDigest(mutatedDesired)
	if err != nil {
		t.Fatal(err)
	}
	if hashMutatedCluster == hashA {
		t.Fatal("expected clusterPolicy mutation to alter canonical shadow hash")
	}

	// Matching reported (with reordered slots & +08:00 offset) -> zero drift & SYNCED.
	reportedSynced := &GatewayReportedConfig{
		ReportedRevision: 4,
		AppliedStatus:    AppliedStatusApplied,
		ClusterPolicy:    cluster.Clone(),
		EnergySchedule:   schedB,
	}
	shadowSynced := buildGatewayConfigShadow("0194f000-1000-7000-8000-000000000001", "0194f000-3000-7000-8000-000000000001", desiredA, reportedSynced)
	if shadowSynced.SyncStatus != ShadowSyncStatusSynced || len(shadowSynced.Drift) != 0 {
		t.Fatalf("expected SYNCED with zero drift, got status=%s drift=%+v", shadowSynced.SyncStatus, shadowSynced.Drift)
	}

	// Older reported revision -> PENDING_SYNC.
	shadowPending := buildGatewayConfigShadow("0194f000-1000-7000-8000-000000000001", "0194f000-3000-7000-8000-000000000001", mutatedDesired, reportedSynced)
	if shadowPending.SyncStatus != ShadowSyncStatusPendingSync {
		t.Fatalf("expected PENDING_SYNC when reportedRevision < desiredRevision, got %s", shadowPending.SyncStatus)
	}

	// Missing vs unexpected drift.
	if d := ComputeConfigShadowDrift(&GatewayDesiredConfig{ClusterPolicy: cluster}, &GatewayReportedConfig{}); len(d) != 1 || d[0].Section != DriftSectionClusterPolicy || d[0].Target != "clusterPolicy" || d[0].Reason != DriftReasonMissingOnEdge {
		t.Fatalf("unexpected cluster missing drift: %+v", d)
	}
	if d := ComputeConfigShadowDrift(&GatewayDesiredConfig{}, &GatewayReportedConfig{ClusterPolicy: cluster}); len(d) != 1 || d[0].Section != DriftSectionClusterPolicy || d[0].Target != "clusterPolicy" || d[0].Reason != DriftReasonUnexpectedOnEdge {
		t.Fatalf("unexpected cluster unexpected drift: %+v", d)
	}
	if d := ComputeConfigShadowDrift(&GatewayDesiredConfig{EnergySchedule: schedA}, &GatewayReportedConfig{}); len(d) != 1 || d[0].Section != DriftSectionEnergySchedule || d[0].Target != "energySchedule" || d[0].Reason != DriftReasonMissingOnEdge {
		t.Fatalf("unexpected schedule missing drift: %+v", d)
	}
	if d := ComputeConfigShadowDrift(&GatewayDesiredConfig{}, &GatewayReportedConfig{EnergySchedule: schedA}); len(d) != 1 || d[0].Section != DriftSectionEnergySchedule || d[0].Target != "energySchedule" || d[0].Reason != DriftReasonUnexpectedOnEdge {
		t.Fatalf("unexpected schedule unexpected drift: %+v", d)
	}

	// Subtle value mismatch (same scheduleId, version, slot count, different slot setpoint).
	mismatchedCluster := cluster.Clone()
	mismatchedCluster.MaxRunningChillers = 4
	mismatchedSched := schedA.Clone()
	altSP := 9.5
	mismatchedSched.Slots[0].SupplyTempSetpointC = &altSP
	shadowDrifted := buildGatewayConfigShadow(
		"0194f000-1000-7000-8000-000000000001",
		"0194f000-3000-7000-8000-000000000001",
		desiredA,
		&GatewayReportedConfig{
			ReportedRevision: 4,
			AppliedStatus:    AppliedStatusApplied,
			ClusterPolicy:    mismatchedCluster,
			EnergySchedule:   mismatchedSched,
		},
	)
	if shadowDrifted.SyncStatus != ShadowSyncStatusDrifted || len(shadowDrifted.Drift) != 2 {
		t.Fatalf("expected DRIFTED with 2 mismatch items, got status=%s drift=%+v", shadowDrifted.SyncStatus, shadowDrifted.Drift)
	}
	if shadowDrifted.Drift[0].Section != DriftSectionClusterPolicy || shadowDrifted.Drift[0].Reason != DriftReasonValueMismatch {
		t.Fatalf("unexpected cluster drift item: %+v", shadowDrifted.Drift[0])
	}
	if shadowDrifted.Drift[1].Section != DriftSectionEnergySchedule || shadowDrifted.Drift[1].Reason != DriftReasonValueMismatch {
		t.Fatalf("unexpected schedule drift item: %+v", shadowDrifted.Drift[1])
	}

	// Report driftDetails whitelist allows CLUSTER_POLICY and ENERGY_SCHEDULE.
	if _, _, err := validateReportDriftDetails([]DriftDetail{
		{Section: DriftSectionClusterPolicy, Target: "clusterPolicy", Reason: DriftReasonEdgeApplyFailed, Detail: "cluster apply failed"},
		{Section: DriftSectionEnergySchedule, Target: "energySchedule", Reason: DriftReasonEdgeApplyFailed, Detail: "version downgrade rejected"},
	}); err != nil {
		t.Fatalf("expected CLUSTER_POLICY and ENERGY_SCHEDULE drift sections allowed, got %v", err)
	}
}

type statefulMemoryShadowStore struct {
	tenantID string
	gwID     string
	actorID  string
	desired  *GatewayDesiredConfig
	reported *GatewayReportedConfig
	clock    time.Time
}

func (s *statefulMemoryShadowStore) GetGatewayConfigShadowForOperator(_ context.Context, _ registryauth.GrantClaims, _ string) (GatewayConfigShadow, error) {
	return buildGatewayConfigShadow(s.tenantID, s.gwID, s.desired, s.reported), nil
}

func (s *statefulMemoryShadowStore) UpdateGatewayDesiredConfig(_ context.Context, _ registryauth.GrantClaims, _ string, input UpdateDesiredConfigInput) (GatewayConfigShadow, error) {
	if _, err := validateShadowReason(input.Reason); err != nil {
		return GatewayConfigShadow{}, err
	}
	currentRev := int64(0)
	if s.desired != nil {
		currentRev = s.desired.DesiredRevision
	}
	if input.ExpectedDesiredRevision != nil && *input.ExpectedDesiredRevision != currentRev {
		return GatewayConfigShadow{}, ErrConfigShadowConflict
	}

	var nextCluster *edgecontrol.ClusterControlPolicy
	if input.ClusterPolicy.IsSet() {
		var err error
		nextCluster, err = validateClusterPolicy(input.ClusterPolicy.Value)
		if err != nil {
			return GatewayConfigShadow{}, err
		}
	} else if s.desired != nil {
		nextCluster = s.desired.ClusterPolicy.Clone()
	}

	var nextSchedule *edgecontrol.EdgeEnergySchedule
	if input.EnergySchedule.IsSet() {
		var err error
		nextSchedule, err = validateEnergySchedule(input.EnergySchedule.Value)
		if err != nil {
			return GatewayConfigShadow{}, err
		}
	} else if s.desired != nil {
		nextSchedule = s.desired.EnergySchedule.Clone()
	}

	s.desired = &GatewayDesiredConfig{
		DesiredRevision:     currentRev + 1,
		PointMappings:       []GatewayPointMapping{},
		SamplingPolicies:    []GatewaySamplingPolicy{},
		ConcentratorConfigs: map[string]ConcentratorUnitShadow{},
		ClusterPolicy:       nextCluster,
		EnergySchedule:      nextSchedule,
		UpdatedBy:           s.actorID,
		Reason:              input.Reason,
		CreatedAt:           s.clock,
		UpdatedAt:           s.clock,
	}
	return buildGatewayConfigShadow(s.tenantID, s.gwID, s.desired, s.reported), nil
}

func (s *statefulMemoryShadowStore) PullGatewayDesiredConfig(_ context.Context, _ string, sinceRevision int64) (GatewayShadowPullResponse, error) {
	if s.desired == nil {
		return GatewayShadowPullResponse{GatewayID: s.gwID}, nil
	}
	hasDelta := s.desired.DesiredRevision > sinceRevision
	resp := GatewayShadowPullResponse{
		GatewayID:       s.gwID,
		DesiredRevision: s.desired.DesiredRevision,
		HasDelta:        hasDelta,
	}
	if hasDelta {
		resp.Desired = s.desired
	}
	return resp, nil
}

func (s *statefulMemoryShadowStore) ReportGatewayAppliedConfig(_ context.Context, _ string, input GatewayShadowReportInput) (GatewayConfigShadow, error) {
	status, err := validateAppliedStatus(input.AppliedStatus)
	if err != nil {
		return GatewayConfigShadow{}, err
	}
	cluster, err := validateClusterPolicy(input.ClusterPolicy)
	if err != nil {
		return GatewayConfigShadow{}, err
	}
	schedule, err := validateEnergySchedule(input.EnergySchedule)
	if err != nil {
		return GatewayConfigShadow{}, err
	}
	inputDrifts, _, err := validateReportDriftDetails(input.DriftDetails)
	if err != nil {
		return GatewayConfigShadow{}, err
	}
	s.reported = &GatewayReportedConfig{
		ReportedRevision:    input.ReportedRevision,
		AppliedStatus:       status,
		PointMappings:       []GatewayPointMapping{},
		SamplingPolicies:    []GatewaySamplingPolicy{},
		ConcentratorConfigs: map[string]ConcentratorUnitShadow{},
		ClusterPolicy:       cluster,
		EnergySchedule:      schedule,
		DriftDetails:        inputDrifts,
		ErrorMessage:        input.ErrorMessage,
		ReportedAt:          s.clock,
		UpdatedAt:           s.clock,
	}
	return buildGatewayConfigShadow(s.tenantID, s.gwID, s.desired, s.reported), nil
}

func TestConfigShadowClusterAndScheduleHTTPRoundTripAndWireCompatibility(t *testing.T) {
	t.Parallel()

	const tenantID = "0194f000-1000-7000-8000-000000000001"
	const gwID = "0194f000-3000-7000-8000-000000000001"
	const actorID = "0194f000-4000-7000-8000-000000000001"

	store := &statefulMemoryShadowStore{
		tenantID: tenantID,
		gwID:     gwID,
		actorID:  actorID,
		clock:    time.Date(2026, 11, 27, 4, 0, 0, 0, time.UTC),
	}
	shadowHandler := NewConfigShadowHandler(store, fakePeerVerifier{gatewayID: gwID}, func(_ *http.Request, _ registryauth.Action) (registryauth.GrantClaims, error) {
		return registryauth.GrantClaims{TenantID: tenantID, PrincipalID: actorID}, nil
	}, nil)
	handler := NewCredentialHandler(nil, nil, WithConfigShadowHandler(shadowHandler))

	gwURI, _ := url.Parse("spiffe://hvac.local/platform-gateway")
	gwPeer := &x509.Certificate{URIs: []*url.URL{gwURI}}
	escapedCert := makeTestGatewayCertHeader(t, gwID)

	// 1. PUT desired clusterPolicy & energySchedule -> 200 PENDING_SYNC.
	putBody, err := json.Marshal(map[string]any{
		"expectedDesiredRevision": 0,
		"clusterPolicy":           sampleValidClusterPolicy(),
		"energySchedule":          sampleValidEnergySchedule(),
		"reason":                  "push cluster and energy schedule",
	})
	if err != nil {
		t.Fatal(err)
	}
	putReq := httptest.NewRequest(http.MethodPut, "/internal/v1/gateways/"+gwID+"/config-shadow", bytes.NewReader(putBody))
	putReq.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{gwPeer}}}
	putRec := httptest.NewRecorder()
	handler.ServeHTTP(putRec, putReq)
	if putRec.Code != http.StatusOK {
		t.Fatalf("PUT status=%d body=%s", putRec.Code, putRec.Body.String())
	}
	var putShadow GatewayConfigShadow
	if err := json.Unmarshal(putRec.Body.Bytes(), &putShadow); err != nil {
		t.Fatal(err)
	}
	if putShadow.SyncStatus != ShadowSyncStatusPendingSync || putShadow.Desired == nil ||
		putShadow.Desired.ClusterPolicy == nil || putShadow.Desired.EnergySchedule == nil {
		t.Fatalf("unexpected PUT shadow: %+v", putShadow)
	}

	// 2. Gateway pull returns wire JSON compatible with edgedaemon + edgecontrol.
	pullReq := httptest.NewRequest(http.MethodGet, "/gateway/v1/config-shadow?sinceRevision=0", nil)
	pullReq.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{gwPeer}}}
	pullReq.Header.Set("X-Gateway-Client-Cert", escapedCert)
	pullRec := httptest.NewRecorder()
	handler.ServeHTTP(pullRec, pullReq)
	if pullRec.Code != http.StatusOK {
		t.Fatalf("pull status=%d body=%s", pullRec.Code, pullRec.Body.String())
	}

	// Verify wire JSON compatibility with strict edgedaemon payload shape and MQTT CHANGE_BATCH envelope.
	var wirePull struct {
		GatewayID       string `json:"gatewayId"`
		DesiredRevision int64  `json:"desiredRevision"`
		HasDelta        bool   `json:"hasDelta"`
		Desired         *struct {
			DesiredRevision int64                             `json:"desiredRevision"`
			ClusterPolicy   *edgecontrol.ClusterControlPolicy `json:"clusterPolicy"`
			EnergySchedule  *edgecontrol.EdgeEnergySchedule   `json:"energySchedule"`
		} `json:"desired"`
	}
	if err := json.Unmarshal(pullRec.Body.Bytes(), &wirePull); err != nil {
		t.Fatal(err)
	}
	if !wirePull.HasDelta || wirePull.Desired == nil || wirePull.Desired.ClusterPolicy == nil || wirePull.Desired.EnergySchedule == nil {
		t.Fatalf("pull wire response missing clusterPolicy/energySchedule: %s", pullRec.Body.String())
	}
	if err := wirePull.Desired.ClusterPolicy.Validate(); err != nil {
		t.Fatalf("pulled clusterPolicy failed edgecontrol validation: %v", err)
	}
	if err := wirePull.Desired.EnergySchedule.Validate(); err != nil {
		t.Fatalf("pulled energySchedule failed edgecontrol validation: %v", err)
	}

	// Encode pulled desired config into MQTT CHANGE_BATCH and decode back to verify end-to-end wire fidelity.
	var pulledResp GatewayShadowPullResponse
	if err := json.Unmarshal(pullRec.Body.Bytes(), &pulledResp); err != nil {
		t.Fatal(err)
	}
	batchBytes, _, err := edgecontrol.EncodeShadowChangeBatch(gwID, pulledResp.DesiredRevision, pulledResp.Desired, store.clock)
	if err != nil {
		t.Fatalf("EncodeShadowChangeBatch: %v", err)
	}
	_, batch, err := edgecontrol.DecodeShadowChangeBatch(batchBytes, gwID)
	if err != nil || len(batch.Items) != 1 || !edgecontrol.VerifyShadowPayloadDigest(batch.Items[0].Payload, batch.Items[0].PayloadDigest) {
		t.Fatalf("DecodeShadowChangeBatch/VerifyDigest failed: batch=%+v err=%v", batch, err)
	}
	var mqttDesired GatewayDesiredConfig
	if err := json.Unmarshal(batch.Items[0].Payload, &mqttDesired); err != nil {
		t.Fatal(err)
	}
	if mqttDesired.ClusterPolicy == nil || !clusterPoliciesEqual(mqttDesired.ClusterPolicy, pulledResp.Desired.ClusterPolicy) ||
		mqttDesired.EnergySchedule == nil || !energySchedulesEqual(mqttDesired.EnergySchedule, pulledResp.Desired.EnergySchedule) {
		t.Fatalf("MQTT CHANGE_BATCH round-trip altered clusterPolicy/energySchedule: %+v", mqttDesired)
	}

	// 3. Gateway report echoes clusterPolicy & energySchedule -> SYNCED.
	reportBytes, err := json.Marshal(map[string]any{
		"reportedRevision":    1,
		"appliedStatus":       "APPLIED",
		"pointMappings":       []any{},
		"samplingPolicies":    []any{},
		"concentratorConfigs": map[string]any{},
		"clusterPolicy":       wirePull.Desired.ClusterPolicy,
		"energySchedule":      wirePull.Desired.EnergySchedule,
	})
	if err != nil {
		t.Fatal(err)
	}
	reportReq := httptest.NewRequest(http.MethodPost, "/gateway/v1/config-shadow/report", bytes.NewReader(reportBytes))
	reportReq.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{gwPeer}}}
	reportReq.Header.Set("X-Gateway-Client-Cert", escapedCert)
	reportRec := httptest.NewRecorder()
	handler.ServeHTTP(reportRec, reportReq)
	if reportRec.Code != http.StatusOK {
		t.Fatalf("report status=%d body=%s", reportRec.Code, reportRec.Body.String())
	}
	var reportedShadow GatewayConfigShadow
	if err := json.Unmarshal(reportRec.Body.Bytes(), &reportedShadow); err != nil {
		t.Fatal(err)
	}
	if reportedShadow.SyncStatus != ShadowSyncStatusSynced || len(reportedShadow.Drift) != 0 {
		t.Fatalf("expected SYNCED after report, got status=%s drift=%+v", reportedShadow.SyncStatus, reportedShadow.Drift)
	}

	// 4. Partial PUT omitting both preserves both.
	preserveReq := httptest.NewRequest(http.MethodPut, "/internal/v1/gateways/"+gwID+"/config-shadow", bytes.NewReader([]byte(`{"expectedDesiredRevision":1,"reason":"preserve"}`)))
	preserveReq.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{gwPeer}}}
	preserveRec := httptest.NewRecorder()
	handler.ServeHTTP(preserveRec, preserveReq)
	if preserveRec.Code != http.StatusOK {
		t.Fatalf("preserve PUT status=%d body=%s", preserveRec.Code, preserveRec.Body.String())
	}
	var preservedShadow GatewayConfigShadow
	if err := json.Unmarshal(preserveRec.Body.Bytes(), &preservedShadow); err != nil {
		t.Fatal(err)
	}
	if preservedShadow.Desired == nil || preservedShadow.Desired.ClusterPolicy == nil || preservedShadow.Desired.EnergySchedule == nil {
		t.Fatalf("omitted partial PUT must preserve clusterPolicy and energySchedule: %+v", preservedShadow.Desired)
	}

	// 5. Explicit null clears clusterPolicy while preserving energySchedule.
	clearReq := httptest.NewRequest(http.MethodPut, "/internal/v1/gateways/"+gwID+"/config-shadow", bytes.NewReader([]byte(`{"expectedDesiredRevision":2,"clusterPolicy":null,"reason":"clear cluster"}`)))
	clearReq.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{gwPeer}}}
	clearRec := httptest.NewRecorder()
	handler.ServeHTTP(clearRec, clearReq)
	if clearRec.Code != http.StatusOK {
		t.Fatalf("clear PUT status=%d body=%s", clearRec.Code, clearRec.Body.String())
	}
	var clearedShadow GatewayConfigShadow
	if err := json.Unmarshal(clearRec.Body.Bytes(), &clearedShadow); err != nil {
		t.Fatal(err)
	}
	if clearedShadow.Desired == nil || clearedShadow.Desired.ClusterPolicy != nil || clearedShadow.Desired.EnergySchedule == nil {
		t.Fatalf("expected clusterPolicy cleared and energySchedule preserved: %+v", clearedShadow.Desired)
	}

	// 6. Real Store validation failures return 422 CONFIG_SHADOW_INVALID.
	realHandler := NewConfigShadowHandler(&Store{}, fakePeerVerifier{gatewayID: gwID}, func(_ *http.Request, _ registryauth.Action) (registryauth.GrantClaims, error) {
		return registryauth.GrantClaims{TenantID: tenantID, PrincipalID: actorID}, nil
	}, nil)
	badCluster := sampleValidClusterPolicy()
	badCluster.StageUpLoadPct = 20
	badBody, err := json.Marshal(map[string]any{
		"clusterPolicy": badCluster,
		"reason":        "invalid bounds",
	})
	if err != nil {
		t.Fatal(err)
	}
	badReq := httptest.NewRequest(http.MethodPut, "/internal/v1/gateways/"+gwID+"/config-shadow", bytes.NewReader(badBody))
	badReq.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{gwPeer}}}
	badRec := httptest.NewRecorder()
	realHandler.ServeHTTP(badRec, badReq)
	if badRec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 on invalid clusterPolicy bounds, got %d body=%s", badRec.Code, badRec.Body.String())
	}
}
