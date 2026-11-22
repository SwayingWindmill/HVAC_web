package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
	"github.com/quanlaihe/hvac-web/libs/observability"
)

func TestEncodeAndDecodeOTAReplicationEnvelope(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 22, 6, 0, 0, 0, time.UTC)
	stage := edgecontrol.OTAStagePayload{
		CampaignID:            "0194f020-3000-7000-8000-000000000001",
		TaskID:                "0194f020-4000-7000-8000-000000000001",
		PackageID:             "0194f020-2000-7000-8000-000000000001",
		Version:               "1.5.0",
		Component:             edgecontrol.OTAComponentEdgeGateway,
		TargetOS:              "linux",
		TargetArch:            "armv7l",
		DownloadURL:           "https://edge.hvac.example.test/gateway/v1/ota/packages/0194f020-2000-7000-8000-000000000001/download",
		FileSizeBytes:         4096,
		SHA256:                strings.Repeat("a", 64),
		SignatureAlg:          edgecontrol.OTASignatureAlgEd25519,
		SignatureB64:          "dGVzdC1zaWduYXR1cmUtYjY0",
		SigningKeyID:          "platform-ota-v1",
		HealthCheckTimeoutSec: 120,
		Action:                edgecontrol.OTAActionApply,
	}
	wireBytes, err := EncodeOTAStage(testGatewayA, stage, now)
	if err != nil {
		t.Fatalf("EncodeOTAStage failed: %v", err)
	}

	var env edgecontrol.ShadowReplicationEnvelope
	if err := json.Unmarshal(wireBytes, &env); err != nil {
		t.Fatalf("unmarshal encoded OTA_STAGE: %v", err)
	}
	if env.SchemaVersion != "1.0" || env.EdgeID != testGatewayA || env.Type != edgecontrol.ShadowReplicationTypeOTAStage || env.SentAt <= 0 {
		t.Fatalf("unexpected OTA_STAGE envelope: %+v", env)
	}

	resInner, err := json.Marshal(edgecontrol.OTAResultPayload{
		CampaignID:     stage.CampaignID,
		TaskID:         stage.TaskID,
		PackageID:      stage.PackageID,
		Stage:          edgecontrol.OTAStageDownloading,
		ProgressPct:    35,
		CurrentVersion: "1.4.0",
		TargetVersion:  "1.5.0",
		OccurredAt:     now.UnixMilli(),
	})
	if err != nil {
		t.Fatal(err)
	}
	resWire, err := json.Marshal(edgecontrol.ShadowReplicationEnvelope{
		SchemaVersion: "1.0",
		MessageID:     testMessageID,
		EdgeID:        testGatewayA,
		SentAt:        now.UnixMilli(),
		Type:          edgecontrol.ShadowReplicationTypeOTAResult,
		Payload:       resInner,
	})
	if err != nil {
		t.Fatal(err)
	}

	decEnv, decRes, err := DecodeOTAResult(resWire, testGatewayA)
	if err != nil {
		t.Fatalf("DecodeOTAResult failed: %v", err)
	}
	if decEnv.MessageID != testMessageID || decRes.Stage != edgecontrol.OTAStageDownloading || decRes.ProgressPct != 35 {
		t.Fatalf("unexpected decoded OTA_RESULT: env=%+v res=%+v", decEnv, decRes)
	}

	if _, _, err := DecodeOTAResult(resWire, testGatewayB); err == nil {
		t.Fatal("expected gateway mismatch rejection")
	}
	extraField := strings.Replace(string(resWire), `"progressPct":35`, `"progressPct":35,"unexpectedField":true`, 1)
	if _, _, err := DecodeOTAResult([]byte(extraField), testGatewayA); err == nil {
		t.Fatal("expected strict unknown field rejection")
	}
	oversized := make([]byte, maximumEnvelopeBytes+16)
	if _, _, err := DecodeOTAResult(oversized, testGatewayA); err == nil {
		t.Fatal("expected oversized envelope rejection")
	}
}

