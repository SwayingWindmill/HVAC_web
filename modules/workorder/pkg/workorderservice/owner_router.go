package workorderservice

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

type OwnerRouterConfig struct {
	GatewaySPIFFE string
	// ReaderSPIFFE may only list a Site's work orders, with a context the Gateway signed in its name
	// (the Operations Agent, #595). Empty when no reader is deployed.
	ReaderSPIFFE string
	Handler      http.Handler
}

// NewOwnerRouter admits the Gateway to the Work Order API and the reader workload to a Site's
// work-order list only; every other mutual-TLS peer is refused before the handler.
func NewOwnerRouter(config OwnerRouterConfig) (http.Handler, error) {
	gateway := strings.TrimSpace(config.GatewaySPIFFE)
	reader := strings.TrimSpace(config.ReaderSPIFFE)
	if !strings.HasPrefix(gateway, "spiffe://") || config.Handler == nil ||
		(reader != "" && (!strings.HasPrefix(reader, "spiffe://") || reader == gateway)) {
		return nil, errors.New("Work Order owner router configuration is invalid")
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		peer := workloadSPIFFE(request)
		if reader != "" && peer == reader {
			if route, ok := matchWorkOrderPath(request.URL.Path); !ok || request.Method != http.MethodGet || route.kind != workOrderCollectionRoute {
				writeOwnerForbidden(writer, "WORK_ORDER_READER_WORKLOAD_FORBIDDEN")
				return
			}
			config.Handler.ServeHTTP(writer, request)
			return
		}
		if peer != gateway {
			writeOwnerForbidden(writer, "WORK_ORDER_GATEWAY_WORKLOAD_FORBIDDEN")
			return
		}
		config.Handler.ServeHTTP(writer, request)
	}), nil
}

func workloadSPIFFE(request *http.Request) string {
	if request == nil || request.TLS == nil || len(request.TLS.PeerCertificates) == 0 {
		return ""
	}
	leaf := request.TLS.PeerCertificates[0]
	if leaf == nil || len(leaf.URIs) != 1 || leaf.URIs[0] == nil || !strings.HasPrefix(leaf.URIs[0].String(), "spiffe://") {
		return ""
	}
	return leaf.URIs[0].String()
}

func writeOwnerForbidden(writer http.ResponseWriter, code string) {
	writer.Header().Set("Content-Type", "application/problem+json")
	writer.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(writer).Encode(map[string]any{"code": code, "retryable": false})
}
