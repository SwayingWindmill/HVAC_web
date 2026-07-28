package adapter

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
)

func deterministicEventID(timestampMilliseconds int64, partition string, value []byte) (string, error) {
	if timestampMilliseconds < 0 || timestampMilliseconds >= 1<<48 {
		return "", fmt.Errorf("sample timestamp is outside UUIDv7 range")
	}
	digest := sha256.Sum256(append(append([]byte(partition), 0), value...))
	var raw [16]byte
	binary.BigEndian.PutUint64(raw[:8], uint64(timestampMilliseconds)<<16)
	copy(raw[6:], digest[:10])
	raw[6] = 0x70 | (raw[6] & 0x0f)
	raw[8] = 0x80 | (raw[8] & 0x3f)
	encoded := make([]byte, 32)
	hex.Encode(encoded, raw[:])
	return string(encoded[0:8]) + "-" + string(encoded[8:12]) + "-" + string(encoded[12:16]) + "-" + string(encoded[16:20]) + "-" + string(encoded[20:32]), nil
}

func partitionFor(deviceID, sourceKey string) string {
	return "thingsboard:" + deviceID + ":" + sourceKey
}
