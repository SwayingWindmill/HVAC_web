package ruleruntime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

func Digest(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func PayloadDigest(payload json.RawMessage) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func ExecutionID(ruleRevisionID string, bindingRevision int64, eventID string) string {
	return Digest("execution", ruleRevisionID, fmt.Sprintf("%d", bindingRevision), eventID)
}

func WorkItemID(executionID, path, nodeID, inputDigest string) string {
	return Digest("work", executionID, path, nodeID, inputDigest)
}

func EffectID(workItemID, outputPort string, ordinal int, payloadDigest string) string {
	return Digest("effect", workItemID, outputPort, fmt.Sprintf("%d", ordinal), payloadDigest)
}

func ContinuationID(workItemID string, wakeAtUnixNano int64, outputPort string, payloadDigest string) string {
	return Digest("continuation", workItemID, fmt.Sprintf("%d", wakeAtUnixNano), outputPort, payloadDigest)
}
