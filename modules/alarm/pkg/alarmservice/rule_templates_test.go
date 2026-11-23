package alarmservice

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/observability"
)

func signedTenantContext(t *testing.T, signer *ecdsa.PrivateKey, now time.Time, action, tenantID string) string {
	t.Helper()
	value, err := identitycontext.SignDelegation(signer, identitycontext.DelegationClaims{
		Issuer: DefaultGatewaySPIFFEID, Subject: "operator", SubjectIssuer: "https://identity.example.test",
		DisplayName: "Operator", ExecutingService: DefaultGatewaySPIFFEID, Audience: DefaultAudience,
		TenantID: tenantID, Actions: []string{action}, Scopes: []string{"tenant:" + tenantID},
		PolicyRevision: "policy-1", SessionID: "session-1", IssuedAt: now.Add(-time.Second).Unix(),
		ExpiresAt: now.Add(30 * time.Second).Unix(), TokenID: "id-tenant-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	return value
}

type stubRuleTemplateStore struct {
	mu        sync.Mutex
	seq       int
	templates map[string]RuleBaselineTemplate
	releases  map[string][]RuleBaselineTemplateRelease
	siteRules map[string][]SiteRule
	siteRev   map[string]uint64
}

func newStubRuleTemplateStore() *stubRuleTemplateStore {
	return &stubRuleTemplateStore{
		templates: make(map[string]RuleBaselineTemplate),
		releases:  make(map[string][]RuleBaselineTemplateRelease),
		siteRules: make(map[string][]SiteRule),
		siteRev:   make(map[string]uint64),
	}
}

func (s *stubRuleTemplateStore) nextUUID() string {
	s.seq++
	return fmt.Sprintf("01950000-0000-7000-8000-%012d", s.seq)
}

func (s *stubRuleTemplateStore) ListRuleTemplates(_ context.Context, _ string, statusFilter string) ([]RuleBaselineTemplate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]RuleBaselineTemplate, 0, len(s.templates))
	for _, t := range s.templates {
		if statusFilter != "" && t.Status != statusFilter {
			continue
		}
		out = append(out, t)
	}
	return out, nil
}

func (s *stubRuleTemplateStore) GetRuleTemplate(_ context.Context, _ string, templateID string) (RuleBaselineTemplateDetail, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.templates[templateID]
	if !ok {
		return RuleBaselineTemplateDetail{}, ErrNotFound
	}
	return RuleBaselineTemplateDetail{Template: t, Releases: slices.Clone(s.releases[templateID])}, nil
}

func (s *stubRuleTemplateStore) CreateRuleTemplate(_ context.Context, _ string, req CreateRuleTemplateRequest, actorID, actorName string, now time.Time) (RuleBaselineTemplateDetail, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.templates {
		if existing.TemplateKey == req.TemplateKey {
			return RuleBaselineTemplateDetail{}, ErrRuleTemplateConflict
		}
	}
	id := s.nextUUID()
	t := RuleBaselineTemplate{
		TemplateID:             id,
		TemplateKey:            req.TemplateKey,
		DisplayName:            req.DisplayName,
		Description:            req.Description,
		DeviceType:             req.DeviceType,
		ThingModelCode:         req.ThingModelCode,
		AlarmType:              req.AlarmType,
		RuleKind:               req.RuleKind,
		DefaultSeverity:        req.DefaultSeverity,
		DefaultExecutionTarget: req.DefaultExecutionTarget,
		BoundSiteIDs:           slices.Clone(req.BoundSiteIDs),
		Status:                 TemplateStatusActive,
		CreatedBy:              actorID,
		UpdatedBy:              actorID,
		CreatedAt:              now.UTC().Format(time.RFC3339Nano),
		UpdatedAt:              now.UTC().Format(time.RFC3339Nano),
	}
	if t.BoundSiteIDs == nil {
		t.BoundSiteIDs = []string{}
	}
	if req.InitialSpec != nil {
		spec := *req.InitialSpec
		if err := normalizeAndValidateTemplateSpec(&spec, t); err != nil {
			return RuleBaselineTemplateDetail{}, err
		}
		t.LatestVersion = 1
		t.LatestSpec = &spec
		s.releases[id] = []RuleBaselineTemplateRelease{{
			ReleaseID:       s.nextUUID(),
			TemplateID:      id,
			Version:         1,
			RuleSpec:        spec,
			ChangeNote:      req.ChangeNote,
			PublishedBy:     actorID,
			PublishedByName: actorName,
			PublishedAt:     now.UTC().Format(time.RFC3339Nano),
		}}
	}
	s.templates[id] = t
	return RuleBaselineTemplateDetail{Template: t, Releases: slices.Clone(s.releases[id])}, nil
}

