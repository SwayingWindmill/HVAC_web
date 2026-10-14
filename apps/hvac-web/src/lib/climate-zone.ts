import type { Site } from '@/api/generated/platformGateway.gen';

export type ClimateZone = NonNullable<Site['climateZone']>;

/** GB 50176 thermal design zones, from cold to mild, in the operator's words. */
export const CLIMATE_ZONE_LABEL: Record<ClimateZone, string> = {
  SEVERE_COLD: '严寒地区',
  COLD: '寒冷地区',
  HOT_SUMMER_COLD_WINTER: '夏热冬冷地区',
  HOT_SUMMER_WARM_WINTER: '夏热冬暖地区',
  MILD: '温和地区',
};
