package adapter

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
)

// ReportGatewayUnavailableCoverage marks a gateway and its active child devices unavailable
// in telemetry source coverage, batching reports by MaximumCoverageBatchSize.
func ReportGatewayUnavailableCoverage(
	ctx context.Context,
	resolver GatewayChildrenResolver,
	reporter CoverageReporter,
	gatewayID string,
	reason string,
	observedAt time.Time,
	sourceRev int64,
) error {
	normalizedGatewayID := normalizeLowerUUIDv7(gatewayID)
	if normalizedGatewayID == "" || reporter == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}
	deviceIDs := []string{normalizedGatewayID}
	if resolver != nil {
		childIDs, err := resolver.ListGatewayChildDeviceIDs(ctx, normalizedGatewayID)
		if err != nil {
			return fmt.Errorf("list gateway child devices: %w", err)
		}
		seen := map[string]struct{}{normalizedGatewayID: {}}
		for _, rawID := range childIDs {
			id := strings.ToLower(strings.TrimSpace(rawID))
			if id == "" {
				continue
			}
			if _, dup := seen[id]; dup {
				continue
			}
			seen[id] = struct{}{}
			deviceIDs = append(deviceIDs, id)
		}
	}
	slices.Sort(deviceIDs)
	observedAtStr := observedAt.UTC().Format(time.RFC3339Nano)
	for start := 0; start < len(deviceIDs); start += MaximumCoverageBatchSize {
		end := min(start+MaximumCoverageBatchSize, len(deviceIDs))
		batch := deviceIDs[start:end]
		if err := reporter.ReportSourceCoverage(ctx, SourceCoverageReport{
			SourceID:       normalizedGatewayID,
			DeviceIDs:      batch,
			GatewayScopes:  []GatewayCoverageScope{{GatewayID: normalizedGatewayID, DeviceIDs: batch}},
			Available:      false,
			ReasonCode:     &reason,
			ObservedAt:     observedAtStr,
			SourceRevision: sourceRev,
		}); err != nil {
			return fmt.Errorf("report gateway unavailable coverage: %w", err)
		}
	}
	return nil
}
