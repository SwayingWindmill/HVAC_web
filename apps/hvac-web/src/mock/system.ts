// Mock data for the /system admin page (ticket #19, v1).
// Boundaries per #18 research (docs/research/system-api-audit.md):
//   - ① IAM user/role: FULLY MOCK (backend has no user-management endpoints).
//   - ② Sites/assets: read would use GET /assets/tree (real); v1 mocks the tree
//       so the page is demoable offline. Write ops are mock-only.
//   - ③ Audit log: list is MOCK, but shaped EXACTLY like the real `audit_logs`
//       entity so swapping to GET /audit later is mechanical.
// Swap these for src/api calls once hvac-backend ships the endpoints.

/* ----------------------------------------------------------------
 * ① IAM — users, roles, scopes  (all mock; backend missing)
 * ---------------------------------------------------------------- */

// Backend role enum ( UserRole in hvac-backend ). Frontend maps
// demo/ops/rd -> READONLY/MAINTENANCE/ADMIN at the IAM boundary.
export type BackendRole = 'ADMIN' | 'MAINTENANCE' | 'READONLY';

export const ROLE_LABEL: Record<BackendRole, string> = {
  ADMIN: '管理员',
  MAINTENANCE: '运维',
  READONLY: '只读',
};

export const ROLE_COLOR: Record<BackendRole, string> = {
  ADMIN: '#0E9C96', // teal-strong — highest privilege
  MAINTENANCE: '#2563EB', // info blue
  READONLY: '#8C8C8C', // neutral gray
};

// 9 hardcoded AuthScope constants from hvac-backend
// (src/iam/constants/auth-scopes.constants.ts). Shown as a static catalog.
export interface ScopeDef {
  key: string;
  label: string;
  desc: string;
}
export const SCOPE_CATALOG: ScopeDef[] = [
  { key: 'asset:read', label: '资产读取', desc: '查看建筑/设备资产树' },
  { key: 'device:read', label: '设备读取', desc: '查看设备详情与遥测' },
  { key: 'device:write', label: '设备写入', desc: '下发设备设定值/期望状态' },
  { key: 'telemetry:read', label: '遥测读取', desc: '订阅实时遥测流' },
  { key: 'command:send', label: '命令下发', desc: '发送控制命令（需二次确认）' },
  { key: 'schedule:read', label: '日程读取', desc: '查看运行日程' },
  { key: 'schedule:write', label: '日程写入', desc: '创建/修改运行日程' },
  { key: 'site:manage', label: '站点管理', desc: '绑定/切换建筑站点' },
  { key: 'user:manage', label: '用户管理', desc: '管理用户与角色（后端待补）' },
];

export interface SystemUser {
  id: string;
  username: string;
  email: string;
  role: BackendRole;
  scopes: string[]; // subset of SCOPE_CATALOG keys
  status: 'active' | 'disabled';
  lastLogin: string; // ISO
}

export const mockUsers: SystemUser[] = [
  { id: 'u1', username: 'zhanghao', email: 'zhanghao@corp.io', role: 'ADMIN', scopes: SCOPE_CATALOG.map((s) => s.key), status: 'active', lastLogin: '2026-07-09T09:12:00+08:00' },
  { id: 'u2', username: 'liwei', email: 'liwei@corp.io', role: 'MAINTENANCE', scopes: ['asset:read', 'device:read', 'telemetry:read', 'command:send', 'schedule:read', 'schedule:write', 'site:manage'], status: 'active', lastLogin: '2026-07-09T08:40:00+08:00' },
  { id: 'u3', username: 'wangfang', email: 'wangfang@corp.io', role: 'READONLY', scopes: ['asset:read', 'device:read', 'telemetry:read'], status: 'active', lastLogin: '2026-07-08T17:05:00+08:00' },
  { id: 'u4', username: 'chenjie', email: 'chenjie@corp.io', role: 'MAINTENANCE', scopes: ['asset:read', 'device:read', 'telemetry:read', 'command:send', 'schedule:read'], status: 'active', lastLogin: '2026-07-08T14:22:00+08:00' },
  { id: 'u5', username: 'zhaomin', email: 'zhaomin@corp.io', role: 'READONLY', scopes: ['asset:read', 'device:read'], status: 'disabled', lastLogin: '2026-06-30T11:00:00+08:00' },
  { id: 'u6', username: 'sunli', email: 'sunli@corp.io', role: 'ADMIN', scopes: SCOPE_CATALOG.map((s) => s.key), status: 'active', lastLogin: '2026-07-09T07:55:00+08:00' },
];

/* ----------------------------------------------------------------
 * ② Sites & asset tree  (mock tree; real shape mirrors /assets/tree)
 * ---------------------------------------------------------------- */

