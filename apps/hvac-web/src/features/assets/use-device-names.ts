import { useMemo } from 'react';
import { useQuery } from '@tanstack/react-query';
import { createPlatformGatewayClient } from '@/api/generated/platformGateway.gen';

const client = createPlatformGatewayClient();

/**
 * Operator-facing names of the Site's devices: the bound asset's name (CHILLER-01)
 * rather than the communication endpoint's.
 */
export function useDeviceNames(siteId: string): ReadonlyMap<string, string> {
  const model = useQuery({
    queryKey: ['site-asset-model', siteId],
    queryFn: ({ signal }) => client.getSiteAssetModel(siteId, { signal }),
    staleTime: 5 * 60_000,
  });
  return useMemo(() => {
    const names = new Map<string, string>();
    const data = model.data?.data;
    if (!data) return names;
    const assetNames = new Map(data.assets.map((asset) => [asset.id, asset.displayName]));
    for (const device of data.devices) names.set(device.id, device.displayName);
    for (const relationship of data.relationships) {
      const assetName = assetNames.get(relationship.toId);
      if (relationship.fromType === 'DEVICE' && relationship.toType === 'ASSET' && relationship.status === 'ACTIVE' && assetName) {
        names.set(relationship.fromId, assetName);
      }
    }
    return names;
  }, [model.data]);
}
