package edgecontrol

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

const (
	WaveformSampleMinPreSeconds    = 5
	WaveformSampleMaxPreSeconds    = 300
	WaveformSampleMinPostSeconds   = 5
	WaveformSampleMaxPostSeconds   = 300
	WaveformSampleMinRateHz        = 1
	WaveformSampleMaxRateHz        = 10
	WaveformMinDeltaWindowSeconds  = 1
	WaveformMaxDeltaWindowSeconds  = 300
	WaveformMinCooldownSeconds     = 5
	WaveformMaxCooldownSeconds     = 3600
	WaveformMaxPointsPerRule       = 24
	WaveformMaxCaptureSamples      = 6000
	DiagnosticsLogMinBytes         = 1024
	DiagnosticsLogMaxBytes         = 262144
	DiagnosticsLogDefaultBytes     = 65536
	DiagnosticsLogMinSinceMinutes  = 1
	DiagnosticsLogMaxSinceMinutes  = 1440
	DiagnosticsLogDefaultSinceMins = 60

	ReplicationTypeWaveformBurst      = "WAVEFORM_BURST"
	ReplicationTypeDiagnosticsCommand = "DIAGNOSTICS_COMMAND"
	ReplicationTypeDiagnosticsReply   = "DIAGNOSTICS_LOG_REPLY"
	ReplicationTypeHostMetrics        = "HOST_METRICS"
)

type WaveformTriggerType string

const (
	WaveformTriggerThresholdHigh WaveformTriggerType = "THRESHOLD_HIGH"
	WaveformTriggerThresholdLow  WaveformTriggerType = "THRESHOLD_LOW"
	WaveformTriggerDeltaJump     WaveformTriggerType = "DELTA_JUMP"
	WaveformTriggerTripState     WaveformTriggerType = "TRIP_STATE"
)

type DiagnosticsDownlinkAction string

const (
	DiagnosticsActionTriggerWaveform DiagnosticsDownlinkAction = "TRIGGER_WAVEFORM"
	DiagnosticsActionPullLogBundle   DiagnosticsDownlinkAction = "PULL_LOG_BUNDLE"
)

type WaveformRuleConfig struct {
	RuleID             string              `json:"ruleId,omitempty"`
	RuleName           string              `json:"ruleName"`
	Enabled            bool                `json:"enabled"`
	DeviceCode         string              `json:"deviceCode"`
	PointCode          string              `json:"pointCode"`
	TriggerType        WaveformTriggerType `json:"triggerType"`
	ThresholdValue     float64             `json:"thresholdValue"`
	DeltaWindowSeconds int                 `json:"deltaWindowSeconds"`
	PreSeconds         int                 `json:"preSeconds"`
	PostSeconds        int                 `json:"postSeconds"`
	SampleRateHz       int                 `json:"sampleRateHz"`
	PointCodes         []string            `json:"pointCodes"`
	CooldownSeconds    int                 `json:"cooldownSeconds"`
}

type WaveformSampleRow struct {
	SampledAtMs int64              `json:"sampledAtMs"`
	Values      map[string]float64 `json:"values"`
	Flags       map[string]string  `json:"flags,omitempty"`
}

type WaveformBurstPayload struct {
	CaptureID     string              `json:"captureId"`
	RuleID        string              `json:"ruleId,omitempty"`
	TriggerSource string              `json:"triggerSource"`
	TriggerReason string              `json:"triggerReason"`
	Status        string              `json:"status"`
	TriggeredAtMs int64               `json:"triggeredAtMs"`
	WindowStartMs int64               `json:"windowStartMs"`
	WindowEndMs   int64               `json:"windowEndMs"`
	SampleRateHz  int                 `json:"sampleRateHz"`
	Samples       []WaveformSampleRow `json:"samples"`
	RequestedBy   string              `json:"requestedBy,omitempty"`
	ErrorMessage  string              `json:"errorMessage,omitempty"`
}