export interface SiteDef {
  id: string;
  name: string;
}
export const mockSites: SiteDef[] = [
  { id: 'b1', name: '总部大厦 A 座' },
  { id: 'b2', name: '研发中心 B 座' },
  { id: 'b3', name: '西区数据中心' },
];

// AntD TreeData-compatible node. type drives icon + add-target.
export interface AssetNode {
  key: string;
  title: string;
  type: 'building' | 'zone' | 'unit';
  children?: AssetNode[];
}

// One site's tree (v1 shows the active site). Mirrors /assets/tree depth:
// building -> zone -> unit (chiller/pump/tower/terminal).
export const mockAssetTree: AssetNode[] = [
  {
    key: 'bld-A', title: '总部大厦 A 座', type: 'building',
    children: [
      {
        key: 'zn-L1', title: '地下一层 · 冷站', type: 'zone',
        children: [
          { key: 'u-ch1', title: '冷水机组 #1', type: 'unit' },
          { key: 'u-ch2', title: '冷水机组 #2', type: 'unit' },
          { key: 'u-ct1', title: '冷却塔 #1', type: 'unit' },
          { key: 'u-pmp1', title: '冷冻泵 #1', type: 'unit' },
        ],
      },
      {
        key: 'zn-F3', title: '三层 · 办公区', type: 'zone',
        children: [
          { key: 'u-ah1', title: '空调末端 #1', type: 'unit' },
          { key: 'u-ah2', title: '空调末端 #2', type: 'unit' },
        ],
      },
    ],
  },
];

/* ----------------------------------------------------------------
 * ③ Audit log  (mock list; shape == real audit_logs entity)
 * ---------------------------------------------------------------- */

// 14 event types from hvac-backend audit-log.entity.ts
export const AUDIT_EVENT_TYPES: string[] = [
  'USER_LOGIN', 'USER_LOGOUT', 'DEVICE_CONTROL', 'CONFIG_CHANGE',
  'SCHEDULE_CREATE', 'SCHEDULE_UPDATE', 'SCHEDULE_DELETE', 'SCHEDULE_ENABLE',
  'COMMAND_SEND', 'COMMAND_RESULT', 'SITE_BIND', 'SITE_SWITCH',
  'ROLE_CHANGE', 'AUDIT_EXPORT',
];

export const AUDIT_EVENT_LABEL: Record<string, string> = {
  USER_LOGIN: '用户登录', USER_LOGOUT: '用户登出', DEVICE_CONTROL: '设备控制',
  CONFIG_CHANGE: '配置变更', SCHEDULE_CREATE: '日程创建', SCHEDULE_UPDATE: '日程修改',
  SCHEDULE_DELETE: '日程删除', SCHEDULE_ENABLE: '日程启停', COMMAND_SEND: '命令下发',
  COMMAND_RESULT: '命令结果', SITE_BIND: '站点绑定', SITE_SWITCH: '站点切换',
  ROLE_CHANGE: '角色变更', AUDIT_EXPORT: '审计导出',
};

export type AuditResult = 'SUCCESS' | 'FAILURE';

export interface AuditLog {
  id: string;
  traceId: string;
  eventType: string;
  userId: string;
  targetEntity: string;
  targetId: string;
  action: string;
  result: AuditResult;
  details: Record<string, unknown>; // jsonb
  ipAddress: string;
  userAgent: string;
  createdAt: string; // ISO
}

function daysAgo(d: number, h = 10): string {
  const dt = new Date('2026-07-09T' + String(h).padStart(2, '0') + ':00:00+08:00');
  dt.setDate(dt.getDate() - d);
  return dt.toISOString();
}

