package adapter

import (
	"time"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
)

// DiagnosticsDownlinkTopic returns the per-gateway remote diagnostics downlink topic.
func DiagnosticsDownlinkTopic(gatewayID string) string {
	return edgecontrol.DiagnosticsDownlinkTopic(gatewayID)
}

// EncodeDiagnosticsDownlink builds a strict remote diagnostics command envelope.
func EncodeDiagnosticsDownlink(gatewayID string, cmd edgecontrol.DiagnosticsDownlinkPayload, now time.Time) ([]byte, error) {
	return edgecontrol.EncodeDiagnosticsDownlink(gatewayID, cmd, now)
}