func (s *stubRuleTemplateStore) UpdateRuleTemplate(_ context.Context, _ string, templateID string, req UpdateRuleTemplateRequest, actorID, _ string, now time.Time) (RuleBaselineTemplate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.templates[templateID]
	if !ok {
		return RuleBaselineTemplate{}, ErrNotFound
	}
	t.DisplayName = req.DisplayName
	t.Description = req.Description
	t.DeviceType = req.DeviceType
	t.ThingModelCode = req.ThingModelCode
	t.DefaultSeverity = req.DefaultSeverity
	t.DefaultExecutionTarget = req.DefaultExecutionTarget
	if req.Status != "" {
		t.Status = req.Status
	}
	t.UpdatedBy = actorID
	t.UpdatedAt = now.UTC().Format(time.RFC3339Nano)
	s.templates[templateID] = t
	return t, nil
}

func (s *stubRuleTemplateStore) ListRuleTemplateReleases(_ context.Context, _ string, templateID string, _ int) ([]RuleBaselineTemplateRelease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.templates[templateID]; !ok {
		return nil, ErrNotFound
	}
	return slices.Clone(s.releases[templateID]), nil
}

func (s *stubRuleTemplateStore) PublishRuleTemplateRelease(_ context.Context, _ string, templateID string, req PublishRuleTemplateReleaseRequest, actorID, actorName string, now time.Time) (RuleBaselineTemplateRelease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.templates[templateID]
	if !ok {
		return RuleBaselineTemplateRelease{}, ErrNotFound
	}
	spec := req.RuleSpec
	if err := normalizeAndValidateTemplateSpec(&spec, t); err != nil {
		return RuleBaselineTemplateRelease{}, err
	}
	t.LatestVersion++
	t.LatestSpec = &spec
	t.DefaultSeverity = spec.Severity
	t.DefaultExecutionTarget = spec.ExecutionTarget
	t.UpdatedBy = actorID
	t.UpdatedAt = now.UTC().Format(time.RFC3339Nano)
	s.templates[templateID] = t
	rel := RuleBaselineTemplateRelease{
		ReleaseID:       s.nextUUID(),
		TemplateID:      templateID,
		Version:         t.LatestVersion,
		RuleSpec:        spec,
		ChangeNote:      req.ChangeNote,
		PublishedBy:     actorID,
		PublishedByName: actorName,
		PublishedAt:     now.UTC().Format(time.RFC3339Nano),
	}
	s.releases[templateID] = append([]RuleBaselineTemplateRelease{rel}, s.releases[templateID]...)
	return rel, nil
}

func (s *stubRuleTemplateStore) UpdateRuleTemplateSites(_ context.Context, _ string, templateID string, siteIDs []string, actorID string, now time.Time) (RuleBaselineTemplate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.templates[templateID]
	if !ok {
		return RuleBaselineTemplate{}, ErrNotFound
	}
	cleaned, err := validateBoundSiteList(siteIDs)
	if err != nil {
		return RuleBaselineTemplate{}, err
	}
	t.BoundSiteIDs = cleaned
	t.UpdatedBy = actorID
	t.UpdatedAt = now.UTC().Format(time.RFC3339Nano)
	s.templates[templateID] = t
	return t, nil
}

