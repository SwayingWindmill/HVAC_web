package adapter

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
)

func TestTelemetryRuntimeClientAcceptObservationBatchWireContract(t *testing.T) {
	var hits atomic.Int32
	var gotPath string
	var gotCount int
	var firstObs Observation
	var handler func(http.ResponseWriter, *http.Request)

	server, client := newTestTelemetryServer(t, func(writer http.ResponseWriter, request *http.Request) {
		hits.Add(1)
		handler(writer, request)
	})
	defer server.Close()

	// Empty and oversized batches are rejected client-side without an HTTP round-trip.
	if _, err := client.AcceptObservationBatch(context.Background(), nil); err == nil {
		t.Fatal("empty batch was accepted")
	}
	oversized := make([]Observation, MaxObservationBatchSize+1)
	if _, err := client.AcceptObservationBatch(context.Background(), oversized); err == nil {
		t.Fatal("oversized batch was accepted")
	}
	if hits.Load() != 0 {
		t.Fatalf("client-side validation still issued %d HTTP calls", hits.Load())
	}

	handler = func(writer http.ResponseWriter, request *http.Request) {
		gotPath = request.URL.Path
		var payload struct {
			Observations []Observation `json:"observations"`
		}
		decoder := json.NewDecoder(request.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&payload); err != nil {
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		gotCount = len(payload.Observations)
		firstObs = payload.Observations[0]
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"receipts": []ObservationReceipt{
				{Status: "ACCEPTED", DeviceID: testDeviceID, BusinessRevision: 1, StateChanged: true, PositionAdvanced: true},
				{Status: "QUARANTINED", QuarantineReason: "POINT_UNREGISTERED"},
			},
		})
	}

	unit := "kW"
	input := []Observation{
		{
			SourceID:           testGatewayA,
			SourcePath:         "PUSH",
			ExternalEntityType: "DEVICE",
			ExternalID:         "METER-01",
			Device:             &ResolvedDevice{TenantID: testTenantID, SiteID: testSiteID, DeviceID: testDeviceID},
			Point:              &ResolvedPoint{PointID: testPointID, PointType: "TELEMETRY", ValueType: "NUMBER", PointRevision: 1},
			TelemetryKey:       "active_power",
			Value:              126.4,
			ValueType:          "NUMBER",
			Unit:               &unit,
			WireQuality:        0,
			SampledAt:          "2026-08-10T10:20:00Z",
			SourcePosition:     SourcePosition{Partition: "mqtt:" + testGatewayA + ":METER-01:active_power", Offset: 1, EventID: testMessageID},
		},
		{
			SourceID:           testGatewayA,
			SourcePath:         "PUSH",
			ExternalEntityType: "DEVICE",
			ExternalID:         "UNKNOWN",
			TelemetryKey:       "active_power",
			Value:              10,
			ValueType:          "NUMBER",
			WireQuality:        0,
			SampledAt:          "2026-08-10T10:20:00Z",
			SourcePosition:     SourcePosition{Partition: "mqtt:" + testGatewayA + ":UNKNOWN:active_power", Offset: 2, EventID: messageID(2)},
		},
	}

	receipts, err := client.AcceptObservationBatch(context.Background(), input)
	if err != nil {
		t.Fatalf("AcceptObservationBatch err=%v", err)
	}
	if gotPath != "/internal/v1/telemetry/sources/observations:acceptBatch" || gotCount != 2 {
		t.Fatalf("path=%s count=%d", gotPath, gotCount)
	}
	if firstObs.SourceID != testGatewayA || firstObs.Device == nil || firstObs.Point == nil || firstObs.SourcePosition.EventID != testMessageID {
		t.Fatalf("decoded firstObs=%#v", firstObs)
	}
	if len(receipts) != 2 || receipts[0].Status != "ACCEPTED" || receipts[1].Status != "QUARANTINED" {
		t.Fatalf("receipts=%#v", receipts)
	}

	for _, badCase := range []struct {
		name string
		body string
		code int
	}{
		{"receipt count mismatch", `{"receipts":[{"status":"ACCEPTED"}]}`, http.StatusOK},
		{"invalid status", `{"receipts":[{"status":"ACCEPTED"},{"status":"UNKNOWN"}]}`, http.StatusOK},
		{"unknown field", `{"receipts":[{"status":"ACCEPTED"},{"status":"ACCEPTED"}],"extra":true}`, http.StatusOK},
		{"trailing json", `{"receipts":[{"status":"ACCEPTED"},{"status":"ACCEPTED"}]}{}`, http.StatusOK},
		{"503 status", `{"error":"unavailable"}`, http.StatusServiceUnavailable},
		{"oversized body", strings.Repeat(" ", int(maximumTelemetryRuntimeBodyBytes)+1), http.StatusOK},
	} {
		t.Run(badCase.name, func(t *testing.T) {
			handler = func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(badCase.code)
				_, _ = writer.Write([]byte(badCase.body))
			}
			if _, err := client.AcceptObservationBatch(context.Background(), input); err == nil {
				t.Fatalf("%s unexpectedly succeeded", badCase.name)
			}
		})
	}
}

func newTestTelemetryServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *TelemetryRuntimeClient) {
	t.Helper()
	dir := t.TempDir()
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, _ := x509.ParseCertificate(caDER)
	issue := func(serial int64, name string, usage x509.ExtKeyUsage) tls.Certificate {
		key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		template := &x509.Certificate{
			SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: name},
			DNSNames: []string{name}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
			NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
			KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage},
		}
		der, err := x509.CreateCertificate(rand.Reader, template, caCert, &key.PublicKey, caKey)
		if err != nil {
			t.Fatal(err)
		}
		return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	}
	writePEM := func(name, kind string, der []byte) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: der}), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	clientCert := issue(2, "connectivity", x509.ExtKeyUsageClientAuth)
	clientKey, _ := x509.MarshalECPrivateKey(clientCert.PrivateKey.(*ecdsa.PrivateKey))
	roots := x509.NewCertPool()
	roots.AddCert(caCert)

	server := httptest.NewUnstartedServer(handler)
	server.TLS = &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{issue(3, "telemetry.local", x509.ExtKeyUsageServerAuth)},
		ClientCAs:    roots,
		ClientAuth:   tls.RequireAndVerifyClientCert,
	}
	server.StartTLS()

	client, err := NewTelemetryRuntimeClient(TelemetryRuntimeConfig{
		BaseURL:    server.URL,
		CAFile:     writePEM("ca.pem", "CERTIFICATE", caDER),
		CertFile:   writePEM("client.pem", "CERTIFICATE", clientCert.Certificate[0]),
		KeyFile:    writePEM("client.key", "EC PRIVATE KEY", clientKey),
		ServerName: "telemetry.local",
	})
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	return server, client
}

