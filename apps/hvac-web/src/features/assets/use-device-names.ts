import { useMemo } from 'react';
import { useQuery } from '@tanstack/react-query';
import { createPlatformGatewayClient } from '@/api/generated/platformGateway.gen';

const client = createPlatformGatewayClient();

export interface SiteEquipmentIndex {
  /** Operator-facing device names: the bound asset's name (CHILLER-01), not the endpoint's. */
  readonly nameByDevice: ReadonlyMap<string, string>;
  /** The device reporting for each bound asset. */
  readonly deviceByAsset: ReadonlyMap<string, { readonly deviceId: string; readonly name: string }>;
  /** Each device's readings by `<device id>/<point code>`, with their names and units. */
  readonly pointByReading: ReadonlyMap<string, { readonly name: string; readonly unit: string | null }>;
}

export function useSiteEquipment(siteId: string): SiteEquipmentIndex {
  const model = useQuery({
    queryKey: ['site-asset-model', siteId],
    queryFn: ({ signal }) => client.getSiteAssetModel(siteId, { signal }),
    staleTime: 5 * 60_000,
  });
  return useMemo(() => {
    const nameByDevice = new Map<string, string>();
    const deviceByAsset = new Map<string, { deviceId: string; name: string }>();
    const pointByReading = new Map<string, { name: string; unit: string | null }>();
    const data = model.data?.data;
    if (!data) return { nameByDevice, deviceByAsset, pointByReading };
    for (const point of data.telemetryPoints) pointByReading.set(`${point.reportingDeviceId}/${point.pointCode}`, { name: point.displayName, unit: point.unit });
    const assetNames = new Map(data.assets.map((asset) => [asset.id, asset.displayName]));
    for (const device of data.devices) nameByDevice.set(device.id, device.displayName);
    for (const relationship of data.relationships) {
      const assetName = assetNames.get(relationship.toId);
      if (relationship.fromType === 'DEVICE' && relationship.toType === 'ASSET' && relationship.status === 'ACTIVE' && assetName) {
        nameByDevice.set(relationship.fromId, assetName);
        deviceByAsset.set(relationship.toId, { deviceId: relationship.fromId, name: assetName });
      }
    }
    return { nameByDevice, deviceByAsset, pointByReading };
  }, [model.data]);
}

export function useDeviceNames(siteId: string): ReadonlyMap<string, string> {
  return useSiteEquipment(siteId).nameByDevice;
}