func (s *stubRuleTemplateStore) PreviewRuleTemplateRollout(_ context.Context, _ string, templateID string, req PreviewRuleTemplateRolloutRequest) (RuleTemplateRolloutPreview, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.templates[templateID]
	if !ok {
		return RuleTemplateRolloutPreview{}, ErrNotFound
	}
	if t.LatestSpec == nil {
		return RuleTemplateRolloutPreview{}, &RuleTemplateValidationError{Code: "ALARM_TEMPLATE_NO_RELEASE", Message: "no release"}
	}
	targets, err := normalizeTargetSites(t.BoundSiteIDs, req.SiteIDs, req.Targets)
	if err != nil {
		return RuleTemplateRolloutPreview{}, err
	}
	prev := RuleTemplateRolloutPreview{
		TemplateID: t.TemplateID, TemplateKey: t.TemplateKey, DisplayName: t.DisplayName, TemplateVersion: t.LatestVersion,
	}
	for _, tgt := range targets {
		sp := RuleTemplateSitePreview{SiteID: tgt.SiteID, BundleRevision: s.siteRev[tgt.SiteID], RuleDiffs: []RuleTemplateSiteRuleDiff{}}
		for _, r := range s.siteRules[tgt.SiteID] {
			if r.Policy.TemplateKey != t.TemplateKey {
				continue
			}
			sp.MatchedRuleCount++
			_, changed, preserved, conflicts, _, err := compareRuleWithTemplate(r, *t.LatestSpec)
			if err != nil {
				return RuleTemplateRolloutPreview{}, err
			}
			act := "UNCHANGED"
			if len(changed) > 0 {
				act = "UPDATE"
				sp.WillUpdateCount++
			}
			sp.PreservedOverrideCount += len(preserved)
			sp.ConflictCount += len(conflicts)
			sp.RuleDiffs = append(sp.RuleDiffs, RuleTemplateSiteRuleDiff{
				RuleID: r.AssignmentID, RuleTitle: r.Policy.Title, Action: act,
				ChangedFields: changed, PreservedOverrides: preserved, Conflicts: conflicts,
			})
		}
		if sp.WillUpdateCount > 0 {
			prev.TotalChangedSites++
		}
		prev.TotalPreservedOverrides += sp.PreservedOverrideCount
		prev.TotalConflicts += sp.ConflictCount
		prev.Sites = append(prev.Sites, sp)
	}
	return prev, nil
}

func (s *stubRuleTemplateStore) ExecuteRuleTemplateRollout(_ context.Context, _ string, templateID string, req ExecuteRuleTemplateRolloutRequest, actorID, _ string, now time.Time) (RuleTemplateRolloutResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.templates[templateID]
	if !ok || t.LatestSpec == nil {
		return RuleTemplateRolloutResult{}, ErrNotFound
	}
	targets, err := normalizeTargetSites(t.BoundSiteIDs, req.SiteIDs, req.Targets)
	if err != nil {
		return RuleTemplateRolloutResult{}, err
	}
	res := RuleTemplateRolloutResult{BatchID: s.nextUUID(), TemplateID: t.TemplateID, TemplateVersion: t.LatestVersion, CreatedBy: actorID, CreatedAt: now.UTC().Format(time.RFC3339Nano)}
	for _, tgt := range targets {
		rules := s.siteRules[tgt.SiteID]
		var changedRules, preservedCount, conflictCount int
		for idx, r := range rules {
			if r.Policy.TemplateKey != t.TemplateKey {
				continue
			}
			merged, changed, preserved, conflicts, _, err := compareRuleWithTemplate(r, *t.LatestSpec)
			if err != nil {
				return RuleTemplateRolloutResult{}, err
			}
			preservedCount += len(preserved)
			conflictCount += len(conflicts)
			if len(changed) > 0 {
				changedRules++
				rules[idx].Policy = merged
			}
		}
		s.siteRules[tgt.SiteID] = rules
		status := "SKIPPED"
		if changedRules > 0 {
			s.siteRev[tgt.SiteID]++
			status = "APPLIED"
			res.TotalAppliedSites++
		}
		res.TotalPreservedOverrides += preservedCount
		res.TotalConflicts += conflictCount
		res.Outcomes = append(res.Outcomes, RuleTemplateSiteRolloutOutcome{
			SiteID: tgt.SiteID, Status: status, BundleRevision: s.siteRev[tgt.SiteID],
			PreservedOverrideCount: preservedCount, ConflictCount: conflictCount, ChangedRuleCount: changedRules,
		})
	}
	return res, nil
}

