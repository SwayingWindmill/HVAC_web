package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/ownershipregistry"
	"github.com/quanlaihe/hvac-web/libs/sessionstore"
	"github.com/quanlaihe/hvac-web/modules/alarm/pkg/alarmservice"
)

type gatewayTestTemplateStore struct {
	mu        sync.Mutex
	seq       int
	templates map[string]alarmservice.RuleBaselineTemplate
	releases  map[string][]alarmservice.RuleBaselineTemplateRelease
	drift     map[string]alarmservice.SiteRuleTemplateDriftReport
	rolledOut []string
	resetIDs  []string
}

func newGatewayTestTemplateStore(siteID, ruleID string) *gatewayTestTemplateStore {
	return &gatewayTestTemplateStore{
		templates: make(map[string]alarmservice.RuleBaselineTemplate),
		releases:  make(map[string][]alarmservice.RuleBaselineTemplateRelease),
		drift: map[string]alarmservice.SiteRuleTemplateDriftReport{
			siteID: {
				SiteID:         siteID,
				BundleRevision: 4,
				Items: []alarmservice.SiteRuleTemplateDriftItem{{
					RuleID:           ruleID,
					RuleTitle:        "冷冻水供水温度过高",
					TemplateID:       "01950000-0000-7000-8000-000000000001",
					TemplateKey:      "chw-supply-high",
					TemplateName:     "冷冻水供温越限基线",
					TemplateVersion:  1,
					OverriddenFields: []string{"raise"},
					DriftedFields:    []string{"raise"},
					Diffs: []alarmservice.SiteRuleTemplateFieldDiff{{
						Field:         "raise",
						Label:         "触发条件",
						SiteValue:     "supply_water_temperature GT 14",
						TemplateValue: "supply_water_temperature GT 12",
						IsOverridden:  true,
						Drifted:       true,
					}},
				}},
			},
		},
	}
}

func (s *gatewayTestTemplateStore) nextUUID() string {
	s.seq++
	return fmt.Sprintf("01950000-0000-7000-8000-%012d", s.seq)
}

func (s *gatewayTestTemplateStore) ListRuleTemplates(_ context.Context, _, _ string) ([]alarmservice.RuleBaselineTemplate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]alarmservice.RuleBaselineTemplate, 0, len(s.templates))
	for _, t := range s.templates {
		out = append(out, t)
	}
	return out, nil
}

func (s *gatewayTestTemplateStore) GetRuleTemplate(_ context.Context, _, templateID string) (alarmservice.RuleBaselineTemplateDetail, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.templates[templateID]
	if !ok {
		return alarmservice.RuleBaselineTemplateDetail{}, alarmservice.ErrNotFound
	}
	return alarmservice.RuleBaselineTemplateDetail{Template: t, Releases: slices.Clone(s.releases[templateID])}, nil
}

func (s *gatewayTestTemplateStore) CreateRuleTemplate(_ context.Context, _ string, req alarmservice.CreateRuleTemplateRequest, actorID, actorName string, now time.Time) (alarmservice.RuleBaselineTemplateDetail, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.nextUUID()
	t := alarmservice.RuleBaselineTemplate{
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
		Status:                 alarmservice.TemplateStatusActive,
		CreatedBy:              actorID,
		UpdatedBy:              actorID,
		CreatedAt:              now.UTC().Format(time.RFC3339Nano),
		UpdatedAt:              now.UTC().Format(time.RFC3339Nano),
	}
	if t.BoundSiteIDs == nil {
		t.BoundSiteIDs = []string{}
	}
	if req.InitialSpec != nil {
		t.LatestVersion = 1
		spec := *req.InitialSpec
		t.LatestSpec = &spec
		s.releases[id] = []alarmservice.RuleBaselineTemplateRelease{{
			ReleaseID: s.nextUUID(), TemplateID: id, Version: 1, RuleSpec: spec,
			ChangeNote: req.ChangeNote, PublishedBy: actorID, PublishedByName: actorName,
			PublishedAt: now.UTC().Format(time.RFC3339Nano),
		}}
	}
	s.templates[id] = t
	return alarmservice.RuleBaselineTemplateDetail{Template: t, Releases: slices.Clone(s.releases[id])}, nil
}

