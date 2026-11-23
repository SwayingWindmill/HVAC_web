package adapter

import (
	"context"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
)

func TestProcessorRoutesWaveformDiagnosticsAndMetricsWithoutTelemetryWrites(t *testing.T) {
	now := time.Date(2026, 11, 23, 10, 5, 0, 0, time.UTC)
	telemetry := &fakeTelemetry{}
	var bursts []edgecontrol.WaveformBurstPayload
	var replies []edgecontrol.DiagnosticsLogReplyPayload
	var metricsList []edgecontrol.GatewayHostMetricsPayload

	processor, err := NewProcessor(
		newFakeIdentities(),
		telemetry,
		WithWaveformBurstHook(func(_ context.Context, gw Gateway, b edgecontrol.WaveformBurstPayload) error {
			bursts = append(bursts, b)
			return nil
		}),
		WithDiagnosticsReplyHook(func(_ context.Context, gw Gateway, r edgecontrol.DiagnosticsLogReplyPayload) error {
			replies = append(replies, r)
			return nil
		}),
		WithHostMetricsHook(func(_ context.Context, gw Gateway, m edgecontrol.GatewayHostMetricsPayload) error {
			metricsList = append(metricsList, m)
			return nil
		}),
	)
	if err != nil {
		t.Fatalf("NewProcessor: %v", err)
	}

	burstRaw, err := edgecontrol.EncodeWaveformBurst(testGatewayA, edgecontrol.WaveformBurstPayload{
		CaptureID:     "0194f000-9000-7000-8000-000000000021",
		TriggerSource: "AUTO_RULE",
		TriggerReason: "threshold exceeded",
		Status:        "COMPLETED",
		TriggeredAtMs: now.UnixMilli(),
		WindowStartMs: now.Add(-15 * time.Second).UnixMilli(),
		WindowEndMs:   now.Add(15 * time.Second).UnixMilli(),
		SampleRateHz:  1,
		Samples: []edgecontrol.WaveformSampleRow{
			{SampledAtMs: now.UnixMilli(), Values: map[string]float64{"chiller-01.chw_supply_temp": 8.4}},
		},
	}, now)
	if err != nil {
		t.Fatalf("EncodeWaveformBurst: %v", err)
	}
	res, err := processor.Process(t.Context(), edgecontrol.WaveformUplinkTopic(testGatewayA), burstRaw)
	if err != nil || res.MessageType != MessageTypeWaveform || len(bursts) != 1 {
		t.Fatalf("waveform Process err=%v res=%+v bursts=%d", err, res, len(bursts))
	}

	replyRaw, err := edgecontrol.EncodeDiagnosticsLogReply(testGatewayA, edgecontrol.DiagnosticsLogReplyPayload{
		RequestID:     "0194f000-9000-7000-8000-000000000022",
		Status:        "SUCCEEDED",
		Content:       "daemon ok",
		ReturnedBytes: 9,
		RedactedCount: 1,
		CollectedAtMs: now.UnixMilli(),
	}, now)
	if err != nil {
		t.Fatalf("EncodeDiagnosticsLogReply: %v", err)
	}
	res, err = processor.Process(t.Context(), edgecontrol.DiagnosticsUplinkTopic(testGatewayA), replyRaw)
	if err != nil || res.MessageType != MessageTypeDiagnostics || len(replies) != 1 {
		t.Fatalf("diagnostics Process err=%v res=%+v replies=%d", err, res, len(replies))
	}

	hostRaw, err := edgecontrol.EncodeHostMetrics(testGatewayA, edgecontrol.GatewayHostMetricsPayload{
		CollectedAtMs:     now.UnixMilli(),
		CPUPercent:        18.2,
		MemoryUsedPercent: 41.5,
		DiskUsedPercent:   22.0,
		UptimeSeconds:     3600,
		RS485TxFrames:     1200,
		RS485RxErrors:     1,
		RS485ErrorRatePPM: 833.3,
		MQTTRTTMs:         24.5,
		MQTTTLSVersion:    "TLSv1.3",
		ActiveRingPoints:  12,
		RingBufferBytes:   64000,
	}, now)
	if err != nil {
		t.Fatalf("EncodeHostMetrics: %v", err)
	}
	res, err = processor.Process(t.Context(), edgecontrol.MetricsUplinkTopic(testGatewayA), hostRaw)
	if err != nil || res.MessageType != MessageTypeMetrics || len(metricsList) != 1 {
		t.Fatalf("metrics Process err=%v res=%+v metrics=%d", err, res, len(metricsList))
	}
	if len(telemetry.batches) != 0 {
		t.Fatalf("waveform/diagnostics/metrics MUST NEVER write telemetry batches, got %d", len(telemetry.batches))
	}
}
