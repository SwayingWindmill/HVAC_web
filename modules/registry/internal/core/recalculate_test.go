package core

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

type memoryAccountingRecalcBackend struct {
	fakeRegistryStore
	RegistryWriter
	mu   sync.Mutex
	jobs map[string][]AccountingRecalcJob
	seq  int
}

func newMemoryAccountingRecalcBackend() *memoryAccountingRecalcBackend {
	return &memoryAccountingRecalcBackend{
		fakeRegistryStore: fakeRegistryStore{
			site: Site{
				ID: testSiteA1, TenantID: testTenantA, Code: "songyaxuan", DisplayName: "松雅轩", Timezone: "Asia/Shanghai", Status: "ACTIVE",
			},
		},
		jobs: map[string][]AccountingRecalcJob{},
	}
}

func (backend *memoryAccountingRecalcBackend) siteVisible(claims registryauth.GrantClaims, siteID string) bool {
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

func (backend *memoryAccountingRecalcBackend) ListAccountingRecalcJobs(_ context.Context, claims registryauth.GrantClaims, siteID string, limit int) (AccountingRecalcJobList, error) {
	if !validUUIDv7(siteID) {
		return AccountingRecalcJobList{}, ErrNotFound
	}
	if limit == 0 {
		limit = defaultAccountingRecalcJobsLimit
	}
	if limit < 1 || limit > maxAccountingRecalcJobsLimit {
		return AccountingRecalcJobList{}, ErrInvalidPage
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if !backend.siteVisible(claims, siteID) {
		return AccountingRecalcJobList{}, ErrNotFound
	}
	all := append([]AccountingRecalcJob(nil), backend.jobs[siteID]...)
	sort.Slice(all, func(i, j int) bool {
		if all[i].CreatedAt != all[j].CreatedAt {
			return all[i].CreatedAt > all[j].CreatedAt
		}
		return all[i].ID > all[j].ID
	})
	if len(all) > limit {
		all = all[:limit]
	}
	if all == nil {
		all = []AccountingRecalcJob{}
	}
	return AccountingRecalcJobList{Items: all}, nil
}

func (backend *memoryAccountingRecalcBackend) RequestAccountingRecalculation(_ context.Context, claims registryauth.GrantClaims, input AccountingRecalculateRequest) (AccountingRecalcJob, error) {
	if _, ok := accountingRecalcActionAllowed(claims); !ok {
		return AccountingRecalcJob{}, ErrMutationForbidden
	}
	normalized, err := input.normalize()
	if err != nil {
		return AccountingRecalcJob{}, err
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if !backend.siteVisible(claims, normalized.siteID) {
		return AccountingRecalcJob{}, ErrNotFound
	}
	windowStart, windowEnd, err := siteDateRangeToUTC(backend.site.Timezone, normalized.fromDate, normalized.toDate)
	if err != nil {
		return AccountingRecalcJob{}, err
	}
	backend.seq++
	now := time.Date(2026, 10, 12, 8, 0, backend.seq, 0, time.UTC)
	jobID := fmt.Sprintf("01990000-9300-7000-8000-%012d", backend.seq)
	display := normalizeActorDisplay(normalized.actorDisplayName, claims.PrincipalID)
	job := AccountingRecalcJob{
		ID:                 jobID,
		TenantID:           claims.TenantID,
		SiteID:             normalized.siteID,
		Granularity:        normalized.granularity,
		WindowStart:        formatInstant(windowStart),
		WindowEnd:          formatInstant(windowEnd),
		Status:             AccountingRecalcStatusPending,
		AvailableAt:        formatInstant(now),
		RequestedBy:        claims.PrincipalID,
		RequestedByDisplay: display,
		Reason:             normalized.reason,
		Source:             AccountingRecalcSourceManual,
		AttemptCount:       0,
		LastError:          "",
		CreatedAt:          formatInstant(now),
		UpdatedAt:          formatInstant(now),
		FinishedAt:         nil,
	}
	backend.jobs[normalized.siteID] = append(backend.jobs[normalized.siteID], job)
	return job, nil
}

func (backend *memoryAccountingRecalcBackend) EnqueueInternalDirtyWindow(_ context.Context, claims registryauth.GrantClaims, input InternalDirtyWindowRequest) (AccountingRecalcJob, error) {
	if len(claims.Actions) != 1 || !registryauth.ActionAllows(claims.Actions[0], registryauth.ActionMeterBindingResolve) {
		return AccountingRecalcJob{}, ErrMutationForbidden
	}
	if !validUUIDv7(claims.PrincipalID) || !validUUIDv7(claims.TenantID) {
		return AccountingRecalcJob{}, ErrInvalidMutation
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	backend.seq++
	now := time.Date(2026, 10, 12, 8, 0, backend.seq, 0, time.UTC)
	normalized, err := input.normalize(claims.PrincipalID, now)
	if err != nil {
		return AccountingRecalcJob{}, err
	}
	if normalized.tenantID != claims.TenantID {
		return AccountingRecalcJob{}, ErrInvalidMutation
	}
	if !registryauth.ScopeAllows(claims, normalized.siteID) || !backend.siteVisible(claims, normalized.siteID) {
		return AccountingRecalcJob{}, ErrNotFound
	}

	existing := backend.jobs[normalized.siteID]
	matchIdx := -1
	for idx, candidate := range existing {
		if candidate.TenantID != normalized.tenantID ||
			candidate.SiteID != normalized.siteID ||
			candidate.Granularity != normalized.granularity ||
			candidate.Source != normalized.source ||
			candidate.Status != AccountingRecalcStatusPending {
			continue
		}
		candStart, parseStartErr := time.Parse("2006-01-02T15:04:05.000Z", candidate.WindowStart)
		candEnd, parseEndErr := time.Parse("2006-01-02T15:04:05.000Z", candidate.WindowEnd)
		if parseStartErr != nil || parseEndErr != nil {
			continue
		}
		if !candStart.After(normalized.windowEnd) && !candEnd.Before(normalized.windowStart) {
			if matchIdx == -1 ||
				candidate.CreatedAt < existing[matchIdx].CreatedAt ||
				(candidate.CreatedAt == existing[matchIdx].CreatedAt && candidate.ID < existing[matchIdx].ID) {
				matchIdx = idx
			}
		}
	}
	if matchIdx >= 0 {
		matched := existing[matchIdx]
		candStart, _ := time.Parse("2006-01-02T15:04:05.000Z", matched.WindowStart)
		candEnd, _ := time.Parse("2006-01-02T15:04:05.000Z", matched.WindowEnd)
		candAvailable, _ := time.Parse("2006-01-02T15:04:05.000Z", matched.AvailableAt)
		if normalized.windowStart.Before(candStart) {
			candStart = normalized.windowStart
		}
		if normalized.windowEnd.After(candEnd) {
			candEnd = normalized.windowEnd
		}
		if normalized.availableAt.After(candAvailable) {
			candAvailable = normalized.availableAt
		}
		matched.WindowStart = formatInstant(candStart)
		matched.WindowEnd = formatInstant(candEnd)
		matched.AvailableAt = formatInstant(candAvailable)
		matched.UpdatedAt = formatInstant(now)
		existing[matchIdx] = matched
		backend.jobs[normalized.siteID] = existing
		return matched, nil
	}

	jobID := fmt.Sprintf("01990000-9400-7000-8000-%012d", backend.seq)
	job := AccountingRecalcJob{
		ID:                 jobID,
		TenantID:           normalized.tenantID,
		SiteID:             normalized.siteID,
		Granularity:        normalized.granularity,
		WindowStart:        formatInstant(normalized.windowStart),
		WindowEnd:          formatInstant(normalized.windowEnd),
		Status:             AccountingRecalcStatusPending,
		AvailableAt:        formatInstant(normalized.availableAt),
		RequestedBy:        normalized.requestedBy,
		RequestedByDisplay: normalized.requestedByDisplay,
		Reason:             normalized.reason,
		Source:             normalized.source,
		AttemptCount:       0,
		LastError:          "",
		CreatedAt:          formatInstant(now),
		UpdatedAt:          formatInstant(now),
		FinishedAt:         nil,
	}
	backend.jobs[normalized.siteID] = append(backend.jobs[normalized.siteID], job)
	return job, nil
}

func TestAccountingRecalculateRequestValidationAndTimezoneConversion(t *testing.T) {
	validBase := AccountingRecalculateRequest{
		SiteID:   testSiteA1,
		FromDate: "2026-10-13",
		ToDate:   "2026-10-16",
		Reason:   "  电价版本修订后手工重算  ",
	}
	normalized, err := validBase.normalize()
	if err != nil {
		t.Fatalf("valid normalize failed: %v", err)
	}
	if normalized.granularity != AccountingRecalcGranularityHour {
		t.Fatalf("default granularity = %q, want HOUR", normalized.granularity)
	}
	if normalized.reason != "电价版本修订后手工重算" {
		t.Fatalf("trimmed reason = %q", normalized.reason)
	}

	windowStart, windowEnd, err := siteDateRangeToUTC("Asia/Shanghai", normalized.fromDate, normalized.toDate)
	if err != nil {
		t.Fatalf("siteDateRangeToUTC failed: %v", err)
	}
	if formatInstant(windowStart) != "2026-10-12T16:00:00.000Z" || formatInstant(windowEnd) != "2026-10-16T16:00:00.000Z" {
		t.Fatalf("window = [%s, %s)", formatInstant(windowStart), formatInstant(windowEnd))
	}

	// Exact 90 inclusive days is allowed (Jan 1 .. Mar 31 in 2026 = 31 + 28 + 31 = 90 days).
	maxAllowed := validBase
	maxAllowed.FromDate = "2026-01-01"
	maxAllowed.ToDate = "2026-03-31"
	if err := maxAllowed.Validate(); err != nil {
		t.Fatalf("90 inclusive days rejected: %v", err)
	}

	invalidCases := []struct {
		name  string
		input AccountingRecalculateRequest
	}{
		{
			name:  "91 inclusive days",
			input: AccountingRecalculateRequest{SiteID: testSiteA1, FromDate: "2026-01-01", ToDate: "2026-04-01", Reason: "too long"},
		},
		{
			name:  "toDate before fromDate",
			input: AccountingRecalculateRequest{SiteID: testSiteA1, FromDate: "2026-10-16", ToDate: "2026-10-13", Reason: "inverted"},
		},
		{
			name:  "bad granularity",
			input: AccountingRecalculateRequest{SiteID: testSiteA1, FromDate: "2026-10-13", ToDate: "2026-10-13", Granularity: "MINUTE", Reason: "bad"},
		},
		{
			name:  "blank reason",
			input: AccountingRecalculateRequest{SiteID: testSiteA1, FromDate: "2026-10-13", ToDate: "2026-10-13", Reason: "   "},
		},
		{
			name:  "reason over 1000 runes",
			input: AccountingRecalculateRequest{SiteID: testSiteA1, FromDate: "2026-10-13", ToDate: "2026-10-13", Reason: strings.Repeat("a", 1001)},
		},
	}
	for _, tc := range invalidCases {
		if err := tc.input.Validate(); err == nil {
			t.Fatalf("%s unexpectedly passed validation", tc.name)
		}
	}
}

func TestCoreHTTPAcceptsBillingOrSavingsManageAndListsAccountingRecalcJobs(t *testing.T) {
	backend := newMemoryAccountingRecalcBackend()
	client := newScheduleHTTPClientWithInterfaces(t, backend, backend)

	readClaims := testGrantClaims(registryauth.ActionSiteRead)
	billingClaims := testGrantClaims(registryauth.ActionBillingManage)
	savingsClaims := testGrantClaims(registryauth.ActionSavingsManage)
	otherClaims := testGrantClaims(registryauth.ActionDeviceWrite)

	// Site read cannot trigger recalculation.
	forbidRead := client.do(t, http.MethodPost, RegistryPathPrefix+"sites/"+testSiteA1+"/accounting/recalculate", readClaims, "观察员", map[string]any{
		"fromDate": "2026-10-13",
		"toDate":   "2026-10-16",
		"reason":   "无权限尝试",
	})
	if forbidRead.Code != http.StatusForbidden {
		t.Fatalf("site.read POST recalculate status=%d, want 403; body=%s", forbidRead.Code, forbidRead.Body.String())
	}

	// Unrelated write action cannot trigger recalculation.
	forbidOther := client.do(t, http.MethodPost, RegistryPathPrefix+"sites/"+testSiteA1+"/accounting/recalculate", otherClaims, "设备员", map[string]any{
		"fromDate": "2026-10-13",
		"toDate":   "2026-10-16",
		"reason":   "无权限尝试",
	})
	if forbidOther.Code != http.StatusForbidden {
		t.Fatalf("device.write POST recalculate status=%d, want 403; body=%s", forbidOther.Code, forbidOther.Body.String())
	}

	// Unknown JSON fields fail with 400.
	badBody := client.do(t, http.MethodPost, RegistryPathPrefix+"sites/"+testSiteA1+"/accounting/recalculate", billingClaims, "财务王工", map[string]any{
		"fromDate": "2026-10-13",
		"toDate":   "2026-10-16",
		"reason":   "未知字段测试",
		"extra":    true,
	})
	if badBody.Code != http.StatusBadRequest {
		t.Fatalf("unknown field POST recalculate status=%d, want 400; body=%s", badBody.Code, badBody.Body.String())
	}

	// billing.manage creates a 202 Accepted job with default HOUR granularity.
	billingResp := client.do(t, http.MethodPost, RegistryPathPrefix+"sites/"+testSiteA1+"/accounting/recalculate", billingClaims, "财务王工", map[string]any{
		"fromDate": "2026-10-13",
		"toDate":   "2026-10-16",
		"reason":   "补录峰谷电价重算",
	})
	if billingResp.Code != http.StatusAccepted {
		t.Fatalf("billing.manage POST recalculate status=%d, want 202; body=%s", billingResp.Code, billingResp.Body.String())
	}
	var job1 AccountingRecalcJob
	if err := json.NewDecoder(billingResp.Body).Decode(&job1); err != nil {
		t.Fatal(err)
	}
	if job1.Granularity != "HOUR" || job1.Status != "PENDING" || job1.Source != "MANUAL" ||
		job1.WindowStart != "2026-10-12T16:00:00.000Z" || job1.WindowEnd != "2026-10-16T16:00:00.000Z" ||
		job1.RequestedByDisplay != "财务王工" || job1.FinishedAt != nil {
		t.Fatalf("unexpected job1: %#v", job1)
	}

	// savings.manage also succeeds with explicit ALL granularity.
	savingsResp := client.do(t, http.MethodPost, RegistryPathPrefix+"sites/"+testSiteA1+"/accounting/recalculate", savingsClaims, "节能李工", map[string]any{
		"fromDate":    "2026-10-01",
		"toDate":      "2026-10-01",
		"granularity": "ALL",
		"reason":      "变比变更后全量重算",
	})
	if savingsResp.Code != http.StatusAccepted {
		t.Fatalf("savings.manage POST recalculate status=%d, want 202; body=%s", savingsResp.Code, savingsResp.Body.String())
	}

	// Invalid limit query fails with 400.
	badQuery := client.do(t, http.MethodGet, RegistryPathPrefix+"sites/"+testSiteA1+"/accounting/recalculate-jobs?limit=999", readClaims, "", nil)
	if badQuery.Code != http.StatusBadRequest {
		t.Fatalf("invalid limit status=%d, want 400; body=%s", badQuery.Code, badQuery.Body.String())
	}

	// site.read lists jobs newest first, respecting limit.
	listResp := client.do(t, http.MethodGet, RegistryPathPrefix+"sites/"+testSiteA1+"/accounting/recalculate-jobs?limit=1", readClaims, "", nil)
	if listResp.Code != http.StatusOK {
		t.Fatalf("GET recalculate-jobs status=%d, want 200; body=%s", listResp.Code, listResp.Body.String())
	}
	var list AccountingRecalcJobList
	if err := json.NewDecoder(listResp.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 1 || list.Items[0].Granularity != "ALL" || list.Items[0].RequestedByDisplay != "节能李工" {
		t.Fatalf("unexpected listed jobs: %#v", list.Items)
	}
}

func TestInternalDirtyWindowRequestValidation(t *testing.T) {
	baseStart := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	baseEnd := baseStart.Add(6 * time.Hour)
	validBase := InternalDirtyWindowRequest{
		TenantID:    testTenantA,
		SiteID:      testSiteA1,
		Granularity: AccountingRecalcGranularityHour,
		WindowStart: baseStart,
		WindowEnd:   baseEnd,
		Reason:      "  补录历史电表读数触发重算  ",
		Source:      AccountingRecalcSourceBackfill,
	}
	if err := validBase.Validate(); err != nil {
		t.Fatalf("valid dirty window request rejected: %v", err)
	}

	for _, gran := range []string{AccountingRecalcGranularityHour, AccountingRecalcGranularityDay, AccountingRecalcGranularityMonth, AccountingRecalcGranularityAll} {
		for _, src := range []string{AccountingRecalcSourceBackfill, AccountingRecalcSourceTariffChange, AccountingRecalcSourceRatioChange} {
			req := validBase
			req.Granularity = gran
			req.Source = src
			if err := req.Validate(); err != nil {
				t.Fatalf("valid (%s, %s) rejected: %v", gran, src, err)
			}
		}
	}

	exact90 := validBase
	exact90.WindowEnd = baseStart.Add(90 * 24 * time.Hour)
	if err := exact90.Validate(); err != nil {
		t.Fatalf("exact 90-day span rejected: %v", err)
	}

	exact1000Runes := validBase
	exact1000Runes.Reason = strings.Repeat("电", 1000)
	if err := exact1000Runes.Validate(); err != nil {
		t.Fatalf("1000-rune UTF-8 reason rejected: %v", err)
	}

	invalidCases := []struct {
		name  string
		build func() InternalDirtyWindowRequest
	}{
		{"invalid tenantId", func() InternalDirtyWindowRequest { r := validBase; r.TenantID = "bad-tenant"; return r }},
		{"invalid siteId", func() InternalDirtyWindowRequest { r := validBase; r.SiteID = "bad-site"; return r }},
		{"invalid requestedBy", func() InternalDirtyWindowRequest { r := validBase; r.RequestedBy = "bad-principal"; return r }},
		{"empty granularity", func() InternalDirtyWindowRequest { r := validBase; r.Granularity = ""; return r }},
		{"bad granularity", func() InternalDirtyWindowRequest { r := validBase; r.Granularity = "MINUTE"; return r }},
		{"zero windowStart", func() InternalDirtyWindowRequest { r := validBase; r.WindowStart = time.Time{}; return r }},
		{"zero windowEnd", func() InternalDirtyWindowRequest { r := validBase; r.WindowEnd = time.Time{}; return r }},
		{"equal window bounds", func() InternalDirtyWindowRequest { r := validBase; r.WindowEnd = baseStart; return r }},
		{"inverted window bounds", func() InternalDirtyWindowRequest { r := validBase; r.WindowEnd = baseStart.Add(-time.Hour); return r }},
		{"span over 90 days", func() InternalDirtyWindowRequest {
			r := validBase
			r.WindowEnd = baseStart.Add(90*24*time.Hour + time.Second)
			return r
		}},
		{"empty source", func() InternalDirtyWindowRequest { r := validBase; r.Source = ""; return r }},
		{"manual source forbidden on internal endpoint", func() InternalDirtyWindowRequest { r := validBase; r.Source = AccountingRecalcSourceManual; return r }},
		{"blank reason", func() InternalDirtyWindowRequest { r := validBase; r.Reason = "   "; return r }},
		{"reason over 1000 runes", func() InternalDirtyWindowRequest { r := validBase; r.Reason = strings.Repeat("电", 1001); return r }},
	}
	for _, tc := range invalidCases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.build().Validate(); err == nil {
				t.Fatalf("%s unexpectedly passed validation", tc.name)
			}
		})
	}
}

func TestPostgresStoreEnqueueInternalDirtyWindowGuards(t *testing.T) {
	var store *PostgresStore
	claims := testGrantClaims(registryauth.ActionMeterBindingResolve)
	baseReq := InternalDirtyWindowRequest{
		TenantID:    testTenantA,
		SiteID:      testSiteA1,
		Granularity: AccountingRecalcGranularityHour,
		WindowStart: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		WindowEnd:   time.Date(2026, 10, 1, 2, 0, 0, 0, time.UTC),
		Reason:      "backfill guard check",
		Source:      AccountingRecalcSourceBackfill,
	}

	if _, err := store.EnqueueInternalDirtyWindow(context.Background(), testGrantClaims(registryauth.ActionSiteRead), baseReq); !errors.Is(err, ErrMutationForbidden) {
		t.Fatalf("wrong action err=%v, want ErrMutationForbidden", err)
	}
	mismatchedTenant := baseReq
	mismatchedTenant.TenantID = "018f1d00-0000-7000-8000-000000000099"
	if _, err := store.EnqueueInternalDirtyWindow(context.Background(), claims, mismatchedTenant); !errors.Is(err, ErrInvalidMutation) {
		t.Fatalf("mismatched tenant err=%v, want ErrInvalidMutation", err)
	}
	outOfScope := baseReq
	outOfScope.SiteID = testSiteA2
	if _, err := store.EnqueueInternalDirtyWindow(context.Background(), claims, outOfScope); !errors.Is(err, ErrNotFound) {
		t.Fatalf("out of scope site err=%v, want ErrNotFound", err)
	}
}

func TestCoreHTTPInternalDirtyWindowEnqueueAndDebounce(t *testing.T) {
	backend := newMemoryAccountingRecalcBackend()
	client := newScheduleHTTPClientWithInterfaces(t, backend, backend)
	path := RegistryPathPrefix + "sites/" + testSiteA1 + "/accounting/dirty-windows"

	resolveClaims := testGrantClaims(registryauth.ActionMeterBindingResolve)
	readClaims := testGrantClaims(registryauth.ActionSiteRead)
	billingClaims := testGrantClaims(registryauth.ActionBillingManage)

	for _, deniedClaims := range []registryauth.GrantClaims{readClaims, billingClaims} {
		resp := client.do(t, http.MethodPost, path, deniedClaims, "", map[string]any{
			"granularity": "HOUR",
			"windowStart": "2026-10-10T01:00:00Z",
			"windowEnd":   "2026-10-10T02:00:00Z",
			"reason":      "unauthorized attempt",
			"source":      "BACKFILL",
		})
		if resp.Code != http.StatusForbidden {
			t.Fatalf("action %v POST dirty-windows status=%d, want 403; body=%s", deniedClaims.Actions, resp.Code, resp.Body.String())
		}
	}

	if resp := client.do(t, http.MethodPost, path+"?foo=bar", resolveClaims, "", map[string]any{
		"granularity": "HOUR",
		"windowStart": "2026-10-10T01:00:00Z",
		"windowEnd":   "2026-10-10T02:00:00Z",
		"reason":      "query forbidden",
		"source":      "BACKFILL",
	}); resp.Code != http.StatusBadRequest {
		t.Fatalf("query param POST dirty-windows status=%d, want 400; body=%s", resp.Code, resp.Body.String())
	}

	if resp := client.do(t, http.MethodPost, path, resolveClaims, "", map[string]any{
		"granularity": "HOUR",
		"windowStart": "2026-10-10T01:00:00Z",
		"windowEnd":   "2026-10-10T02:00:00Z",
		"reason":      "unknown field test",
		"source":      "BACKFILL",
		"extra":       123,
	}); resp.Code != http.StatusBadRequest {
		t.Fatalf("unknown field POST dirty-windows status=%d, want 400; body=%s", resp.Code, resp.Body.String())
	}

	// Trailing JSON payload is rejected strictly.
	trailingClaims := resolveClaims
	trailingClaims.IssuedAt = client.now.Unix()
	trailingClaims.ExpiresAt = client.now.Add(20 * time.Second).Unix()
	trailingClaims.Presenter = testPresenter
	grant, err := registryauth.SignGrant(client.signer, trailingClaims)
	if err != nil {
		t.Fatal(err)
	}
	trailingReq := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(`{"granularity":"HOUR","windowStart":"2026-10-10T01:00:00Z","windowEnd":"2026-10-10T02:00:00Z","reason":"trailing","source":"BACKFILL"}{"extra":true}`))
	trailingReq.Header.Set("Content-Type", "application/json")
	trailingReq.Header.Set("X-Delegation-Grant", grant)
	peerURI, _ := url.Parse(testPresenter)
	peer := &x509.Certificate{URIs: []*url.URL{peerURI}}
	trailingReq.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{peer}, VerifiedChains: [][]*x509.Certificate{{peer}}}
	trailingRec := httptest.NewRecorder()
	client.handler.ServeHTTP(trailingRec, trailingReq)
	if trailingRec.Code != http.StatusBadRequest {
		t.Fatalf("trailing JSON POST dirty-windows status=%d, want 400; body=%s", trailingRec.Code, trailingRec.Body.String())
	}

	if resp := client.do(t, http.MethodPost, path, resolveClaims, "", map[string]any{
		"tenantId":    "018f1d00-0000-7000-8000-000000000099",
		"granularity": "HOUR",
		"windowStart": "2026-10-10T01:00:00Z",
		"windowEnd":   "2026-10-10T02:00:00Z",
		"reason":      "tenant mismatch",
		"source":      "BACKFILL",
	}); resp.Code != http.StatusBadRequest {
		t.Fatalf("mismatched tenantId status=%d, want 400; body=%s", resp.Code, resp.Body.String())
	}

	if resp := client.do(t, http.MethodPost, path, resolveClaims, "", map[string]any{
		"siteId":      testSiteA2,
		"granularity": "HOUR",
		"windowStart": "2026-10-10T01:00:00Z",
		"windowEnd":   "2026-10-10T02:00:00Z",
		"reason":      "site mismatch",
		"source":      "BACKFILL",
	}); resp.Code != http.StatusBadRequest {
		t.Fatalf("mismatched siteId status=%d, want 400; body=%s", resp.Code, resp.Body.String())
	}

	if resp := client.do(t, http.MethodPost, RegistryPathPrefix+"sites/"+testSiteA2+"/accounting/dirty-windows", resolveClaims, "", map[string]any{
		"granularity": "HOUR",
		"windowStart": "2026-10-10T01:00:00Z",
		"windowEnd":   "2026-10-10T02:00:00Z",
		"reason":      "out of scope site",
		"source":      "BACKFILL",
	}); resp.Code != http.StatusNotFound {
		t.Fatalf("out of scope site status=%d, want 404; body=%s", resp.Code, resp.Body.String())
	}

	// First BACKFILL HOUR request (with explicit tenantId/siteId/availableAt/requestedBy).
	customActor := "018f1e00-2000-7000-8000-000000000099"
	firstResp := client.do(t, http.MethodPost, path, resolveClaims, "", map[string]any{
		"tenantId":           testTenantA,
		"siteId":             testSiteA1,
		"granularity":        "HOUR",
		"windowStart":        "2026-10-10T02:00:00Z",
		"windowEnd":          "2026-10-10T04:00:00Z",
		"availableAt":        "2026-10-12T08:01:00Z",
		"requestedBy":        customActor,
		"requestedByDisplay": "energy-projector",
		"reason":             "backfill batch 1",
		"source":             "BACKFILL",
	})
	if firstResp.Code != http.StatusAccepted {
		t.Fatalf("first POST dirty-windows status=%d, want 202; body=%s", firstResp.Code, firstResp.Body.String())
	}
	var job1 AccountingRecalcJob
	if err := json.NewDecoder(firstResp.Body).Decode(&job1); err != nil {
		t.Fatal(err)
	}
	if job1.Status != AccountingRecalcStatusPending || job1.Source != AccountingRecalcSourceBackfill ||
		job1.RequestedBy != customActor || job1.RequestedByDisplay != "energy-projector" ||
		job1.WindowStart != "2026-10-10T02:00:00.000Z" || job1.WindowEnd != "2026-10-10T04:00:00.000Z" ||
		job1.AvailableAt != "2026-10-12T08:01:00.000Z" {
		t.Fatalf("unexpected job1: %#v", job1)
	}

	// Overlapping / extending BACKFILL HOUR request merges into job1.
	secondResp := client.do(t, http.MethodPost, path, resolveClaims, "", map[string]any{
		"granularity": "HOUR",
		"windowStart": "2026-10-10T01:00:00Z",
		"windowEnd":   "2026-10-10T05:00:00Z",
		"availableAt": "2026-10-12T08:02:00Z",
		"reason":      "backfill batch 2",
		"source":      "BACKFILL",
	})
	if secondResp.Code != http.StatusAccepted {
		t.Fatalf("second POST dirty-windows status=%d, want 202; body=%s", secondResp.Code, secondResp.Body.String())
	}
	var job2 AccountingRecalcJob
	if err := json.NewDecoder(secondResp.Body).Decode(&job2); err != nil {
		t.Fatal(err)
	}
	if job2.ID != job1.ID ||
		job2.WindowStart != "2026-10-10T01:00:00.000Z" ||
		job2.WindowEnd != "2026-10-10T05:00:00.000Z" ||
		job2.AvailableAt != "2026-10-12T08:02:00.000Z" {
		t.Fatalf("expected debounced merge into job1=%s, got %#v", job1.ID, job2)
	}

	// Different source (RATIO_CHANGE) creates a separate job and falls back requestedBy to claims.PrincipalID.
	ratioResp := client.do(t, http.MethodPost, path, resolveClaims, "", map[string]any{
		"granularity": "HOUR",
		"windowStart": "2026-10-10T02:00:00Z",
		"windowEnd":   "2026-10-10T03:00:00Z",
		"reason":      "ratio revision released",
		"source":      "RATIO_CHANGE",
	})
	if ratioResp.Code != http.StatusAccepted {
		t.Fatalf("RATIO_CHANGE POST dirty-windows status=%d, want 202; body=%s", ratioResp.Code, ratioResp.Body.String())
	}
	var job3 AccountingRecalcJob
	if err := json.NewDecoder(ratioResp.Body).Decode(&job3); err != nil {
		t.Fatal(err)
	}
	if job3.ID == job1.ID || job3.Source != AccountingRecalcSourceRatioChange ||
		job3.RequestedBy != testPrincipal || job3.RequestedByDisplay != testPrincipal {
		t.Fatalf("unexpected RATIO_CHANGE job3: %#v", job3)
	}

	listResp := client.do(t, http.MethodGet, RegistryPathPrefix+"sites/"+testSiteA1+"/accounting/recalculate-jobs", readClaims, "", nil)
	if listResp.Code != http.StatusOK {
		t.Fatalf("GET recalculate-jobs status=%d, want 200; body=%s", listResp.Code, listResp.Body.String())
	}
	var listed AccountingRecalcJobList
	if err := json.NewDecoder(listResp.Body).Decode(&listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Items) != 2 {
		t.Fatalf("expected 2 dirty-window jobs after debounce merge, got %d (%#v)", len(listed.Items), listed.Items)
	}
}
