package alarmservice

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

type OwnerRouterConfig struct {
	GatewaySPIFFE   string
	TelemetrySPIFFE string
	// ReaderSPIFFE may only list a Site's alarms, with a grant the Gateway issued in its name
	// (the Operations Agent, #594). Empty when no reader is deployed.
	ReaderSPIFFE     string
	GatewayHandler   http.Handler
	TelemetryHandler http.Handler
}

func NewOwnerRouter(config OwnerRouterConfig) (http.Handler, error) {
	gatewaySPIFFE := strings.TrimSpace(config.GatewaySPIFFE)
	telemetrySPIFFE := strings.TrimSpace(config.TelemetrySPIFFE)
	if !strings.HasPrefix(gatewaySPIFFE, "spiffe://") || !strings.HasPrefix(telemetrySPIFFE, "spiffe://") || config.GatewayHandler == nil || config.TelemetryHandler == nil {
		return nil, errors.New("Alarm owner router configuration is invalid")
	}
	readerSPIFFE := strings.TrimSpace(config.ReaderSPIFFE)
	if readerSPIFFE != "" && (!strings.HasPrefix(readerSPIFFE, "spiffe://") || readerSPIFFE == gatewaySPIFFE || readerSPIFFE == telemetrySPIFFE) {
		return nil, errors.New("Alarm owner router reader identity is invalid")
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		peer := workloadSPIFFE(request)
		if request.URL.Path == InternalTelemetryEvaluationPath {
			if peer != telemetrySPIFFE {
				writeOwnerForbidden(writer, "ALARM_TELEMETRY_WORKLOAD_FORBIDDEN")
				return
			}
			config.TelemetryHandler.ServeHTTP(writer, request)
			return
		}
		if readerSPIFFE != "" && peer == readerSPIFFE {
			if route, ok := matchAlarmPath(request.URL.Path); !ok || request.Method != http.MethodGet || route.alarmID != "" {
				writeOwnerForbidden(writer, "ALARM_READER_WORKLOAD_FORBIDDEN")
				return
			}
			config.GatewayHandler.ServeHTTP(writer, request)
			return
		}
		if peer != gatewaySPIFFE {
			writeOwnerForbidden(writer, "ALARM_GATEWAY_WORKLOAD_FORBIDDEN")
			return
		}
		config.GatewayHandler.ServeHTTP(writer, request)
	}), nil
}

func workloadSPIFFE(request *http.Request) string {
	if request == nil || request.TLS == nil || len(request.TLS.PeerCertificates) == 0 {
		return ""
	}
	leaf := request.TLS.PeerCertificates[0]
	if leaf == nil || len(leaf.URIs) != 1 || leaf.URIs[0] == nil {
		return ""
	}
	identity := leaf.URIs[0].String()
	if !strings.HasPrefix(identity, "spiffe://") {
		return ""
	}
	return identity
}

func writeOwnerForbidden(writer http.ResponseWriter, code string) {
	writer.Header().Set("Content-Type", "application/problem+json")
	writer.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(writer).Encode(map[string]any{"code": code, "retryable": false})
}
