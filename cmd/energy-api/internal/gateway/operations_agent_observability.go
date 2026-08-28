package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
)

const (
	operationsGatewayUpstreamRequests = "operations_gateway_upstream_requests_total"
	operationsGatewayUpstreamDuration = "operations_gateway_upstream_duration_seconds"
	operationsGatewayRecoveryTotal    = "operations_gateway_recovery_total"
)

var operationsGatewayRecoveryModes = map[string]struct{}{
	"FULL_SNAPSHOT": {},
	"RESUME":        {},
}

var operationsGatewayRecoveryReasons = map[string]struct{}{
	"INITIAL":  {},
	"VALID":    {},
	"EXPIRED":  {},
	"UNKNOWN":  {},
	"FUTURE":   {},
	"CONFLICT": {},
	"INVALID":  {},
}

type operationsUpstreamTelemetry struct {
	handler    *handler
	span       *observability.Span
	startedAt  time.Time
	operation  string
	result     string
	statusCode int
}

func operationsTelemetryCorrelation(kind, value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 512 {
		return ""
	}
	for _, character := range value {
		if character == rune(13) || character == rune(10) {
			return ""
		}
	}
	sum := sha256.Sum256([]byte(kind + ":" + value))
	return "sha256:" + hex.EncodeToString(sum[:16])
}

func (h *handler) startOperationsUpstreamTelemetry(
	ctx context.Context,
	route publicOperationsRoute,
) (context.Context, *operationsUpstreamTelemetry) {
	attributes := map[string]any{
		"operations.operation": route.kind,
		"http.route":           route.template,
	}
	if correlation := operationsTelemetryCorrelation("investigation", route.investigationID); correlation != "" {
		attributes["operations.investigation.correlation"] = correlation
	}
	ctx, span := observability.Start(ctx, "operations.gateway.upstream", observability.SpanKindClient, attributes)
	return ctx, &operationsUpstreamTelemetry{
		handler:    h,
		span:       span,
		startedAt:  h.now(),
		operation:  route.kind,
		result:     "error",
		statusCode: http.StatusBadGateway,
	}
}

func (telemetry *operationsUpstreamTelemetry) setResult(result string, statusCode int) {
	if telemetry == nil {
		return
	}
	telemetry.result = result
	telemetry.statusCode = statusCode
}

func (telemetry *operationsUpstreamTelemetry) finish() {
	if telemetry == nil {
		return
	}
	duration := telemetry.handler.now().Sub(telemetry.startedAt)
	telemetry.span.SetAttributes(map[string]any{
		"operations.result":         telemetry.result,
		"http.response.status_code": telemetry.statusCode,
		"operations.duration_ms":    duration.Milliseconds(),
	})
	if telemetry.result == "success" || telemetry.result == "not_found" || telemetry.result == "rejected" {
		telemetry.span.SetStatus("ok", "")
	} else {
		telemetry.span.SetStatus("error", telemetry.result)
	}
	telemetry.span.End()
	if telemetry.handler.observability == nil {
		return
	}
	labels := map[string]string{
		"service":   serviceName,
		"operation": telemetry.operation,
		"result":    telemetry.result,
	}
	_ = telemetry.handler.observability.Metrics.AddCounter(
		operationsGatewayUpstreamRequests,
		"Operations Agent upstream requests.",
		labels,
		1,
	)
	_ = telemetry.handler.observability.Metrics.ObserveHistogram(
		operationsGatewayUpstreamDuration,
		"Operations Agent upstream request latency.",
		labels,
		duration.Seconds(),
		nil,
	)
}

func injectOperationsTrace(ctx context.Context, header http.Header) {
	observability.InjectHTTP(ctx, header)
}

func (h *handler) recordOperationsRecovery(
	ctx context.Context,
	operation string,
	mode string,
	reason string,
) {
	if _, ok := operationsGatewayRecoveryModes[mode]; !ok {
		return
	}
	if _, ok := operationsGatewayRecoveryReasons[reason]; !ok {
		return
	}
	_, span := observability.Start(ctx, "operations.gateway.recovery", observability.SpanKindInternal, map[string]any{
		"operations.operation":       operation,
		"operations.recovery.mode":   mode,
		"operations.recovery.reason": reason,
	})
	span.SetStatus("ok", "")
	span.End()
	if h.observability == nil {
		return
	}
	_ = h.observability.Metrics.AddCounter(
		operationsGatewayRecoveryTotal,
		"Operations event stream recovery decisions.",
		map[string]string{
			"service":   serviceName,
			"operation": operation,
			"mode":      mode,
			"reason":    reason,
		},
		1,
	)
}
