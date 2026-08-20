package edgefleet

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"testing"
)

func TestInterruptedBootstrapResumesWithoutMutatingActiveState(t *testing.T) {
	dir := t.TempDir()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	replica, err := OpenReplica(dir, RuntimeDescriptor{RuntimeVersion: "1.4.0", ProtocolSchemaVersion: 1, Capabilities: []string{"registry.v1", "config.v1"}})
	if err != nil {
		t.Fatal(err)
	}
	replica.SetTrustedKey("release-key", publicKey)
	initialMeta, initialChunks, err := BuildSnapshot(10, 7, "release-a", []ProjectionItem{
		cloudProjection(OwnerRegistry, "device-a", 1, `{"name":"A"}`),
	}, 1)
	if err != nil {
		t.Fatal(err)
	}
	initialRelease := signedReleaseForSnapshot(t, initialMeta, privateKey)
	if err := replica.StageRelease(initialRelease, nil); err != nil {
		t.Fatal(err)
	}
	if err := replica.BeginSnapshot(initialMeta); err != nil {
		t.Fatal(err)
	}
	if err := replica.StageSnapshotChunk(initialChunks[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := replica.ActivateSnapshotRelease(initialMeta, initialRelease, sha256Hex([]byte("manifest-a")), nil); err != nil {
		t.Fatal(err)
	}

	nextMeta, nextChunks, err := BuildSnapshot(11, 8, "release-b", []ProjectionItem{
		cloudProjection(OwnerRegistry, "device-a", 2, `{"name":"A2"}`),
		cloudProjection(OwnerProfile, "profile-a", 3, `{"pollSeconds":5}`),
	}, 1)
	if err != nil {
		t.Fatal(err)
	}
	nextRelease := signedReleaseForSnapshot(t, nextMeta, privateKey)
	if err := replica.StageRelease(nextRelease, nil); err != nil {
		t.Fatal(err)
	}
	if err := replica.BeginSnapshot(nextMeta); err != nil {
		t.Fatal(err)
	}
	if err := replica.StageSnapshotChunk(nextChunks[0]); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenReplica(dir, RuntimeDescriptor{RuntimeVersion: "1.4.0", ProtocolSchemaVersion: 1, Capabilities: []string{"registry.v1", "config.v1"}})
	if err != nil {
		t.Fatal(err)
	}
	reopened.SetTrustedKey("release-key", publicKey)
	observed := reopened.ObservedState()
	if observed.ActiveSnapshotRevision != 10 {
		t.Fatalf("active snapshot mutated before activation: %d", observed.ActiveSnapshotRevision)
	}
	plan := reopened.ReconnectPlan(11, 0)
	if plan.Mode != ReconnectSnapshotResume || plan.ResumeChunk != 1 || plan.SnapshotRevision != 11 {
		t.Fatalf("resume plan=%+v", plan)
	}
	if err := reopened.StageSnapshotChunk(nextChunks[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.ActivateSnapshotRelease(nextMeta, nextRelease, sha256Hex([]byte("manifest-b")), nil); err != nil {
		t.Fatal(err)
	}
	if got := reopened.ObservedState().ActiveSnapshotRevision; got != 11 {
		t.Fatalf("active snapshot=%d want=11", got)
	}
}

func TestBadDeliveryIsQuarantinedWithoutSilentCursorAdvance(t *testing.T) {
	replica, err := OpenReplica(t.TempDir(), RuntimeDescriptor{RuntimeVersion: "1.0.0", ProtocolSchemaVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	first := delivery(1, "delivery-1", OwnerRegistry, "device-a", 1, `{"enabled":true}`)
	ack, err := replica.ApplyDelivery(first)
	if err != nil || ack.Status != DeliveryAcked || replica.ObservedState().DeliveryCursor != 1 {
		t.Fatalf("first delivery ack=%+v err=%v observed=%+v", ack, err, replica.ObservedState())
	}

	bad := delivery(2, "delivery-2", OwnerRegistry, "device-b", 1, `{"enabled":true}`)
	bad.PayloadDigest = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	ack, err = replica.ApplyDelivery(bad)
	if !errors.Is(err, ErrDeliveryQuarantined) || ack.Status != DeliveryQuarantined {
		t.Fatalf("bad delivery ack=%+v err=%v", ack, err)
	}

	third := delivery(3, "delivery-3", OwnerProfile, "profile-a", 1, `{"pollSeconds":10}`)
	ack, err = replica.ApplyDelivery(third)
	if err != nil || ack.Status != DeliveryAcked {
		t.Fatalf("unrelated delivery blocked: ack=%+v err=%v", ack, err)
	}
	if got := replica.ObservedState().DeliveryCursor; got != 1 {
		t.Fatalf("cursor silently crossed quarantine: %d", got)
	}

	if err := replica.DisposeQuarantine(2, sha256Hex([]byte("operator-approved-skip"))); err != nil {
		t.Fatal(err)
	}
	if got := replica.ObservedState().DeliveryCursor; got != 3 {
		t.Fatalf("explicit disposition did not advance contiguous cursor: %d", got)
	}
}

func TestCloudEdgeDualWriteOfOneFieldIsRejected(t *testing.T) {
	replica, err := OpenReplica(t.TempDir(), RuntimeDescriptor{RuntimeVersion: "1.0.0", ProtocolSchemaVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	item := delivery(1, "delivery-observed", OwnerObservedManifest, "edge-a", 2, `{"manifestDigest":"abc"}`)
	ack, err := replica.ApplyDelivery(item)
	if !errors.Is(err, ErrAuthorityViolation) || ack.Status != DeliveryQuarantined {
		t.Fatalf("edge-owned field accepted from Cloud: ack=%+v err=%v", ack, err)
	}
	if got := replica.ObservedState().DeliveryCursor; got != 0 {
		t.Fatalf("authority violation advanced cursor: %d", got)
	}
}

func TestReconnectChoosesDeltaOnlyWhenSnapshotAndCursorAreRetained(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	replica, err := OpenReplica(t.TempDir(), RuntimeDescriptor{RuntimeVersion: "1.0.0", ProtocolSchemaVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	replica.SetTrustedKey("release-key", publicKey)
	meta, chunks, err := BuildSnapshot(21, 12, "release-21", []ProjectionItem{cloudProjection(OwnerRegistry, "device-a", 1, `{"name":"A"}`)}, 1)
	if err != nil {
		t.Fatal(err)
	}
	release := signedReleaseForSnapshot(t, meta, privateKey)
	if err := replica.StageRelease(release, nil); err != nil {
		t.Fatal(err)
	}
	if err := replica.BeginSnapshot(meta); err != nil {
		t.Fatal(err)
	}
	if err := replica.StageSnapshotChunk(chunks[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := replica.ActivateSnapshotRelease(meta, release, sha256Hex([]byte("manifest-21")), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := replica.ApplyDelivery(delivery(1, "d1", OwnerRegistry, "device-a", 2, `{"name":"A2"}`)); err != nil {
		t.Fatal(err)
	}

	if plan := replica.ReconnectPlan(21, 1); plan.Mode != ReconnectDelta || plan.DeliveryCursor != 1 {
		t.Fatalf("delta plan=%+v", plan)
	}
	if plan := replica.ReconnectPlan(22, 1); plan.Mode != ReconnectSnapshot {
		t.Fatalf("changed desired snapshot should force full snapshot: %+v", plan)
	}
	if plan := replica.ReconnectPlan(21, 2); plan.Mode != ReconnectSnapshot {
		t.Fatalf("cursor below retained floor should force full snapshot: %+v", plan)
	}
}

func TestUnsignedOrIncompatibleOTAArtifactCannotStage(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	runtime := RuntimeDescriptor{RuntimeVersion: "2.3.0", ProtocolSchemaVersion: 1, Capabilities: []string{"ota.v1", "rollback.v1"}}
	replica, err := OpenReplica(t.TempDir(), runtime)
	if err != nil {
		t.Fatal(err)
	}
	replica.SetTrustedKey("release-key-1", publicKey)

	payload := OTAArtifactPayload{
		ArtifactID: "ota-2026-08", Version: "2.4.0", PackageRef: "artifact://test/ota-2026-08", PackageSHA256: sha256Hex([]byte("package-v2.4.0")),
		MinRuntimeVersion: "2.0.0", MaxRuntimeVersion: "2.9.9", RequiredCapabilities: []string{"ota.v1"}, RollbackArtifactID: "ota-2026-07",
	}
	unsigned := SignedOTAArtifact{Payload: payload, Digest: otaArtifactDigest(payload), SignerKeyID: "release-key-1"}
	if err := replica.StageOTA(unsigned, payload.PackageSHA256, func() error { return nil }); !errors.Is(err, ErrSignatureInvalid) {
		t.Fatalf("unsigned artifact stage err=%v", err)
	}
	if _, err := replica.ActivateOTA(unsigned, func() error { return nil }); !errors.Is(err, ErrSignatureInvalid) {
		t.Fatalf("unsigned artifact activate err=%v", err)
	}

	signed, err := SignOTAArtifact(payload, "release-key-1", privateKey)
	if err != nil {
		t.Fatal(err)
	}
	incompatible := signed
	incompatible.Payload.MinRuntimeVersion = "3.0.0"
	incompatible.Payload.MaxRuntimeVersion = "3.9.9"
	incompatible, err = SignOTAArtifact(incompatible.Payload, "release-key-1", privateKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := replica.StageOTA(incompatible, incompatible.Payload.PackageSHA256, func() error { return nil }); !errors.Is(err, ErrRuntimeIncompatible) {
		t.Fatalf("incompatible artifact stage err=%v", err)
	}
	if _, err := replica.ActivateOTA(incompatible, func() error { return nil }); !errors.Is(err, ErrRuntimeIncompatible) {
		t.Fatalf("incompatible artifact activate err=%v", err)
	}
	if err := replica.StageOTA(signed, signed.Payload.PackageSHA256, func() error { return nil }); err != nil {
		t.Fatalf("compatible signed artifact rejected: %v", err)
	}
	if result, err := replica.ActivateOTA(signed, func() error { return nil }); err != nil || result.ActiveArtifactID != signed.Payload.ArtifactID || result.RolledBack {
		t.Fatalf("compatible OTA activation result=%+v err=%v", result, err)
	}

	nextPayload := payload
	nextPayload.ArtifactID = "ota-2026-09"
	nextPayload.Version = "2.5.0"
	nextPayload.PackageRef = "artifact://test/ota-2026-09"
	nextPayload.PackageSHA256 = sha256Hex([]byte("package-v2.5.0"))
	nextPayload.RollbackArtifactID = signed.Payload.ArtifactID
	next, err := SignOTAArtifact(nextPayload, "release-key-1", privateKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := replica.StageOTA(next, next.Payload.PackageSHA256, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	result, err := replica.ActivateOTA(next, func() error { return errors.New("startup health failed") })
	if err != nil || !result.RolledBack || result.ActiveArtifactID != signed.Payload.ArtifactID || result.PreviousArtifactID != next.Payload.ArtifactID {
		t.Fatalf("OTA rollback result=%+v err=%v", result, err)
	}
}

func TestOfflinePressureShedsDiagnosticsBeforeSafetyControlAuditEvidence(t *testing.T) {
	directory := t.TempDir()
	buffer, err := OpenOfflineBuffer(directory, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []OfflineItem{
		{ID: "diag-1", Class: EvidenceDiagnostic, Payload: bytesOfSize(40)},
		{ID: "telemetry-normal", Class: EvidenceTelemetryNormal, Payload: bytesOfSize(30)},
		{ID: "audit-1", Class: EvidenceAudit, Payload: bytesOfSize(20)},
	} {
		if _, err := buffer.Admit(item); err != nil {
			t.Fatal(err)
		}
	}
	result, err := buffer.Admit(OfflineItem{ID: "safety-1", Class: EvidenceSafety, Payload: bytesOfSize(35)})
	if err != nil {
		t.Fatal(err)
	}
	if !contains(result.ShedIDs, "diag-1") {
		t.Fatalf("expected diagnostic shedding before safety rejection, result=%+v", result)
	}
	reopened, err := OpenOfflineBuffer(directory, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !reopened.Contains("audit-1") || !reopened.Contains("safety-1") {
		t.Fatalf("high-value evidence was not durable: audit=%v safety=%v", reopened.Contains("audit-1"), reopened.Contains("safety-1"))
	}
	if reopened.Contains("diag-1") {
		t.Fatal("diagnostic evidence was retained on disk ahead of safety evidence")
	}
	pending := reopened.Pending()
	if len(pending) != 3 || pending[0].ID != "safety-1" || pending[1].ID != "audit-1" || pending[2].ID != "telemetry-normal" {
		t.Fatalf("offline flush priority is not safety/audit before telemetry: %+v", pending)
	}
}

func signedReleaseForSnapshot(t testing.TB, meta SnapshotMeta, privateKey ed25519.PrivateKey) SignedEdgeRelease {
	t.Helper()
	release, err := SignEdgeRelease(EdgeReleasePayload{
		ReleaseID: meta.ReleaseID, RuntimeRevision: "runtime-test", ManifestRevision: "manifest-test",
		RegistryProjectionRevision: 1, DriverRevision: "driver-test", SafetyPolicyRevision: "safety-test",
		DesiredConfigRevision: meta.DesiredRevision, MinRuntimeVersion: "1.0.0", MaxRuntimeVersion: "9.9.9",
	}, "release-key", privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return release
}

func cloudProjection(domain OwnerDomain, entityID string, revision uint64, raw string) ProjectionItem {
	return ProjectionItem{OwnerDomain: domain, EntityID: entityID, OwnerRevision: revision, Payload: json.RawMessage(raw)}
}

func delivery(cursor uint64, id string, domain OwnerDomain, orderingKey string, revision uint64, raw string) DeliveryItem {
	payload := json.RawMessage(raw)
	return DeliveryItem{
		Cursor: cursor, DeliveryID: id, OwnerDomain: domain, OrderingKey: orderingKey, SourceRevision: revision,
		Payload: payload, PayloadDigest: sha256Hex(payload),
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func bytesOfSize(size int) []byte {
	value := make([]byte, size)
	for index := range value {
		value[index] = byte('a' + index%26)
	}
	return value
}
