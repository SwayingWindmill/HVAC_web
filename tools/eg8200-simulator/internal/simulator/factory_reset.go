package simulator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/eclipse/paho.golang/paho"
	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
)

const maxSecureWipeBytes int64 = 4 << 20

func secureRemoveFile(path string) error {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return nil
	}
	f, err := os.OpenFile(trimmed, os.O_WRONLY, 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open %s for secure wipe: %w", trimmed, err)
	}
	var overwriteErr error
	if info, statErr := f.Stat(); statErr == nil && info.Size() > 0 {
		remaining := min(info.Size(), maxSecureWipeBytes)
		zeros := make([]byte, 4096)
		offset := int64(0)
		for remaining > 0 {
			chunk := zeros
			if remaining < int64(len(chunk)) {
				chunk = zeros[:remaining]
			}
			n, writeErr := f.WriteAt(chunk, offset)
			if writeErr != nil {
				overwriteErr = writeErr
				break
			}
			offset += int64(n)
			remaining -= int64(n)
		}
		if syncErr := f.Sync(); syncErr != nil && overwriteErr == nil {
			overwriteErr = syncErr
		}
	}
	_ = f.Close()
	if err := os.Remove(trimmed); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove %s after wipe: %w", trimmed, err)
	}
	if overwriteErr != nil {
		return fmt.Errorf("overwrite %s before removal: %w", trimmed, overwriteErr)
	}
	return nil
}

func (publisher *MQTTPublisher) handleDownlinkFactoryResetMessage(ctx context.Context, payload []byte) error {
	if publisher == nil {
		return nil
	}
	_, cmd, err := edgecontrol.DecodeFactoryReset(payload, publisher.gatewayID)
	if err != nil {
		return err
	}

	wipedArtifacts := make([]string, 0, 3)
	var errMsgs []string
	var credWipeErr error

	if cmd.WipeCredentials {
		for _, target := range []string{publisher.config.CertFile, publisher.config.KeyFile} {
			if rmErr := secureRemoveFile(target); rmErr != nil {
				credWipeErr = rmErr
				errMsgs = append(errMsgs, rmErr.Error())
			}
		}
		if credWipeErr == nil {
			wipedArtifacts = append(wipedArtifacts, "credentials")
		}
	}

	if cmd.WipeConfigShadow {
		if err := publisher.configShadow.Reset(ctx); err != nil {
			errMsgs = append(errMsgs, err.Error())
		} else {
			wipedArtifacts = append(wipedArtifacts, "config_shadow")
		}
	}

	if cmd.WipeOfflineBuffer {
		publisher.pendingObsMu.Lock()
		publisher.pendingObsByMsgID = make(map[string][]PublishedObservation)
		publisher.pendingObsMu.Unlock()

		publisher.snapshotMu.Lock()
		publisher.lastPointStates = make(map[string]lastEmittedPointState)
		publisher.lastFullSnapshotAt = time.Time{}
		publisher.deadbandFilter = nil
		publisher.snapshotMu.Unlock()

		var offlineErr error
		if err := publisher.evidenceSpool.Purge(); err != nil {
			offlineErr = err
			errMsgs = append(errMsgs, err.Error())
		}
		if publisher.resendQueue != nil {
			if err := publisher.resendQueue.Purge(); err != nil {
				offlineErr = err
				errMsgs = append(errMsgs, err.Error())
			}
		} else if publisher.edgeRuntime != nil {
			if purger, ok := any(publisher.edgeRuntime.Timedata()).(interface{ Purge() error }); ok {
				if err := purger.Purge(); err != nil {
					offlineErr = err
					errMsgs = append(errMsgs, err.Error())
				}
			}
		}
		if publisher.evidenceSpool != nil && publisher.metrics != nil {
			_ = publisher.RefreshMetrics()
		}
		if offlineErr == nil {
			wipedArtifacts = append(wipedArtifacts, "offline_buffer")
		}
	}

	now := time.Now().UTC()
	ack := edgecontrol.FactoryResetAckPayload{
		ResetID:        cmd.ResetID,
		Status:         edgecontrol.FactoryResetAckStatusWiped,
		WipedArtifacts: wipedArtifacts,
		OccurredAt:     now.UnixMilli(),
	}
	if len(errMsgs) > 0 {
		ack.Status = edgecontrol.FactoryResetAckStatusFailed
		ack.ErrorMessage = clampShadowRunes(strings.Join(errMsgs, "; "), 1024)
	}

	ackBytes, encodeErr := edgecontrol.EncodeFactoryResetAck(publisher.gatewayID, ack, now)
	if encodeErr != nil {
		return encodeErr
	}
	var pubErr error
	if qosPub := publisher.currentQoSPublisher(); qosPub != nil {
		pubCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, pubErr = qosPub.Publish(pubCtx, &paho.Publish{
			Topic:   edgecontrol.LifecycleUplinkTopic(publisher.gatewayID),
			QoS:     1,
			Retain:  false,
			Payload: ackBytes,
		})
		cancel()
	}

	if cmd.WipeCredentials && credWipeErr == nil {
		if publisher.connected != nil {
			publisher.connected.Store(false)
		}
		if mgr := publisher.currentManager(); mgr != nil {
			_ = publisher.Disconnect(ctx)
		}
	}
	return pubErr
}
