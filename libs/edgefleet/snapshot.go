package edgefleet

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

func BuildSnapshot(snapshotRevision, desiredRevision uint64, releaseID string, items []ProjectionItem, chunkSize int) (SnapshotMeta, []SnapshotChunk, error) {
	if snapshotRevision == 0 || desiredRevision == 0 || strings.TrimSpace(releaseID) == "" {
		return SnapshotMeta{}, nil, errors.New("snapshot, desired revision and release ID are required")
	}
	if chunkSize <= 0 {
		return SnapshotMeta{}, nil, errors.New("snapshot chunk size must be positive")
	}

	ordered := append([]ProjectionItem(nil), items...)
	for _, item := range ordered {
		if err := item.validateDownlink(); err != nil {
			return SnapshotMeta{}, nil, fmt.Errorf("invalid snapshot item %s/%s: %w", item.OwnerDomain, item.EntityID, err)
		}
	}
	sort.Slice(ordered, func(left, right int) bool {
		leftKey := projectionKey(ordered[left].OwnerDomain, ordered[left].EntityID)
		rightKey := projectionKey(ordered[right].OwnerDomain, ordered[right].EntityID)
		if leftKey == rightKey {
			return ordered[left].OwnerRevision < ordered[right].OwnerRevision
		}
		return leftKey < rightKey
	})
	for index := 1; index < len(ordered); index++ {
		if projectionKey(ordered[index-1].OwnerDomain, ordered[index-1].EntityID) == projectionKey(ordered[index].OwnerDomain, ordered[index].EntityID) {
			return SnapshotMeta{}, nil, fmt.Errorf("snapshot contains duplicate projection %s/%s", ordered[index].OwnerDomain, ordered[index].EntityID)
		}
	}

	chunkCount := 0
	if len(ordered) > 0 {
		chunkCount = (len(ordered) + chunkSize - 1) / chunkSize
	}
	chunks := make([]SnapshotChunk, 0, chunkCount)
	for index := 0; index < chunkCount; index++ {
		from := index * chunkSize
		to := from + chunkSize
		if to > len(ordered) {
			to = len(ordered)
		}
		chunk := SnapshotChunk{SnapshotRevision: snapshotRevision, Index: index, Items: append([]ProjectionItem(nil), ordered[from:to]...)}
		digest, err := snapshotChunkDigest(chunk)
		if err != nil {
			return SnapshotMeta{}, nil, err
		}
		chunk.Digest = digest
		chunks = append(chunks, chunk)
	}

	meta := SnapshotMeta{
		SnapshotRevision: snapshotRevision,
		DesiredRevision:  desiredRevision,
		ReleaseID:        strings.TrimSpace(releaseID),
		ChunkCount:       chunkCount,
	}
	finalDigest, err := snapshotFinalDigest(meta, chunks)
	if err != nil {
		return SnapshotMeta{}, nil, err
	}
	meta.FinalDigest = finalDigest
	return meta, chunks, nil
}

func snapshotChunkDigest(chunk SnapshotChunk) (string, error) {
	canonical := struct {
		SnapshotRevision uint64           `json:"snapshotRevision"`
		Index            int              `json:"index"`
		Items            []ProjectionItem `json:"items"`
	}{SnapshotRevision: chunk.SnapshotRevision, Index: chunk.Index, Items: chunk.Items}
	encoded, err := canonicalJSON(canonical)
	if err != nil {
		return "", err
	}
	return sha256Hex(encoded), nil
}

func snapshotFinalDigest(meta SnapshotMeta, chunks []SnapshotChunk) (string, error) {
	chunkDigests := make([]string, len(chunks))
	for index, chunk := range chunks {
		chunkDigests[index] = chunk.Digest
	}
	canonical := struct {
		SnapshotRevision uint64   `json:"snapshotRevision"`
		DesiredRevision  uint64   `json:"desiredRevision"`
		ReleaseID        string   `json:"releaseId"`
		ChunkDigests     []string `json:"chunkDigests"`
	}{
		SnapshotRevision: meta.SnapshotRevision,
		DesiredRevision:  meta.DesiredRevision,
		ReleaseID:        meta.ReleaseID,
		ChunkDigests:     chunkDigests,
	}
	encoded, err := canonicalJSON(canonical)
	if err != nil {
		return "", err
	}
	return sha256Hex(encoded), nil
}
