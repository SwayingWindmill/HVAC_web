package core

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

const (
	testOperatorPrincipal = "018f1e00-2000-7000-8000-000000000011"
	testManagerPrincipal  = "018f1e00-2000-7000-8000-000000000012"
	testOtherPrincipal    = "018f1e00-2000-7000-8000-000000000013"
)

type memoryScheduleBackend struct {
	fakeRegistryStore
	mu         sync.Mutex
	schedules  map[string]OperatingSchedule
	exceptions map[string]map[string]OperatingException
	seq        int
}

func newMemoryScheduleBackend() *memoryScheduleBackend {
	return &memoryScheduleBackend{
		fakeRegistryStore: fakeRegistryStore{
			site: Site{
				ID: testSiteA1, TenantID: testTenantA, Code: "songyaxuan", DisplayName: "松雅轩", Timezone: "Asia/Shanghai", Status: "ACTIVE",
			},
		},
		schedules:  map[string]OperatingSchedule{},
		exceptions: map[string]map[string]OperatingException{},
	}
}

func (backend *memoryScheduleBackend) siteVisible(claims registryauth.GrantClaims, siteID string) bool {
	if siteID != testSiteA1 {
		return false
	}
	for _, denied := range claims.DeniedSiteIDs {
		if denied == siteID {
			return false
		}
	}
	for _, allowed := range claims.AllowedSiteIDs {
		if allowed == siteID {
			return true
		}
	}
	return false
}

func (backend *memoryScheduleBackend) GetOperatingSchedule(_ context.Context, claims registryauth.GrantClaims, siteID string) (OperatingSchedule, error) {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if !backend.siteVisible(claims, siteID) {
		return OperatingSchedule{}, ErrNotFound
	}
	if schedule, ok := backend.schedules[siteID]; ok {
		return schedule, nil
	}
	return defaultOperatingSchedule(testTenantA, siteID, "Asia/Shanghai"), nil
}

func (backend *memoryScheduleBackend) ListOperatingExceptions(_ context.Context, claims registryauth.GrantClaims, siteID string, filter OperatingExceptionFilter) (OperatingExceptionList, error) {
	if err := filter.Validate(); err != nil {
		return OperatingExceptionList{}, err
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if !backend.siteVisible(claims, siteID) {
		return OperatingExceptionList{}, ErrNotFound
	}
	items := []OperatingException{}
	for _, item := range backend.exceptions[siteID] {
		if filter.Status != "" && item.Status != filter.Status {
			continue
		}
		if filter.From != "" && item.EndDate < filter.From {
			continue
		}
		if filter.To != "" && item.StartDate > filter.To {
			continue
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].StartDate != items[j].StartDate {
			return items[i].StartDate > items[j].StartDate
		}
		return items[i].ID > items[j].ID
	})
	return OperatingExceptionList{Items: items}, nil
}

