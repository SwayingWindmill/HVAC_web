import { z } from "zod";
import type { AlarmIssueItem } from "../../alarms/api/alarm-types";
import type { WorkOrderItem } from "../../work-orders/api/work-order-types";
import type { DeviceItem } from "../../operations/systems-devices/api/systems-devices-types";

export const operationActionSchema = z.discriminatedUnion("type", [
  z.object({ type: z.literal("acknowledge"), id: z.string(), at: z.string() }),
  z.object({ type: z.literal("assign"), id: z.string(), at: z.string() }),
  z.object({
    type: z.literal("task"),
    id: z.string(),
    taskId: z.string(),
    at: z.string(),
  }),
  z.object({ type: z.literal("complete"), id: z.string(), at: z.string() }),
  z.object({
    type: z.literal("verify"),
    id: z.string(),
    condition: z.enum(["stable", "persistent"]),
    at: z.string(),
  }),
]);
export type OperationAction = z.infer<typeof operationActionSchema>;
export type RecoveryCheck = {
  label: string;
  pointCode: string;
  unit: string;
  min: number;
  max: number;
  actual: number;
};
export type RecoveryResult = {
  status: "PASSED" | "FAILED";
  at: string;
  checks: RecoveryCheck[];
};
export type OperationsState = {
  devices: DeviceItem[];
  alarms: AlarmIssueItem[];
  works: WorkOrderItem[];
  recovery: Record<string, RecoveryResult>;
};

const recoveryFrames: Record<
  string,
  {
    checks: Omit<RecoveryCheck, "actual">[];
    stable: number[];
    persistent: number[];
  }
> = {
  "wo-101": {
    checks: [
      {
        label: "蒸发温度",
        pointCode: "CH01_TE_EVAP",
        unit: "℃",
        min: 4,
        max: 5,
      },
      {
        label: "蒸发压力",
        pointCode: "CH01_PE_EVAP",
        unit: "kPa",
        min: 330,
        max: 360,
      },
      {
        label: "吸气过热度",
        pointCode: "CH01_SUPERHEAT",
        unit: "K",
        min: 4,
        max: 6.5,
      },
      {
        label: "冷水供水温度",
        pointCode: "CH01_TW_SUP",
        unit: "℃",
        min: 6.5,
        max: 7.5,
      },
    ],
    stable: [4.5, 345, 5.2, 7],
    persistent: [1.8, 285.4, 11.2, 5.2],
  },
  "wo-082": {
    checks: [
      {
        label: "滤网压差",
        pointCode: "PAU02_DP_FILTER",
        unit: "Pa",
        min: 0,
        max: 180,
      },
    ],
    stable: [75],
    persistent: [225],
  },
};
export const hasRecoveryCriteria = (id: string) => id in recoveryFrames;

export function createOperationsState(
  devices: readonly DeviceItem[],
  alarms: AlarmIssueItem[],
  works: WorkOrderItem[],
): OperationsState {
  const state: OperationsState = structuredClone({
    devices: [...devices],
    alarms,
    works,
    recovery: {},
  });
  // Existing completed maintenance is awaiting an independent recovery observation.
  const completedAlarm = state.alarms.find((x) => x.code === "AL-2026-0897")!;
  completedAlarm.state = "VERIFYING";
  for (const device of state.devices) {
    const active = state.alarms.filter(
      (x) => x.deviceId === device.id && x.state !== "RESOLVED",
    );
    state.devices = state.devices.map((x) =>
      x.id === device.id
        ? {
            ...x,
            activeAlarmsCount: active.length,
            activeAlarms: active.map((a) => ({
              id: a.id,
              severity: a.severity === "CRITICAL" ? "critical" : "warning",
              message: a.title,
              triggeredAt: a.firstOccurredAt,
            })),
            ...(active.length
              ? { status: "alarm", statusLabel: "诊断异常" }
              : {}),
          }
        : x,
    );
  }
  return state;
}