func TestProcessorHandlesOTAResultMetricsHooksAndQuarantine(t *testing.T) {
	t.Parallel()

	identities := newFakeIdentities()
	metrics := observability.NewRegistry()

	var (
		onlineUpdates  []bool
		received       []edgecontrol.OTAResultPayload
		packetChannels []string
		hookErr        error
	)
	processor, err := NewProcessor(
		identities,
		&fakeTelemetry{},
		WithProcessorMetrics(metrics),
		WithGatewayPresenceHook(func(_ string, online bool) {
			onlineUpdates = append(onlineUpdates, online)
		}),
		WithOTAResultHook(func(_ context.Context, _ Gateway, res edgecontrol.OTAResultPayload) error {
			received = append(received, res)
			return hookErr
		}),
		WithProcessorPacketHook(func(_, channel, _, _ string, _ string, _ []byte) {
			packetChannels = append(packetChannels, channel)
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	buildResult := func(stage string, pct int) []byte {
		inner, _ := json.Marshal(edgecontrol.OTAResultPayload{
			CampaignID:     "0194f020-3000-7000-8000-000000000001",
			TaskID:         "0194f020-4000-7000-8000-000000000001",
			PackageID:      "0194f020-2000-7000-8000-000000000001",
			Stage:          stage,
			ProgressPct:    pct,
			CurrentVersion: "1.4.0",
			TargetVersion:  "1.5.0",
			OccurredAt:     1786352400000,
		})
		outer, _ := json.Marshal(edgecontrol.ShadowReplicationEnvelope{
			SchemaVersion: "1.0",
			MessageID:     testMessageID,
			EdgeID:        testGatewayA,
			SentAt:        1786352400000,
			Type:          edgecontrol.ShadowReplicationTypeOTAResult,
			Payload:       inner,
		})
		return outer
	}

	// 1. Valid OTA_RESULT accepted.
	outcomes, err := processor.ProcessPrefix(context.Background(), []UplinkMessage{{
		Topic:   OTAUplinkTopic(testGatewayA),
		Payload: buildResult(edgecontrol.OTAStageDownloading, 45),
	}})
	if err != nil || len(outcomes) != 1 || outcomes[0].Err != nil ||
		outcomes[0].Result.Accepted != 1 || outcomes[0].Result.MessageType != MessageTypeOTA {
		t.Fatalf("unexpected accepted OTA_RESULT outcome: %+v err=%v", outcomes, err)
	}

	// 2. Hook returning ErrOTAResultRejected -> quarantined cleanly.
	hookErr = fmt.Errorf("unknown task: %w", ErrOTAResultRejected)
	outcomesQ, err := processor.ProcessPrefix(context.Background(), []UplinkMessage{{
		Topic:   OTAUplinkTopic(testGatewayA),
		Payload: buildResult(edgecontrol.OTAStageVerifying, 60),
	}})
	if err != nil || len(outcomesQ) != 1 || !isPermanentMessageError(outcomesQ[0].Err) {
		t.Fatalf("expected permanent quarantine error for ErrOTAResultRejected, got %+v err=%v", outcomesQ, err)
	}

	// 3. Transient hook error -> returns non-permanent top-level error for retry and does NOT quarantine.
	hookErr = errors.New("temporary postgres connection reset")
	outcomesTransient, err := processor.ProcessPrefix(context.Background(), []UplinkMessage{{
		Topic:   OTAUplinkTopic(testGatewayA),
		Payload: buildResult(edgecontrol.OTAStageApplying, 80),
	}})
	if err == nil || isPermanentMessageError(err) || len(outcomesTransient) != 0 {
		t.Fatalf("expected non-permanent retryable error on transient hook failure, got outcomes=%+v err=%v", outcomesTransient, err)
	}

	// 4. Malformed payload -> permanently rejected and quarantined.
	hookErr = nil
	outcomesBad, err := processor.ProcessPrefix(context.Background(), []UplinkMessage{{
		Topic:   OTAUplinkTopic(testGatewayA),
		Payload: []byte(`{"malformed":true}`),
	}})
	if err != nil || len(outcomesBad) != 1 || !isPermanentMessageError(outcomesBad[0].Err) {
		t.Fatalf("expected permanent quarantine error on malformed OTA uplink, got %+v err=%v", outcomesBad, err)
	}

	if len(received) != 3 || len(onlineUpdates) != 3 || len(identities.quarantines) != 2 {
		t.Fatalf("received=%d onlineUpdates=%d quarantines=%d", len(received), len(onlineUpdates), len(identities.quarantines))
	}
	wantChannels := []string{"EVENT", "EVENT", "QUARANTINE", "EVENT", "QUARANTINE"}
	if len(packetChannels) != len(wantChannels) {
		t.Fatalf("unexpected packet channels: got %v, want %v", packetChannels, wantChannels)
	}
	for i, wantCh := range wantChannels {
		if packetChannels[i] != wantCh {
			t.Fatalf("packetChannels[%d] = %q, want %q (%v)", i, packetChannels[i], wantCh, packetChannels)
		}
	}

	rec := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	metricsBody := rec.Body.String()
	for _, want := range []string{
		`hvac_mqtt_ota_replication_total{outcome="accepted"} 1`,
		`hvac_mqtt_ota_replication_total{outcome="quarantined"} 1`,
		`hvac_mqtt_ota_replication_total{outcome="failed"} 1`,
		`hvac_mqtt_ota_replication_total{outcome="rejected"} 1`,
	} {
		if !strings.Contains(metricsBody, want) {
			t.Fatalf("expected metric %q in:\n%s", want, metricsBody)
		}
	}
}
