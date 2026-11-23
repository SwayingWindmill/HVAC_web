package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/eclipse/paho.golang/paho"
	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/modules/connectivity/pkg/adapter"
	"github.com/quanlaihe/hvac-web/modules/connectivity/pkg/connectivity"
	"github.com/quanlaihe/hvac-web/modules/connectivity/pkg/packettap"
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

	applyCertificateRevokeHook(nil, lister, reporter, gatewayID, strings.Repeat("a", 64), true)
	if len(reporter.reports) != 0 {
		t.Fatalf("expected 0 coverage reports while another cert stays active, got %+v", reporter.reports)
	}

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

func TestGatewayOnlineTrackerPublishesOnlineCountGauge(t *testing.T) {
	metrics := observability.NewRegistry()
	tracker := newGatewayOnlineTrackerWithMetrics(metrics)
	scrape := func() string {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
		metrics.Handler().ServeHTTP(rec, req)
		return rec.Body.String()
	}
	if tracker.Count() != 0 {
		t.Fatalf("initial Count() = %d, want 0", tracker.Count())
	}
	if body := scrape(); !strings.Contains(body, "hvac_mqtt_online_gateways 0\n") {
		t.Fatalf("expected initial gauge 0 in /metrics, got:\n%s", body)
	}

	const gw = "018f3e00-4000-7000-8000-000000000101"
	tracker.SetOnline(gw, true)
	if tracker.Count() != 1 {
		t.Fatalf("Count() = %d, want 1", tracker.Count())
	}
	if body := scrape(); !strings.Contains(body, "hvac_mqtt_online_gateways 1\n") {
		t.Fatalf("expected gauge 1 in /metrics, got:\n%s", body)
	}

	tracker.SetOnline(gw, false)
	if tracker.Count() != 0 {
		t.Fatalf("Count() = %d, want 0 after offline", tracker.Count())
	}
	if body := scrape(); !strings.Contains(body, "hvac_mqtt_online_gateways 0\n") {
		t.Fatalf("expected gauge 0 in /metrics after offline, got:\n%s", body)
	}
}

type fakeShadowPuller struct {
	resp connectivity.GatewayShadowPullResponse
	err  error
}

func (f *fakeShadowPuller) PullGatewayDesiredConfig(context.Context, string, int64) (connectivity.GatewayShadowPullResponse, error) {
	return f.resp, f.err
}

type fakeShadowPublisher struct {
	published []*paho.Publish
	err       error
}

func (f *fakeShadowPublisher) Publish(_ context.Context, pub *paho.Publish) (*paho.PublishResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.published = append(f.published, pub)
	return &paho.PublishResponse{}, nil
}

func TestPublishGatewayDesiredShadowEmitsChangeBatchAndMetrics(t *testing.T) {
	const gwID = "018f3e00-4000-7000-8000-000000000201"
	metrics := observability.NewRegistry()
	tap := packettap.NewManager()
	puller := &fakeShadowPuller{
		resp: connectivity.GatewayShadowPullResponse{
			GatewayID:       gwID,
			DesiredRevision: 4,
			HasDelta:        true,
			Desired: &connectivity.GatewayDesiredConfig{
				DesiredRevision:     4,
				PointMappings:       []connectivity.GatewayPointMapping{},
				SamplingPolicies:    []connectivity.GatewaySamplingPolicy{},
				ConcentratorConfigs: map[string]connectivity.ConcentratorUnitShadow{},
			},
		},
	}
	pub := &fakeShadowPublisher{}
	publishGatewayDesiredShadow(context.Background(), puller, pub, nil, metrics, tap, gwID)
	if len(pub.published) != 1 || pub.published[0].Topic != adapter.ShadowDownlinkTopic(gwID) || pub.published[0].QoS != 1 {
		t.Fatalf("unexpected published shadow messages: %+v", pub.published)
	}

	pub.err = fmt.Errorf("broker disconnected")
	publishGatewayDesiredShadow(context.Background(), puller, pub, nil, metrics, tap, gwID)

	rec := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rec.Body.String()
	for _, want := range []string{
		`hvac_mqtt_shadow_replication_total{outcome="success"} 1`,
		`hvac_mqtt_shadow_replication_total{outcome="failed"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected metric %q in:\n%s", want, body)
		}
	}
}

type fakeFactoryResetPublisher struct {
	published []*paho.Publish
	err       error
}

func (f *fakeFactoryResetPublisher) Publish(_ context.Context, pub *paho.Publish) (*paho.PublishResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.published = append(f.published, pub)
	return &paho.PublishResponse{}, nil
}

func TestPublishGatewayFactoryResetPublishesLifecycleEnvelopeAndMetrics(t *testing.T) {
	t.Parallel()

	const gatewayID = "0195f300-3000-7000-8000-000000000099"
	const resetID = "0195f300-6000-7000-8000-000000000099"
	const transferID = "0195f300-5000-7000-8000-000000000099"
	reset := edgecontrol.FactoryResetPayload{
		ResetID:           resetID,
		TransferID:        transferID,
		WipeCredentials:   true,
		WipeConfigShadow:  true,
		WipeOfflineBuffer: true,
		Reason:            "ownership transfer cleanup",
		IssuedAt:          time.Date(2026, 11, 23, 1, 30, 0, 0, time.UTC).UnixMilli(),
	}

	metrics := observability.NewRegistry()
	pub := &fakeFactoryResetPublisher{}
	if err := publishGatewayFactoryReset(context.Background(), pub, nil, metrics, nil, gatewayID, reset); err != nil {
		t.Fatalf("publishGatewayFactoryReset success failed: %v", err)
	}
	if len(pub.published) != 1 {
		t.Fatalf("published count=%d, want 1", len(pub.published))
	}
	msg := pub.published[0]
	if msg.Topic != adapter.LifecycleDownlinkTopic(gatewayID) || msg.QoS != 1 || msg.Retain {
		t.Fatalf("unexpected publish packet: topic=%q qos=%d retain=%v", msg.Topic, msg.QoS, msg.Retain)
	}
	env, decoded, err := edgecontrol.DecodeFactoryReset(msg.Payload, gatewayID)
	if err != nil {
		t.Fatalf("DecodeFactoryReset: %v", err)
	}
	if env.Type != edgecontrol.ShadowReplicationTypeFactoryReset || decoded.ResetID != resetID || decoded.TransferID != transferID || !decoded.WipeCredentials || !decoded.WipeConfigShadow || !decoded.WipeOfflineBuffer {
		t.Fatalf("decoded mismatch: env=%+v payload=%+v", env, decoded)
	}
	var envMap map[string]any
	if err := json.Unmarshal(msg.Payload, &envMap); err != nil || envMap["type"] != "FACTORY_RESET" {
		t.Fatalf("unexpected raw envelope=%s err=%v", string(msg.Payload), err)
	}

	failPub := &fakeFactoryResetPublisher{err: errors.New("broker disconnected")}
	if err := publishGatewayFactoryReset(context.Background(), failPub, nil, metrics, nil, gatewayID, reset); err == nil {
		t.Fatal("expected publishGatewayFactoryReset failure error")
	}

	rec := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rec.Body.String()
	for _, want := range []string{
		adapter.LifecycleReplicationMetricName + `{outcome="success"} 1`,
		adapter.LifecycleReplicationMetricName + `{outcome="failed"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected metric %q in:\n%s", want, body)
		}
	}
}