export function applyOperation(
  state: OperationsState,
  action: OperationAction,
): OperationsState {
  const next = structuredClone(state);
  if (action.type === "acknowledge") {
    const alarm = next.alarms.find((x) => x.id === action.id);
    if (!alarm || alarm.acknowledged) throw new Error("告警不存在或已经确认");
    alarm.acknowledged = true;
    alarm.acknowledgedAt = action.at;
    alarm.timeline.push({
      timestamp: action.at,
      operation: "确认告警",
      operator: "前端评审员",
      description: "已接收处理责任，恢复状态保持不变。",
      type: "ACKNOWLEDGE",
    });
    return next;
  }
  const work = next.works.find((x) => x.id === action.id);
  if (!work) throw new Error("工单不存在");
  const alarm = next.alarms.find((x) => x.code === work.sourceAlarmCode);
  let detail: string;
  let eventType: WorkOrderItem["timeline"][number]["type"];
  if (action.type === "assign") {
    if (work.status !== "OPEN") throw new Error("该工单不能开始处理");
    work.assigneeName ??= "前端评审员";
    work.status = "IN_PROGRESS";
    if (alarm) alarm.assigneeName = work.assigneeName;
    detail = `${work.assigneeName}开始处理`;
    eventType = "ASSIGN";
  } else if (action.type === "task") {
    if (work.status !== "IN_PROGRESS" || !work.assigneeName)
      throw new Error("仅进行中的已分派工单可以记录检查项");
    const task = work.checklist.find((x) => x.id === action.taskId);
    if (!task || task.completed) throw new Error("检查项不存在或已完成");
    task.completed = true;
    task.operator = work.assigneeName;
    task.completedAt = action.at;
    work.tasksCompleted = work.checklist.filter((x) => x.completed).length;
    detail = `完成检查项：${task.title}`;
    eventType = "START";
  } else if (action.type === "complete") {
    if (
      work.status !== "IN_PROGRESS" ||
      work.checklist.some((x) => !x.completed)
    )
      throw new Error("需先完成全部检查项，且工单不能处于受阻状态");
    work.status = "COMPLETED";
    if (alarm && alarm.state !== "RESOLVED") alarm.state = "VERIFYING";
    detail = "作业完成，等待独立恢复验证";
    eventType = "COMPLETE";
  } else {
    if (work.status !== "COMPLETED")
      throw new Error("仅已完成工单可以进行恢复验证");
    const frame = recoveryFrames[work.id];
    if (!frame) throw new Error("该工单尚未配置恢复判据");
    if (next.recovery[work.id]?.status === "PASSED")
      throw new Error("恢复已经验证通过");
    const values = frame[action.condition];
    const checks = frame.checks.map((c, i) => ({ ...c, actual: values[i] }));
    const passed = checks.every((c) => c.actual >= c.min && c.actual <= c.max);
    next.recovery[work.id] = {
      status: passed ? "PASSED" : "FAILED",
      at: action.at,
      checks,
    };
    if (alarm) {
      alarm.state = passed ? "RESOLVED" : "VERIFYING";
      alarm.timeline.push({
        timestamp: action.at,
        operation: passed ? "恢复验证通过" : "恢复验证未通过",
        operator: "前端评审员",
        description: checks
          .map((c) => `${c.label} ${c.actual} ${c.unit}`)
          .join("；"),
        type: passed ? "RESOLVE" : "VERIFY",
      });
    }
    next.devices = next.devices.map((device) => {
      if (device.id !== work.deviceId) return device;
      const remaining = next.alarms.filter(
        (x) => x.deviceId === device.id && x.state !== "RESOLVED",
      );
      return {
        ...device,
        activeAlarmsCount: remaining.length,
        activeAlarms: remaining.map((a) => ({
          id: a.id,
          severity: a.severity === "CRITICAL" ? "critical" : "warning",
          message: a.title,
          triggeredAt: a.firstOccurredAt,
        })),
        status: passed && !remaining.length ? "running" : "alarm",
        statusLabel: passed && !remaining.length ? "运行中" : "诊断异常",
      };
    });
    detail = passed ? "恢复判据全部通过" : "恢复判据未通过，异常保持待验证";
    eventType = "VERIFY";
  }
  work.updatedAt = action.at;
  work.timeline.push({
    id: `simulation-${work.timeline.length}`,
    time: action.at,
    action: detail,
    operator: "前端评审员",
    detail,
    type: eventType,
  });
  return next;
}
