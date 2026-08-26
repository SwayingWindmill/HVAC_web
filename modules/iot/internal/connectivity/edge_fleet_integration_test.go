package connectivity

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/quanlaihe/hvac-web/libs/edgefleet"
)

func TestEdgeFleetDurableQuarantineKeepsContiguousCursor(t *testing.T) {
	dsn := os.Getenv("CONNECTIVITY_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("CONNECTIVITY_POSTGRES_DSN is not set")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()

	const (
		tenantID      = "0191f100-0000-7000-8000-000000000001"
		siteID        = "0191f100-0000-7000-8000-000000000002"
		profileID     = "0191f100-0000-7000-8000-000000000003"
		integrationID = "0191f100-0000-7000-8000-000000000004"
		credentialID  = "0191f100-0000-7000-8000-000000000005"
		sessionID     = "0191f100-0000-7000-8000-000000000006"
		edgeNodeID    = "0191f100-0000-7000-8000-000000000007"
		enrollmentID  = "0191f100-0000-7000-8000-000000000008"
		identityID    = "0191f100-0000-7000-8000-000000000009"
		handshakeID   = "0191f100-0000-7000-8000-000000000010"
		releaseID     = "0191f100-0000-7000-8000-000000000011"
		delivery1ID   = "0191f100-0000-7000-8000-000000000012"
		delivery2ID   = "0191f100-0000-7000-8000-000000000013"
		delivery3ID   = "0191f100-0000-7000-8000-000000000014"
		gatewayID     = "S12-GATEWAY-FLEET"
	)

	cleanup := func() {
		for _, table := range []string{
			"connectivity.edge_fleet_events", "connectivity.edge_ota_assignments", "connectivity.edge_ota_campaigns",
			"connectivity.edge_ota_artifacts", "connectivity.edge_delivery_items", "connectivity.edge_delivery_cursors",
			"connectivity.edge_sync_sessions", "connectivity.edge_snapshot_chunks", "connectivity.edge_snapshots",
			"connectivity.observed_edge_states", "connectivity.desired_edge_states", "connectivity.edge_handshakes",
			"connectivity.edge_identity_bindings", "connectivity.edge_enrollments", "connectivity.edge_nodes", "connectivity.edge_releases",
			"connectivity.sessions", "connectivity.credential_refs", "connectivity.integration_instances", "connectivity.transport_profiles",
		} {
			_, _ = admin.Exec(ctx, `DELETE FROM `+table+` WHERE tenant_id=$1::uuid`, tenantID)
		}
		_, _ = admin.Exec(ctx, `DELETE FROM core_registry.sites WHERE tenant_id=$1::uuid`, tenantID)
		_, _ = admin.Exec(ctx, `DELETE FROM iam.tenants WHERE id=$1::uuid`, tenantID)
	}
	cleanup()
	t.Cleanup(cleanup)

	now := time.Now().UTC()
	statements := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO iam.tenants (id,code,display_name,timezone,currency,country,status,revision,created_at,updated_at) VALUES ($1::uuid,'s12-fleet-test','S12 fleet test','UTC','USD','US','ACTIVE',1,$2,$2)`, []any{tenantID, now}},
		{`INSERT INTO core_registry.sites (id,code,display_name,timezone,status,revision,created_at,updated_at,tenant_id) VALUES ($1::uuid,'s12-fleet-site','S12 fleet site','UTC','ACTIVE',1,$3,$3,$2::uuid)`, []any{siteID, tenantID, now}},
		{`INSERT INTO connectivity.transport_profiles (id,tenant_id,protocol,broker_origin,topic_namespace,status,revision,created_at,updated_at) VALUES ($1::uuid,$2::uuid,'MQTT','tls://mqtt-broker:8883','energy/v1','ACTIVE',1,$3,$3)`, []any{profileID, tenantID, now}},
		{`INSERT INTO connectivity.integration_instances (id,tenant_id,site_id,transport_profile_id,gateway_external_id,status,revision,created_at,updated_at) VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,'ACTIVE',1,$6,$6)`, []any{integrationID, tenantID, siteID, profileID, gatewayID, now}},
		{`INSERT INTO connectivity.credential_refs (id,tenant_id,integration_instance_id,credential_kind,secret_ref,certificate_fingerprint_sha256,token_hash_sha256,status,valid_from,valid_until,rotated_from_id,revoked_at,revision,created_at,updated_at) VALUES ($1::uuid,$2::uuid,$3::uuid,'MTLS_CERTIFICATE','secretref://s12/fleet','bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb',NULL,'ACTIVE',$4,$5,NULL,NULL,1,$4,$4)`, []any{credentialID, tenantID, integrationID, now.Add(-time.Minute), now.Add(time.Hour)}},
		{`INSERT INTO connectivity.sessions (id,tenant_id,site_id,integration_instance_id,credential_ref_id,credential_revision,gateway_external_id,status,opened_at,expires_at,closed_at,close_reason,revision,updated_at) VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,1,$6,'ACTIVE',$7,$8,NULL,NULL,1,$7)`, []any{sessionID, tenantID, siteID, integrationID, credentialID, gatewayID, now, now.Add(30 * time.Minute)}},
	}
	for _, statement := range statements {
		if _, err := admin.Exec(ctx, statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}

	store, err := Open(ctx, dsn, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	hardwareHash := digestHex([]byte("s12-edge-hardware"))
	challengeHash := digestHex([]byte("s12-enrollment-challenge"))
	if err := store.RegisterEdgeNode(ctx, EdgeNodeInput{ID: edgeNodeID, SiteID: siteID, IntegrationInstanceID: integrationID, ExternalID: gatewayID, HardwareIdentityHash: hardwareHash}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateEdgeEnrollment(ctx, EdgeEnrollmentInput{ID: enrollmentID, EdgeNodeID: edgeNodeID, HardwareIdentityHash: hardwareHash, ChallengeHash: challengeHash, ExpiresAt: now.Add(10 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if err := store.ConsumeEdgeEnrollment(ctx, EdgeEnrollmentConsumeInput{EnrollmentID: enrollmentID, HardwareIdentityHash: hardwareHash, ChallengeHash: challengeHash, CredentialRefID: credentialID, IdentityBindingID: identityID}); err != nil {
		t.Fatal(err)
	}
	result, err := store.RecordEdgeHandshake(ctx, EdgeHandshakeInput{
		ID: handshakeID, EdgeNodeID: edgeNodeID, SessionID: sessionID,
		Request: edgefleet.HandshakeRequest{EdgeID: gatewayID, RuntimeVersion: "2.3.0", ProtocolSchemaVersion: 1, Capabilities: []string{"config.v1", "registry.v1"}, MaxPayloadBytes: 1 << 20, CredentialRevision: 1},
		Policy:  edgefleet.HandshakePolicy{ProtocolSchemaVersion: 1, MinRuntimeVersion: "2.0.0", MaxRuntimeVersion: "2.9.9", RequiredCapabilities: []string{"registry.v1"}, MaxPayloadBytes: 1 << 20},
	})
	if err != nil || result.Status != edgefleet.HandshakeAccepted {
		t.Fatalf("handshake=%+v err=%v", result, err)
	}

	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	release, err := edgefleet.SignEdgeRelease(edgefleet.EdgeReleasePayload{
		ReleaseID: releaseID, RuntimeRevision: "runtime-2.3", ManifestRevision: "manifest-1", RegistryProjectionRevision: 1,
		DriverRevision: "driver-1", SafetyPolicyRevision: "safety-1", DesiredConfigRevision: 1,
		MinRuntimeVersion: "2.0.0", MaxRuntimeVersion: "2.9.9", RequiredCapabilities: []string{"registry.v1"},
	}, "release-key-1", privateKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PublishEdgeRelease(ctx, release); err != nil {
		t.Fatal(err)
	}
	meta, chunks, err := edgefleet.BuildSnapshot(1, 1, releaseID, []edgefleet.ProjectionItem{{OwnerDomain: edgefleet.OwnerRegistry, EntityID: "device-a", OwnerRevision: 1, Payload: json.RawMessage(`{"name":"A"}`)}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PublishEdgeSnapshot(ctx, edgeNodeID, meta, chunks); err != nil {
		t.Fatal(err)
	}
	if err := store.SetDesiredEdgeState(ctx, DesiredEdgeStateInput{EdgeNodeID: edgeNodeID, ReleaseID: releaseID, DesiredRevision: 1, SnapshotRevision: 1}); err != nil {
		t.Fatal(err)
	}

	appendDelivery := func(id string, key string, revision uint64) edgefleet.DeliveryItem {
		t.Helper()
		payload := json.RawMessage(`{"enabled":true}`)
		item, appendErr := store.AppendEdgeDelivery(ctx, EdgeDeliveryInput{
			DeliveryID: id, EdgeNodeID: edgeNodeID, OwnerDomain: edgefleet.OwnerRegistry, OrderingKey: key, SourceRevision: revision,
			PayloadDigest: digestHex(payload), Payload: payload, Priority: "CONFIG_NORMAL",
		})
		if appendErr != nil {
			t.Fatal(appendErr)
		}
		return item
	}
	first := appendDelivery(delivery1ID, "device-a", 2)
	second := appendDelivery(delivery2ID, "device-b", 1)
	third := appendDelivery(delivery3ID, "device-c", 1)

	ack1 := edgefleet.DeliveryAck{Cursor: first.Cursor, DeliveryID: first.DeliveryID, Status: edgefleet.DeliveryAcked, AppliedOwnerRevision: 2, PayloadDigest: first.PayloadDigest}
	if got, err := store.RecordEdgeDeliveryAck(ctx, edgeNodeID, ack1); err != nil || got.CommittedCursor != 1 {
		t.Fatalf("first ack=%+v err=%v", got, err)
	}
	ack2 := edgefleet.DeliveryAck{Cursor: second.Cursor, DeliveryID: second.DeliveryID, Status: edgefleet.DeliveryQuarantined, Reason: "BAD_REFERENCE", PayloadDigest: second.PayloadDigest}
	if got, err := store.RecordEdgeDeliveryAck(ctx, edgeNodeID, ack2); err != nil || got.CommittedCursor != 1 {
		t.Fatalf("quarantine ack=%+v err=%v", got, err)
	}
	ack3 := edgefleet.DeliveryAck{Cursor: third.Cursor, DeliveryID: third.DeliveryID, Status: edgefleet.DeliveryAcked, AppliedOwnerRevision: 1, PayloadDigest: third.PayloadDigest}
	if got, err := store.RecordEdgeDeliveryAck(ctx, edgeNodeID, ack3); err != nil || got.CommittedCursor != 1 {
		t.Fatalf("unrelated ack crossed quarantine=%+v err=%v", got, err)
	}
	committed, err := store.DisposeEdgeDelivery(ctx, edgeNodeID, second.Cursor, digestHex([]byte("operator-disposition")))
	if err != nil || committed != 3 {
		t.Fatalf("disposition committed=%d err=%v", committed, err)
	}
}

func digestHex(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}
