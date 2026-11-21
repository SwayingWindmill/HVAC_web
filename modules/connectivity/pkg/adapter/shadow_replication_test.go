package adapter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
)

func TestEncodeAndDecodeShadowReplicationEnvelope(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	desired := map[string]any{
		"desiredRevision": 5,
		"pointMappings":   []any{},
	}
	wireBytes, msgID, err := EncodeShadowChangeBatch(testGatewayA, 5, desired, now)
	if err != nil {
		t.Fatalf("EncodeShadowChangeBatch failed: %v", err)
	}
	if !replicationUUIDPattern.MatchString(msgID) {
		t.Fatalf("expected UUID messageId, got %q", msgID)
	}

	var env ShadowReplicationEnvelope
	if err := json.Unmarshal(wireBytes, &env); err != nil {
		t.Fatalf("unmarshal encoded CHANGE_BATCH: %v", err)
	}
	if env.SchemaVersion != "1.0" || env.EdgeID != testGatewayA || env.Type != ShadowReplicationTypeBatch || env.SentAt <= 0 {
		t.Fatalf("unexpected encoded envelope header: %+v", env)
	}
	var batch ShadowChangeBatchPayload
	if err := json.Unmarshal(env.Payload, &batch); err != nil {
		t.Fatalf("unmarshal CHANGE_BATCH payload: %v", err)
	}
	if len(batch.Items) != 1 {
		t.Fatalf("expected 1 batch item, got %d", len(batch.Items))
	}
	item := batch.Items[0]
	wantSum := sha256.Sum256(item.Payload)
	if item.Cursor != 5 || item.SourceRevision != 5 || item.OwnerDomain != ShadowOwnerDomainDriverConfig ||
		item.OrderingKey != "gateway-shadow:"+testGatewayA || item.Tombstone ||
		item.PayloadDigest != hex.EncodeToString(wantSum[:]) {
		t.Fatalf("unexpected CHANGE_BATCH item: %+v", item)
	}

	rev := int64(5)
	ackBytes, err := json.Marshal(ShadowReplicationEnvelope{
		SchemaVersion: "1.0",
		MessageID:     testMessageID,
		EdgeID:        testGatewayA,
		SentAt:        now.UnixMilli(),
		Type:          ShadowReplicationTypeAck,
		Payload: func() json.RawMessage {
			b, _ := json.Marshal(ShadowChangeAckPayload{
				Cursor:               5,
				DeliveryID:           item.DeliveryID,
				Status:               ShadowAckStatusAcked,
				AppliedOwnerRevision: &rev,
				PayloadDigest:        item.PayloadDigest,
			})
			return b
		}(),
	})
	if err != nil {
		t.Fatal(err)
	}
	decEnv, decAck, err := DecodeShadowChangeAck(ackBytes, testGatewayA)
	if err != nil {
		t.Fatalf("DecodeShadowChangeAck failed: %v", err)
	}
	if decEnv.MessageID != testMessageID || decAck.Status != ShadowAckStatusAcked || decAck.AppliedOwnerRevision == nil || *decAck.AppliedOwnerRevision != 5 {
		t.Fatalf("unexpected decoded CHANGE_ACK: env=%+v ack=%+v", decEnv, decAck)
	}

	// Reject gateway mismatch, unknown fields, and bad digest.
	if _, _, err := DecodeShadowChangeAck(ackBytes, testGatewayB); err == nil {
		t.Fatal("expected gateway mismatch rejection")
	}
	badDigest := strings.Replace(string(ackBytes), item.PayloadDigest, "INVALID-DIGEST", 1)
	if _, _, err := DecodeShadowChangeAck([]byte(badDigest), testGatewayA); err == nil {
		t.Fatal("expected invalid payloadDigest rejection")
	}
	extraField := strings.Replace(string(ackBytes), `"status":"ACKED"`, `"status":"ACKED","extra":1`, 1)
	if _, _, err := DecodeShadowChangeAck([]byte(extraField), testGatewayA); err == nil {
		t.Fatal("expected strict unknown field rejection")
	}
}

