package simulator

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/eclipse/paho.golang/paho"
	"github.com/quanlaihe/hvac-web/libs/edgefleet"
)

const (
	edgeFleetRuntimeVersion = "1.0.0"
	edgeFleetMaxPayload     = 2 << 20
)

type edgeFleetHandler struct {
	gatewayID string
	config    MQTTGatewayConfig
	replica   *edgefleet.Replica
	runtime   *EdgeControlRuntime
	spool     *mqttEvidenceSpool
	now       func() time.Time
}

func newEdgeFleetHandler(config MQTTGatewayConfig, gatewayID string, runtime *EdgeControlRuntime, spool *mqttEvidenceSpool) (*edgeFleetHandler, error) {
	if runtime == nil || spool == nil || strings.TrimSpace(gatewayID) == "" {
		return nil, errors.New("Edge Fleet runtime dependencies are invalid")
	}
	keyHex, err := os.ReadFile(config.FleetReleasePublicKeyFile)
	if err != nil {
		return nil, fmt.Errorf("read Edge Fleet release public key: %w", err)
	}
	key, err := hex.DecodeString(strings.TrimSpace(string(keyHex)))
	if err != nil || len(key) != ed25519.PublicKeySize {
		return nil, errors.New("Edge Fleet release public key must be 32-byte Ed25519 hex")
	}
	replica, err := edgefleet.OpenReplica(filepath.Join(config.QueueDirectory, "fleet"), edgefleet.RuntimeDescriptor{
		RuntimeVersion:        edgeFleetRuntimeVersion,
		ProtocolSchemaVersion: 1,
		Capabilities:          []string{"registry.v1", "config.v1", "ota.v1", "rollback.v1"},
		MaxPayloadBytes:       edgeFleetMaxPayload,
	})
	if err != nil {
		return nil, err
	}
	replica.SetTrustedKey(config.FleetReleaseKeyID, ed25519.PublicKey(key))
	return &edgeFleetHandler{gatewayID: strings.TrimSpace(gatewayID), config: config, replica: replica, runtime: runtime, spool: spool, now: time.Now}, nil
}

func (handler *edgeFleetHandler) DownlinkTopic() string {
	return "energy/v1/" + handler.config.TenantID + "/" + handler.config.SiteID + "/" + handler.gatewayID + "/fleet/down"
}

func (handler *edgeFleetHandler) UplinkTopic() string {
	return "energy/v1/" + handler.config.TenantID + "/" + handler.config.SiteID + "/" + handler.gatewayID + "/fleet/up"
}

func (handler *edgeFleetHandler) HandshakeEnvelope() ([]byte, error) {
	request := edgefleet.HandshakeRequest{
		EdgeID: handler.gatewayID, RuntimeVersion: edgeFleetRuntimeVersion, ProtocolSchemaVersion: 1,
		Capabilities:    []string{"registry.v1", "config.v1", "ota.v1", "rollback.v1"},
		MaxPayloadBytes: edgeFleetMaxPayload, CredentialRevision: handler.config.CredentialRevision,
	}
	return handler.encode(edgefleet.ReplicationHandshake, request)
}

