import { applicationModule } from '../application/index.js';

export {
  createOperationsAuditDeliveryWorker,
  createOperationsAuditHttpClient,
  type OperationsAuditDeliveryClient,
  type OperationsAuditDeliveryRunResult,
  type OperationsAuditDeliveryWorkerOptions,
  type OperationsAuditHttpClientOptions,
} from './internal/operations-audit-delivery.js';

export const schedulingModule = Object.freeze({
  name: 'scheduling',
  layer: 'adapter',
  dependencies: [applicationModule.name],
} as const);

export type SchedulingModule = typeof schedulingModule;
