package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/eclipse/paho.golang/paho"

	"github.com/quanlaihe/hvac-web/libs/mqttclient"
	"github.com/quanlaihe/hvac-web/libs/observability"
)

type fakeBrokerCRLPublisher struct {
	mu      sync.Mutex
	pubs    []*paho.Publish
	err     error
	notifyC chan struct{}
}

func (f *fakeBrokerCRLPublisher) Publish(_ context.Context, pub *paho.Publish) (*paho.PublishResponse, error) {
	f.mu.Lock()
	f.pubs = append(f.pubs, pub)
	notifyC := f.notifyC
	err := f.err
	f.mu.Unlock()
	if notifyC != nil {
		select {
		case notifyC <- struct{}{}:
		default:
		}
	}
	if err != nil {
		return nil, err
	}
	return &paho.PublishResponse{}, nil
}

func (f *fakeBrokerCRLPublisher) snapshot() []*paho.Publish {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*paho.Publish, len(f.pubs))
	copy(out, f.pubs)
	return out
}

func scrapeRegistry(metrics *observability.Registry) string {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	metrics.Handler().ServeHTTP(rec, req)
	return rec.Body.String()
}

func TestBrokerCRLSyncerPublishesPayloadsAndRecordsMetrics(t *testing.T) {
	pub := &fakeBrokerCRLPublisher{}
	metrics := observability.NewRegistry()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	syncer := newBrokerCRLSyncer(pub, logger, metrics)

	if err := syncer.NotifyGatewayRevoked(context.Background(), "gw-101"); err != nil {
		t.Fatalf("NotifyGatewayRevoked: %v", err)
	}
	if err := syncer.NotifyCertificateRevoked(context.Background(), "gw-101", "AABBCC", true); err != nil {
		t.Fatalf("NotifyCertificateRevoked(hasRemaining=true): %v", err)
	}
	if err := syncer.NotifyCertificateRevoked(context.Background(), "gw-102", "DDEEFF", false); err != nil {
		t.Fatalf("NotifyCertificateRevoked(hasRemaining=false): %v", err)
	}
	if err := syncer.NotifyReload(context.Background()); err != nil {
		t.Fatalf("NotifyReload: %v", err)
	}

	pubs := pub.snapshot()
	if len(pubs) != 4 {
		t.Fatalf("expected 4 control publishes, got %d", len(pubs))
	}
	for i, pkt := range pubs {
		if pkt.Topic != BrokerCRLControlTopic {
			t.Fatalf("pub[%d] topic = %q, want %q", i, pkt.Topic, BrokerCRLControlTopic)
		}
		if pkt.QoS != 1 {
			t.Fatalf("pub[%d] qos = %d, want 1", i, pkt.QoS)
		}
	}

	var p0 brokerCRLControlPayload
	if err := json.Unmarshal(pubs[0].Payload, &p0); err != nil {
		t.Fatalf("unmarshal p0: %v", err)
	}
	if p0.Action != "reload_and_kick" || p0.GatewayID != "gw-101" || !p0.RevokeAll || !p0.WithWill {
		t.Fatalf("unexpected gateway revoke payload: %+v", p0)
	}

	var p1 brokerCRLControlPayload
	if err := json.Unmarshal(pubs[1].Payload, &p1); err != nil {
		t.Fatalf("unmarshal p1: %v", err)
	}
	if p1.Action != "reload_and_kick" || p1.GatewayID != "gw-101" || p1.FingerprintSHA256 != "aabbcc" || p1.RevokeAll || p1.WithWill {
		t.Fatalf("unexpected single-cert revoke payload (with remaining): %+v", p1)
	}

	var p2 brokerCRLControlPayload
	if err := json.Unmarshal(pubs[2].Payload, &p2); err != nil {
		t.Fatalf("unmarshal p2: %v", err)
	}
	if p2.Action != "reload_and_kick" || p2.GatewayID != "gw-102" || p2.FingerprintSHA256 != "ddeeff" || p2.RevokeAll || !p2.WithWill {
		t.Fatalf("unexpected last-cert revoke payload: %+v", p2)
	}

	var p3 brokerCRLControlPayload
	if err := json.Unmarshal(pubs[3].Payload, &p3); err != nil {
		t.Fatalf("unmarshal p3: %v", err)
	}
	if p3.Action != "reload" || p3.GatewayID != "" || p3.RevokeAll || p3.WithWill {
		t.Fatalf("unexpected reload payload: %+v", p3)
	}

	body := scrapeRegistry(metrics)
	if !strings.Contains(body, `hvac_mqtt_crl_control_events_total{action="reload_and_kick",outcome="success"} 3`) {
		t.Fatalf("missing reload_and_kick success metric in:\n%s", body)
	}
	if !strings.Contains(body, `hvac_mqtt_crl_control_events_total{action="reload",outcome="success"} 1`) {
		t.Fatalf("missing reload success metric in:\n%s", body)
	}
}

func TestBrokerCRLSyncerPublishErrorAndNilSafety(t *testing.T) {
	var nilSyncer *brokerCRLSyncer
	if err := nilSyncer.NotifyGatewayRevoked(context.Background(), "gw-1"); err != nil {
		t.Fatalf("expected nil syncer to no-op, got %v", err)
	}

	var typedNilSession *mqttclient.SharedSession
	noPubSyncer := newBrokerCRLSyncer(typedNilSession, nil, observability.NewRegistry())
	if err := noPubSyncer.NotifyReload(context.Background()); err != nil {
		t.Fatalf("expected typed-nil session syncer to no-op, got %v", err)
	}

	failingPub := &fakeBrokerCRLPublisher{err: errors.New("broker unavailable")}
	metrics := observability.NewRegistry()
	syncer := newBrokerCRLSyncer(failingPub, slog.New(slog.NewJSONHandler(io.Discard, nil)), metrics)
	err := syncer.NotifyGatewayRevoked(context.Background(), "gw-fail")
	if err == nil || !strings.Contains(err.Error(), "broker unavailable") {
		t.Fatalf("expected error from publisher, got %v", err)
	}

	body := scrapeRegistry(metrics)
	if !strings.Contains(body, `hvac_mqtt_crl_control_events_total{action="reload_and_kick",outcome="failed"} 1`) {
		t.Fatalf("missing reload_and_kick failed metric in:\n%s", body)
	}
}

func TestStartCRLRefreshLoopRunsAndStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var calls atomic.Int32
	ticked := make(chan struct{}, 8)
	done := startCRLRefreshLoop(ctx, slog.New(slog.NewJSONHandler(io.Discard, nil)), observability.NewRegistry(), 10*time.Millisecond, func() {
		calls.Add(1)
		select {
		case ticked <- struct{}{}:
		default:
		}
	})

	timeout := time.After(2 * time.Second)
	for calls.Load() < 2 {
		select {
		case <-ticked:
		case <-timeout:
			t.Fatalf("expected at least 2 periodic ticks, got %d", calls.Load())
		}
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for CRL refresh loop to exit")
	}
}
