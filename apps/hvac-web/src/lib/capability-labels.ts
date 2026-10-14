// What a principal may do, in the operator's words and grouped as the users tab shows it.
// Capability keys are the IAM owner's; a key the product does not know is counted, never shown.

export type CapabilityGroup = '查看' | '运维' | '管理';

const CATALOG: Readonly<Record<string, { readonly group: CapabilityGroup; readonly label: string }>> = {
  'site.list': { group: '查看', label: '站点' },
  'site.read': { group: '查看', label: '站点' },
  'asset.list': { group: '查看', label: '设备资产' },
  'asset.read': { group: '查看', label: '设备资产' },
  'device.list': { group: '查看', label: '设备' },
  'device.read': { group: '查看', label: '设备' },
  'telemetry.snapshot.read': { group: '查看', label: '实时读数' },
  'telemetry.batch.read': { group: '查看', label: '实时读数' },
  'telemetry.subscribe': { group: '查看', label: '实时推送' },
  'telemetry.resubscribe': { group: '查看', label: '实时推送' },
  'telemetry.recovery.checkpoint': { group: '查看', label: '实时推送' },
  'telemetry.recovery.use': { group: '查看', label: '实时推送' },
  'registry.read': { group: '查看', label: '登记资源' },
  'telemetry.history.read': { group: '查看', label: '历史数据' },
  'alarm.list': { group: '查看', label: '告警' },
  'alarm.read': { group: '查看', label: '告警' },
  'work-order.list': { group: '查看', label: '工单' },
  'work-order.read': { group: '查看', label: '工单' },
  'audit.read': { group: '查看', label: '操作记录' },
  'audit.events': { group: '查看', label: '操作记录' },
  'alarm.assign': { group: '运维', label: '分配告警' },
  'alarm.intent.publish': { group: '运维', label: '发送告警通知' },
  'work-order.create': { group: '运维', label: '创建工单' },
  'work-order.assign': { group: '运维', label: '分配工单' },
  'work-order.lifecycle': { group: '运维', label: '处理工单' },
  'savings.manage': { group: '运维', label: '节能措施' },
  'site.write': { group: '管理', label: '站点' },
  'space.write': { group: '管理', label: '空间结构' },
  'asset.write': { group: '管理', label: '设备资产' },
  'device.write': { group: '管理', label: '设备与网关' },
  'sensor.write': { group: '管理', label: '传感器' },
  'point.write': { group: '管理', label: '测点' },
  'binding.write': { group: '管理', label: '测点绑定' },
  'registry.import': { group: '管理', label: '登记导入' },
  'registry.retire': { group: '管理', label: '停用登记资源' },
  'rule.manage': { group: '管理', label: '自动化规则' },
  'iam.admin': { group: '管理', label: '用户与权限' },
};

export const CAPABILITY_GROUPS: readonly CapabilityGroup[] = ['查看', '运维', '管理'];

/** A principal's capabilities as distinct labels per group, and how many keys the product cannot name. */
export function capabilityGroups(capabilities: readonly string[]): { readonly groups: ReadonlyMap<CapabilityGroup, readonly string[]>; readonly unnamed: number } {
  const groups = new Map<CapabilityGroup, string[]>(CAPABILITY_GROUPS.map((group) => [group, []]));
  let unnamed = 0;
  for (const capability of capabilities) {
    const entry = CATALOG[capability];
    if (!entry) { unnamed += 1; continue; }
    const labels = groups.get(entry.group)!;
    if (!labels.includes(entry.label)) labels.push(entry.label);
  }
  return { groups, unnamed };
}
