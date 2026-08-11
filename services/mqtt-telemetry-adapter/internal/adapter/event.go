package adapter

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func sourcePartition(gatewayID, externalDeviceID, telemetryKey string) string {
	return "mqtt:" + strings.TrimSpace(gatewayID) + ":" + strings.TrimSpace(externalDeviceID) + ":" + strings.TrimSpace(telemetryKey)
}

func deterministicPointEventID(point EnvelopePoint, partition string) (string, error) {
	sampledAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(point.SampledAt))
	if err != nil {
		return "", fmt.Errorf("parse point timestamp: %w", err)
	}
	milliseconds := sampledAt.UnixMilli()
	if milliseconds < 0 || milliseconds >= 1<<48 {
		return "", fmt.Errorf("point timestamp is outside UUIDv7 range")
	}
	value, err := json.Marshal(point.Value)
	if err != nil {
		return "", fmt.Errorf("encode point value: %w", err)
	}
	seed := make([]byte, 0, len(partition)+len(value)+64)
	seed = append(seed, partition...)
	seed = append(seed, 0)
	var sequence [8]byte
	binary.BigEndian.PutUint64(sequence[:], point.Sequence)
	seed = append(seed, sequence[:]...)
	seed = append(seed, 0)
	seed = append(seed, value...)
	digest := sha256.Sum256(seed)
	var raw [16]byte
	binary.BigEndian.PutUint64(raw[:8], uint64(milliseconds)<<16)
	copy(raw[6:], digest[:10])
	raw[6] = 0x70 | (raw[6] & 0x0f)
	raw[8] = 0x80 | (raw[8] & 0x3f)
	encoded := make([]byte, 32)
	hex.Encode(encoded, raw[:])
	return string(encoded[0:8]) + "-" + string(encoded[8:12]) + "-" + string(encoded[12:16]) + "-" + string(encoded[16:20]) + "-" + string(encoded[20:32]), nil
}