func (s *gatewayTestTemplateStore) UpdateRuleTemplate(_ context.Context, _, templateID string, req alarmservice.UpdateRuleTemplateRequest, actorID, _ string, now time.Time) (alarmservice.RuleBaselineTemplate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.templates[templateID]
	if !ok {
		return alarmservice.RuleBaselineTemplate{}, alarmservice.ErrNotFound
	}
	t.DisplayName = req.DisplayName
	t.Description = req.Description
	t.DeviceType = req.DeviceType
	t.DefaultSeverity = req.DefaultSeverity
	t.DefaultExecutionTarget = req.DefaultExecutionTarget
	t.UpdatedBy = actorID
	t.UpdatedAt = now.UTC().Format(time.RFC3339Nano)
	s.templates[templateID] = t
	return t, nil
}

func (s *gatewayTestTemplateStore) ListRuleTemplateReleases(_ context.Context, _, templateID string, _ int) ([]alarmservice.RuleBaselineTemplateRelease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.templates[templateID]; !ok {
		return nil, alarmservice.ErrNotFound
	}
	return slices.Clone(s.releases[templateID]), nil
}

func (s *gatewayTestTemplateStore) PublishRuleTemplateRelease(_ context.Context, _, templateID string, req alarmservice.PublishRuleTemplateReleaseRequest, actorID, actorName string, now time.Time) (alarmservice.RuleBaselineTemplateRelease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.templates[templateID]
	if !ok {
		return alarmservice.RuleBaselineTemplateRelease{}, alarmservice.ErrNotFound
	}
	t.LatestVersion++
	spec := req.RuleSpec
	t.LatestSpec = &spec
	s.templates[templateID] = t
	rel := alarmservice.RuleBaselineTemplateRelease{
		ReleaseID: s.nextUUID(), TemplateID: templateID, Version: t.LatestVersion, RuleSpec: spec,
		ChangeNote: req.ChangeNote, PublishedBy: actorID, PublishedByName: actorName, PublishedAt: now.UTC().Format(time.RFC3339Nano),
	}
	s.releases[templateID] = append([]alarmservice.RuleBaselineTemplateRelease{rel}, s.releases[templateID]...)
	return rel, nil
}

func (s *gatewayTestTemplateStore) UpdateRuleTemplateSites(_ context.Context, _, templateID string, siteIDs []string, _ string, _ time.Time) (alarmservice.RuleBaselineTemplate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.templates[templateID]
	if !ok {
		return alarmservice.RuleBaselineTemplate{}, alarmservice.ErrNotFound
	}
	t.BoundSiteIDs = slices.Clone(siteIDs)
	s.templates[templateID] = t
	return t, nil
}

func (s *gatewayTestTemplateStore) PreviewRuleTemplateRollout(_ context.Context, _, templateID string, req alarmservice.PreviewRuleTemplateRolloutRequest) (alarmservice.RuleTemplateRolloutPreview, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.templates[templateID]
	if !ok {
		return alarmservice.RuleTemplateRolloutPreview{}, alarmservice.ErrNotFound
	}
	siteIDs := req.SiteIDs
	if len(siteIDs) == 0 {
		siteIDs = t.BoundSiteIDs
	}
	sites := make([]alarmservice.RuleTemplateSitePreview, 0, len(siteIDs))
	for _, sid := range siteIDs {
		sites = append(sites, alarmservice.RuleTemplateSitePreview{
			SiteID: sid, BundleRevision: 4, MatchedRuleCount: 1, WillUpdateCount: 1,
			PreservedOverrideCount: 1, ConflictCount: 1,
			RuleDiffs: []alarmservice.RuleTemplateSiteRuleDiff{{
				RuleID: "01910000-0000-7000-8000-000000000099", RuleTitle: "冷冻水供水温度过高",
				Action: "UPDATE", ChangedFields: []string{"executionTarget"}, PreservedOverrides: []string{"raise"},
				Conflicts: []alarmservice.RuleTemplateFieldConflict{{
					RuleID: "01910000-0000-7000-8000-000000000099", RuleTitle: "冷冻水供水温度过高",
					Field: "raise", FieldLabel: "触发条件", SiteValue: "14.0", TemplateValue: "12.0", Resolution: "PRESERVED_SITE_OVERRIDE",
				}},
			}},
		})
	}
	return alarmservice.RuleTemplateRolloutPreview{
		TemplateID: t.TemplateID, TemplateKey: t.TemplateKey, DisplayName: t.DisplayName,
		TemplateVersion: t.LatestVersion, Sites: sites, TotalChangedSites: len(sites),
		TotalPreservedOverrides: len(sites), TotalConflicts: len(sites),
	}, nil
}