type DiagnosticsDownlinkPayload struct {
	RequestID     string                    `json:"requestId"`
	Action        DiagnosticsDownlinkAction `json:"action"`
	CaptureID     string                    `json:"captureId,omitempty"`
	RuleID        string                    `json:"ruleId,omitempty"`
	DeviceCode    string                    `json:"deviceCode,omitempty"`
	PointCode     string                    `json:"pointCode,omitempty"`
	PointCodes    []string                  `json:"pointCodes,omitempty"`
	PreSeconds    int                       `json:"preSeconds,omitempty"`
	PostSeconds   int                       `json:"postSeconds,omitempty"`
	SampleRateHz  int                       `json:"sampleRateHz,omitempty"`
	TriggerReason string                    `json:"triggerReason,omitempty"`
	RequestedBy   string                    `json:"requestedBy,omitempty"`
	MaxBytes      int                       `json:"maxBytes,omitempty"`
	SinceMinutes  int                       `json:"sinceMinutes,omitempty"`
}

type DiagnosticsLogReplyPayload struct {
	RequestID     string `json:"requestId"`
	Status        string `json:"status"`
	Content       string `json:"content"`
	ReturnedBytes int    `json:"returnedBytes"`
	RedactedCount int    `json:"redactedCount"`
	Truncated     bool   `json:"truncated"`
	ErrorMessage  string `json:"errorMessage,omitempty"`
	CollectedAtMs int64  `json:"collectedAtMs"`
}

type GatewayHostMetricsPayload struct {
	CollectedAtMs      int64   `json:"collectedAtMs"`
	CPUPercent         float64 `json:"cpuPercent"`
	MemoryUsedPercent  float64 `json:"memoryUsedPercent"`
	DiskUsedPercent    float64 `json:"diskUsedPercent"`
	UptimeSeconds      int64   `json:"uptimeSeconds"`
	RS485TxFrames      int64   `json:"rs485TxFrames"`
	RS485RxErrors      int64   `json:"rs485RxErrors"`
	RS485ErrorRatePPM  float64 `json:"rs485ErrorRatePpm"`
	MQTTRTTMs          float64 `json:"mqttRttMs"`
	MQTTTLSVersion     string  `json:"mqttTlsVersion"`
	MQTTReconnectCount int64   `json:"mqttReconnectCount"`
	ActiveRingPoints   int     `json:"activeRingPoints"`
	RingBufferBytes    int64   `json:"ringBufferBytes"`
}

func WaveformUplinkTopic(gatewayID string) string {
	return "hvac/v1/" + strings.TrimSpace(gatewayID) + "/up/waveform"
}

func DiagnosticsUplinkTopic(gatewayID string) string {
	return "hvac/v1/" + strings.TrimSpace(gatewayID) + "/up/diagnostics"
}

func MetricsUplinkTopic(gatewayID string) string {
	return "hvac/v1/" + strings.TrimSpace(gatewayID) + "/up/metrics"
}

func DiagnosticsDownlinkTopic(gatewayID string) string {
	return "hvac/v1/" + strings.TrimSpace(gatewayID) + "/down/diagnostics"
}

