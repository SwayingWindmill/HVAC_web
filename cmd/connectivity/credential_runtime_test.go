package main

import (
	"context"
	"fmt"
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
