package simulator

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func buildTestChangeBatch(t *testing.T, gwID string, rev int64, desired GatewayDesiredConfig, corruptDigest bool) ([]byte, string, string) {
	t.Helper()

	desiredBytes, err := json.Marshal(desired)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(desiredBytes)
	digest := hex.EncodeToString(sum[:])
	if corruptDigest {
		digest = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	}
	deliveryID := "0193f000-7000-7000-8000-000000000099"
	batchBytes, err := json.Marshal(edgeChangeBatchPayload{
		Items: []edgeChangeBatchItem{
			{
				Cursor:         rev,
				DeliveryID:     deliveryID,
				OwnerDomain:    "DRIVER_CONFIG",
				OrderingKey:    "gateway-shadow:" + gwID,
				SourceRevision: rev,
				PayloadDigest:  digest,
				Tombstone:      false,
				Payload:        desiredBytes,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	wireBytes, err := json.Marshal(edgeReplicationEnvelope{
		SchemaVersion: edgeReplicationSchemaVersion,
		MessageID:     "0193f000-7000-7000-8000-000000000098",
		EdgeID:        gwID,
		SentAt:        1786352400000,
		Type:          edgeReplicationTypeBatch,
		Payload:       batchBytes,
	})
	if err != nil {
		t.Fatal(err)
	}
	return wireBytes, deliveryID, digest
}

func TestHandleDownlinkShadowMessageInlineAckAndQuarantine(t *testing.T) {
	const gwID = "0193f000-2000-7000-8000-000000000001"
	shadow := newEdgeConfigShadow(filepath.Join(t.TempDir(), "shadow.v1.json"))
	cfg := MQTTGatewayConfig{GatewayID: gwID}

	var publishedTopics []string
	var publishedPayloads [][]byte
	publishFn := func(_ context.Context, topic string, payload []byte) error {
		publishedTopics = append(publishedTopics, topic)
		publishedPayloads = append(publishedPayloads, payload)
		return nil
	}

	desired := GatewayDesiredConfig{
		DesiredRevision: 3,
		PointMappings: []GatewayPointMapping{
			{SourceChannel: "ch-01:temp", PrimaryDeviceKey: "ch-01", PrimaryPointCode: "supply_temp"},
		},
		SamplingPolicies:    []GatewaySamplingPolicy{},
		ConcentratorConfigs: map[string]ConcentratorUnitShadow{},
	}
	validBatch, deliveryID, digest := buildTestChangeBatch(t, gwID, 3, desired, false)
	if err := handleDownlinkShadowMessage(context.Background(), cfg, shadow, nil, publishFn, validBatch); err != nil {
		t.Fatalf("inline handleDownlinkShadowMessage failed: %v", err)
	}
	if len(publishedTopics) != 1 || publishedTopics[0] != mqttShadowUplinkTopic(gwID) {
		t.Fatalf("unexpected ACK topic list: %v", publishedTopics)
	}
	var envAck edgeReplicationEnvelope
	if err := json.Unmarshal(publishedPayloads[0], &envAck); err != nil {
		t.Fatal(err)
	}
	var ack edgeChangeAckPayload
	if err := json.Unmarshal(envAck.Payload, &ack); err != nil {
		t.Fatal(err)
	}
	if ack.Status != edgeShadowAckStatusAcked || ack.DeliveryID != deliveryID || ack.PayloadDigest != digest ||
		ack.AppliedOwnerRevision == nil || *ack.AppliedOwnerRevision != 3 {
		t.Fatalf("unexpected inline CHANGE_ACK: %+v", ack)
	}
	if snap := shadow.snapshot(); snap.AppliedRevision != 3 || len(snap.PointMappings) != 1 {
		t.Fatalf("expected inline shadow rev 3 persisted, got %+v", snap)
	}

	badBatch, _, badDigest := buildTestChangeBatch(t, gwID, 4, desired, true)
	if err := handleDownlinkShadowMessage(context.Background(), cfg, shadow, nil, publishFn, badBatch); err != nil {
		t.Fatalf("quarantine handleDownlinkShadowMessage failed: %v", err)
	}
	if err := json.Unmarshal(publishedPayloads[1], &envAck); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(envAck.Payload, &ack); err != nil {
		t.Fatal(err)
	}
	if ack.Status != edgeShadowAckStatusQuar || ack.PayloadDigest != badDigest || ack.Reason == "" {
		t.Fatalf("expected QUARANTINED CHANGE_ACK on digest mismatch, got %+v", ack)
	}
	if snap := shadow.snapshot(); snap.AppliedRevision != 3 {
		t.Fatalf("expected shadow revision unchanged (3) after quarantine, got %d", snap.AppliedRevision)
	}

	invalidItemEnv, _ := json.Marshal(edgeReplicationEnvelope{
		SchemaVersion: edgeReplicationSchemaVersion,
		MessageID:     "0193f000-7000-7000-8000-000000000097",
		EdgeID:        gwID,
		SentAt:        1786352400000,
		Type:          edgeReplicationTypeBatch,
		Payload:       json.RawMessage(`{"items":[{"cursor":0,"deliveryId":"bad","ownerDomain":"DRIVER_CONFIG","orderingKey":"k","sourceRevision":0,"payloadDigest":"bad"}]}`),
	})
	if err := handleDownlinkShadowMessage(context.Background(), cfg, shadow, nil, publishFn, invalidItemEnv); err == nil {
		t.Fatal("expected malformed CHANGE_BATCH item to return error instead of skipping")
	}
}

func TestHandleDownlinkShadowMessageTriggersHTTPSyncAndAcks(t *testing.T) {
	const gwID = "0193f000-2000-7000-8000-000000000001"
	dir := t.TempDir()
	caPath, certPath, keyPath, serverCert := writeTestMTLSBundle(t, dir)

	desired := GatewayDesiredConfig{
		DesiredRevision: 5,
		PointMappings: []GatewayPointMapping{
			{SourceChannel: "ch-01:pwr", PrimaryDeviceKey: "ch-01", PrimaryPointCode: "active_power"},
		},
		SamplingPolicies:    []GatewaySamplingPolicy{},
		ConcentratorConfigs: map[string]ConcentratorUnitShadow{},
	}

	var mu sync.Mutex
	var pullCount, reportCount int
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/gateway/v1/config-shadow":
			mu.Lock()
			pullCount++
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(GatewayShadowPullResponse{
				GatewayID:       gwID,
				DesiredRevision: desired.DesiredRevision,
				HasDelta:        true,
				Desired:         &desired,
			})
		case r.Method == http.MethodPost && r.URL.Path == "/gateway/v1/config-shadow/report":
			mu.Lock()
			reportCount++
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"syncStatus":"SYNCED"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	caPEM, err := os.ReadFile(caPath)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(caPEM)
	srv.TLS = &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{serverCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    pool,
	}
	srv.StartTLS()
	defer srv.Close()

	cfg := MQTTGatewayConfig{
		GatewayID:            gwID,
		EnrollmentURL:        srv.URL,
		EnrollmentCAFile:     caPath,
		EnrollmentServerName: "127.0.0.1",
		CertFile:             certPath,
		KeyFile:              keyPath,
		QueueDirectory:       filepath.Join(dir, "queue"),
	}
	shadow := newEdgeConfigShadow(filepath.Join(dir, "shadow.v1.json"))
	desiredBytes, err := json.Marshal(desired)
	if err != nil {
		t.Fatal(err)
	}
	digestSum := sha256.Sum256(desiredBytes)
	digest := hex.EncodeToString(digestSum[:])
	multiBatchBytes, err := json.Marshal(edgeChangeBatchPayload{
		Items: []edgeChangeBatchItem{
			{
				Cursor:         4,
				DeliveryID:     "0193f000-7000-7000-8000-000000000091",
				OwnerDomain:    "DRIVER_CONFIG",
				OrderingKey:    "gateway-shadow:" + gwID,
				SourceRevision: 4,
				PayloadDigest:  digest,
				Payload:        desiredBytes,
			},
			{
				Cursor:         5,
				DeliveryID:     "0193f000-7000-7000-8000-000000000092",
				OwnerDomain:    "DRIVER_CONFIG",
				OrderingKey:    "gateway-shadow:" + gwID,
				SourceRevision: 5,
				PayloadDigest:  digest,
				Payload:        desiredBytes,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	validBatch, err := json.Marshal(edgeReplicationEnvelope{
		SchemaVersion: edgeReplicationSchemaVersion,
		MessageID:     "0193f000-7000-7000-8000-000000000098",
		EdgeID:        gwID,
		SentAt:        1786352400000,
		Type:          edgeReplicationTypeBatch,
		Payload:       multiBatchBytes,
	})
	if err != nil {
		t.Fatal(err)
	}

	var ackPayloads [][]byte
	if err := handleDownlinkShadowMessage(context.Background(), cfg, shadow, nil, func(_ context.Context, _ string, payload []byte) error {
		ackPayloads = append(ackPayloads, payload)
		return nil
	}, validBatch); err != nil {
		t.Fatalf("HTTP shadow replication handler failed: %v", err)
	}
	mu.Lock()
	pulls, reports := pullCount, reportCount
	mu.Unlock()
	if pulls != 1 || reports != 1 {
		t.Fatalf("expected coalesced pull=1 report=1 across 2-item batch, got pull=%d report=%d", pulls, reports)
	}
	if len(ackPayloads) != 2 {
		t.Fatalf("expected 2 CHANGE_ACK payloads, got %d", len(ackPayloads))
	}
	var envAck edgeReplicationEnvelope
	if err := json.Unmarshal(ackPayloads[1], &envAck); err != nil {
		t.Fatal(err)
	}
	var ack edgeChangeAckPayload
	if err := json.Unmarshal(envAck.Payload, &ack); err != nil {
		t.Fatal(err)
	}
	if ack.Status != edgeShadowAckStatusAcked || ack.AppliedOwnerRevision == nil || *ack.AppliedOwnerRevision != 5 {
		t.Fatalf("expected ACKED revision 5 after HTTP sync, got %+v", ack)
	}
}