func ValidateWaveformRuleConfig(rule WaveformRuleConfig) (WaveformRuleConfig, error) {
	out := rule
	out.RuleID = strings.TrimSpace(rule.RuleID)
	out.RuleName = strings.TrimSpace(rule.RuleName)
	out.DeviceCode = strings.TrimSpace(rule.DeviceCode)
	out.PointCode = strings.TrimSpace(rule.PointCode)
	if out.RuleName == "" || len(out.RuleName) > 120 {
		return WaveformRuleConfig{}, errors.New("waveform ruleName must be 1..120 bytes")
	}
	if out.DeviceCode == "" || len(out.DeviceCode) > 128 {
		return WaveformRuleConfig{}, errors.New("waveform deviceCode is required")
	}
	if out.PointCode == "" || len(out.PointCode) > 128 {
		return WaveformRuleConfig{}, errors.New("waveform pointCode is required")
	}
	switch out.TriggerType {
	case WaveformTriggerThresholdHigh, WaveformTriggerThresholdLow, WaveformTriggerDeltaJump, WaveformTriggerTripState:
	default:
		return WaveformRuleConfig{}, fmt.Errorf("unsupported waveform triggerType %q", out.TriggerType)
	}
	if out.DeltaWindowSeconds == 0 {
		out.DeltaWindowSeconds = 5
	}
	if out.DeltaWindowSeconds < WaveformMinDeltaWindowSeconds || out.DeltaWindowSeconds > WaveformMaxDeltaWindowSeconds {
		return WaveformRuleConfig{}, errors.New("deltaWindowSeconds must be between 1 and 300")
	}
	if out.PreSeconds == 0 {
		out.PreSeconds = 30
	}
	if out.PreSeconds < WaveformSampleMinPreSeconds || out.PreSeconds > WaveformSampleMaxPreSeconds {
		return WaveformRuleConfig{}, errors.New("preSeconds must be between 5 and 300")
	}
	if out.PostSeconds == 0 {
		out.PostSeconds = 60
	}
	if out.PostSeconds < WaveformSampleMinPostSeconds || out.PostSeconds > WaveformSampleMaxPostSeconds {
		return WaveformRuleConfig{}, errors.New("postSeconds must be between 5 and 300")
	}
	if out.SampleRateHz == 0 {
		out.SampleRateHz = 1
	}
	if out.SampleRateHz < WaveformSampleMinRateHz || out.SampleRateHz > WaveformSampleMaxRateHz {
		return WaveformRuleConfig{}, errors.New("sampleRateHz must be between 1 and 10")
	}
	if out.CooldownSeconds == 0 {
		out.CooldownSeconds = 120
	}
	if out.CooldownSeconds < WaveformMinCooldownSeconds || out.CooldownSeconds > WaveformMaxCooldownSeconds {
		return WaveformRuleConfig{}, errors.New("cooldownSeconds must be between 5 and 3600")
	}
	seen := make(map[string]struct{}, len(rule.PointCodes)+1)
	cleaned := make([]string, 0, len(rule.PointCodes)+1)
	for _, rawCode := range rule.PointCodes {
		code := strings.TrimSpace(rawCode)
		if code == "" {
			continue
		}
		if _, ok := seen[code]; ok {
			continue
		}
		seen[code] = struct{}{}
		cleaned = append(cleaned, code)
	}
	if len(cleaned) == 0 {
		cleaned = append(cleaned, out.DeviceCode+"."+out.PointCode)
	}
	if len(cleaned) > WaveformMaxPointsPerRule {
		return WaveformRuleConfig{}, errors.New("pointCodes exceeds maximum of 24 points")
	}
	out.PointCodes = cleaned
	return out, nil
}

func ClampLogPullBounds(maxBytes, sinceMinutes int) (int, int) {
	if maxBytes <= 0 {
		maxBytes = DiagnosticsLogDefaultBytes
	}
	if maxBytes < DiagnosticsLogMinBytes {
		maxBytes = DiagnosticsLogMinBytes
	}
	if maxBytes > DiagnosticsLogMaxBytes {
		maxBytes = DiagnosticsLogMaxBytes
	}
	if sinceMinutes <= 0 {
		sinceMinutes = DiagnosticsLogDefaultSinceMins
	}
	if sinceMinutes < DiagnosticsLogMinSinceMinutes {
		sinceMinutes = DiagnosticsLogMinSinceMinutes
	}
	if sinceMinutes > DiagnosticsLogMaxSinceMinutes {
		sinceMinutes = DiagnosticsLogMaxSinceMinutes
	}
	return maxBytes, sinceMinutes
}

var (
	pemBlockPattern = regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]+-----[\r\n]+.*?-----END [A-Z0-9 ]+-----`)
	bearerPattern   = regexp.MustCompile(`(?i)\b(Bearer|Basic)\s+[A-Za-z0-9._~+/=-]{6,}`)
	dsnCredPattern  = regexp.MustCompile(`(?i)\b((?:postgres(?:ql)?|redis|mqtts?|https?)://[^:\s/@]+:)([^@\s/]+)(@)`)
	kvSecretPattern = regexp.MustCompile(`(?i)\b(password|passwd|secret|token|api[_-]?key|private[_-]?key|enrollment[_-]?code|csrf[_-]?token|authorization|x-delegation-grant)\b(\s*[:=]\s*"?)([^\s",;]+)("?)`)
)