export const mockAuditLogs: AuditLog[] = [
  { id: 'a1', traceId: 'tr-9f1', eventType: 'USER_LOGIN', userId: 'zhanghao', targetEntity: 'user', targetId: 'u1', action: '登录系统', result: 'SUCCESS', details: { provider: 'legacy' }, ipAddress: '10.2.3.11', userAgent: 'Chrome/128', createdAt: daysAgo(0, 9) },
  { id: 'a2', traceId: 'tr-9f2', eventType: 'SITE_SWITCH', userId: 'zhanghao', targetEntity: 'site', targetId: 'b1', action: '切换活动站点', result: 'SUCCESS', details: { from: 'b2', to: 'b1' }, ipAddress: '10.2.3.11', userAgent: 'Chrome/128', createdAt: daysAgo(0, 9) },
  { id: 'a3', traceId: 'tr-9f3', eventType: 'COMMAND_SEND', userId: 'liwei', targetEntity: 'device', targetId: 'u-ch1', action: '下发冷冻水温度设定', result: 'SUCCESS', details: { from: '7.0', to: '6.5', unit: '℃' }, ipAddress: '10.2.3.22', userAgent: 'Chrome/128', createdAt: daysAgo(0, 8) },
  { id: 'a4', traceId: 'tr-9f4', eventType: 'COMMAND_RESULT', userId: 'system', targetEntity: 'device', targetId: 'u-ch1', action: '命令执行回执', result: 'SUCCESS', details: { accepted: true, latencyMs: 320 }, ipAddress: 'internal', userAgent: 'worker', createdAt: daysAgo(0, 8) },
  { id: 'a5', traceId: 'tr-9f5', eventType: 'CONFIG_CHANGE', userId: 'sunli', targetEntity: 'schedule', targetId: 'sch-3', action: '修改运行日程', result: 'SUCCESS', details: { field: 'startHour', from: '08:00', to: '07:30' }, ipAddress: '10.2.3.9', userAgent: 'Chrome/128', createdAt: daysAgo(0, 7) },
  { id: 'a6', traceId: 'tr-9f6', eventType: 'ROLE_CHANGE', userId: 'sunli', targetEntity: 'user', targetId: 'u3', action: '调整用户角色', result: 'SUCCESS', details: { from: 'MAINTENANCE', to: 'READONLY' }, ipAddress: '10.2.3.9', userAgent: 'Chrome/128', createdAt: daysAgo(1, 16) },
  { id: 'a7', traceId: 'tr-9f7', eventType: 'SCHEDULE_ENABLE', userId: 'liwei', targetEntity: 'schedule', targetId: 'sch-7', action: '启停运行日程', result: 'FAILURE', details: { reason: '设备离线' }, ipAddress: '10.2.3.22', userAgent: 'Chrome/128', createdAt: daysAgo(1, 15) },
  { id: 'a8', traceId: 'tr-9f8', eventType: 'DEVICE_CONTROL', userId: 'liwei', targetEntity: 'device', targetId: 'u-pmp1', action: '调整冷冻泵频率', result: 'SUCCESS', details: { from: '45Hz', to: '50Hz' }, ipAddress: '10.2.3.22', userAgent: 'Chrome/128', createdAt: daysAgo(2, 11) },
  { id: 'a9', traceId: 'tr-9f9', eventType: 'USER_LOGIN', userId: 'wangfang', targetEntity: 'user', targetId: 'u3', action: '登录系统', result: 'SUCCESS', details: { provider: 'legacy' }, ipAddress: '10.2.3.30', userAgent: 'Safari/17', createdAt: daysAgo(2, 10) },
  { id: 'a10', traceId: 'tr-9fa', eventType: 'SITE_BIND', userId: 'sunli', targetEntity: 'site', targetId: 'b3', action: '绑定站点', result: 'SUCCESS', details: { customerId: 'tb-cust-003' }, ipAddress: '10.2.3.9', userAgent: 'Chrome/128', createdAt: daysAgo(3, 14) },
  { id: 'a11', traceId: 'tr-9fb', eventType: 'COMMAND_SEND', userId: 'liwei', targetEntity: 'device', targetId: 'u-ct1', action: '冷却塔风机调速', result: 'FAILURE', details: { reason: '越限保护' }, ipAddress: '10.2.3.22', userAgent: 'Chrome/128', createdAt: daysAgo(3, 13) },
  { id: 'a12', traceId: 'tr-9fc', eventType: 'AUDIT_EXPORT', userId: 'zhanghao', targetEntity: 'audit', targetId: 'all', action: '导出审计日志', result: 'SUCCESS', details: { range: '7d', rows: 128 }, ipAddress: '10.2.3.11', userAgent: 'Chrome/128', createdAt: daysAgo(4, 18) },
  { id: 'a13', traceId: 'tr-9fd', eventType: 'SCHEDULE_CREATE', userId: 'sunli', targetEntity: 'schedule', targetId: 'sch-12', action: '创建运行日程', result: 'SUCCESS', details: { name: '周末节能', startHour: '09:00' }, ipAddress: '10.2.3.9', userAgent: 'Chrome/128', createdAt: daysAgo(5, 10) },
  { id: 'a14', traceId: 'tr-9fe', eventType: 'USER_LOGOUT', userId: 'wangfang', targetEntity: 'user', targetId: 'u3', action: '登出系统', result: 'SUCCESS', details: {}, ipAddress: '10.2.3.30', userAgent: 'Safari/17', createdAt: daysAgo(5, 19) },
  { id: 'a15', traceId: 'tr-9ff', eventType: 'CONFIG_CHANGE', userId: 'liwei', targetEntity: 'device', targetId: 'u-ch2', action: '修改 COP 目标', result: 'SUCCESS', details: { from: '6.0', to: '6.2' }, ipAddress: '10.2.3.22', userAgent: 'Chrome/128', createdAt: daysAgo(6, 9) },
];