func (s *stubRuleTemplateStore) GetSiteRuleTemplateDrift(_ context.Context, _ string, siteID string) (SiteRuleTemplateDriftReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rep := SiteRuleTemplateDriftReport{SiteID: siteID, BundleRevision: s.siteRev[siteID], Items: []SiteRuleTemplateDriftItem{}}
	for _, r := range s.siteRules[siteID] {
		if r.Policy.TemplateKey == "" {
			continue
		}
		for _, t := range s.templates {
			if t.TemplateKey == r.Policy.TemplateKey && t.LatestSpec != nil {
				_, _, _, _, diffs, err := compareRuleWithTemplate(r, *t.LatestSpec)
				if err != nil {
					return SiteRuleTemplateDriftReport{}, err
				}
				var drifted []string
				for _, d := range diffs {
					if d.Drifted {
						drifted = append(drifted, d.Field)
					}
				}
				if len(r.Policy.OverriddenFields) > 0 || len(drifted) > 0 {
					rep.Items = append(rep.Items, SiteRuleTemplateDriftItem{
						RuleID: r.AssignmentID, RuleTitle: r.Policy.Title, TemplateID: t.TemplateID,
						TemplateKey: t.TemplateKey, TemplateName: t.DisplayName, TemplateVersion: t.LatestVersion,
						OverriddenFields: slices.Clone(r.Policy.OverriddenFields), DriftedFields: drifted, Diffs: diffs,
					})
				}
			}
		}
	}
	return rep, nil
}

func (s *stubRuleTemplateStore) ResetSiteRuleToTemplate(_ context.Context, _ string, siteID, ruleID string, req ResetSiteRuleToTemplateRequest, actorID, actorName string, now time.Time) (SiteRulePublishResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rules := s.siteRules[siteID]
	for idx, r := range rules {
		if r.AssignmentID != ruleID {
			continue
		}
		for _, t := range s.templates {
			if t.TemplateKey == r.Policy.TemplateKey && t.LatestSpec != nil {
				nextPolicy, err := resetSiteRulePolicyFields(r, *t.LatestSpec, req.Fields)
				if err != nil {
					return SiteRulePublishResult{}, err
				}
				rules[idx].Policy = nextPolicy
				s.siteRules[siteID] = rules
				s.siteRev[siteID]++
				return SiteRulePublishResult{
					BundleRevision: s.siteRev[siteID], BundleDigest: strings.Repeat("d", 64),
					Source: RuleBundleSourceTemplateRollout, Changed: true, ChangeNote: req.ChangeNote,
					PublishedAt: now.UTC().Format(time.RFC3339Nano), PublishedBy: actorID, PublishedByName: actorName,
					Summary: SiteRuleSyncSummary{Changed: 1, ChangedRuleIDs: []string{ruleID}, AddedRuleIDs: []string{}, UnchangedRuleIDs: []string{}, RetiredRuleIDs: []string{}},
					Graph:   SiteRuleGraphView{Nodes: []SiteRuleGraphNodeView{}, Edges: []SiteRuleGraphEdgeView{}, TopologicalOrder: []string{ruleID}},
					Items:   []SiteRuleView{},
				}, nil
			}
		}
	}
	return SiteRulePublishResult{}, ErrNotFound
}