func RedactSensitiveLogText(raw string, maxBytes int) (string, int, bool) {
	redactedCount := 0
	text := pemBlockPattern.ReplaceAllStringFunc(raw, func(_ string) string {
		redactedCount++
		return "[REDACTED_PEM]"
	})
	text = bearerPattern.ReplaceAllStringFunc(text, func(match string) string {
		redactedCount++
		parts := strings.SplitN(match, " ", 2)
		if len(parts) > 0 {
			return parts[0] + " [REDACTED]"
		}
		return "[REDACTED]"
	})
	text = dsnCredPattern.ReplaceAllStringFunc(text, func(match string) string {
		sub := dsnCredPattern.FindStringSubmatch(match)
		if len(sub) == 4 {
			redactedCount++
			return sub[1] + "[REDACTED]" + sub[3]
		}
		return match
	})
	text = kvSecretPattern.ReplaceAllStringFunc(text, func(match string) string {
		sub := kvSecretPattern.FindStringSubmatch(match)
		if len(sub) == 5 && strings.TrimSpace(sub[3]) != "[REDACTED]" {
			redactedCount++
			return sub[1] + sub[2] + "[REDACTED]" + sub[4]
		}
		return match
	})
	truncated := false
	if maxBytes > 0 && len(text) > maxBytes {
		truncated = true
		cut := len(text) - maxBytes
		text = text[cut:]
		if idx := strings.IndexByte(text, '\n'); idx >= 0 && idx < 256 {
			text = text[idx+1:]
		}
	}
	return text, redactedCount, truncated
}

func EncodeWaveformBurst(gatewayID string, burst WaveformBurstPayload, now time.Time) ([]byte, error) {
	gwID := strings.TrimSpace(gatewayID)
	if gwID == "" || len(gwID) > 128 {
		return nil, errors.New("WAVEFORM_BURST requires valid edgeId")
	}
	if strings.TrimSpace(burst.CaptureID) == "" || len(burst.Samples) > WaveformMaxCaptureSamples {
		return nil, errors.New("invalid waveform burst payload")
	}
	return encodeReplicationEnvelope(gwID, ReplicationTypeWaveformBurst, burst, now)
}

func DecodeWaveformBurst(payload []byte, topicGatewayID string) (ShadowReplicationEnvelope, WaveformBurstPayload, error) {
	env, err := DecodeShadowReplicationEnvelope(payload, strings.TrimSpace(topicGatewayID), ReplicationTypeWaveformBurst)
	if err != nil {
		return ShadowReplicationEnvelope{}, WaveformBurstPayload{}, err
	}
	var burst WaveformBurstPayload
	if err := DecodeStrictShadowObject(env.Payload, &burst, "WAVEFORM_BURST payload"); err != nil {
		return ShadowReplicationEnvelope{}, WaveformBurstPayload{}, err
	}
	if strings.TrimSpace(burst.CaptureID) == "" || len(burst.Samples) > WaveformMaxCaptureSamples {
		return ShadowReplicationEnvelope{}, WaveformBurstPayload{}, errors.New("invalid waveform burst payload")
	}
	return env, burst, nil
}

func EncodeDiagnosticsDownlink(gatewayID string, cmd DiagnosticsDownlinkPayload, now time.Time) ([]byte, error) {
	gwID := strings.TrimSpace(gatewayID)
	if gwID == "" || len(gwID) > 128 {
		return nil, errors.New("DIAGNOSTICS_COMMAND requires valid edgeId")
	}
	switch cmd.Action {
	case DiagnosticsActionTriggerWaveform, DiagnosticsActionPullLogBundle:
	default:
		return nil, fmt.Errorf("unsupported diagnostics action %q", cmd.Action)
	}
	if strings.TrimSpace(cmd.RequestID) == "" {
		return nil, errors.New("diagnostics requestId is required")
	}
	return encodeReplicationEnvelope(gwID, ReplicationTypeDiagnosticsCommand, cmd, now)
}