func (s *gatewayTestTemplateStore) ExecuteRuleTemplateRollout(_ context.Context, _, templateID string, req alarmservice.ExecuteRuleTemplateRolloutRequest, actorID, _ string, now time.Time) (alarmservice.RuleTemplateRolloutResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.templates[templateID]
	if !ok {
		return alarmservice.RuleTemplateRolloutResult{}, alarmservice.ErrNotFound
	}
	siteIDs := req.SiteIDs
	if len(siteIDs) == 0 {
		siteIDs = t.BoundSiteIDs
	}
	s.rolledOut = append(s.rolledOut, siteIDs...)
	outcomes := make([]alarmservice.RuleTemplateSiteRolloutOutcome, 0, len(siteIDs))
	for _, sid := range siteIDs {
		outcomes = append(outcomes, alarmservice.RuleTemplateSiteRolloutOutcome{
			SiteID: sid, Status: "APPLIED", BundleRevision: 5, PreservedOverrideCount: 1, ConflictCount: 1, ChangedRuleCount: 1,
		})
	}
	return alarmservice.RuleTemplateRolloutResult{
		BatchID: s.nextUUID(), TemplateID: templateID, TemplateVersion: t.LatestVersion,
		CreatedBy: actorID, CreatedAt: now.UTC().Format(time.RFC3339Nano), Outcomes: outcomes,
		TotalAppliedSites: len(outcomes), TotalPreservedOverrides: len(outcomes), TotalConflicts: len(outcomes),
	}, nil
}

func (s *gatewayTestTemplateStore) GetSiteRuleTemplateDrift(_ context.Context, _, siteID string) (alarmservice.SiteRuleTemplateDriftReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.drift[siteID], nil
}

func (s *gatewayTestTemplateStore) ResetSiteRuleToTemplate(_ context.Context, _, siteID, ruleID string, req alarmservice.ResetSiteRuleToTemplateRequest, actorID, actorName string, now time.Time) (alarmservice.SiteRulePublishResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resetIDs = append(s.resetIDs, ruleID)
	s.drift[siteID] = alarmservice.SiteRuleTemplateDriftReport{SiteID: siteID, BundleRevision: 6, Items: []alarmservice.SiteRuleTemplateDriftItem{}}
	return alarmservice.SiteRulePublishResult{
		BundleRevision: 6, BundleDigest: strings.Repeat("e", 64), Source: alarmservice.RuleBundleSourceTemplateRollout,
		Changed: true, ChangeNote: req.ChangeNote, PublishedAt: now.UTC().Format(time.RFC3339Nano),
		PublishedBy: actorID, PublishedByName: actorName,
		Summary: alarmservice.SiteRuleSyncSummary{Changed: 1, ChangedRuleIDs: []string{ruleID}, AddedRuleIDs: []string{}, UnchangedRuleIDs: []string{}, RetiredRuleIDs: []string{}},
		Graph:   alarmservice.SiteRuleGraphView{Nodes: []alarmservice.SiteRuleGraphNodeView{}, Edges: []alarmservice.SiteRuleGraphEdgeView{}, TopologicalOrder: []string{ruleID}},
		Items:   []alarmservice.SiteRuleView{},
	}, nil
}

type emptyGatewaySiteRuleReader struct{}

func (emptyGatewaySiteRuleReader) SiteRules(context.Context, string, string) ([]alarmservice.SiteRule, error) {
	return nil, nil
}

