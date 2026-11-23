package optimization

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
)

type memoryEdgeModelStore struct {
	mu          sync.Mutex
	seq         int64
	artifacts   []EdgeModelArtifactRecord
	approvals   []EdgeModelApprovalRecord
	deployments []EdgeModelDeploymentRecord
	evaluations []EdgeModelEvaluationRecord
	drifts      []EdgeModelDriftRecord
	rollbacks   []EdgeModelRollbackRecord
}

func newMemoryEdgeModelStore() *memoryEdgeModelStore {
	return &memoryEdgeModelStore{}
}

func (s *memoryEdgeModelStore) LoadEdgeModelSiteSnapshot(_ context.Context, tenantID, siteID string) (EdgeModelSiteSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	filter := func() EdgeModelSiteSnapshot {
		var out EdgeModelSiteSnapshot
		for _, rec := range s.artifacts {
			if rec.TenantID == tenantID && rec.SiteID == siteID {
				out.Artifacts = append(out.Artifacts, rec)
			}
		}
		for _, rec := range s.approvals {
			if rec.TenantID == tenantID && rec.SiteID == siteID {
				out.Approvals = append(out.Approvals, rec)
			}
		}
		for _, rec := range s.deployments {
			if rec.TenantID == tenantID && rec.SiteID == siteID {
				out.Deployments = append(out.Deployments, rec)
			}
		}
		for _, rec := range s.evaluations {
			if rec.TenantID == tenantID && rec.SiteID == siteID {
				out.Evaluations = append(out.Evaluations, rec)
			}
		}
		for _, rec := range s.drifts {
			if rec.TenantID == tenantID && rec.SiteID == siteID {
				out.DriftObservations = append(out.DriftObservations, rec)
			}
		}
		for _, rec := range s.rollbacks {
			if rec.TenantID == tenantID && rec.SiteID == siteID {
				out.Rollbacks = append(out.Rollbacks, rec)
			}
		}
		slices.Reverse(out.Approvals)
		slices.Reverse(out.Deployments)
		slices.Reverse(out.Evaluations)
		return out
	}
	return filter(), nil
}

func (s *memoryEdgeModelStore) InsertEdgeModelArtifact(_ context.Context, record EdgeModelArtifactRecord) (EdgeModelArtifactRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, prev := range s.artifacts {
		if prev.TenantID == record.TenantID && prev.VersionID == record.VersionID {
			return EdgeModelArtifactRecord{}, ErrEdgeModelConflict
		}
	}
	s.artifacts = append(s.artifacts, record)
	return record, nil
}

func (s *memoryEdgeModelStore) InsertEdgeModelApproval(_ context.Context, record EdgeModelApprovalRecord) (EdgeModelApprovalRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.approvals = append(s.approvals, record)
	return record, nil
}

