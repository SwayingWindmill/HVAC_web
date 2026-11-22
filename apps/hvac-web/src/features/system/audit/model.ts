import type { AuditSearchRecord } from '../../../api/generated/platformGateway.gen';
import type { StatusTone } from '../../../components/status-badge';

export type AuditCategory =
  | 'all'
  | 'remote-command'
  | 'control-schedule'
  | 'registry-mutation'
  | 'iam-admin'
  | 'gateway-credential'
  | 'bff-session';

export const AUDIT_CATEGORIES: ReadonlyArray<{ readonly value: AuditCategory; readonly label: string }> = [
  { value: 'all', label: '全部' },
  { value: 'remote-command', label: '远程控制' },
  { value: 'control-schedule', label: '运行与锁定' },
  { value: 'registry-mutation', label: '设备登记' },
  { value: 'iam-admin', label: '成员权限' },
  { value: 'gateway-credential', label: '接入凭证' },
  { value: 'bff-session', label: '登录会话' },
];

export interface AuditRow {
  readonly key: string;
  readonly occurredAt: string;
  readonly clientKind: string;
  readonly actor: string;
  readonly action: string;
  readonly resource: string;
  readonly target: string;
  readonly summary: string;
  readonly outcome: string;
  readonly tone: StatusTone;
}

export interface AuditViewer {
  readonly subject: string;
  readonly displayName: string;
}