func (handler *edgeFleetHandler) Handle(received paho.PublishReceived) (bool, error) {
	if handler == nil || received.Packet == nil || received.Client == nil || received.Packet.Topic != handler.DownlinkTopic() {
		return false, nil
	}
	envelope, err := edgefleet.DecodeReplicationEnvelope(received.Packet.Payload, edgeFleetMaxPayload)
	if err != nil || envelope.EdgeID != handler.gatewayID {
		return true, nil
	}
	switch envelope.Type {
	case edgefleet.ReplicationHandshakeResult:
		result, err := edgefleet.DecodeReplicationPayload[edgefleet.HandshakeResultPayload](envelope)
		if err != nil {
			return true, nil
		}
		if result.Result.Status == edgefleet.HandshakeRejected {
			return true, nil
		}
		return true, handler.publishObserved(received.Client)

	case edgefleet.ReplicationReleaseStage:
		release, err := edgefleet.DecodeReplicationPayload[edgefleet.SignedEdgeRelease](envelope)
		if err != nil {
			return true, nil
		}
		return true, handler.replica.StageRelease(release, func(edgefleet.EdgeReleasePayload) error {
			_, err := handler.runtime.Manifest("central-plant:v1", handler.now().UTC())
			return err
		})

	case edgefleet.ReplicationSnapshotBegin:
		meta, err := edgefleet.DecodeReplicationPayload[edgefleet.SnapshotMeta](envelope)
		if err != nil {
			return true, nil
		}
		return true, handler.replica.BeginSnapshot(meta)

	case edgefleet.ReplicationSnapshotChunk:
		chunk, err := edgefleet.DecodeReplicationPayload[edgefleet.SnapshotChunk](envelope)
		if err != nil {
			return true, nil
		}
		return true, handler.replica.StageSnapshotChunk(chunk)

	case edgefleet.ReplicationSnapshotCommit:
		commit, err := edgefleet.DecodeReplicationPayload[edgefleet.SnapshotCommitPayload](envelope)
		if err != nil {
			return true, nil
		}
		manifestDigest, err := handler.manifestDigest()
		if err != nil {
			return true, err
		}
		result, activateErr := handler.replica.ActivateSnapshotRelease(commit.Meta, commit.Release, manifestDigest, func(edgefleet.EdgeReleasePayload) error {
			_, err := handler.runtime.Manifest("central-plant:v1", handler.now().UTC())
			return err
		})
		if activateErr != nil {
			result = edgefleet.ReleaseActivationResult{Reason: activateErr.Error()}
		}
		if err := handler.publish(received.Client, edgefleet.ReplicationReleaseResult, edgefleet.ReleaseResultPayload{Result: result}); err != nil {
			return true, err
		}
		if err := handler.publishObserved(received.Client); err != nil {
			return true, err
		}
		return true, activateErr

	case edgefleet.ReplicationChangeBatch:
		batch, err := edgefleet.DecodeReplicationPayload[edgefleet.ChangeBatchPayload](envelope)
		if err != nil {
			return true, nil
		}
		for _, item := range batch.Items {
			ack, applyErr := handler.replica.ApplyDelivery(item)
			if err := handler.publish(received.Client, edgefleet.ReplicationChangeAck, ack); err != nil {
				return true, err
			}
			if applyErr != nil && ack.Status != edgefleet.DeliveryQuarantined {
				return true, applyErr
			}
		}
		return true, handler.publishObserved(received.Client)

	case edgefleet.ReplicationQuarantineDisposition:
		disposition, err := edgefleet.DecodeReplicationPayload[edgefleet.QuarantineDispositionPayload](envelope)
		if err != nil {
			return true, nil
		}
		if err := handler.replica.DisposeQuarantine(disposition.Cursor, disposition.EvidenceDigest); err != nil {
			return true, err
		}
		return true, handler.publishObserved(received.Client)

	case edgefleet.ReplicationOTAStage:
		stage, err := edgefleet.DecodeReplicationPayload[edgefleet.OTAStagePayload](envelope)
		if err != nil {
			return true, nil
		}
		downloadedDigest, err := handler.resolveOTAPackageDigest(stage.Artifact.Payload.PackageRef)
		if err != nil {
			result := edgefleet.OTAActivationResult{ArtifactID: stage.Artifact.Payload.ArtifactID, Reason: err.Error()}
			return true, handler.publish(received.Client, edgefleet.ReplicationOTAResult, edgefleet.OTAResultPayload{Result: result})
		}
		if err := handler.replica.StageOTA(stage.Artifact, downloadedDigest, func() error {
			_, err := handler.runtime.Manifest("central-plant:v1", handler.now().UTC())
			return err
		}); err != nil {
			result := edgefleet.OTAActivationResult{ArtifactID: stage.Artifact.Payload.ArtifactID, Reason: err.Error()}
			return true, handler.publish(received.Client, edgefleet.ReplicationOTAResult, edgefleet.OTAResultPayload{Result: result})
		}
		result, activateErr := handler.replica.ActivateOTA(stage.Artifact, func() error {
			_, err := handler.runtime.Manifest("central-plant:v1", handler.now().UTC())
			return err
		})
		if activateErr != nil {
			result = edgefleet.OTAActivationResult{ArtifactID: stage.Artifact.Payload.ArtifactID, ActiveArtifactID: handler.replica.ObservedState().ActiveOTAArtifactID, Reason: activateErr.Error()}
		}
		if err := handler.publish(received.Client, edgefleet.ReplicationOTAResult, edgefleet.OTAResultPayload{Result: result}); err != nil {
			return true, err
		}
		if err := handler.publishObserved(received.Client); err != nil {
			return true, err
		}
		return true, nil
	default:
		return true, nil
	}
}

func (handler *edgeFleetHandler) publishObserved(client *paho.Client) error {
	status := handler.replica.SyncStatus()
	status.Health = "HEALTHY"
	status.Observed.CapacityState = handler.spool.State()
	status.BacklogBytes = handler.spool.UsedBytes()
	if status.Observed.StagedReleaseID != "" || status.StagingSnapshotRevision != 0 {
		status.DriftStatus = "STAGING"
	} else if status.Observed.ActiveReleaseID != "" {
		status.DriftStatus = "CONVERGED"
	} else {
		status.DriftStatus = "UNKNOWN"
	}
	return handler.publish(client, edgefleet.ReplicationObservedState, status)
}

func (handler *edgeFleetHandler) publish(client *paho.Client, messageType edgefleet.ReplicationType, payload any) error {
	envelope, err := edgefleet.NewReplicationEnvelope(handler.gatewayID, messageType, payload, handler.now().UTC())
	if err != nil {
		return err
	}
	body, err := edgefleet.EncodeReplicationEnvelope(envelope)
	if err != nil {
		return err
	}
	if _, err := handler.spool.Enqueue(envelope.MessageID, edgefleet.EvidenceConfigResult, handler.UplinkTopic(), body); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = handler.spool.Flush(ctx, client)
	return nil
}

func (handler *edgeFleetHandler) encode(messageType edgefleet.ReplicationType, payload any) ([]byte, error) {
	envelope, err := edgefleet.NewReplicationEnvelope(handler.gatewayID, messageType, payload, handler.now().UTC())
	if err != nil {
		return nil, err
	}
	return edgefleet.EncodeReplicationEnvelope(envelope)
}

func (handler *edgeFleetHandler) resolveOTAPackageDigest(packageRef string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(packageRef))
	if err != nil || parsed.Scheme != "artifact" || parsed.Host == "" || parsed.Path == "" {
		return "", errors.New("OTA packageRef is invalid")
	}
	root := filepath.Join(handler.config.QueueDirectory, "ota-packages")
	candidate := filepath.Join(root, parsed.Host, filepath.FromSlash(strings.TrimPrefix(parsed.Path, "/")))
	relative, err := filepath.Rel(root, candidate)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("OTA packageRef escapes the local artifact root")
	}
	file, err := os.Open(candidate)
	if err != nil {
		return "", fmt.Errorf("open OTA package: %w", err)
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", fmt.Errorf("hash OTA package: %w", err)
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func (handler *edgeFleetHandler) manifestDigest() (string, error) {
	manifest, err := handler.runtime.Manifest("central-plant:v1", handler.now().UTC())
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return "", fmt.Errorf("encode Edge manifest: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}