func DecodeDiagnosticsDownlink(payload []byte, topicGatewayID string) (ShadowReplicationEnvelope, DiagnosticsDownlinkPayload, error) {
	env, err := DecodeShadowReplicationEnvelope(payload, strings.TrimSpace(topicGatewayID), ReplicationTypeDiagnosticsCommand)
	if err != nil {
		return ShadowReplicationEnvelope{}, DiagnosticsDownlinkPayload{}, err
	}
	var cmd DiagnosticsDownlinkPayload
	if err := DecodeStrictShadowObject(env.Payload, &cmd, "DIAGNOSTICS_COMMAND payload"); err != nil {
		return ShadowReplicationEnvelope{}, DiagnosticsDownlinkPayload{}, err
	}
	switch cmd.Action {
	case DiagnosticsActionTriggerWaveform, DiagnosticsActionPullLogBundle:
	default:
		return ShadowReplicationEnvelope{}, DiagnosticsDownlinkPayload{}, fmt.Errorf("unsupported diagnostics action %q", cmd.Action)
	}
	if strings.TrimSpace(cmd.RequestID) == "" {
		return ShadowReplicationEnvelope{}, DiagnosticsDownlinkPayload{}, errors.New("diagnostics requestId is required")
	}
	return env, cmd, nil
}

func EncodeDiagnosticsLogReply(gatewayID string, reply DiagnosticsLogReplyPayload, now time.Time) ([]byte, error) {
	gwID := strings.TrimSpace(gatewayID)
	if gwID == "" || len(gwID) > 128 {
		return nil, errors.New("DIAGNOSTICS_LOG_REPLY requires valid edgeId")
	}
	if strings.TrimSpace(reply.RequestID) == "" {
		return nil, errors.New("diagnostics reply requestId is required")
	}
	return encodeReplicationEnvelope(gwID, ReplicationTypeDiagnosticsReply, reply, now)
}

func DecodeDiagnosticsLogReply(payload []byte, topicGatewayID string) (ShadowReplicationEnvelope, DiagnosticsLogReplyPayload, error) {
	env, err := DecodeShadowReplicationEnvelope(payload, strings.TrimSpace(topicGatewayID), ReplicationTypeDiagnosticsReply)
	if err != nil {
		return ShadowReplicationEnvelope{}, DiagnosticsLogReplyPayload{}, err
	}
	var reply DiagnosticsLogReplyPayload
	if err := DecodeStrictShadowObject(env.Payload, &reply, "DIAGNOSTICS_LOG_REPLY payload"); err != nil {
		return ShadowReplicationEnvelope{}, DiagnosticsLogReplyPayload{}, err
	}
	if strings.TrimSpace(reply.RequestID) == "" {
		return ShadowReplicationEnvelope{}, DiagnosticsLogReplyPayload{}, errors.New("diagnostics reply requestId is required")
	}
	return env, reply, nil
}

func EncodeHostMetrics(gatewayID string, metrics GatewayHostMetricsPayload, now time.Time) ([]byte, error) {
	gwID := strings.TrimSpace(gatewayID)
	if gwID == "" || len(gwID) > 128 {
		return nil, errors.New("HOST_METRICS requires valid edgeId")
	}
	return encodeReplicationEnvelope(gwID, ReplicationTypeHostMetrics, metrics, now)
}

func DecodeHostMetrics(payload []byte, topicGatewayID string) (ShadowReplicationEnvelope, GatewayHostMetricsPayload, error) {
	env, err := DecodeShadowReplicationEnvelope(payload, strings.TrimSpace(topicGatewayID), ReplicationTypeHostMetrics)
	if err != nil {
		return ShadowReplicationEnvelope{}, GatewayHostMetricsPayload{}, err
	}
	var metrics GatewayHostMetricsPayload
	if err := DecodeStrictShadowObject(env.Payload, &metrics, "HOST_METRICS payload"); err != nil {
		return ShadowReplicationEnvelope{}, GatewayHostMetricsPayload{}, err
	}
	return env, metrics, nil
}