func TestProcessorHandlesShadowAckMetricsHooksAndQuarantine(t *testing.T) {
	t.Parallel()

	identities := newFakeIdentities()
	metrics := observability.NewRegistry()

	var onlineUpdates []bool
	var acks []ShadowChangeAckPayload
	var packetChannels []string
	processor, err := NewProcessor(
		identities,
		&fakeTelemetry{},
		WithProcessorMetrics(metrics),
		WithGatewayPresenceHook(func(_ string, online bool) {
			onlineUpdates = append(onlineUpdates, online)
		}),
		WithShadowAckHook(func(_ context.Context, _ Gateway, ack ShadowChangeAckPayload) {
			acks = append(acks, ack)
		}),
		WithProcessorPacketHook(func(_, channel, _, _ string, _ string, _ []byte) {
			packetChannels = append(packetChannels, channel)
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	digest := strings.Repeat("a", 64)
	validAckPayload := func(status, reason string) []byte {
		inner, _ := json.Marshal(ShadowChangeAckPayload{
			Cursor:        3,
			DeliveryID:    testMessageID,
			Status:        status,
			Reason:        reason,
			PayloadDigest: digest,
		})
		outer, _ := json.Marshal(ShadowReplicationEnvelope{
			SchemaVersion: "1.0",
			MessageID:     testMessageID,
			EdgeID:        testGatewayA,
			SentAt:        1786352400000,
			Type:          ShadowReplicationTypeAck,
			Payload:       inner,
		})
		return outer
	}

	outcomes, err := processor.ProcessPrefix(context.Background(), []UplinkMessage{{
		Topic:   ShadowUplinkTopic(testGatewayA),
		Payload: validAckPayload(ShadowAckStatusAcked, ""),
	}})
	if err != nil || len(outcomes) != 1 || outcomes[0].Err != nil || outcomes[0].Result.Accepted != 1 || outcomes[0].Result.MessageType != MessageTypeShadow {
		t.Fatalf("unexpected ACKED outcome: outcomes=%+v err=%v", outcomes, err)
	}

	resQ, err := processor.Process(context.Background(), ShadowUplinkTopic(testGatewayA), validAckPayload(ShadowAckStatusQuarantined, "checksum mismatch"))
	if err != nil || resQ.Quarantined != 1 || resQ.MessageType != MessageTypeShadow {
		t.Fatalf("unexpected QUARANTINED outcome: res=%+v err=%v", resQ, err)
	}

	outcomesBad, err := processor.ProcessPrefix(context.Background(), []UplinkMessage{{
		Topic:   ShadowUplinkTopic(testGatewayA),
		Payload: []byte(`{"bad":true}`),
	}})
	if err != nil || len(outcomesBad) != 1 || !isPermanentMessageError(outcomesBad[0].Err) {
		t.Fatalf("expected permanent quarantine error on bad shadow uplink, got %+v err=%v", outcomesBad, err)
	}

	if len(acks) != 2 || len(onlineUpdates) != 2 || len(identities.quarantines) != 1 {
		t.Fatalf("acks=%d onlineUpdates=%d quarantined=%d", len(acks), len(onlineUpdates), len(identities.quarantines))
	}
	if len(packetChannels) != 3 || packetChannels[0] != "SHADOW" || packetChannels[2] != "QUARANTINE" {
		t.Fatalf("unexpected packet channels: %v", packetChannels)
	}

	rec := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	metricsBody := rec.Body.String()
	for _, want := range []string{
		`hvac_mqtt_shadow_replication_total{outcome="accepted"} 1`,
		`hvac_mqtt_shadow_replication_total{outcome="quarantined"} 1`,
		`hvac_mqtt_shadow_replication_total{outcome="rejected"} 1`,
	} {
		if !strings.Contains(metricsBody, want) {
			t.Fatalf("expected metric %q in:\n%s", want, metricsBody)
		}
	}
}
