import { applicationModule } from '../application/index.js';

export {
  createEnergyAnalyticsOwnerReader,
  type EnergyAnalyticsOwnerReaderConfig,
  type EnergyGranularity,
  type EnergyQualityPolicy,
  type EnergyQualitySummaryDto,
  type EnergySeriesMetadataDto,
  type EnergySeriesPointDto,
  type EnergySeriesResponseDto,
} from './internal/energy-analytics-owner-reader.js';
export {
  createGatewayToolAuthorizationReader,
  type GatewayToolAuthorizationReaderConfig,
} from './internal/gateway-tool-authorization-reader.js';
export {
  createRegistryOwnerReader,
  type RegistryAssetDto,
  type RegistryOwnerPayload,
  type RegistryOwnerReaderConfig,
  type RegistrySiteDto,
} from './internal/registry-owner-reader.js';

export const toolsModule = Object.freeze({
  name: 'tools',
  layer: 'adapter',
  dependencies: [applicationModule.name],
} as const);

export type ToolsModule = typeof toolsModule;
