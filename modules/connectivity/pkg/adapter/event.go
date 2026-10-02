package adapter

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

func sourcePartition(gatewayID, deviceID, pointCode string) string {
	return "mqtt:" + strings.TrimSpace(gatewayID) + ":" + strings.TrimSpace(deviceID) + ":" + strings.TrimSpace(pointCode)
}

// deterministicPointEventID binds one normalized Point fact to the original MQTT
// message identity. Store-and-forward replay keeps messageId unchanged, therefore
// a replay produces the exact same source event identity and is deduplicated.
func deterministicEvidenceEventID(messageID string, observedAt int64, discriminator string) (string, error) {
	if observedAt < 0 || observedAt >= 1<<48 {
		return "", fmt.Errorf("evidence timestamp is outside UUIDv7 range")
	}
	digest := sha256.Sum256([]byte(strings.TrimSpace(messageID) + "|" + strings.TrimSpace(discriminator)))
	var raw [16]byte
	binary.BigEndian.PutUint64(raw[:8], uint64(observedAt)<<16)
	copy(raw[6:], digest[:10])
	raw[6] = 0x70 | (raw[6] & 0x0f)
	raw[8] = 0x80 | (raw[8] & 0x3f)
	encoded := make([]byte, 32)
	hex.Encode(encoded, raw[:])
	return string(encoded[0:8]) + "-" + string(encoded[8:12]) + "-" + string(encoded[12:16]) + "-" + string(encoded[16:20]) + "-" + string(encoded[20:32]), nil
}

func deterministicPointEventID(messageID string, deviceTimestamp int64, point EnvelopePoint, partition string) (string, error) {
	if deviceTimestamp < 0 || deviceTimestamp >= 1<<48 {
		return "", fmt.Errorf("point timestamp is outside UUIDv7 range")
	}
	value, err := json.Marshal(point.Value)
	if err != nil {
		return "", fmt.Errorf("encode point value: %w", err)
	}
	seed := make([]byte, 0, len(messageID)+len(partition)+len(value)+64)
	seed = append(seed, strings.TrimSpace(messageID)...)
	seed = append(seed, 0)
	seed = append(seed, partition...)
	seed = append(seed, 0)
	seed = append(seed, point.Code...)
	seed = append(seed, 0)
	seed = append(seed, point.Quality)
	seed = append(seed, 0)
	seed = append(seed, value...)
	digest := sha256.Sum256(seed)
	var raw [16]byte
	binary.BigEndian.PutUint64(raw[:8], uint64(deviceTimestamp)<<16)
	copy(raw[6:], digest[:10])
	raw[6] = 0x70 | (raw[6] & 0x0f)
	raw[8] = 0x80 | (raw[8] & 0x3f)
	encoded := make([]byte, 32)
	hex.Encode(encoded, raw[:])
	return string(encoded[0:8]) + "-" + string(encoded[8:12]) + "-" + string(encoded[12:16]) + "-" + string(encoded[16:20]) + "-" + string(encoded[20:32]), nil
}