func (s *memoryEdgeModelStore) DeployEdgeModelVersion(_ context.Context, tenantID, siteID string, artifact EdgeModelArtifactRecord, approvalID, environment, servingRef string, at time.Time) (EdgeModelDeploymentRecord, []EdgeModelDeploymentRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	byVer := map[string]EdgeModelArtifactRecord{}
	for _, a := range s.artifacts {
		byVer[a.VersionID] = a
	}
	now := at.UTC()
	for i := range s.deployments {
		dep := &s.deployments[i]
		if dep.TenantID == tenantID && dep.SiteID == siteID && dep.Status == "ACTIVE" && dep.Environment == environment && dep.Domain == artifact.Domain && byVer[dep.VersionID].Metadata.ModelKey == artifact.Metadata.ModelKey {
			effTo := now
			if !effTo.After(dep.EffectiveFrom) {
				effTo = dep.EffectiveFrom.Add(time.Millisecond)
			}
			dep.Status = "RETIRED"
			dep.EffectiveTo = &effTo
			dep.UpdatedAt = now
		}
	}
	s.seq++
	rec := EdgeModelDeploymentRecord{
		ID:            deterministicV7("mem_edge_deploy", tenantID+"|"+artifact.VersionID, now.Add(time.Duration(s.seq)*time.Millisecond)),
		TenantID:      tenantID,
		SiteID:        siteID,
		Domain:        artifact.Domain,
		VersionID:     artifact.VersionID,
		ApprovalID:    approvalID,
		Environment:   environment,
		Status:        "ACTIVE",
		ServingRef:    servingRef,
		EffectiveFrom: now,
		Revision:      s.seq,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	s.deployments = append(s.deployments, rec)
	return rec, slices.Clone(s.deployments), nil
}

func (s *memoryEdgeModelStore) RollbackEdgeModelDeployment(_ context.Context, tenantID, siteID, deploymentID string, targetArtifact *EdgeModelArtifactRecord, targetApprovalID, reason, actorType, actorID, servingRef string, at time.Time) (EdgeModelRollbackRecord, *EdgeModelDeploymentRecord, []EdgeModelDeploymentRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := at.UTC()
	var current *EdgeModelDeploymentRecord
	for i := range s.deployments {
		if s.deployments[i].ID == deploymentID && s.deployments[i].TenantID == tenantID && s.deployments[i].SiteID == siteID {
			current = &s.deployments[i]
			break
		}
	}
	if current == nil {
		return EdgeModelRollbackRecord{}, nil, nil, ErrEdgeModelNotFound
	}
	if current.Status != "ACTIVE" {
		return EdgeModelRollbackRecord{}, nil, nil, ErrEdgeModelRolloutConflict
	}
	effTo := now
	if !effTo.After(current.EffectiveFrom) {
		effTo = current.EffectiveFrom.Add(time.Millisecond)
	}
	current.Status = "ROLLED_BACK"
	current.EffectiveTo = &effTo
	current.UpdatedAt = now

	var restored *EdgeModelDeploymentRecord
	toVer := current.VersionID
	if targetArtifact != nil {
		toVer = targetArtifact.VersionID
		s.seq++
		rec := EdgeModelDeploymentRecord{
			ID:            deterministicV7("mem_edge_restore", tenantID+"|"+toVer, now.Add(time.Duration(s.seq)*time.Millisecond)),
			TenantID:      tenantID,
			SiteID:        siteID,
			Domain:        targetArtifact.Domain,
			VersionID:     targetArtifact.VersionID,
			ApprovalID:    targetApprovalID,
			Environment:   current.Environment,
			Status:        "ACTIVE",
			ServingRef:    servingRef,
			EffectiveFrom: now,
			Revision:      s.seq,
			CreatedAt:     now,
			UpdatedAt:     now,
		}
		s.deployments = append(s.deployments, rec)
		restored = &rec
	}
	applied := now
	rb := EdgeModelRollbackRecord{
		ID:            deterministicV7("mem_edge_rb", deploymentID, now),
		TenantID:      tenantID,
		SiteID:        siteID,
		DeploymentID:  deploymentID,
		FromVersionID: current.VersionID,
		ToVersionID:   toVer,
		Reason:        reason,
		ActorType:     actorType,
		ActorID:       actorID,
		Status:        "APPLIED",
		RequestedAt:   now,
		AppliedAt:     &applied,
		CreatedAt:     now,
	}
	s.rollbacks = append(s.rollbacks, rb)
	return rb, restored, slices.Clone(s.deployments), nil
}

func (s *memoryEdgeModelStore) InsertEdgeModelEvaluation(_ context.Context, eval EdgeModelEvaluationRecord, drift *EdgeModelDriftRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.evaluations = append(s.evaluations, eval)
	if drift != nil {
		s.drifts = append(s.drifts, *drift)
	}
	return nil
}

func TestHTTPEdgeModelLifecycleGuardrailsAndRollback(t *testing.T) {
	t.Parallel()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	fixedNow := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	nowCursor := fixedNow
	fixture := newOptimizationFixture(t)
	fixture.service.clock = func() time.Time { return nowCursor }
	store := newMemoryEdgeModelStore()
	fixture.service.
		WithEdgeModelStore(store).
		WithEdgeModelSigningKey("edge-k1", priv).
		WithSiteVisibility(stubSiteVisibility{tenantID: testTenant, siteID: testSite})

	base := "/v1/sites/" + testSite + "/edge-models"
	gwID := "018f3d00-2000-7000-8000-000000000001"

	// 1. Physical bounds / EDGE-003 rejection returns 422.
	badSafety := `{"domain":"EDGE_CONTROL","modelKey":"chiller-cop-opt","version":"v0.1.0","hyperparams":{"supplyTempSetpointMinC":2.0,"targetPointCodes":["CHW_SUPPLY_TEMP_SP"]}}`
	badResp := fixture.call(t, http.MethodPost, base+"/artifacts", badSafety, testGatewaySPIFFE, testGatewaySPIFFE, WriteAction, testTenant, testSite)
	if badResp.Code != http.StatusUnprocessableEntity {
		t.Fatalf("out-of-bounds artifact status=%d body=%s, want 422", badResp.Code, badResp.Body.String())
	}
	badHardRT := `{"domain":"EDGE_CONTROL","modelKey":"chiller-cop-opt","version":"v0.2.0","hyperparams":{"supplyTempSetpointMinC":7.0,"targetPointCodes":["HIGH_PRESSURE_TRIP_STATUS"]}}`
	if resp := fixture.call(t, http.MethodPost, base+"/artifacts", badHardRT, testGatewaySPIFFE, testGatewaySPIFFE, WriteAction, testTenant, testSite); resp.Code != http.StatusUnprocessableEntity {
		t.Fatalf("EDGE-003 protected point status=%d body=%s, want 422", resp.Code, resp.Body.String())
	}

	// 2. Register v1 and v2.
	v1Body := `{"domain":"EDGE_CONTROL","modelKey":"chiller-cop-opt","version":"1.0.0","name":"冷机 COP 基准","maxShadowDeviation":1.2,"gatewayId":"` + gwID + `","hyperparams":{"copCoefficients":[3.8,0.06,-0.002],"supplyTempSetpointMinC":6.5,"supplyTempSetpointMaxC":10.5,"pumpFrequencyMinHz":28,"pumpFrequencyMaxHz":50,"stageUpLoadPct":82,"stageDownLoadPct":45}}`
	v1Resp := fixture.call(t, http.MethodPost, base+"/artifacts", v1Body, testGatewaySPIFFE, testGatewaySPIFFE, WriteAction, testTenant, testSite)
	if v1Resp.Code != http.StatusCreated {
		t.Fatalf("create v1 status=%d body=%s", v1Resp.Code, v1Resp.Body.String())
	}
	var artV1 EdgeModelArtifactView
	if err := json.Unmarshal(v1Resp.Body.Bytes(), &artV1); err != nil {
		t.Fatal(err)
	}

	nowCursor = nowCursor.Add(time.Minute)
	v2Body := `{"domain":"EDGE_CONTROL","modelKey":"chiller-cop-opt","version":"1.1.0","name":"冷机 COP 调优候选","maxShadowDeviation":0.8,"gatewayId":"` + gwID + `","hyperparams":{"copCoefficients":[4.1,0.08,-0.003],"supplyTempSetpointMinC":7.0,"supplyTempSetpointMaxC":11.5,"pumpFrequencyMinHz":30,"pumpFrequencyMaxHz":50,"stageUpLoadPct":85,"stageDownLoadPct":48}}`
	v2Resp := fixture.call(t, http.MethodPost, base+"/artifacts", v2Body, testGatewaySPIFFE, testGatewaySPIFFE, WriteAction, testTenant, testSite)
	if v2Resp.Code != http.StatusCreated {
		t.Fatalf("create v2 status=%d body=%s", v2Resp.Code, v2Resp.Body.String())
	}
	var artV2 EdgeModelArtifactView
	if err := json.Unmarshal(v2Resp.Body.Bytes(), &artV2); err != nil {
		t.Fatal(err)
	}

	// 3. Deploying without approval returns 409 Conflict.
	unapprovedDeploy := fixture.call(t, http.MethodPost, base+"/deployments", `{"artifactId":"`+artV1.ID+`","environment":"PRODUCTION"}`, testGatewaySPIFFE, testGatewaySPIFFE, WriteAction, testTenant, testSite)
	if unapprovedDeploy.Code != http.StatusConflict {
		t.Fatalf("unapproved deploy status=%d body=%s, want 409", unapprovedDeploy.Code, unapprovedDeploy.Body.String())
	}

	// 4. Approve v1 & v2, deploy v1 to PRODUCTION, v2 to SHADOW.
	for _, id := range []string{artV1.ID, artV2.ID} {
		nowCursor = nowCursor.Add(time.Minute)
		apResp := fixture.call(t, http.MethodPost, base+"/artifacts/"+id+"/approve", `{"decision":"APPROVED","reason":"离线仿真达标"}`, testGatewaySPIFFE, testGatewaySPIFFE, WriteAction, testTenant, testSite)
		if apResp.Code != http.StatusOK {
			t.Fatalf("approve %s status=%d body=%s", id, apResp.Code, apResp.Body.String())
		}
	}
	nowCursor = nowCursor.Add(time.Minute)
	d1Resp := fixture.call(t, http.MethodPost, base+"/deployments", `{"artifactId":"`+artV1.ID+`","environment":"PRODUCTION","gatewayId":"`+gwID+`"}`, testGatewaySPIFFE, testGatewaySPIFFE, WriteAction, testTenant, testSite)
	if d1Resp.Code != http.StatusOK {
		t.Fatalf("deploy v1 status=%d body=%s", d1Resp.Code, d1Resp.Body.String())
	}
	var dep1Res EdgeModelDeploymentResult
	if err := json.Unmarshal(d1Resp.Body.Bytes(), &dep1Res); err != nil {
		t.Fatal(err)
	}
	if len(dep1Res.DesiredBundles) != 1 || dep1Res.DesiredBundles[0].Signature == "" || dep1Res.DesiredBundles[0].RolloutMode != edgecontrol.EdgeModelRolloutActive {
		t.Fatalf("unexpected v1 desired bundles: %+v", dep1Res.DesiredBundles)
	}

	nowCursor = nowCursor.Add(time.Minute)
	d2Resp := fixture.call(t, http.MethodPost, base+"/deployments", `{"artifactId":"`+artV2.ID+`","environment":"PRODUCTION","gatewayId":"`+gwID+`"}`, testGatewaySPIFFE, testGatewaySPIFFE, WriteAction, testTenant, testSite)
	if d2Resp.Code != http.StatusOK {
		t.Fatalf("deploy v2 status=%d body=%s", d2Resp.Code, d2Resp.Body.String())
	}
	var dep2Res EdgeModelDeploymentResult
	if err := json.Unmarshal(d2Resp.Body.Bytes(), &dep2Res); err != nil {
		t.Fatal(err)
	}

	// 5. Record breaching shadow evaluation -> drift observation recorded as BREACHED.
	nowCursor = nowCursor.Add(time.Minute)
	evResp := fixture.call(t, http.MethodPost, base+"/evaluations", `{"artifactId":"`+artV2.ID+`","deploymentId":"`+dep2Res.Deployment.ID+`","evaluationType":"SHADOW","sampleCount":120,"meanAbsDeviation":0.45,"maxAbsDeviation":1.15,"notes":"影子输出超阈值"}`, testGatewaySPIFFE, testGatewaySPIFFE, WriteAction, testTenant, testSite)
	if evResp.Code != http.StatusCreated {
		t.Fatalf("eval status=%d body=%s", evResp.Code, evResp.Body.String())
	}
	var evalOut EdgeModelEvaluationResult
	if err := json.Unmarshal(evResp.Body.Bytes(), &evalOut); err != nil {
		t.Fatal(err)
	}
	if !evalOut.Breached || evalOut.DriftObservation == nil || evalOut.DriftObservation.DriftStatus != "BREACHED" {
		t.Fatalf("expected breached drift observation, got %+v", evalOut)
	}

	// 6. Rollback v2 restores v1 in desiredBundles.
	nowCursor = nowCursor.Add(time.Minute)
	rbResp := fixture.call(t, http.MethodPost, base+"/deployments/"+dep2Res.Deployment.ID+"/rollback", `{"reason":"影子偏差超阈值回退"}`, testGatewaySPIFFE, testGatewaySPIFFE, WriteAction, testTenant, testSite)
	if rbResp.Code != http.StatusOK {
		t.Fatalf("rollback status=%d body=%s", rbResp.Code, rbResp.Body.String())
	}
	var rbOut EdgeModelRollbackResult
	if err := json.Unmarshal(rbResp.Body.Bytes(), &rbOut); err != nil {
		t.Fatal(err)
	}
	if rbOut.RestoredDeployment == nil || rbOut.RestoredDeployment.VersionID != artV1.VersionID || len(rbOut.DesiredBundles) != 1 || rbOut.DesiredBundles[0].Version != "1.0.0" {
		t.Fatalf("expected v1 restored on rollback, got %+v", rbOut)
	}

	// 7. Workspace GET aggregates all records + RBAC denies Read/Write cross-action.
	wsResp := fixture.call(t, http.MethodGet, base, "", testGatewaySPIFFE, testGatewaySPIFFE, ReadAction, testTenant, testSite)
	if wsResp.Code != http.StatusOK {
		t.Fatalf("workspace status=%d body=%s", wsResp.Code, wsResp.Body.String())
	}
	var ws EdgeModelWorkspace
	if err := json.Unmarshal(wsResp.Body.Bytes(), &ws); err != nil {
		t.Fatal(err)
	}
	if len(ws.Artifacts) != 2 || len(ws.Rollbacks) != 1 || len(ws.DriftObservations) != 1 {
		t.Fatalf("unexpected workspace counts: artifacts=%d rollbacks=%d drifts=%d", len(ws.Artifacts), len(ws.Rollbacks), len(ws.DriftObservations))
	}
	if forbidden := fixture.call(t, http.MethodGet, base, "", testGatewaySPIFFE, testGatewaySPIFFE, WriteAction, testTenant, testSite); forbidden.Code != http.StatusForbidden {
		t.Fatalf("GET with WriteAction status=%d, want 403", forbidden.Code)
	}
}