func TestTracePropagationFromEnvelopeToObservationEventAndHTTPTraceparent(t *testing.T) {
	const rawUpperTrace = "  0123456789ABCDEF0123456789ABCDEF  "
	const wantTrace = "0123456789abcdef0123456789abcdef"
	const wantTraceparent = "00-" + wantTrace + "-0000000000000001-01"

	var gotObsTraceparent string
	var gotObsTraceID string
	var gotEventTraceparent string
	var gotEventTraceID string

	server, client := newTestTelemetryServer(t, func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case sourceObservationBatchPath:
			gotObsTraceparent = request.Header.Get("traceparent")
			var payload struct {
				Observations []Observation `json:"observations"`
			}
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				writer.WriteHeader(http.StatusBadRequest)
				return
			}
			if len(payload.Observations) > 0 {
				gotObsTraceID = payload.Observations[0].TraceID
			}
			writer.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"receipts": []ObservationReceipt{{Status: "ACCEPTED", DeviceID: testDeviceID}},
			})
		case mqttRuntimeEventPath:
			gotEventTraceparent = request.Header.Get("traceparent")
			var evidence RuntimeEventEvidence
			if err := json.NewDecoder(request.Body).Decode(&evidence); err != nil {
				writer.WriteHeader(http.StatusBadRequest)
				return
			}
			gotEventTraceID = evidence.TraceID
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"accepted":true}`))
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	})
	defer server.Close()

	processor, err := NewProcessor(newFakeIdentities(), client)
	if err != nil {
		t.Fatal(err)
	}

	telPayload := []byte(`{"schemaVersion":"2.0","messageId":"` + testMessageID + `","gatewayId":"` + testGatewayA + `","timestamp":1786352400000,"sequence":1,"traceId":"` + rawUpperTrace + `","replay":false,"payload":{"devices":[{"deviceId":"METER-01","deviceTimestamp":1786352399000,"points":[{"code":"active_power","value":126.4,"quality":0,"unit":"kW"}]}]}}`)
	if _, err := processor.Process(context.Background(), telemetryTopic(testGatewayA), telPayload); err != nil {
		t.Fatalf("Process telemetry failed: %v", err)
	}
	if gotObsTraceID != wantTrace || gotObsTraceparent != wantTraceparent {
		t.Fatalf("telemetry traceId=%q traceparent=%q, want (%q, %q)", gotObsTraceID, gotObsTraceparent, wantTrace, wantTraceparent)
	}

	evtPayload := []byte(`{"schemaVersion":"2.0","messageId":"` + messageID(2) + `","gatewayId":"` + testGatewayA + `","timestamp":1786352401000,"sequence":2,"traceId":"` + rawUpperTrace + `","payload":{"eventType":"DEVICE_OFFLINE","sourceType":"DEVICE","sourceId":"METER-01","eventTime":1786352400000,"severity":"WARNING","data":{}}}`)
	if _, err := processor.Process(context.Background(), eventTopic(testGatewayA), evtPayload); err != nil {
		t.Fatalf("Process event failed: %v", err)
	}
	if gotEventTraceID != wantTrace || gotEventTraceparent != wantTraceparent {
		t.Fatalf("event traceId=%q traceparent=%q, want (%q, %q)", gotEventTraceID, gotEventTraceparent, wantTrace, wantTraceparent)
	}

	gotObsTraceparent = ""
	gotObsTraceID = "non-empty"
	invalidTelPayload := []byte(`{"schemaVersion":"2.0","messageId":"` + messageID(3) + `","gatewayId":"` + testGatewayA + `","timestamp":1786352402000,"sequence":3,"traceId":"00000000000000000000000000000000","replay":false,"payload":{"devices":[{"deviceId":"METER-01","deviceTimestamp":1786352401000,"points":[{"code":"active_power","value":127.0,"quality":0,"unit":"kW"}]}]}}`)
	if _, err := processor.Process(context.Background(), telemetryTopic(testGatewayA), invalidTelPayload); err != nil {
		t.Fatalf("Process telemetry with zero traceId failed: %v", err)
	}
	if gotObsTraceID != "" || gotObsTraceparent != "" {
		t.Fatalf("zero traceId propagated as (%q, %q), want empty", gotObsTraceID, gotObsTraceparent)
	}

	const callerTraceparent = "00-abcdef0123456789abcdef0123456789-1111111111111111-01"
	callerCtx := observability.ContextWithRemoteParent(context.Background(), client.tracer, callerTraceparent, "")
	if _, err := processor.Process(callerCtx, telemetryTopic(testGatewayA), telPayload); err != nil {
		t.Fatalf("Process telemetry with caller trace failed: %v", err)
	}
	if gotObsTraceparent != callerTraceparent {
		t.Fatalf("caller traceparent=%q, want %q", gotObsTraceparent, callerTraceparent)
	}
}
