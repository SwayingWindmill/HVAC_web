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

func deterministicPointEventID(messageID string, deviceTimestamp int64, point EnvelopePoint, partition string) (string, error) {
	if deviceTimestamp < 0 || deviceTimestamp >= 1<<48 {
		return "", fmt.Errorf("point timestamp is outside UUIDv7 range")
	}
	value, err := json.Marshal(point.Value)
	if err != nil {
		return "", fmt.Errorf("encode point value: %w", err)
	}
	// Stream the fields straight into SHA-256 instead of building one buffer whose
	// size is the sum of untrusted lengths. The bytes hashed are unchanged.
	hasher := sha256.New()
	for _, field := range [][]byte{
		[]byte(strings.TrimSpace(messageID)), {0},
		[]byte(partition), {0},
		[]byte(point.Code), {0},
		{point.Quality, 0},
		value,
	} {
		hasher.Write(field)
	}
	var digest [sha256.Size]byte
	hasher.Sum(digest[:0])
	var raw [16]byte
	binary.BigEndian.PutUint64(raw[:8], uint64(deviceTimestamp)<<16)
	copy(raw[6:], digest[:10])
	raw[6] = 0x70 | (raw[6] & 0x0f)
	raw[8] = 0x80 | (raw[8] & 0x3f)
	encoded := make([]byte, 32)
	hex.Encode(encoded, raw[:])
	return string(encoded[0:8]) + "-" + string(encoded[8:12]) + "-" + string(encoded[12:16]) + "-" + string(encoded[16:20]) + "-" + string(encoded[20:32]), nil
}