func (backend *memoryScheduleBackend) SaveOperatingSchedule(_ context.Context, claims registryauth.GrantClaims, input OperatingScheduleMutation) (OperatingSchedule, bool, error) {
	if err := input.Validate(); err != nil {
		return OperatingSchedule{}, false, err
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if !backend.siteVisible(claims, input.SiteID) {
		return OperatingSchedule{}, false, ErrNotFound
	}
	current, exists := backend.schedules[input.SiteID]
	var currentRev int64
	if exists {
		currentRev = current.Revision
	}
	if currentRev != input.Meta.ExpectedRevision {
		return OperatingSchedule{}, false, ErrRevisionConflict
	}
	now := "2026-10-12T08:00:00.000Z"
	id := "01990000-8100-7000-8000-000000000001"
	if exists && current.ID != nil {
		id = *current.ID
	}
	updatedBy := claims.PrincipalID
	slots := make([]OperatingScheduleSlot, 0, len(input.Slots))
	for idx, slot := range input.Slots {
		slots = append(slots, OperatingScheduleSlot{
			ID:                "01990000-8200-7000-8000-00000000000" + string(rune('1'+idx)),
			DayOfWeek:         slot.DayOfWeek,
			StartMinute:       slot.StartMinute,
			EndMinute:         slot.EndMinute,
			TargetSupplyTempC: slot.TargetSupplyTempC,
			SpanGroupID:       slot.SpanGroupID,
		})
	}
	saved := OperatingSchedule{
		ID:                  &id,
		TenantID:            claims.TenantID,
		SiteID:              input.SiteID,
		Timezone:            "Asia/Shanghai",
		Name:                input.Name,
		PreCoolLeadMinutes:  input.PreCoolLeadMinutes,
		CoastDownLagMinutes: input.CoastDownLagMinutes,
		Revision:            currentRev + 1,
		UpdatedBy:           &updatedBy,
		UpdatedAt:           &now,
		Slots:               slots,
	}
	backend.schedules[input.SiteID] = saved
	return saved, false, nil
}

func (backend *memoryScheduleBackend) CreateOperatingException(_ context.Context, claims registryauth.GrantClaims, input CreateOperatingExceptionRequest) (OperatingException, bool, error) {
	if err := input.Validate(); err != nil {
		return OperatingException{}, false, err
	}
	if input.AutoApprove && !grantAllowsAction(claims, registryauth.ActionScheduleManage) {
		return OperatingException{}, false, ErrMutationForbidden
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if !backend.siteVisible(claims, input.SiteID) {
		return OperatingException{}, false, ErrNotFound
	}
	if input.AutoApprove && backend.overlapsApprovedLocked(input.SiteID, "", input.StartDate, input.EndDate) {
		return OperatingException{}, false, ErrBindingConflict
	}
	backend.seq++
	id := "01990000-8300-7000-8000-00000000000" + string(rune('0'+backend.seq))
	now := "2026-10-12T08:00:00.000Z"
	actorDisplay := normalizeActorDisplay(input.ActorDisplayName, claims.PrincipalID)
	status := "PENDING_APPROVAL"
	var decidedBy, decidedByDisplay, decidedAt *string
	if input.AutoApprove {
		status = "APPROVED"
		principalID := claims.PrincipalID
		decidedBy = &principalID
		decidedByDisplay = &actorDisplay
		decidedAt = &now
	}
	slots := append([]OperatingExceptionSlot{}, input.ReplacementSlots...)
	if slots == nil {
		slots = []OperatingExceptionSlot{}
	}
	created := OperatingException{
		ID:                 id,
		TenantID:           claims.TenantID,
		SiteID:             input.SiteID,
		Title:              input.Title,
		Kind:               input.Kind,
		StartDate:          input.StartDate,
		EndDate:            input.EndDate,
		ReplacementSlots:   slots,
		Reason:             input.Meta.Reason,
		Status:             status,
		RequestedBy:        claims.PrincipalID,
		RequestedByDisplay: actorDisplay,
		DecidedBy:          decidedBy,
		DecidedByDisplay:   decidedByDisplay,
		DecidedAt:          decidedAt,
		DecisionNote:       "",
		Revision:           1,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if backend.exceptions[input.SiteID] == nil {
		backend.exceptions[input.SiteID] = map[string]OperatingException{}
	}
	backend.exceptions[input.SiteID][id] = created
	return created, false, nil
}

func (backend *memoryScheduleBackend) DecideOperatingException(_ context.Context, claims registryauth.GrantClaims, input DecideOperatingExceptionRequest) (OperatingException, bool, error) {
	if err := input.Validate(); err != nil {
		return OperatingException{}, false, err
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if !backend.siteVisible(claims, input.SiteID) {
		return OperatingException{}, false, ErrNotFound
	}
	current, ok := backend.exceptions[input.SiteID][input.ExceptionID]
	if !ok {
		return OperatingException{}, false, ErrNotFound
	}
	hasManage := grantAllowsAction(claims, registryauth.ActionScheduleManage)
	if err := authorizeExceptionDecision(claims.PrincipalID, hasManage, current, input.Decision); err != nil {
		return OperatingException{}, false, err
	}
	if current.Revision != input.Meta.ExpectedRevision {
		return OperatingException{}, false, ErrRevisionConflict
	}
	if input.Decision == "APPROVE" && backend.overlapsApprovedLocked(input.SiteID, current.ID, current.StartDate, current.EndDate) {
		return OperatingException{}, false, ErrBindingConflict
	}
	now := "2026-10-12T09:00:00.000Z"
	actorDisplay := normalizeActorDisplay(input.ActorDisplayName, claims.PrincipalID)
	principalID := claims.PrincipalID
	current.DecidedBy = &principalID
	current.DecidedByDisplay = &actorDisplay
	current.DecidedAt = &now
	current.DecisionNote = input.DecisionNote
	current.Revision++
	current.UpdatedAt = now
	switch input.Decision {
	case "APPROVE":
		current.Status = "APPROVED"
	case "REJECT":
		current.Status = "REJECTED"
	case "CANCEL":
		current.Status = "CANCELLED"
	}
	backend.exceptions[input.SiteID][current.ID] = current
	return current, false, nil
}

func (backend *memoryScheduleBackend) overlapsApprovedLocked(siteID, excludeID, startDate, endDate string) bool {
	for _, other := range backend.exceptions[siteID] {
		if other.ID == excludeID || other.Status != "APPROVED" {
			continue
		}
		if other.EndDate >= startDate && other.StartDate <= endDate {
			return true
		}
	}
	return false
}

// Stub remaining RegistryWriter methods so memoryScheduleBackend satisfies RegistryWriter.
func (*memoryScheduleBackend) SaveSite(context.Context, registryauth.GrantClaims, SiteMutation) (Site, bool, error) {
	return Site{}, false, ErrInvalidMutation
}
func (*memoryScheduleBackend) SaveSpace(context.Context, registryauth.GrantClaims, SpaceMutation) (Space, bool, error) {
	return Space{}, false, ErrInvalidMutation
}
func (*memoryScheduleBackend) SaveAsset(context.Context, registryauth.GrantClaims, AssetMutation) (Asset, bool, error) {
	return Asset{}, false, ErrInvalidMutation
}
func (*memoryScheduleBackend) SaveDevice(context.Context, registryauth.GrantClaims, DeviceMutation) (Device, bool, error) {
	return Device{}, false, ErrInvalidMutation
}
func (*memoryScheduleBackend) SaveSensor(context.Context, registryauth.GrantClaims, SensorMutation) (Sensor, bool, error) {
	return Sensor{}, false, ErrInvalidMutation
}
func (*memoryScheduleBackend) SavePoint(context.Context, registryauth.GrantClaims, PointMutation) (TelemetryPoint, bool, error) {
	return TelemetryPoint{}, false, ErrInvalidMutation
}
func (*memoryScheduleBackend) Rebind(context.Context, registryauth.GrantClaims, RebindRequest) (BindingMutationResult, error) {
	return BindingMutationResult{}, ErrInvalidMutation
}
func (*memoryScheduleBackend) ReleaseTemplate(context.Context, registryauth.GrantClaims, ReleaseTemplateRequest) (TemplateRevision, bool, error) {
	return TemplateRevision{}, false, ErrInvalidMutation
}
func (*memoryScheduleBackend) AssignTemplate(context.Context, registryauth.GrantClaims, AssignTemplateRequest) (TemplateAssignment, bool, error) {
	return TemplateAssignment{}, false, ErrInvalidMutation
}
func (*memoryScheduleBackend) PlanImport(context.Context, registryauth.GrantClaims, ImportPlanRequest) (ImportPlan, error) {
	return ImportPlan{}, ErrInvalidMutation
}
func (*memoryScheduleBackend) CommitImport(context.Context, registryauth.GrantClaims, ImportCommitRequest) (ImportCommitResult, error) {
	return ImportCommitResult{}, ErrInvalidMutation
}
func (*memoryScheduleBackend) Retire(context.Context, registryauth.GrantClaims, RetireRequest) (RetirementResult, error) {
	return RetirementResult{}, ErrInvalidMutation
}

type scheduleHTTPClient struct {
	handler http.Handler
	signer  *ecdsa.PrivateKey
	now     time.Time
}

func newScheduleHTTPClient(t *testing.T, backend *memoryScheduleBackend) scheduleHTTPClient {
	t.Helper()
	now := time.Date(2026, 10, 12, 8, 0, 0, 0, time.UTC)
	signer, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	codec, err := NewCursorCodec([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(ServerConfig{
		Store:                  backend,
		Writer:                 backend,
		CursorCodec:            codec,
		GrantPublicKey:         &signer.PublicKey,
		GrantIssuer:            testGrantIssuer,
		AllowedPresenterSPIFFE: testPresenter,
		Audience:               testAudience,
		GrantStatus:            StaticGrantStatusProvider{PolicyRevision: testPolicy},
		Logger:                 slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Now:                    func() time.Time { return now },
	})
	return scheduleHTTPClient{handler: handler, signer: signer, now: now}
}

func (client scheduleHTTPClient) do(t *testing.T, method, path string, claims registryauth.GrantClaims, actorDisplay string, body any) *httptest.ResponseRecorder {
	t.Helper()
	claims.IssuedAt = client.now.Unix()
	claims.ExpiresAt = client.now.Add(20 * time.Second).Unix()
	claims.Presenter = testPresenter
	grant, err := registryauth.SignGrant(client.signer, claims)
	if err != nil {
		t.Fatal(err)
	}
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("X-Delegation-Grant", grant)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if actorDisplay != "" {
		req.Header.Set("X-Actor-Display-Name", actorDisplay)
	}
	peerURI, _ := url.Parse(testPresenter)
	peer := &x509.Certificate{URIs: []*url.URL{peerURI}}
	req.TLS = &tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{peer},
		VerifiedChains:   [][]*x509.Certificate{{peer}},
	}
	recorder := httptest.NewRecorder()
	client.handler.ServeHTTP(recorder, req)
	return recorder
}

func TestCoreOperatingScheduleDefaultSaveAndOptimisticConcurrency(t *testing.T) {
	backend := newMemoryScheduleBackend()
	client := newScheduleHTTPClient(t, backend)

	readClaims := testGrantClaims(registryauth.ActionSiteRead)
	res := client.do(t, http.MethodGet, RegistryPathPrefix+"sites/"+testSiteA1+"/operating-schedule", readClaims, "", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("GET unconfigured schedule = %d %s", res.Code, res.Body.String())
	}
	var initial OperatingSchedule
	if err := json.NewDecoder(res.Body).Decode(&initial); err != nil {
		t.Fatal(err)
	}
	if initial.Revision != 0 || initial.PreCoolLeadMinutes != 45 || initial.CoastDownLagMinutes != 30 || len(initial.Slots) != 0 || initial.Timezone != "Asia/Shanghai" {
		t.Fatalf("unexpected default schedule: %#v", initial)
	}

	// Unauthorized site returns 404.
	unauthRes := client.do(t, http.MethodGet, RegistryPathPrefix+"sites/"+testSiteA2+"/operating-schedule", readClaims, "", nil)
	if unauthRes.Code != http.StatusNotFound {
		t.Fatalf("GET unauthorized site = %d %s", unauthRes.Code, unauthRes.Body.String())
	}

	// Operator with schedule.request cannot PUT /operating-schedule.
	operatorClaims := testGrantClaims(registryauth.ActionScheduleRequest)
	operatorClaims.PrincipalID = testOperatorPrincipal
	temp7 := 7.0
	saveBody := OperatingScheduleMutation{
		Name:                "工作日供冷计划",
		PreCoolLeadMinutes:  45,
		CoastDownLagMinutes: 30,
		Slots: []OperatingScheduleSlotInput{
			{DayOfWeek: 1, StartMinute: 480, EndMinute: 1260, TargetSupplyTempC: &temp7},
		},
		Meta: MutationMeta{ExpectedRevision: 0, IdempotencyKey: "sched-save-0001", Reason: "初始化工作日供冷计划"},
	}
	forbiddenRes := client.do(t, http.MethodPut, RegistryPathPrefix+"sites/"+testSiteA1+"/operating-schedule", operatorClaims, "张工", saveBody)
	if forbiddenRes.Code != http.StatusForbidden {
		t.Fatalf("operator PUT schedule = %d %s", forbiddenRes.Code, forbiddenRes.Body.String())
	}

	// Overlapping same-day slots are rejected with 400.
	managerClaims := testGrantClaims(registryauth.ActionScheduleManage)
	managerClaims.PrincipalID = testManagerPrincipal
	overlapBody := saveBody
	overlapBody.Slots = []OperatingScheduleSlotInput{
		{DayOfWeek: 1, StartMinute: 480, EndMinute: 900, TargetSupplyTempC: &temp7},
		{DayOfWeek: 1, StartMinute: 840, EndMinute: 1200, TargetSupplyTempC: &temp7},
	}
	overlapRes := client.do(t, http.MethodPut, RegistryPathPrefix+"sites/"+testSiteA1+"/operating-schedule", managerClaims, "李主管", overlapBody)
	if overlapRes.Code != http.StatusBadRequest {
		t.Fatalf("overlapping slots = %d %s", overlapRes.Code, overlapRes.Body.String())
	}

	// Valid save increments revision from 0 to 1.
	saveRes := client.do(t, http.MethodPut, RegistryPathPrefix+"sites/"+testSiteA1+"/operating-schedule", managerClaims, "李主管", saveBody)
	if saveRes.Code != http.StatusOK {
		t.Fatalf("manager PUT schedule = %d %s", saveRes.Code, saveRes.Body.String())
	}
	var saved OperatingSchedule
	if err := json.NewDecoder(saveRes.Body).Decode(&saved); err != nil {
		t.Fatal(err)
	}
	if saved.Revision != 1 || len(saved.Slots) != 1 || saved.Name != "工作日供冷计划" {
		t.Fatalf("saved schedule = %#v", saved)
	}

	// Stale expectedRevision fails with 409 REGISTRY_REVISION_CONFLICT.
	staleBody := saveBody
	staleBody.Meta.IdempotencyKey = "sched-save-stale-0002"
	staleRes := client.do(t, http.MethodPut, RegistryPathPrefix+"sites/"+testSiteA1+"/operating-schedule", managerClaims, "李主管", staleBody)
	if staleRes.Code != http.StatusConflict {
		t.Fatalf("stale PUT schedule = %d %s", staleRes.Code, staleRes.Body.String())
	}
}

func TestCoreOperatingExceptionRequestApprovalOverlapAndCancelRules(t *testing.T) {
	backend := newMemoryScheduleBackend()
	client := newScheduleHTTPClient(t, backend)

	operatorClaims := testGrantClaims(registryauth.ActionScheduleRequest)
	operatorClaims.PrincipalID = testOperatorPrincipal

	otherOperatorClaims := testGrantClaims(registryauth.ActionScheduleRequest)
	otherOperatorClaims.PrincipalID = testOtherPrincipal

	managerClaims := testGrantClaims(registryauth.ActionScheduleManage)
	managerClaims.PrincipalID = testManagerPrincipal

	// Operator cannot autoApprove.
	autoApproveReq := CreateOperatingExceptionRequest{
		Title:            "国庆停机",
		Kind:             "HOLIDAY_SHUTDOWN",
		StartDate:        "2026-10-01",
		EndDate:          "2026-10-03",
		ReplacementSlots: []OperatingExceptionSlot{},
		AutoApprove:      true,
		Meta:             MutationMeta{ExpectedRevision: 0, IdempotencyKey: "exc-op-auto-0001", Reason: "国庆长假停机"},
	}
	if res := client.do(t, http.MethodPost, RegistryPathPrefix+"sites/"+testSiteA1+"/operating-exceptions", operatorClaims, "张工", autoApproveReq); res.Code != http.StatusForbidden {
		t.Fatalf("operator autoApprove = %d %s", res.Code, res.Body.String())
	}

	// OVERTIME_COOLING requires at least 1 non-overlapping replacement slot.
	invalidOvertime := CreateOperatingExceptionRequest{
		Title:            "周六临时加班供冷",
		Kind:             "OVERTIME_COOLING",
		StartDate:        "2026-10-17",
		EndDate:          "2026-10-17",
		ReplacementSlots: []OperatingExceptionSlot{},
		Meta:             MutationMeta{ExpectedRevision: 0, IdempotencyKey: "exc-invalid-0001", Reason: "租户展厅活动加班"},
	}
	if res := client.do(t, http.MethodPost, RegistryPathPrefix+"sites/"+testSiteA1+"/operating-exceptions", operatorClaims, "张工", invalidOvertime); res.Code != http.StatusBadRequest {
		t.Fatalf("empty OVERTIME_COOLING slots = %d %s", res.Code, res.Body.String())
	}

	// Operator submits valid PENDING_APPROVAL OVERTIME_COOLING request.
	temp75 := 7.5
	overtimeReq := CreateOperatingExceptionRequest{
		Title:     "周六展厅加班供冷",
		Kind:      "OVERTIME_COOLING",
		StartDate: "2026-10-17",
		EndDate:   "2026-10-18",
		ReplacementSlots: []OperatingExceptionSlot{
			{StartMinute: 540, EndMinute: 1080, TargetSupplyTempC: &temp75},
		},
		Meta: MutationMeta{ExpectedRevision: 0, IdempotencyKey: "exc-create-0001", Reason: "周末展厅招商活动供冷"},
	}
	createRes := client.do(t, http.MethodPost, RegistryPathPrefix+"sites/"+testSiteA1+"/operating-exceptions", operatorClaims, "张工", overtimeReq)
	if createRes.Code != http.StatusCreated {
		t.Fatalf("operator create exception = %d %s", createRes.Code, createRes.Body.String())
	}
	var created OperatingException
	if err := json.NewDecoder(createRes.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.Status != "PENDING_APPROVAL" || created.RequestedByDisplay != "张工" || created.DecidedAt != nil {
		t.Fatalf("created exception = %#v", created)
	}

	// Operator cannot APPROVE own request, and another operator cannot CANCEL it.
	approveAttempt := DecideOperatingExceptionRequest{
		Decision:     "APPROVE",
		DecisionNote: "自行通过",
		Meta:         MutationMeta{ExpectedRevision: 1, IdempotencyKey: "exc-self-approve-0001", Reason: "自行通过"},
	}
	if res := client.do(t, http.MethodPost, RegistryPathPrefix+"sites/"+testSiteA1+"/operating-exceptions/"+created.ID+"/decide", operatorClaims, "张工", approveAttempt); res.Code != http.StatusForbidden {
		t.Fatalf("operator APPROVE = %d %s", res.Code, res.Body.String())
	}
	cancelOther := DecideOperatingExceptionRequest{
		Decision:     "CANCEL",
		DecisionNote: "代人取消",
		Meta:         MutationMeta{ExpectedRevision: 1, IdempotencyKey: "exc-other-cancel-0001", Reason: "代人取消"},
	}
	if res := client.do(t, http.MethodPost, RegistryPathPrefix+"sites/"+testSiteA1+"/operating-exceptions/"+created.ID+"/decide", otherOperatorClaims, "王工", cancelOther); res.Code != http.StatusForbidden {
		t.Fatalf("other operator CANCEL = %d %s", res.Code, res.Body.String())
	}

	// Manager approves created exception -> APPROVED.
	approveReq := DecideOperatingExceptionRequest{
		Decision:     "APPROVE",
		DecisionNote: "同意周末展厅供冷",
		Meta:         MutationMeta{ExpectedRevision: 1, IdempotencyKey: "exc-mgr-approve-0001", Reason: "审批通过展厅加班供冷"},
	}
	approveRes := client.do(t, http.MethodPost, RegistryPathPrefix+"sites/"+testSiteA1+"/operating-exceptions/"+created.ID+"/decide", managerClaims, "李主管", approveReq)
	if approveRes.Code != http.StatusOK {
		t.Fatalf("manager APPROVE = %d %s", approveRes.Code, approveRes.Body.String())
	}
	var approved OperatingException
	if err := json.NewDecoder(approveRes.Body).Decode(&approved); err != nil {
		t.Fatal(err)
	}
	if approved.Status != "APPROVED" || approved.DecidedByDisplay == nil || *approved.DecidedByDisplay != "李主管" || approved.Revision != 2 {
		t.Fatalf("approved exception = %#v", approved)
	}

	// Operator cannot CANCEL an already APPROVED exception; only schedule.manage can.
	cancelApprovedByOp := DecideOperatingExceptionRequest{
		Decision: "CANCEL",
		Meta:     MutationMeta{ExpectedRevision: 2, IdempotencyKey: "exc-op-cancel-approved-0001", Reason: "活动取消"},
	}
	if res := client.do(t, http.MethodPost, RegistryPathPrefix+"sites/"+testSiteA1+"/operating-exceptions/"+created.ID+"/decide", operatorClaims, "张工", cancelApprovedByOp); res.Code != http.StatusForbidden {
		t.Fatalf("operator CANCEL APPROVED = %d %s", res.Code, res.Body.String())
	}

	// Manager creating or approving an overlapping APPROVED exception on 2026-10-18 fails with 409.
	overlappingAutoApprove := CreateOperatingExceptionRequest{
		Title:            "周日冷机检修停机",
		Kind:             "MAINTENANCE_WINDOW",
		StartDate:        "2026-10-18",
		EndDate:          "2026-10-19",
		ReplacementSlots: []OperatingExceptionSlot{},
		AutoApprove:      true,
		Meta:             MutationMeta{ExpectedRevision: 0, IdempotencyKey: "exc-overlap-0001", Reason: "周日检修停机"},
	}
	if res := client.do(t, http.MethodPost, RegistryPathPrefix+"sites/"+testSiteA1+"/operating-exceptions", managerClaims, "李主管", overlappingAutoApprove); res.Code != http.StatusConflict {
		t.Fatalf("overlapping APPROVED exception = %d %s", res.Code, res.Body.String())
	}

	// Operator can submit a second PENDING_APPROVAL request and CANCEL their own PENDING_APPROVAL request.
	secondReq := CreateOperatingExceptionRequest{
		Title:            "下周三检修停机",
		Kind:             "MAINTENANCE_WINDOW",
		StartDate:        "2026-10-21",
		EndDate:          "2026-10-21",
		ReplacementSlots: []OperatingExceptionSlot{},
		Meta:             MutationMeta{ExpectedRevision: 0, IdempotencyKey: "exc-second-0001", Reason: "冷却塔清洗检修"},
	}
	secondRes := client.do(t, http.MethodPost, RegistryPathPrefix+"sites/"+testSiteA1+"/operating-exceptions", operatorClaims, "张工", secondReq)
	if secondRes.Code != http.StatusCreated {
		t.Fatalf("second exception create = %d %s", secondRes.Code, secondRes.Body.String())
	}
	var second OperatingException
	_ = json.NewDecoder(secondRes.Body).Decode(&second)

	cancelOwn := DecideOperatingExceptionRequest{
		Decision:     "CANCEL",
		DecisionNote: "改期清洗",
		Meta:         MutationMeta{ExpectedRevision: 1, IdempotencyKey: "exc-own-cancel-0001", Reason: "冷却塔改期清洗"},
	}
	cancelRes := client.do(t, http.MethodPost, RegistryPathPrefix+"sites/"+testSiteA1+"/operating-exceptions/"+second.ID+"/decide", operatorClaims, "张工", cancelOwn)
	if cancelRes.Code != http.StatusOK {
		t.Fatalf("operator CANCEL own pending = %d %s", cancelRes.Code, cancelRes.Body.String())
	}

	// List filtered by status=APPROVED&from=2026-10-15&to=2026-10-20 returns exactly 1 item.
	readClaims := testGrantClaims(registryauth.ActionSiteRead)
	listRes := client.do(t, http.MethodGet, RegistryPathPrefix+"sites/"+testSiteA1+"/operating-exceptions?from=2026-10-15&to=2026-10-20&status=APPROVED", readClaims, "", nil)
	if listRes.Code != http.StatusOK {
		t.Fatalf("GET exceptions = %d %s", listRes.Code, listRes.Body.String())
	}
	var listed OperatingExceptionList
	if err := json.NewDecoder(listRes.Body).Decode(&listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Items) != 1 || listed.Items[0].ID != created.ID {
		t.Fatalf("filtered exceptions = %#v", listed)
	}
}
