package edgecontrol

import (
	"strings"
	"testing"
	"time"
)

func TestValidateWaveformRuleAndRedaction(t *testing.T) {
	rule, err := ValidateWaveformRuleConfig(WaveformRuleConfig{
		RuleName:       "冷机喘振突变监测",
		Enabled:        true,
		DeviceCode:     "chiller-01",
		PointCode:      "compressor_current",
		TriggerType:    WaveformTriggerDeltaJump,
		ThresholdValue: 18.5,
		PreSeconds:     30,
		PostSeconds:    60,
		SampleRateHz:   1,
		PointCodes:     []string{"chiller-01.compressor_current", "chiller-01.chw_supply_temp"},
	})
	if err != nil {
		t.Fatalf("ValidateWaveformRuleConfig: %v", err)
	}
	if rule.CooldownSeconds != 120 || len(rule.PointCodes) != 2 {
		t.Fatalf("unexpected validated rule: %+v", rule)
	}

	rawLog := strings.Join([]string{
		`info connected mqtt://gateway:superpass123@broker:8883`,
		`warn header Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.secretpayload`,
		`debug token="topsecretvalue99" enrollment_code=ABC-999-XYZ`,
		"-----BEGIN EC PRIVATE KEY-----\nMHcCAQEEIPSECRETKEYDATA\n-----END EC PRIVATE KEY-----",
	}, "\n")
	clean, count, truncated := RedactSensitiveLogText(rawLog, 4096)
	if truncated {
		t.Fatalf("unexpected truncation")
	}
	if count < 4 {
		t.Fatalf("expected >=4 redactions, got %d", count)
	}
	for _, forbidden := range []string{"superpass123", "secretpayload", "topsecretvalue99", "ABC-999-XYZ", "MHcCAQEEIPSECRETKEYDATA"} {
		if strings.Contains(clean, forbidden) {
			t.Fatalf("redacted log still contains secret %q:\n%s", forbidden, clean)
		}
	}
}

func TestWaveformAndDiagnosticsCodecRoundTrip(t *testing.T) {
	now := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	gwID := "0194f000-2000-7000-8000-000000000001"
	raw, err := EncodeWaveformBurst(gwID, WaveformBurstPayload{
		CaptureID:     "0194f000-9000-7000-8000-000000000011",
		TriggerSource: "MANUAL",
		TriggerReason: "手动抓拍瞬态波形",
		Status:        "COMPLETED",
		TriggeredAtMs: now.UnixMilli(),
		WindowStartMs: now.Add(-30 * time.Second).UnixMilli(),
		WindowEndMs:   now.Add(10 * time.Second).UnixMilli(),
		SampleRateHz:  1,
		Samples: []WaveformSampleRow{
			{SampledAtMs: now.UnixMilli(), Values: map[string]float64{"chiller-01.chw_supply_temp": 7.1}},
		},
	}, now)
	if err != nil {
		t.Fatalf("EncodeWaveformBurst: %v", err)
	}
	if _, burst, err := DecodeWaveformBurst(raw, gwID); err != nil || len(burst.Samples) != 1 {
		t.Fatalf("DecodeWaveformBurst err=%v len=%d", err, len(burst.Samples))
	}

	if _, err := EncodeDiagnosticsDownlink(gwID, DiagnosticsDownlinkPayload{
		RequestID: "req-1",
		Action:    DiagnosticsDownlinkAction("SHELL_EXEC"),
	}, now); err == nil {
		t.Fatal("expected arbitrary shell action to be rejected")
	}

	syncWire, err := EncodeDiagnosticsDownlink(gwID, DiagnosticsDownlinkPayload{
		RequestID: "req-sync-1",
		Action:    DiagnosticsActionSyncWaveformRules,
		Rules: []WaveformRuleConfig{{
			RuleID:         "0194f000-9900-7000-8000-000000000001",
			RuleName:       "冷冻水高限",
			Enabled:        true,
			DeviceCode:     "chiller-01",
			PointCode:      "chw_supply_temp",
			TriggerType:    WaveformTriggerThresholdHigh,
			ThresholdValue: 11.5,
		}},
	}, now)
	if err != nil {
		t.Fatalf("EncodeDiagnosticsDownlink SYNC_WAVEFORM_RULES: %v", err)
	}
	_, decodedSync, err := DecodeDiagnosticsDownlink(syncWire, gwID)
	if err != nil || decodedSync.Action != DiagnosticsActionSyncWaveformRules || len(decodedSync.Rules) != 1 || decodedSync.Rules[0].SampleRateHz != 1 {
		t.Fatalf("DecodeDiagnosticsDownlink SYNC_WAVEFORM_RULES: cmd=%+v err=%v", decodedSync, err)
	}

	if _, err := EncodeDiagnosticsDownlink(gwID, DiagnosticsDownlinkPayload{
		RequestID: "req-sync-bad",
		Action:    DiagnosticsActionSyncWaveformRules,
		Rules: []WaveformRuleConfig{{
			RuleName: "",
		}},
	}, now); err == nil {
		t.Fatal("expected invalid waveform rule in SYNC_WAVEFORM_RULES to be rejected")
	}
}
