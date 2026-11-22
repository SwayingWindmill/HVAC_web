import { queryOptions } from '@tanstack/react-query';
import {
  createPlatformGatewayClient,
  type OTACampaignCreateRequest,
  type OTACampaignDetail,
  type OTACampaignList,
  type OTACampaignSummary,
  type OTAPackage,
  type OTAPackageList,
} from '@/api/generated/platformGateway.gen';
import { isOTACampaignActive } from './ota-model';

const gatewayClient = createPlatformGatewayClient();

const OTA_POLL_INTERVAL_MS = 5_000;

export const otaKeys = {
  packages: () => ['integrations', 'ota', 'packages'] as const,
  campaigns: (status: OTACampaignSummary['status'] | 'ALL' = 'ALL') =>
    ['integrations', 'ota', 'campaigns', status] as const,
  campaignDetail: (campaignId: string) =>
    ['integrations', 'ota', 'campaign-detail', campaignId] as const,
};

export function otaPackagesQuery(enabled: boolean) {
  return queryOptions({
    queryKey: otaKeys.packages(),
    queryFn: async ({ signal }): Promise<OTAPackageList> => {
      const response = await gatewayClient.listOTAPackages({ limit: 100 }, { signal });
      return response.data;
    },
    enabled,
    staleTime: 15_000,
    retry: false,
  });
}

export function otaCampaignsQuery(enabled: boolean) {
  return queryOptions({
    queryKey: otaKeys.campaigns('ALL'),
    queryFn: async ({ signal }): Promise<OTACampaignList> => {
      const response = await gatewayClient.listOTACampaigns({ limit: 100 }, { signal });
      return response.data;
    },
    enabled,
    staleTime: 5_000,
    retry: false,
    refetchInterval: (query) => {
      const items = query.state.data?.items;
      if (!items) return false;
      return items.some((item) => isOTACampaignActive(item.status)) ? OTA_POLL_INTERVAL_MS : false;
    },
  });
}

export function otaCampaignDetailQuery(campaignId: string | undefined, enabled: boolean) {
  return queryOptions({
    queryKey: otaKeys.campaignDetail(campaignId ?? 'NONE'),
    queryFn: async ({ signal }): Promise<OTACampaignDetail> => {
      const response = await gatewayClient.getOTACampaign(campaignId!, { signal });
      return response.data;
    },
    enabled: Boolean(campaignId) && enabled,
    staleTime: 3_000,
    retry: false,
    refetchInterval: (query) => {
      const detail = query.state.data;
      if (!detail) return false;
      return isOTACampaignActive(detail.status) ? OTA_POLL_INTERVAL_MS : false;
    },
  });
}

export const otaApi = {
  uploadPackage: async (formData: FormData, csrfToken: string): Promise<OTAPackage> =>
    (await gatewayClient.uploadOTAPackage(formData, csrfToken)).data,
  createCampaign: async (
    body: OTACampaignCreateRequest,
    csrfToken: string,
  ): Promise<OTACampaignDetail> =>
    (await gatewayClient.createOTACampaign(body, csrfToken)).data,
  pauseCampaign: async (
    campaignId: string,
    reason: string | undefined,
    csrfToken: string,
  ): Promise<OTACampaignDetail> => {
    const trimmed = reason?.trim();
    return (
      await gatewayClient.pauseOTACampaign(
        campaignId,
        trimmed ? { reason: trimmed } : {},
        csrfToken,
      )
    ).data;
  },
  resumeCampaign: async (
    campaignId: string,
    reason: string | undefined,
    csrfToken: string,
  ): Promise<OTACampaignDetail> => {
    const trimmed = reason?.trim();
    return (
      await gatewayClient.resumeOTACampaign(
        campaignId,
        trimmed ? { reason: trimmed } : {},
        csrfToken,
      )
    ).data;
  },
  rollbackCampaign: async (
    campaignId: string,
    reason: string | undefined,
    csrfToken: string,
  ): Promise<OTACampaignDetail> => {
    const trimmed = reason?.trim();
    return (
      await gatewayClient.rollbackOTACampaign(
        campaignId,
        trimmed ? { reason: trimmed } : {},
        csrfToken,
      )
    ).data;
  },
};