func TestCompareAndResetTemplatePreservesOverridesAndRestoresDefaults(t *testing.T) {
	siteRule := supplyTooWarmRule()
	siteRule.Policy.TemplateKey = "chw-supply-temp-high"
	siteRule.Policy.ExecutionTarget = ExecutionTargetCloud
	siteRule.Policy.OverriddenFields = []string{"raise"}
	// Site intentionally overrode raise threshold to 14.0 whereas template baseline is 11.5 (upgraded from 12.0).
	siteRule.Policy.Raise = Condition{
		Kind:     ConditionCompare,
		Input:    testDeviceID + "/supply_water_temperature",
		Operator: CompareGT,
		Value:    NumberValue(14.0),
	}

	tmplSpec := RuleTemplateSpec{
		Title:           "冷冻水供水温度过高",
		Severity:        alarmmodel.SeverityCritical,
		RuleKind:        RuleKindThreshold,
		ExecutionTarget: ExecutionTargetCloudAndEdge,
		TriggerMode:     TriggerDuration,
		DurationSeconds: 180,
		Raise: RuleTemplateCondition{
			Kind:      ConditionCompare,
			PointCode: "supply_water_temperature",
			Operator:  CompareGT,
			Value:     11.5,
		},
		Clear: RuleTemplateCondition{
			Kind:      ConditionCompare,
			PointCode: "supply_water_temperature",
			Operator:  CompareLTE,
			Value:     10.5,
		},
	}

	merged, changedFields, preservedFields, conflicts, diffs, err := compareRuleWithTemplate(siteRule, tmplSpec)
	if err != nil {
		t.Fatalf("compareRuleWithTemplate failed: %v", err)
	}
	// raise was overridden by the site: MUST stay at 14.0 and report conflict!
	if !slices.Contains(preservedFields, "raise") {
		t.Fatalf("expected raise in preservedFields, got %v", preservedFields)
	}
	if merged.Raise.Value.Number != 14.0 {
		t.Fatalf("overridden raise threshold overwritten! got %v want 14.0", merged.Raise.Value.Number)
	}
	if len(conflicts) != 1 || conflicts[0].Field != "raise" || conflicts[0].Resolution != "PRESERVED_SITE_OVERRIDE" {
		t.Fatalf("expected 1 raise conflict with PRESERVED_SITE_OVERRIDE, got %+v", conflicts)
	}
	// Non-overridden fields like severity and executionTarget MUST be updated by template rollout.
	if !slices.Contains(changedFields, "severity") || !slices.Contains(changedFields, "executionTarget") {
		t.Fatalf("expected severity and executionTarget in changedFields, got %v", changedFields)
	}
	if merged.ExecutionTarget != ExecutionTargetCloudAndEdge || merged.Severity != alarmmodel.SeverityCritical {
		t.Fatalf("unexpected merged metadata: %+v", merged)
	}
	if len(diffs) < 10 {
		t.Fatalf("expected full field diff slice, got %d items", len(diffs))
	}

	// Single-site reset restores template baseline 11.5 while preserving site device UUIDv7 and clearing overriddenFields.
	resetPolicy, err := resetSiteRulePolicyFields(SiteRule{AssignmentID: siteRule.AssignmentID, Policy: merged}, tmplSpec, nil)
	if err != nil {
		t.Fatalf("resetSiteRulePolicyFields failed: %v", err)
	}
	if resetPolicy.Raise.Input != testDeviceID+"/supply_water_temperature" {
		t.Fatalf("reset dropped deviceId binding: %q", resetPolicy.Raise.Input)
	}
	if resetPolicy.Raise.Value.Number != 11.5 {
		t.Fatalf("expected reset raise threshold 11.5, got %v", resetPolicy.Raise.Value.Number)
	}
	if len(resetPolicy.OverriddenFields) != 0 {
		t.Fatalf("expected overriddenFields cleared after reset, got %v", resetPolicy.OverriddenFields)
	}
}

