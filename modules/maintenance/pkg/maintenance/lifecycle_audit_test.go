package maintenance

import (
	"context"
	"strings"
	"testing"
	"time"
)

func intPtr(value int) *int {
	return &value
}

func strPtr(value string) *string {
	return &value
}

func timePtr(value time.Time) *time.Time {
	return &value
}

func TestEvaluateLifecycleGovernanceDetectsInconsistentArchivePolicy(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	policies := []LifecyclePolicyRecord{
		{
			ID:               "01990000-1600-7000-8000-000000000001",
			TenantID:         "018f1d00-0000-7000-8000-000000000001",
			DatasetCode:      "metric_result_facts",
			DataClass:        "CRITICAL",
			HotRetentionDays: 90,
			ArchiveAfterDays: nil,
			DeleteAfterDays:  intPtr(365),
			ArchiveRequired:  true,
			EffectiveFrom:    now.Add(-24 * time.Hour),
			Status:           "RELEASED",
			Revision:         1,
		},
		{
			ID:               "01990000-1600-7000-8000-000000000002",
			TenantID:         "018f1d00-0000-7000-8000-000000000001",
			DatasetCode:      "energy_interval_facts",
			DataClass:        "IMPORTANT",
			HotRetentionDays: 30,
			ArchiveAfterDays: intPtr(60),
			DeleteAfterDays:  intPtr(45),
			ArchiveRequired:  true,
			EffectiveFrom:    now.Add(-24 * time.Hour),
			Status:           "RELEASED",
			Revision:         1,
		},
	}

	findings := EvaluateLifecycleGovernance(policies, nil, now)
	if len(findings) != 2 {
		t.Fatalf("expected 2 findings, got %d: %+v", len(findings), findings)
	}
	if findings[0].PolicyID != "01990000-1600-7000-8000-000000000001" ||
		findings[0].Code != "ARCHIVE_AFTER_DAYS_MISSING" ||
		findings[0].Severity != "CRITICAL" {
		t.Fatalf("unexpected first finding: %+v", findings[0])
	}
	if findings[1].PolicyID != "01990000-1600-7000-8000-000000000002" ||
		findings[1].Code != "DELETE_BEFORE_ARCHIVE" ||
		findings[1].Severity != "CRITICAL" {
		t.Fatalf("unexpected second finding: %+v", findings[1])
	}
	for _, f := range findings {
		if !strings.HasPrefix(f.DedupKey, lifecycleAuditDedupPref) {
			t.Fatalf("expected dedup key prefix %q, got %q", lifecycleAuditDedupPref, f.DedupKey)
		}
	}
}

func TestEvaluateLifecycleGovernanceDetectsZeroRetentionRisk(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	policies := []LifecyclePolicyRecord{
		{
			ID:               "01990000-1600-7000-8000-000000000011",
			TenantID:         "018f1d00-0000-7000-8000-000000000001",
			DatasetCode:      "observations",
			DataClass:        "CRITICAL",
			HotRetentionDays: 0,
			ArchiveAfterDays: intPtr(0),
			DeleteAfterDays:  intPtr(365),
			ArchiveRequired:  true,
			EffectiveFrom:    now.Add(-time.Hour),
			Status:           "RELEASED",
			Revision:         2,
		},
		{
			ID:               "01990000-1600-7000-8000-000000000012",
			TenantID:         "018f1d00-0000-7000-8000-000000000001",
			DatasetCode:      "diagnostic_logs",
			DataClass:        "LOW_VALUE",
			HotRetentionDays: 0,
			DeleteAfterDays:  intPtr(7),
			ArchiveRequired:  false,
			EffectiveFrom:    now.Add(-time.Hour),
			Status:           "RELEASED",
			Revision:         1,
		},
		{
			ID:               "01990000-1600-7000-8000-000000000013",
			TenantID:         "018f1d00-0000-7000-8000-000000000001",
			DatasetCode:      "temp_scratch",
			DataClass:        "LOW_VALUE",
			HotRetentionDays: 0,
			DeleteAfterDays:  intPtr(0),
			ArchiveRequired:  false,
			EffectiveFrom:    now.Add(-time.Hour),
			Status:           "DRAFT",
			Revision:         1,
		},
	}

	findings := EvaluateLifecycleGovernance(policies, nil, now)
	if len(findings) != 2 {
		t.Fatalf("expected 2 findings (draft policy ignored), got %d: %+v", len(findings), findings)
	}
	if findings[0].Code != "ZERO_RETENTION_RISK" || findings[0].Severity != "CRITICAL" {
		t.Fatalf("expected CRITICAL zero-retention risk for CRITICAL dataset, got %+v", findings[0])
	}
	if findings[1].Code != "ZERO_RETENTION_RISK" || findings[1].Severity != "WARNING" {
		t.Fatalf("expected WARNING zero-retention risk for LOW_VALUE hot=0 dataset, got %+v", findings[1])
	}
}