func TestGatewayAlarmRuleTemplatesRBACMultiSiteAndReset(t *testing.T) {
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	signer := commandTestSigner(t)
	ruleID := "01910000-0000-7000-8000-000000000099"
	fixture := &alarmContractFixture{
		tenantID: "01910000-0000-7000-8000-000000000001",
		siteID:   gatewayAlarmSiteID,
	}

	memStore, err := alarmservice.NewMemoryStore(nil)
	if err != nil {
		t.Fatal(err)
	}
	tmplStore := newGatewayTestTemplateStore(fixture.siteID, ruleID)
	alarmHandler, err := alarmservice.NewHTTPHandler(alarmservice.HTTPConfig{
		Store: memStore, Rules: emptyGatewaySiteRuleReader{}, Templates: tmplStore,
		Metrics: observability.NewRegistry(), GatewayPublicKey: &signer.PublicKey,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("alarm NewHTTPHandler: %v", err)
	}
	alarmServer := httptest.NewServer(alarmHandler)
	defer alarmServer.Close()

	ownership, err := ownershipregistry.Parse(readRepositoryFile(t, "contracts", "ownership", "route-ownership.v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	sessions := sessionstore.NewMemoryStore()
	configured := NewHandler(Config{
		Now:          func() time.Time { return now },
		RouteManager: ownershipregistry.NewManager(ownership, ownershipregistry.NewMemoryAuditSink(), func() time.Time { return now }),
		Identity: &IdentityConfig{
			OIDCIssuer: "https://issuer.example.test", OIDCClientID: "client", OIDCRedirectURI: "https://web.example.test/api/v1/auth/callback",
			PublicOrigin: "https://web.example.test", IAMURL: "https://iam.example.test", IAMAudience: "iam-service",
			ExecutingWorkloadSPIFFE: "spiffe://hvac.local/platform-gateway", PolicyRevision: "identity-policy-1", DelegationSigner: signer,
			TokenEncryptionKey: make([]byte, 32), SessionStore: sessions, SessionTTL: time.Hour, DelegationTTL: 30 * time.Second,
			IAMHTTPClient: fixture.iamClient(t, signer, now),
		},
		Registry: &RegistryConfig{CoreBaseURL: "https://registry.example.test", CoreHTTPClient: fixture.registryClient(now), CoreTimeout: time.Second},
		Alarm:    &AlarmConfig{BackendBaseURL: alarmServer.URL, BackendHTTPClient: alarmServer.Client(), BackendAudience: "alarm-service", Timeout: time.Second, Now: func() time.Time { return now }},
	}).(*handler)
	fixture.handler = configured

	ciphertext, err := configured.identity.encryptBytes([]byte("alarm-contract-csrf"))
	if err != nil {
		t.Fatal(err)
	}
	created, err := sessions.CreateSession(context.Background(), sessionstore.Session{
		Client: sessionstore.ClientBrowser, ID: "alarm-templates-session",
		Principal: identitycontext.UserPrincipal{Subject: "alarm-operator", Issuer: "https://issuer.example.test", Roles: []string{"operator"}},
		TenantID:  fixture.tenantID, CSRFTokenCiphertext: ciphertext, ExpiresAt: now.Add(time.Hour),
	}, sessionstore.MutationContext{
		Action: "SESSION_CREATED", Result: "SUCCEEDED", PolicyRevision: "identity-policy-1", CorrelationID: "alarm-templates",
		TraceID: strings.Repeat("a", 32), Traceparent: "00-" + strings.Repeat("a", 32) + "-" + strings.Repeat("b", 16) + "-01",
		ExecutingService: "platform-gateway", ExecutingSPIFFEID: "spiffe://hvac.local/platform-gateway", OccurredAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.sessionID = created.ID

	// 1. Missing CSRF on template creation returns 403
	createPayload := `{"templateKey":"chw-supply-high","displayName":"冷冻水供温越限基线","deviceType":"CHILLER","alarmType":"chiller/supply-high","ruleKind":"THRESHOLD","defaultSeverity":"WARNING","defaultExecutionTarget":"CLOUD_AND_EDGE","boundSiteIds":["` + fixture.siteID + `"],"initialSpec":{"title":"冷冻水供温越限","severity":"WARNING","executionTarget":"CLOUD_AND_EDGE","triggerMode":"DURATION","durationSeconds":120,"raise":{"kind":"COMPARE","pointCode":"supply_water_temperature","operator":"GT","value":12},"clear":{"kind":"COMPARE","pointCode":"supply_water_temperature","operator":"LTE","value":10}}}`
	noCSRF := httptest.NewRequest(http.MethodPost, "https://web.example.test/api/v1/alarm-rule-templates", strings.NewReader(createPayload))
	noCSRF.AddCookie(&http.Cookie{Name: sessionCookieName, Value: fixture.sessionID})
	noCSRF.Header.Set("Origin", "https://web.example.test")
	noCSRF.Header.Set("Content-Type", "application/json")
	noCSRFRec := httptest.NewRecorder()
	fixture.handler.ServeHTTP(noCSRFRec, noCSRF)
	if noCSRFRec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 without CSRF, got %d", noCSRFRec.Code)
	}

	// 2. Denied IAM permission returns 403
	fixture.deny.Store(true)
	deniedCreate := fixture.serve(http.MethodPost, "/api/v1/alarm-rule-templates", createPayload, "")
	if deniedCreate.Code != http.StatusForbidden {
		t.Fatalf("expected 403 when schedule.manage denied, got %d body=%s", deniedCreate.Code, deniedCreate.Body.String())
	}
	fixture.deny.Store(false)

	// 3. Create template returns 201
	createRec := fixture.serve(http.MethodPost, "/api/v1/alarm-rule-templates", createPayload, "")
	if createRec.Code != http.StatusCreated {
		t.Fatalf("POST /api/v1/alarm-rule-templates status=%d body=%s", createRec.Code, createRec.Body.String())
	}
	var createEnv struct {
		Data alarmservice.RuleBaselineTemplateDetail `json:"data"`
	}
	if err := json.Unmarshal(createRec.Body.Bytes(), &createEnv); err != nil {
		t.Fatal(err)
	}
	tid := createEnv.Data.Template.TemplateID

	// 4. Multi-site rollout with an unauthorized siteId rejects 403 without executing rollout
	unauthSite := "01910000-0001-7000-8000-0000000000fe"
	crossSiteRec := fixture.serve(http.MethodPost, "/api/v1/alarm-rule-templates/"+tid+"/rollout", `{"siteIds":["`+fixture.siteID+`","`+unauthSite+`"]}`, "")
	if crossSiteRec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 when rollout includes unauthorized siteId, got %d body=%s", crossSiteRec.Code, crossSiteRec.Body.String())
	}
	if len(tmplStore.rolledOut) != 0 {
		t.Fatalf("unauthorized cross-site rollout should not reach alarm service")
	}

	// 5. Preview and execute rollout on authorized bound site succeed (200)
	prevRec := fixture.serve(http.MethodPost, "/api/v1/alarm-rule-templates/"+tid+"/preview-rollout", `{}`, "")
	if prevRec.Code != http.StatusOK {
		t.Fatalf("preview-rollout status=%d body=%s", prevRec.Code, prevRec.Body.String())
	}
	rollRec := fixture.serve(http.MethodPost, "/api/v1/alarm-rule-templates/"+tid+"/rollout", `{"changeNote":"发布跨站协同基线"}`, "")
	if rollRec.Code != http.StatusOK {
		t.Fatalf("rollout status=%d body=%s", rollRec.Code, rollRec.Body.String())
	}

	// 6. Site drift inspection and one-click reset succeed and clear drift
	driftRec := fixture.serve(http.MethodGet, "/api/v1/alarm-rules/template-drift?siteId="+fixture.siteID, "", "")
	if driftRec.Code != http.StatusOK || !strings.Contains(driftRec.Body.String(), ruleID) {
		t.Fatalf("template-drift status=%d body=%s", driftRec.Code, driftRec.Body.String())
	}
	resetRec := fixture.serve(http.MethodPost, "/api/v1/alarm-rules/"+ruleID+"/reset-template?siteId="+fixture.siteID, `{"changeNote":"一键回正"}`, "")
	if resetRec.Code != http.StatusOK || !slices.Contains(tmplStore.resetIDs, ruleID) {
		t.Fatalf("reset-template status=%d body=%s", resetRec.Code, resetRec.Body.String())
	}
}