func TestRuleTemplateHTTPEndpointsAuthAndRolloutFlow(t *testing.T) {
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	signer := testSigner(t)
	memory, err := NewMemoryStore(nil)
	if err != nil {
		t.Fatal(err)
	}
	tmplStore := newStubRuleTemplateStore()
	siteRule := supplyTooWarmRule()
	siteRule.Policy.TemplateKey = "chiller-supply-high"
	siteRule.Policy.OverriddenFields = []string{"raise"}
	siteRule.Policy.Raise.Value = NumberValue(13.5)
	tmplStore.siteRules[testSiteID] = []SiteRule{siteRule}
	tmplStore.siteRev[testSiteID] = 3

	handler, err := NewHTTPHandler(HTTPConfig{
		Store:            memory,
		Rules:            siteRules{siteID: testSiteID, rules: []SiteRule{siteRule}},
		Templates:        tmplStore,
		Metrics:          observability.NewRegistry(),
		GatewayPublicKey: &signer.PublicKey,
		Now:              func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewHTTPHandler: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	client := server.Client()
	readTok := signedTenantContext(t, signer, now, AlarmListAction, testTenantID)
	writeTok := signedTenantContext(t, signer, now, AlarmRulePublishAction, testTenantID)

	// 1. Create without write context returns 403
	createPayload := `{"templateKey":"chiller-supply-high","displayName":"冷机出水超温基线","deviceType":"CHILLER","alarmType":"chiller/supply-high","ruleKind":"THRESHOLD","defaultSeverity":"WARNING","defaultExecutionTarget":"CLOUD_AND_EDGE","boundSiteIds":["` + testSiteID + `"],"initialSpec":{"title":"冷机出水超温","severity":"WARNING","executionTarget":"CLOUD_AND_EDGE","triggerMode":"DURATION","durationSeconds":120,"raise":{"kind":"COMPARE","pointCode":"supply_water_temperature","operator":"GT","value":12},"clear":{"kind":"COMPARE","pointCode":"supply_water_temperature","operator":"LTE","value":10}}}`
	reqUnauth, _ := http.NewRequest(http.MethodPost, server.URL+"/internal/v1/alarm-rule-templates", bytes.NewBufferString(createPayload))
	reqUnauth.Header.Set("Content-Type", "application/json")
	reqUnauth.Header.Set(AlarmReadContextHeader, readTok)
	respUnauth, err := client.Do(reqUnauth)
	if err != nil {
		t.Fatal(err)
	}
	_ = respUnauth.Body.Close()
	if respUnauth.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 with read token on POST template, got %d", respUnauth.StatusCode)
	}

	// 2. Authorized create returns 201 with v1 release
	reqCreate, _ := http.NewRequest(http.MethodPost, server.URL+"/internal/v1/alarm-rule-templates", bytes.NewBufferString(createPayload))
	reqCreate.Header.Set("Content-Type", "application/json")
	reqCreate.Header.Set(AlarmWriteContextHeader, writeTok)
	respCreate, err := client.Do(reqCreate)
	if err != nil {
		t.Fatal(err)
	}
	var detail RuleBaselineTemplateDetail
	if err := json.NewDecoder(respCreate.Body).Decode(&detail); err != nil {
		t.Fatal(err)
	}
	_ = respCreate.Body.Close()
	if respCreate.StatusCode != http.StatusCreated || detail.Template.LatestVersion != 1 || len(detail.Releases) != 1 {
		t.Fatalf("unexpected create response status=%d detail=%+v", respCreate.StatusCode, detail)
	}

	// 3. Preview rollout verifies conflict preserved
	reqPrev, _ := http.NewRequest(http.MethodPost, server.URL+"/internal/v1/alarm-rule-templates/"+detail.Template.TemplateID+":preview-rollout", bytes.NewBufferString(`{}`))
	reqPrev.Header.Set("Content-Type", "application/json")
	reqPrev.Header.Set(AlarmReadContextHeader, readTok)
	respPrev, err := client.Do(reqPrev)
	if err != nil {
		t.Fatal(err)
	}
	var preview RuleTemplateRolloutPreview
	if err := json.NewDecoder(respPrev.Body).Decode(&preview); err != nil {
		t.Fatal(err)
	}
	_ = respPrev.Body.Close()
	if respPrev.StatusCode != http.StatusOK || preview.TotalConflicts != 1 || preview.TotalPreservedOverrides != 1 {
		t.Fatalf("unexpected preview: status=%d preview=%+v", respPrev.StatusCode, preview)
	}

	// 4. Execute rollout applies non-overridden executionTarget while keeping site raise=13.5
	reqRoll, _ := http.NewRequest(http.MethodPost, server.URL+"/internal/v1/alarm-rule-templates/"+detail.Template.TemplateID+":rollout", bytes.NewBufferString(`{"changeNote":"统一云边协同"}`))
	reqRoll.Header.Set("Content-Type", "application/json")
	reqRoll.Header.Set(AlarmWriteContextHeader, writeTok)
	respRoll, err := client.Do(reqRoll)
	if err != nil {
		t.Fatal(err)
	}
	var rollRes RuleTemplateRolloutResult
	if err := json.NewDecoder(respRoll.Body).Decode(&rollRes); err != nil {
		t.Fatal(err)
	}
	_ = respRoll.Body.Close()
	if respRoll.StatusCode != http.StatusOK || rollRes.TotalAppliedSites != 1 || tmplStore.siteRules[testSiteID][0].Policy.Raise.Value.Number != 13.5 {
		t.Fatalf("unexpected rollout result: status=%d res=%+v", respRoll.StatusCode, rollRes)
	}

	// 5. Inspect drift & execute single-site reset using site-scoped token
	siteReadTok := signedReadContext(t, signer, now, AlarmListAction, testTenantID, testSiteID, "")
	siteWriteTok := signedReadContext(t, signer, now, AlarmRulePublishAction, testTenantID, testSiteID, "")

	reqDrift, _ := http.NewRequest(http.MethodGet, server.URL+"/internal/v1/sites/"+testSiteID+"/alarm-rules/template-drift", nil)
	reqDrift.Header.Set(AlarmReadContextHeader, siteReadTok)
	respDrift, err := client.Do(reqDrift)
	if err != nil {
		t.Fatal(err)
	}
	var drift SiteRuleTemplateDriftReport
	if err := json.NewDecoder(respDrift.Body).Decode(&drift); err != nil {
		t.Fatal(err)
	}
	_ = respDrift.Body.Close()
	if respDrift.StatusCode != http.StatusOK || len(drift.Items) != 1 || !slices.Contains(drift.Items[0].DriftedFields, "raise") {
		t.Fatalf("unexpected drift report: status=%d drift=%+v", respDrift.StatusCode, drift)
	}

	reqReset, _ := http.NewRequest(http.MethodPost, server.URL+"/internal/v1/sites/"+testSiteID+"/alarm-rules/"+siteRule.AssignmentID+":reset-template", bytes.NewBufferString(`{"changeNote":"一键恢复模板默认值"}`))
	reqReset.Header.Set("Content-Type", "application/json")
	reqReset.Header.Set(AlarmWriteContextHeader, siteWriteTok)
	respReset, err := client.Do(reqReset)
	if err != nil {
		t.Fatal(err)
	}
	var pubRes SiteRulePublishResult
	if err := json.NewDecoder(respReset.Body).Decode(&pubRes); err != nil {
		t.Fatal(err)
	}
	_ = respReset.Body.Close()
	if respReset.StatusCode != http.StatusOK || pubRes.Source != RuleBundleSourceTemplateRollout {
		t.Fatalf("unexpected reset response: status=%d pubRes=%+v", respReset.StatusCode, pubRes)
	}
	if len(tmplStore.siteRules[testSiteID][0].Policy.OverriddenFields) != 0 || tmplStore.siteRules[testSiteID][0].Policy.Raise.Value.Number != 12.0 {
		t.Fatalf("expected overriddenFields cleared and raise=12.0, got %+v", tmplStore.siteRules[testSiteID][0].Policy)
	}
}
