import {
  queryOptions,
  useMutation,
  useQueryClient,
} from "@tanstack/react-query";
import { z } from "zod";
import { getAlarms } from "@/features/alarms/api/alarm-service";
import { getWorkOrders } from "@/features/work-orders/api/work-order-service";
import { systemsDevicesService } from "@/features/operations/systems-devices/api/systems-devices-service";
import {
  applyOperation,
  createOperationsState,
  operationActionSchema,
  type OperationAction,
} from "./operations-simulation";

const storageKey = "hvac-operations-review-actions";
const actionLogSchema = z.array(operationActionSchema);
// The operations review simulation exists only in the frontend-review build.
const requireSample = (_scope: string) => {
  if (!__HVAC_WEB_FRONTEND_REVIEW__) throw new Error("运维业务数据尚未接入");
};
const readLog = () => {
  const stored = sessionStorage.getItem(storageKey);
  try {
    return actionLogSchema.parse(JSON.parse(stored ?? "[]"));
  } catch {
    throw new Error("模拟处理记录无法读取，请重置模拟");
  }
};
async function readState(scope: string) {
  requireSample(scope);
  const [devices, alarms, works] = await Promise.all([
    systemsDevicesService.getDevices(),
    getAlarms(),
    getWorkOrders(),
  ]);
  return readLog().reduce(
    applyOperation,
    createOperationsState(devices, alarms, works),
  );
}
export const operationsFlowQueryOptions = (scope: string) =>
  queryOptions({
    queryKey: ["operations-flow", scope],
    queryFn: () => readState(scope),
    retry: false,
  });
export function useOperationsAction(scope: string) {
  const client = useQueryClient();
  return useMutation({
    mutationFn: async (action: OperationAction) => {
      // Only this session's sample owner is written. No physical control or production API.
      const state = await readState(scope);
      const next = applyOperation(state, action);
      sessionStorage.setItem(
        storageKey,
        JSON.stringify([...readLog(), action]),
      );
      return next;
    },
    onSuccess: (state) =>
      client.setQueryData(operationsFlowQueryOptions(scope).queryKey, state),
  });
}
export function useOperationsReset(scope: string) {
  const client = useQueryClient();
  return useMutation({
    mutationFn: async () => {
      requireSample(scope);
      sessionStorage.removeItem(storageKey);
      return readState(scope);
    },
    onSuccess: (state) =>
      client.setQueryData(operationsFlowQueryOptions(scope).queryKey, state),
  });
}
