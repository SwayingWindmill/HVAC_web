import { useMemo } from 'react';
import { useQuery } from '@tanstack/react-query';
import { siteAssetModelQuery } from '@/api/site-asset-model';
import { resolveTelemetryPointLabel } from '@/domain/centralPlantTelemetry';

export interface SiteEquipmentIndex {
  /** Operator-facing device names: the bound asset's name (CHILLER-01), not the endpoint's. */
  readonly nameByDevice: ReadonlyMap<string, string>;
  /** Operator-facing asset names by asset ID. */
  readonly nameByAsset: ReadonlyMap<string, string>;
  /** The device reporting for each bound asset. */
  readonly deviceByAsset: ReadonlyMap<string, { readonly deviceId: string; readonly name: string }>;
  /** Each device's readings by `<device id>/<point code>`, with their names and units. */
  readonly pointByReading: ReadonlyMap<string, { readonly name: string; readonly unit: string | null }>;
  readonly isLoading?: boolean;
}

export function useSiteEquipment(siteId: string): SiteEquipmentIndex {
  const model = useQuery(siteAssetModelQuery(siteId, { staleTime: 5 * 60_000 }));
  return useMemo(() => {
    const nameByDevice = new Map<string, string>();
    const nameByAsset = new Map<string, string>();
    const deviceByAsset = new Map<string, { deviceId: string; name: string }>();
    const pointByReading = new Map<string, { name: string; unit: string | null }>();
    const data = model.data;
    if (!data) return { nameByDevice, nameByAsset, deviceByAsset, pointByReading, isLoading: model.isLoading };
    for (const point of data.telemetryPoints) {
      pointByReading.set(`${point.reportingDeviceId}/${point.pointCode}`, {
        name: resolveTelemetryPointLabel(point),
        unit: point.unit,
      });
    }
    for (const asset of data.assets) nameByAsset.set(asset.id, asset.displayName);
    for (const device of data.devices) nameByDevice.set(device.id, device.displayName);
    for (const relationship of data.relationships) {
      const assetName = nameByAsset.get(relationship.toId);
      if (relationship.fromType === 'DEVICE' && relationship.toType === 'ASSET' && relationship.status === 'ACTIVE' && assetName) {
        nameByDevice.set(relationship.fromId, assetName);
        deviceByAsset.set(relationship.toId, { deviceId: relationship.fromId, name: assetName });
      }
    }
    return { nameByDevice, nameByAsset, deviceByAsset, pointByReading, isLoading: model.isLoading };
  }, [model.data, model.isLoading]);
}

export function useDeviceNames(siteId: string): ReadonlyMap<string, string> {
  return useSiteEquipment(siteId).nameByDevice;
}
