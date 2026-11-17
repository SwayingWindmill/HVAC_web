package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/quanlaihe/hvac-web/modules/connectivity/pkg/adapter"
)

type fakeRevokeChildLister struct {
	ids []string
}

func (f *fakeRevokeChildLister) ListGatewayChildDeviceIDs(context.Context, string) ([]string, error) {
	return append([]string(nil), f.ids...), nil
}

type fakeRevokeCoverageReporter struct {
	reports []adapter.SourceCoverageReport
}

func (f *fakeRevokeCoverageReporter) ReportSourceCoverage(_ context.Context, report adapter.SourceCoverageReport) error {
	f.reports = append(f.reports, report)
	return nil
}

func TestReportGatewayRevokedCoverageChunksAt2048AndDeduplicatesGateway(t *testing.T) {
	const gatewayID = "018f3e00-4000-7000-8000-000000000100"
	children := make([]string, 2050)
	for i := range children {
		children[i] = fmt.Sprintf("018f3e00-5000-7000-8000-%012d", i+1)
	}
	lister := &fakeRevokeChildLister{ids: children}
	reporter := &fakeRevokeCoverageReporter{}

	reportGatewayRevokedCoverageWithLister(lister, reporter, gatewayID)

	if len(reporter.reports) != 2 {
		t.Fatalf("report chunks = %d, want 2", len(reporter.reports))
	}
	if len(reporter.reports[0].DeviceIDs) != 2048 || len(reporter.reports[1].DeviceIDs) != 3 {
		t.Fatalf("chunk lengths = [%d, %d], want [2048, 3]", len(reporter.reports[0].DeviceIDs), len(reporter.reports[1].DeviceIDs))
	}
	for _, rep := range reporter.reports {
		if rep.Available || rep.ReasonCode == nil || *rep.ReasonCode != "SOURCE_UNAVAILABLE" {
			t.Fatalf("unexpected revoke coverage report: %+v", rep)
		}
	}
}

func TestCertificateRevokeHookRespectsRemainingCerts(t *testing.T) {
	const gatewayID = "018f3e00-4000-7000-8000-000000000100"
	lister := &fakeRevokeChildLister{ids: []string{"018f3e00-5000-7000-8000-000000000001"}}
	reporter := &fakeRevokeCoverageReporter{}

	// Remaining active certificate: no coverage unavailability reported.
	applyCertificateRevokeHook(nil, lister, reporter, gatewayID, strings.Repeat("a", 64), true)
	if len(reporter.reports) != 0 {
		t.Fatalf("expected 0 coverage reports while another cert stays active, got %+v", reporter.reports)
	}

	// Last active certificate revoked: coverage marks gateway and child devices unavailable.
	applyCertificateRevokeHook(nil, lister, reporter, gatewayID, strings.Repeat("b", 64), false)
	if len(reporter.reports) != 1 || reporter.reports[0].Available {
		t.Fatalf("expected 1 unavailable coverage report when last cert revoked, got %+v", reporter.reports)
	}
}

func TestWriteAtomicCRLFileReplacesTargetWithoutLeavingTempFiles(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "crl", "gateway-ca.crl.pem")
	first := []byte("-----BEGIN X509 CRL-----\nZmlyc3Q=\n-----END X509 CRL-----\n")
	if err := writeAtomicCRLFile(target, first); err != nil {
		t.Fatalf("first writeAtomicCRLFile failed: %v", err)
	}
	second := []byte("-----BEGIN X509 CRL-----\nc2Vjb25k\n-----END X509 CRL-----\n")
	if err := writeAtomicCRLFile(target, second); err != nil {
		t.Fatalf("second writeAtomicCRLFile failed: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", target, err)
	}
	if string(got) != string(second) {
		t.Fatalf("CRL content=%q, want %q", string(got), string(second))
	}
	entries, err := os.ReadDir(filepath.Dir(target))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "gateway-ca.crl.pem" {
		t.Fatalf("unexpected temp files left behind: %+v", entries)
	}
}

func TestGatewayOnlineTrackerTracksNormalizedPresence(t *testing.T) {
	tracker := newGatewayOnlineTracker()
	const gwUpper = "018F3E00-4000-7000-8000-000000000100"
	const gwLower = "018f3e00-4000-7000-8000-000000000100"

	if tracker.IsOnline(gwLower) {
		t.Fatal("new tracker must report unknown gateway offline")
	}
	tracker.SetOnline(" "+gwUpper+" ", true)
	if !tracker.IsOnline(gwLower) {
		t.Fatal("expected normalized gateway online after SetOnline(true)")
	}
	tracker.SetOnline(gwLower, false)
	if tracker.IsOnline(gwLower) {
		t.Fatal("expected gateway offline after SetOnline(false)")
	}
}