func TestEvaluateLifecycleGovernanceDetectsActiveLegalHoldBlockAndIgnoresExpired(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	policies := []LifecyclePolicyRecord{
		{
			ID:               "01990000-1600-7000-8000-000000000021",
			TenantID:         "018f1d00-0000-7000-8000-000000000001",
			DatasetCode:      "metric_result_facts",
			DataClass:        "CRITICAL",
			HotRetentionDays: 90,
			ArchiveAfterDays: intPtr(90),
			DeleteAfterDays:  intPtr(2555),
			ArchiveRequired:  true,
			EffectiveFrom:    now.Add(-48 * time.Hour),
			Status:           "RELEASED",
			Revision:         1,
		},
		{
			ID:               "01990000-1600-7000-8000-000000000022",
			TenantID:         "018f1d00-0000-7000-8000-000000000002",
			DatasetCode:      "metric_result_facts",
			DataClass:        "CRITICAL",
			HotRetentionDays: 90,
			ArchiveAfterDays: intPtr(90),
			DeleteAfterDays:  intPtr(2555),
			ArchiveRequired:  true,
			EffectiveFrom:    now.Add(-48 * time.Hour),
			EffectiveTo:      timePtr(now.Add(-time.Hour)),
			Status:           "RELEASED",
			Revision:         1,
		},
	}

	holds := []LegalHoldScope{
		{
			ID:            "01990000-1610-7000-8000-000000000001",
			TenantID:      "018f1d00-0000-7000-8000-000000000001",
			ScopeType:     "TENANT",
			Status:        "ACTIVE",
			EffectiveFrom: now.Add(-2 * time.Hour),
		},
		{
			ID:            "01990000-1610-7000-8000-000000000002",
			TenantID:      "018f1d00-0000-7000-8000-000000000001",
			SiteID:        strPtr("018f1e00-1000-7000-8000-000000000001"),
			DatasetCode:   strPtr("metric_result_facts"),
			ScopeType:     "SITE",
			Status:        "ACTIVE",
			EffectiveFrom: now.Add(-time.Hour),
		},
		{
			ID:            "01990000-1610-7000-8000-000000000003",
			TenantID:      "018f1d00-0000-7000-8000-000000000001",
			DatasetCode:   strPtr("metric_result_facts"),
			ScopeType:     "TENANT",
			Status:        "LIFTED",
			EffectiveFrom: now.Add(-10 * time.Hour),
		},
		{
			ID:            "01990000-1610-7000-8000-000000000004",
			TenantID:      "018f1d00-0000-7000-8000-000000000001",
			DatasetCode:   strPtr("metric_result_facts"),
			ScopeType:     "TENANT",
			Status:        "ACTIVE",
			EffectiveFrom: now.Add(-10 * time.Hour),
			EffectiveTo:   timePtr(now),
		},
	}

	findings := EvaluateLifecycleGovernance(policies, holds, now)
	if len(findings) != 1 {
		t.Fatalf("expected 1 legal-hold finding, got %d: %+v", len(findings), findings)
	}
	finding := findings[0]
	if finding.Code != "LEGAL_HOLD_BLOCKED" || finding.Severity != "WARNING" || finding.ActionCode != "REVIEW_LEGAL_HOLD_CONFLICT" {
		t.Fatalf("unexpected legal hold finding: %+v", finding)
	}
	holdIDs, ok := finding.Details["holdIds"].([]string)
	if !ok || len(holdIDs) != 2 || holdIDs[0] != "01990000-1610-7000-8000-000000000001" || holdIDs[1] != "01990000-1610-7000-8000-000000000002" {
		t.Fatalf("expected only active holds, got %#v", finding.Details["holdIds"])
	}
	if blocked, _ := finding.Details["tenantWideBlocked"].(bool); !blocked {
		t.Fatalf("expected tenantWideBlocked=true, got %#v", finding.Details["tenantWideBlocked"])
	}
}

func TestEvaluateLifecycleGovernanceDetectsOverlappingReleasedPolicies(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	policies := []LifecyclePolicyRecord{
		{
			ID:               "01990000-1600-7000-8000-000000000031",
			TenantID:         "018f1d00-0000-7000-8000-000000000001",
			DatasetCode:      "metric_result_facts",
			DataClass:        "IMPORTANT",
			HotRetentionDays: 30,
			ArchiveAfterDays: intPtr(30),
			DeleteAfterDays:  intPtr(365),
			ArchiveRequired:  true,
			EffectiveFrom:    now.Add(-24 * time.Hour),
			EffectiveTo:      timePtr(now.Add(24 * time.Hour)),
			Status:           "RELEASED",
			Revision:         1,
		},
		{
			ID:               "01990000-1600-7000-8000-000000000032",
			TenantID:         "018f1d00-0000-7000-8000-000000000001",
			DatasetCode:      "metric_result_facts",
			DataClass:        "IMPORTANT",
			HotRetentionDays: 60,
			ArchiveAfterDays: intPtr(60),
			DeleteAfterDays:  intPtr(730),
			ArchiveRequired:  true,
			EffectiveFrom:    now,
			Status:           "RELEASED",
			Revision:         2,
		},
	}

	findings := EvaluateLifecycleGovernance(policies, nil, now)
	if len(findings) != 2 {
		t.Fatalf("expected 2 overlapping findings, got %d: %+v", len(findings), findings)
	}
	for _, f := range findings {
		if f.Code != "OVERLAPPING_RELEASED_POLICY" || f.Severity != "CRITICAL" {
			t.Fatalf("unexpected overlapping finding: %+v", f)
		}
	}
}

func TestStoreLifecycleAuditGuardsClosedStore(t *testing.T) {
	var store *Store
	if _, err := store.ActiveLegalHoldScopes(context.Background(), time.Now().UTC()); err == nil {
		t.Fatal("expected error for nil store in ActiveLegalHoldScopes")
	}
	if _, err := store.AuditLifecycleGovernance(context.Background(), time.Now().UTC()); err == nil {
		t.Fatal("expected error for nil store in AuditLifecycleGovernance")
	}
}
