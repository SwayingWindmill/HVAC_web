package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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