const UUID_PATTERN = /[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/i;

const ACTIONS: Readonly<Record<string, string>> = {
  SESSION_CREATED: '登录',
  SESSION_LOGGED_OUT: '退出登录',
  SESSION_REVOKED: '撤销会话',
  INVESTIGATION_STARTED: '发起智能排查',
  INVESTIGATION_COMPLETED: '完成智能排查',
  COMMAND_SUBMITTED: '下发控制指令',
  COMMAND_APPROVED: '审批控制指令',
  COMMAND_RECONCILED: '确认指令结果',
  COMMAND_CANCELED: '取消控制指令',
  COMMAND_MUTATED: '操作控制指令',
  SITE_CONTROL_MODE_UPDATED: '切换站点控制模式',
  ASSET_CONTROL_LOCK_UPDATED: '调整设备锁定状态',
  SITE_CONTROL_SHUTDOWN: '执行紧急停机',
  OPERATING_SCHEDULE_UPDATED: '更新运行计划',
  OPERATING_EXCEPTION_CREATED: '提交运行例外',
  OPERATING_EXCEPTION_DECIDED: '审批运行例外',
  REGISTRY_SITE_MUTATED: '变更站点档案',
  REGISTRY_SPACE_MUTATED: '变更空间区域',
  REGISTRY_ASSET_MUTATED: '变更设备档案',
  REGISTRY_DEVICE_MUTATED: '变更采集设备',
  REGISTRY_DEVICE_HARDWARE_REPLACED: '更换设备硬件',
  REGISTRY_SENSOR_MUTATED: '变更传感器配置',
  REGISTRY_POINT_MUTATED: '变更监控点位',
  REGISTRY_BINDING_UPDATED: '调整设备绑定',
  REGISTRY_TEMPLATE_RELEASED: '发布模板版本',
  REGISTRY_TEMPLATE_ASSIGNED: '应用模板配置',
  REGISTRY_IMPORT_DRY_RUN: '校验批量导入',
  REGISTRY_IMPORT_COMMITTED: '提交批量导入',
  REGISTRY_RESOURCE_RETIRED: '退役登记对象',
  REGISTRY_MUTATED: '变更设备登记',
  IAM_MEMBER_CREATED: '创建成员账号',
  IAM_MEMBER_PASSWORD_RESET: '重置成员密码',
  IAM_MEMBER_STATUS_UPDATED: '变更成员状态',
  IAM_MEMBER_PROFILE_UPDATED: '更新成员资料',
  IAM_ROLE_ASSIGNED: '分配成员角色',
  IAM_ROLE_REVOKED: '收回成员角色',
  IAM_OWN_PASSWORD_CHANGED: '修改个人密码',
  IAM_ADMIN_ACCESS: '成员权限管理',
  GATEWAY_ENROLLMENT_CODE_ISSUED: '生成网关接入码',
  GATEWAY_CREDENTIAL_REVOKED: '吊销网关凭证',
  GATEWAY_CREDENTIAL_ROTATED: '轮换网关证书',
  GATEWAY_CERTIFICATE_REVOKED: '吊销客户端证书',
  GATEWAY_CREDENTIAL_MUTATED: '变更接入凭证',
};

const RESOURCES: Readonly<Record<string, string>> = {
  'bff-session': '登录会话',
  'operations-investigation': '智能排查',
  'remote-command': '远程控制',
  'control-lock': '控制锁定',
  'operating-schedule': '运行计划',
  'registry-mutation': '设备登记',
  'iam-admin': '成员权限',
  'gateway-credential': '接入凭证',
};

const CLIENT_KINDS: Readonly<Record<string, string>> = {
  BROWSER: '网页',
  MINI_PROGRAM: '小程序',
  SYSTEM: '系统',
};

const OUTCOMES: Readonly<Record<string, { label: string; tone: StatusTone }>> = {
  SUCCEEDED: { label: '成功', tone: 'success' },
  FAILED: { label: '失败', tone: 'destructive' },
  DENIED: { label: '拒绝', tone: 'destructive' },
  REJECTED: { label: '退回', tone: 'warning' },
};

const CONTROL_MODES: Readonly<Record<string, string>> = {
  REMOTE_AUTO: '远程自动',
  MANUAL_OVERRIDE: '本地手动',
  SAFE_SHUTDOWN_EXIT: '安全退出停机',
};

const LOCK_MODES: Readonly<Record<string, string>> = {
  UNLOCKED: '已解锁',
  MANUAL_LOCK: '手动锁定',
  MAINTENANCE_LOCK: '维保锁定',
};

const SHUTDOWN_MODES: Readonly<Record<string, string>> = {
  PLANT_SAFE_SHUTDOWN: '全站安全停机',
  EMERGENCY_DO_RELEASE: '紧急直出释放',
  ABORT_UNIT_SEQUENCE: '中止机组序列',
};

const EXCEPTION_KINDS: Readonly<Record<string, string>> = {
  HOLIDAY_SHUTDOWN: '节假日停机',
  EXTENDED_OPERATION: '延时运行',
  CUSTOM_SCHEDULE: '临时时段',
  MAINTENANCE_WINDOW: '维保窗口',
};

const EXCEPTION_DECISIONS: Readonly<Record<string, string>> = {
  APPROVE: '同意',
  REJECT: '驳回',
  CANCEL: '取消',
};

const RECONCILED_OUTCOMES: Readonly<Record<string, string>> = {
  APPLIED: '已生效',
  NOT_APPLIED: '未生效',
};

const ROLE_NAMES: Readonly<Record<string, string>> = {
  admin: '管理员',
  'site-manager': '运维主管',
  operator: '运维人员',
  viewer: '只读',
};

const CREDENTIAL_KINDS: Readonly<Record<string, string>> = {
  ENROLLMENT_CODE: '一次性接入授权码',
  CERTIFICATE: '网关通信证书',
  CREDENTIAL: '网关接入凭证',
};

const REGISTRY_FIELDS: Readonly<Record<string, string>> = {
  displayName: '名称',
  name: '名称',
  code: '编码',
  pointCode: '点位编码',
  sourceKey: '采集源标识',
  parentSpaceId: '上级空间',
  parentId: '上级节点',
  spaceType: '空间类型',
  deviceType: '设备类型',
  newExternalId: '新硬件标识',
  newGatewayDeviceId: '目标网关',
  newSourceKey: '采集源标识',
  newProtocolAddress: '通信地址',
  assetType: '资产类型',
  sensorType: '传感器类型',
  pointType: '点位类型',
  valueType: '数值类型',
  unit: '单位',
  writable: '是否可写',
  sampleIntervalMs: '采样周期',
  publishIntervalMs: '上报周期',
  staleAfterMs: '离线超时',
  status: '状态',
  tags: '标签',
  payload: '模板内容',
  templateKey: '模板标识',
  templateKind: '模板类型',
  templateRevisionId: '模板版本',
  releaseReferences: '发布引用',
  targetId: '目标对象',
  targetType: '目标类型',
  sourceId: '来源对象',
  kind: '类型',
  role: '关系角色',
  effectiveAt: '生效时间',
  closeOnly: '仅关闭绑定',
  resourceType: '对象类型',
  resourceId: '对象标识',
  floorAreaM2: '建筑面积',
  climateZone: '气候分区',
  manufacturer: '厂商',
  model: '型号',
  serialNumber: '序列号',
  calibrationDueAt: '校准到期日',
  metadata: '扩展信息',
  sourceMetadata: '源点位信息',
  reportingDeviceId: '所属采集设备',
  sensorId: '关联传感器',
  namespace: '命名空间',
  rows: '导入明细',
  plan: '导入方案',
  description: '说明',
  binding: '设备绑定',
  timezone: '时区',
};

function isSafeDisplayText(value: unknown): value is string {
  return typeof value === 'string' && value.trim().length > 0 && !UUID_PATTERN.test(value);
}

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function formatParameters(raw: unknown): string | null {
  if (!isPlainObject(raw)) return null;
  const parts: string[] = [];
  if (typeof raw.setpointC === 'number') parts.push('温度设定 ' + String(raw.setpointC) + '℃');
  if (typeof raw.frequencyHz === 'number') parts.push('频率 ' + String(raw.frequencyHz) + 'Hz');
  if (typeof raw.fanSpeedPct === 'number') parts.push('风速 ' + String(raw.fanSpeedPct) + '%');
  if (typeof raw.loadLimitPct === 'number') parts.push('负荷上限 ' + String(raw.loadLimitPct) + '%');
  if (typeof raw.openingPct === 'number') parts.push('开度 ' + String(raw.openingPct) + '%');
  if (typeof raw.enabled === 'boolean') parts.push(raw.enabled ? '启停：开启' : '启停：关闭');
  if (parts.length > 0) return parts.join('、');
  const count = Object.keys(raw).length;
  return count > 0 ? '参数 ' + String(count) + ' 项' : null;
}

function formatRoles(summary: Record<string, unknown>): string | null {
  if (Array.isArray(summary.roles)) {
    const names = summary.roles
      .map((item) => (typeof item === 'string' ? ROLE_NAMES[item] ?? '授权角色' : '授权角色'))
      .filter((item, idx, arr) => arr.indexOf(item) === idx);
    if (names.length > 0) return '角色：' + names.join('、');
  }
  if (typeof summary.roleKey === 'string' && summary.roleKey.trim().length > 0) {
    const label = ROLE_NAMES[summary.roleKey] ?? '授权角色';
    const scope = summary.hasSiteScope === true ? '（指定站点）' : '';
    return '角色：' + label + scope;
  }
  return null;
}

function formatChangeSummary(record: AuditSearchRecord): string {
  const summary = record.changeSummary;
  const parts: string[] = [];

  if (isPlainObject(summary)) {
    if (typeof summary.afterMode === 'string') {
      const label = CONTROL_MODES[summary.afterMode];
      if (label) parts.push('模式：' + label);
    }
    if (typeof summary.afterLockMode === 'string') {
      const label = LOCK_MODES[summary.afterLockMode];
      if (label) parts.push('锁定：' + label);
    }
    if (typeof summary.mode === 'string') {
      const label = SHUTDOWN_MODES[summary.mode];
      if (label) parts.push('停机方式：' + label);
    }
    if (typeof summary.reconciledOutcome === 'string') {
      const label = RECONCILED_OUTCOMES[summary.reconciledOutcome];
      if (label) parts.push('确认状态：' + label);
    }
    const paramSummary = formatParameters(summary.parameters);
    if (paramSummary) parts.push(paramSummary);

    if (isSafeDisplayText(summary.scheduleName)) {
      parts.push('计划：' + summary.scheduleName.trim());
    }
    if (isSafeDisplayText(summary.title)) {
      parts.push('主题：' + summary.title.trim());
    }
    if (typeof summary.entriesCount === 'number') {
      parts.push('时段 ' + String(summary.entriesCount) + ' 条');
    }
    if (typeof summary.preCoolLeadMinutes === 'number') {
      parts.push('预冷提前 ' + String(summary.preCoolLeadMinutes) + ' 分钟');
    }
    if (typeof summary.coastDownLagMinutes === 'number') {
      parts.push('停机滞后 ' + String(summary.coastDownLagMinutes) + ' 分钟');
    }
    if (typeof summary.kind === 'string') {
      const label = EXCEPTION_KINDS[summary.kind];
      if (label) parts.push('类型：' + label);
    }
    if (typeof summary.decision === 'string') {
      const label = EXCEPTION_DECISIONS[summary.decision];
      if (label) parts.push('审批：' + label);
    }
    if (isSafeDisplayText(summary.decisionNote)) {
      parts.push('审批意见：' + summary.decisionNote.trim());
    }
    if (isSafeDisplayText(summary.startDate) && isSafeDisplayText(summary.endDate)) {
      const range = summary.startDate === summary.endDate
        ? summary.startDate
        : summary.startDate + ' 至 ' + summary.endDate;
      parts.push('日期：' + range);
    }

    if (typeof summary.beforeEnabled === 'boolean' && typeof summary.afterEnabled === 'boolean') {
      parts.push(summary.afterEnabled ? '状态：停用 → 启用' : '状态：启用 → 停用');
    }
    if (isSafeDisplayText(summary.beforeDisplayName) && isSafeDisplayText(summary.afterDisplayName) && summary.beforeDisplayName !== summary.afterDisplayName) {
      parts.push('姓名：' + summary.beforeDisplayName + ' → ' + summary.afterDisplayName);
    }
    if (summary.emailChanged === true) {
      parts.push('已更新邮箱');
    }
    const roleText = formatRoles(summary);
    if (roleText) parts.push(roleText);
    if (summary.passwordChanged === true) {
      parts.push('已完成密码更新');
    }

    if (Array.isArray(summary.changedFields) && summary.changedFields.length > 0) {
      const names = summary.changedFields
        .map((field) => (typeof field === 'string' ? REGISTRY_FIELDS[field] : undefined))
        .filter((name): name is string => Boolean(name));
      if (names.length > 0) {
        parts.push('变更字段：' + [...new Set(names)].join('、'));
      } else {
        parts.push('更新字段 ' + String(summary.changedFields.length) + ' 项');
      }
    }
    if (typeof summary.credentialKind === 'string') {
      const label = CREDENTIAL_KINDS[summary.credentialKind];
      if (label) parts.push(label);
    }
    if (typeof summary.cancelledCount === 'number' && summary.cancelledCount > 0) {
      parts.push('取消待发指令 ' + String(summary.cancelledCount) + ' 条');
    }
    if (summary.stepUpRequired === true) {
      parts.push('二次校验未通过');
    }
    if (isSafeDisplayText(summary.reason)) {
      parts.push('原因：' + summary.reason.trim());
    }
  }

  if (parts.length > 0) return parts.join('；');
  if (record.outcome === 'DENIED') return '权限校验未通过';
  if (record.outcome === 'REJECTED') return '输入或状态校验未通过';
  return '—';
}

function resolveActorLabel(record: AuditSearchRecord, viewer: AuditViewer): string {
  if (isSafeDisplayText(record.actorDisplay)) {
    return record.actorDisplay.trim();
  }
  return record.actor === viewer.subject ? viewer.displayName : '其他账号';
}

// Operators see business facts only: never raw identifiers, UUIDs, trace IDs, or internal codes.
export function presentAuditRecord(record: AuditSearchRecord, viewer: AuditViewer): AuditRow {
  const outcome = OUTCOMES[record.outcome] ?? { label: '其他结果', tone: 'neutral' as const };
  const actor = resolveActorLabel(record, viewer);
  const resource = RESOURCES[record.resourceType] ?? '其他对象';
  const target = isSafeDisplayText(record.targetLabel) ? record.targetLabel.trim() : resource;
  const clientKind = record.clientKind ? (CLIENT_KINDS[record.clientKind] ?? '—') : '—';

  return {
    key: String(record.ledgerSequence),
    occurredAt: record.occurredAt,
    clientKind,
    actor,
    action: ACTIONS[record.action] ?? '未识别的操作',
    resource,
    target,
    summary: formatChangeSummary(record),
    outcome: outcome.label,
    tone: outcome.tone,
  };
}
