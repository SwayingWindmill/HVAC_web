import { useState, useMemo } from 'react';
import type { ColumnDef } from '@tanstack/react-table';
import {
  ShieldCheck,
  KeyRound,
  FileSpreadsheet,
  Search,
  Fingerprint,
  Shield,
  Download,
  CheckCircle2,
  AlertOctagon,
  Copy,
  Check,
} from 'lucide-react';
import { Main } from '@/components/layout/Main';
import {
  DataTable,
  DataTablePagination,
  DataTableViewPills,
  StatusPillBadge,
  type DataTableFeatures,
  type DataTableViewPillOption,
} from '@/components/data-table';
import { useDataTable } from '@/hooks/use-data-table';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet';

interface AuditLog {
  readonly id: string;
  readonly timestamp: string;
  readonly operator: string;
  readonly role: string;
  readonly sourceIp: string;
  readonly targetSite: string;
  readonly targetObject: string;
  readonly action: string;
  readonly valueDiff: string;
  readonly authMethod: 'PASSWORD_MFA' | 'DUAL_SIGN' | 'SESSION_TOKEN';
  readonly result: 'SUCCESS' | 'BLOCKED';
  readonly eventHash: string;
  readonly payload: Record<string, unknown>;
}

const AUDIT_LOGS: readonly AuditLog[] = [
  {
    id: 'AUD-202609-0912',
    timestamp: '2026-09-18 14:15:20',
    operator: 'lin_operator (林值班)',
    role: '运行调度长',
    sourceIp: '192.168.1.104',
    targetSite: '北京国贸超高层冷站',
    targetObject: 'CH-02 防喘振排气阀开度',
    action: '下发强制最小开度升压保护 (就地超驰)',
    valueDiff: '0% → 25%',
    authMethod: 'PASSWORD_MFA',
    result: 'SUCCESS',
    eventHash: 'e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855',
    payload: {
      siteId: '01940000-0001-7000-8000-000000000001',
      deviceId: 'CH-02',
      parameter: 'ANTI_SURGE_VALVE_POS',
      requestedValue: 25.0,
      previousValue: 0.0,
      authTicket: 'MFA_TOTP_VERIFIED_77192',
      clientCertSerial: '4A:2B:99:1C:88:02',
    },
  },
  {
    id: 'AUD-202609-0911',
    timestamp: '2026-09-18 11:30:05',
    operator: 'wang_engineer (王建平)',
    role: '暖通主任工程师',
    sourceIp: '192.168.1.88',
    targetSite: '上海虹桥综合枢纽站',
    targetObject: 'STRAT-CW-OPT-01 控制策略',
    action: '签署候选版本 v2.4 发布审批',
    valueDiff: 'REVIEWING → APPROVED',
    authMethod: 'DUAL_SIGN',
    result: 'SUCCESS',
    eventHash: '8f434346648f6b96df89dda901c5176b10a6d83961dd3c1ac88b59b2dc327aa4',
    payload: {
      siteId: '01940000-0002-7000-8000-000000000001',
      strategyId: 'STRAT-CW-OPT-01',
      version: 'v2.4',
      signoffPrincipals: ['wang_engineer', 'li_chief_engineer'],
      dualSignatureStamp: 'ECDSA_SHA256_STAMP_0918',
    },
  },
  {
    id: 'AUD-202609-0910',
    timestamp: '2026-09-18 10:05:42',
    operator: 'zhang_tech (张志远)',
    role: '暖通技术员',
    sourceIp: '192.168.2.45',
    targetSite: '武汉光谷中心城能源中心',
    targetObject: 'VALVE-BYPASS-02',
    action: '电动旁通阀转手动检修模式',
    valueDiff: 'AUTO → MANUAL',
    authMethod: 'SESSION_TOKEN',
    result: 'SUCCESS',
    eventHash: 'ca978112ca1bbdcafac231b39a23dc4da786eff8147c4e72b9807785afee48bb',
    payload: {
      siteId: '01940000-0003-7000-8000-000000000001',
      valveId: 'VALVE-BYPASS-02',
      mode: 'MANUAL',
      workOrderReference: 'WO-202609-082',
    },
  },
  {
    id: 'AUD-202609-0909',
    timestamp: '2026-09-18 09:22:18',
    operator: 'guest_guest (访客账号)',
    role: '未授权角色',
    sourceIp: '10.0.4.12',
    targetSite: '上海虹桥综合枢纽站',
    targetObject: 'CH-01 离心机启停控制',
    action: '尝试越权下发主机停机指令',
    valueDiff: 'RUN → STOP (被拦截)',
    authMethod: 'SESSION_TOKEN',
    result: 'BLOCKED',
    eventHash: '5e884898da28047151d0e56f8dc6292773603d0d6aabbdd62a11ef721d1542d8',
    payload: {
      siteId: '01940000-0002-7000-8000-000000000001',
      deviceId: 'CH-01',
      attemptedAction: 'STOP_CHILLER',
      rejectionReason: 'PERMISSION_DENIED_MISSING_CAPABILITY_CONTROL_EXECUTE',
      securityEnforcement: 'SOD_GATE_IMMEDIATE_BLOCK',
    },
  },
];

const RESULT_PILLS: readonly DataTableViewPillOption[] = [
  { key: 'ALL', label: '全部判定', count: 4 },
  { key: 'SUCCESS', label: '已放行执行', count: 3 },
  { key: 'BLOCKED', label: '越权拦截', count: 1 },
];

const AUTH_PILLS: readonly DataTableViewPillOption[] = [
  { key: 'ALL', label: '全部鉴权' },
  { key: 'PASSWORD_MFA', label: 'MFA 双因子' },
  { key: 'DUAL_SIGN', label: '双人签批' },
  { key: 'SESSION_TOKEN', label: '会话令牌' },
];

const ROLES_MATRIX = [
  {
    role: '系统安全管理员 (Admin)',
    code: 'ROLE_ADMIN',
    usersCount: 2,
    readView: '全站只读 + 系统级参数',
    dataExport: '全量原始日志与点位',
    paramEdit: '仅限系统通信配置',
    directControl: '禁止直接下发工况控制 (职责分离)',
    strategyApprove: '仅限流程合规流转',
    mfaRequirement: '强制 FIDO2 / 硬件 Ukey',
  },
  {
    role: '暖通主任工程师 / 总工 (Lead Engineer)',
    code: 'ROLE_LEAD_ENGINEER',
    usersCount: 3,
    readView: '全部站点物理与模型数据',
    dataExport: '全量能效与仿真模型',
    paramEdit: '允许修改优化模型与边界约束',
    directControl: '允许双人签署下发控制',
    strategyApprove: '拥有最终仿真审批与投产发布权',
    mfaRequirement: '强制 TOTP 动态口令',
  },
  {
    role: '暖通运行调度员 (Operator)',
    code: 'ROLE_OPERATOR',
    usersCount: 9,
    readView: '所属机房与站区实时遥测',
    dataExport: '班次运行排班与日志',
    paramEdit: '仅允许微调设定值 (±1.0℃)',
    directControl: '允许日常模式启停与告警处置',
    strategyApprove: '无审批权限 (仅提请)',
    mfaRequirement: '强制 短信 / TOTP 双因子',
  },
  {
    role: '巡检与只读审计员 (Auditor / Viewer)',
    code: 'ROLE_VIEWER',
    usersCount: 4,
    readView: '授权站区只读数据',
    dataExport: '脱敏汇总报表',
    paramEdit: '无修改权限',
    directControl: '无控制权限',
    strategyApprove: '无审批权限',
    mfaRequirement: '账号密码 + 定期轮换',
  },
];

export function AccessControlAuditWorkspace() {
  const [searchQuery, setSearchQuery] = useState('');
  const [resultFilter, setResultFilter] = useState<'ALL' | 'SUCCESS' | 'BLOCKED'>('ALL');
  const [authFilter, setAuthFilter] = useState<'ALL' | 'PASSWORD_MFA' | 'DUAL_SIGN' | 'SESSION_TOKEN'>('ALL');
  const [selectedLog, setSelectedLog] = useState<AuditLog | null>(null);
  const [copiedHash, setCopiedHash] = useState(false);

  const filteredLogs = useMemo(() => {
    return AUDIT_LOGS.filter((log) => {
      if (resultFilter !== 'ALL' && log.result !== resultFilter) return false;
      if (authFilter !== 'ALL' && log.authMethod !== authFilter) return false;
      if (!searchQuery.trim()) return true;
      const q = searchQuery.toLowerCase();
      return (
        log.id.toLowerCase().includes(q) ||
        log.operator.toLowerCase().includes(q) ||
        log.targetObject.toLowerCase().includes(q) ||
        log.action.toLowerCase().includes(q) ||
        log.targetSite.toLowerCase().includes(q)
      );
    });
  }, [searchQuery, resultFilter, authFilter]);

  const auditColumns = useMemo<Array<ColumnDef<DataTableFeatures, AuditLog>>>(() => [
    { id: 'id', header: '流水凭证号', cell: ({ row }) => <span className="font-mono text-[11px] font-medium text-foreground">{row.original.id}</span> },
    { id: 'timestamp', header: '记录时间', cell: ({ row }) => <span className="font-mono text-[11px] text-muted-foreground tabular-nums">{row.original.timestamp}</span> },
    {
      id: 'operator',
      header: '操作人',
      cell: ({ row }) => <div><div className="font-medium leading-snug text-foreground">{row.original.operator}</div><div className="text-[11px] text-muted-foreground">{row.original.role}</div></div>,
    },
    { id: 'sourceIp', header: '源客户端 IP', cell: ({ row }) => <span className="font-mono text-[11px] text-muted-foreground">{row.original.sourceIp}</span> },
    {
      id: 'target',
      header: '目标站点与受控对象',
      cell: ({ row }) => <div><div className="leading-snug text-foreground">{row.original.targetSite}</div><div className="text-[11px] text-muted-foreground">{row.original.targetObject}</div></div>,
    },
    { id: 'action', header: '操作内容', cell: ({ row }) => <span className="text-foreground">{row.original.action}</span> },
    { id: 'valueDiff', header: '设定值变更', cell: ({ row }) => <span className="font-mono text-[11px] text-muted-foreground tabular-nums">{row.original.valueDiff}</span> },
    {
      id: 'authMethod',
      header: '鉴权机制',
      cell: ({ row }) => <span className="rounded-full border border-border/80 bg-muted/30 px-2 py-0.5 font-mono text-[10px] text-muted-foreground">{row.original.authMethod}</span>,
    },
    {
      id: 'result',
      header: '执行判定',
      cell: ({ row }) => <StatusPillBadge tone={row.original.result === 'SUCCESS' ? 'success' : 'destructive'} pulse={row.original.result === 'BLOCKED'}>{row.original.result === 'SUCCESS' ? '放行执行' : '越权拦截'}</StatusPillBadge>,
    },
    {
      id: 'actions',
      header: '操作',
      cell: ({ row }) => <Button variant="ghost" size="sm" onClick={() => setSelectedLog(row.original)} className="h-7 px-2 text-xs text-primary hover:bg-primary/10 hover:text-primary">查看详情</Button>,
      enableSorting: false,
      enableHiding: false,
    },
  ], []);

  const auditTable = useDataTable({
    key: 'surface-36-access-audit-log',
    data: [...filteredLogs],
    columns: auditColumns,
    pageSize: 10,
    getRowId: (row) => row.id,
  });

  type RoleMatrixRow = (typeof ROLES_MATRIX)[number];

  const roleColumns = useMemo<Array<ColumnDef<DataTableFeatures, RoleMatrixRow>>>(() => [
    {
      id: 'role',
      header: '角色名称',
      cell: ({ row }) => (
        <div>
          <div className="font-semibold leading-snug text-foreground">{row.original.role}</div>
          <div className="font-mono text-[10px] text-muted-foreground">{row.original.code}</div>
        </div>
      ),
    },
    { id: 'usersCount', header: '用户数', cell: ({ row }) => <span className="font-medium tabular-nums">{row.original.usersCount}</span> },
    { id: 'readView', header: '遥测与模型数据范围', cell: ({ row }) => <span className="text-muted-foreground">{row.original.readView}</span> },
    { id: 'dataExport', header: '报表与流水导出', cell: ({ row }) => <span className="text-muted-foreground">{row.original.dataExport}</span> },
    { id: 'paramEdit', header: '控制参数变更范围', cell: ({ row }) => <span className="text-[11px] font-medium text-foreground">{row.original.paramEdit}</span> },
    {
      id: 'directControl',
      header: '设备控制下发',
      cell: ({ row }) => <span className={row.original.directControl.includes('禁止') ? 'text-[11px] text-muted-foreground' : 'text-[11px] font-medium text-foreground'}>{row.original.directControl}</span>,
    },
    {
      id: 'strategyApprove',
      header: '策略发布审批',
      cell: ({ row }) => <span className={row.original.strategyApprove.includes('无') || row.original.strategyApprove.includes('仅限') ? 'text-[11px] text-muted-foreground' : 'text-[11px] font-medium text-primary'}>{row.original.strategyApprove}</span>,
    },
    { id: 'mfaRequirement', header: '认证要求', cell: ({ row }) => <span className="text-[11px] font-medium text-emerald-600">{row.original.mfaRequirement}</span> },
  ], []);

  const roleTable = useDataTable({
    key: 'surface-36-role-matrix',
    data: [...ROLES_MATRIX],
    columns: roleColumns,
    paginate: false,
    getRowId: (row) => row.code,
  });

  const copyHashToClipboard = (hash: string) => {
    navigator.clipboard?.writeText(hash);
    setCopiedHash(true);
    setTimeout(() => setCopiedHash(false), 2000);
  };

  return (
    <Main className="space-y-6">
      {/* 顶部标题区 */}
      <div className="flex flex-col gap-3 md:flex-row md:items-center md:justify-between border-b pb-4">
        <div>
          <div className="flex items-center gap-2">
            <h1 className="text-2xl font-bold tracking-tight text-foreground">用户、权限与安全审计</h1>
          </div>
          <p className="text-sm text-muted-foreground mt-1">
            统一管理系统操作角色、控制下发权限矩阵与关键操作审计追溯日志
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" className="h-8 text-xs gap-1.5">
            <Download className="h-3.5 w-3.5" />
            导出审计日志 (.csv)
          </Button>
          <Button size="sm" className="h-8 text-xs gap-1.5">
            <KeyRound className="h-3.5 w-3.5" />
            新增用户/角色
          </Button>
        </div>
      </div>

      {/* 紧凑型系统安全态势带 (替代模板式 4 大卡片) */}
      <div className="rounded-lg border border-border/80 bg-muted/20 p-4">
        <div className="grid grid-cols-2 gap-4 md:grid-cols-4 md:divide-x md:divide-border/60">
          <div className="space-y-1">
            <div className="text-[11px] font-medium text-muted-foreground">用户与角色</div>
            <div className="flex items-baseline gap-2">
              <span className="text-xl font-bold tracking-tight tabular-nums text-foreground">18</span>
              <span className="text-xs text-muted-foreground">个在册用户 (4 类角色)</span>
            </div>
            <div className="text-[11px] text-emerald-600 font-medium flex items-center gap-1">
              <ShieldCheck className="h-3 w-3" />
              MFA 双因子全员启用
            </div>
          </div>

          <div className="space-y-1 md:pl-6">
            <div className="text-[11px] font-medium text-muted-foreground">控制指令双人复核</div>
            <div className="flex items-baseline gap-2">
              <span className="text-xl font-bold text-foreground">严格执行</span>
            </div>
            <div className="text-[11px] text-muted-foreground">
              关键设备控制指令需复核审批
            </div>
          </div>

          <div className="space-y-1 md:pl-6">
            <div className="text-[11px] font-medium text-muted-foreground">今日控制操作 / 拦截</div>
            <div className="flex items-baseline gap-2">
              <span className="text-xl font-bold tracking-tight tabular-nums text-foreground">4</span>
              <span className="text-xs text-muted-foreground">起操作</span>
              <span className="text-xs font-medium text-destructive ml-1"><span className="tabular-nums">1</span> 起拦截</span>
            </div>
            <div className="text-[11px] text-muted-foreground">
              操作均经凭证验签
            </div>
          </div>

          <div className="space-y-1 md:pl-6">
            <div className="text-[11px] font-medium text-muted-foreground">审计日志完整性</div>
            <div className="flex items-baseline gap-2">
              <span className="text-xl font-bold text-emerald-600">已校验（正常）</span>
            </div>
            <div className="text-[11px] text-muted-foreground">
              SHA-256 连续验签 · 留存 ≥ 3年
            </div>
          </div>
        </div>
      </div>

      {/* 工作区 Tabs */}
      <Tabs defaultValue="audit" className="space-y-4">
        <TabsList className="bg-muted/50 p-1 border border-border/60">
          <TabsTrigger value="audit" className="text-xs gap-1.5">
            <FileSpreadsheet className="h-3.5 w-3.5" />
            操作与鉴权审计日志 ({AUDIT_LOGS.length})
          </TabsTrigger>
          <TabsTrigger value="roles" className="text-xs gap-1.5">
            <Shield className="h-3.5 w-3.5" />
            角色权限矩阵 (4)
          </TabsTrigger>
        </TabsList>

        {/* Tab 1: 审计日志 */}
        <TabsContent value="audit" className="space-y-3 mt-0">
          {/* 筛选与搜索工具栏 (符合 shadcn-admin Data Table 交互标准) */}
          <div className="flex flex-col gap-2.5 sm:flex-row sm:items-center sm:justify-between">
            <div className="flex flex-wrap items-center gap-3">
              <div className="relative w-64">
                <Search className="absolute left-2.5 top-2.5 h-3.5 w-3.5 text-muted-foreground" />
                <Input
                  placeholder="按操作人 / 设备 / 动作 / 站点搜索..."
                  value={searchQuery}
                  onChange={(e) => setSearchQuery(e.target.value)}
                  className="h-8 pl-8 text-xs bg-background"
                />
              </div>

              {/* 判定状态筛选 - sadmann7/tablecn segmented pills */}
              <DataTableViewPills
                options={RESULT_PILLS}
                value={resultFilter}
                onValueChange={(val) => setResultFilter(val as 'ALL' | 'SUCCESS' | 'BLOCKED')}
                ariaLabel="判定状态筛选"
              />

              {/* 鉴权方式筛选 */}
              <DataTableViewPills
                options={AUTH_PILLS}
                value={authFilter}
                onValueChange={(val) => setAuthFilter(val as 'ALL' | 'PASSWORD_MFA' | 'DUAL_SIGN' | 'SESSION_TOKEN')}
                ariaLabel="鉴权机制筛选"
              />
            </div>

            <div className="text-xs text-muted-foreground">
              共 <span className="tabular-nums font-medium">{filteredLogs.length}</span> 条审计记录
            </div>
          </div>

          <DataTable
            table={auditTable}
            tableAriaLabel="用户权限安全审计日志"
            empty="没有符合筛选条件的审计记录"
            getHeaderRowProps={() => ({ className: 'bg-muted/40 text-xs hover:bg-muted/40' })}
            getHeaderCellProps={(header) => ({
              className:
                header.id === 'id' ? 'w-[140px] font-medium' :
                header.id === 'timestamp' ? 'w-[150px] font-medium' :
                header.id === 'operator' ? 'w-[180px] font-medium' :
                header.id === 'sourceIp' ? 'w-[120px] font-medium' :
                header.id === 'target' ? 'w-[170px] font-medium' :
                header.id === 'action' ? 'font-medium' :
                header.id === 'valueDiff' ? 'w-[150px] font-medium' :
                header.id === 'authMethod' || header.id === 'result' ? 'w-[110px] text-center font-medium' :
                header.id === 'actions' ? 'w-[90px] text-right font-medium' :
                undefined,
            })}
            getRowProps={() => ({ className: 'text-xs transition-colors hover:bg-muted/30' })}
            getCellProps={(cell) => ({
              className:
                cell.column.id === 'authMethod' || cell.column.id === 'result'
                  ? 'text-center'
                  : cell.column.id === 'actions'
                    ? 'text-right'
                    : undefined,
            })}
            footer={(
              <DataTablePagination
                table={auditTable}
                totalRows={filteredLogs.length}
              />
            )}
          />
        </TabsContent>

        {/* Tab 2: RBAC 矩阵 */}
        <TabsContent value="roles" className="space-y-4 mt-0">
          <div className="rounded-lg border border-border/80 overflow-hidden bg-card">
            <div className="p-4 border-b border-border/60 bg-muted/10">
              <div className="text-sm font-semibold text-foreground">角色权限配置与审批职责矩阵</div>
              <div className="text-xs text-muted-foreground mt-0.5">
                明确各岗位数据查看、控制下发与策略审批权限范围，确保关键操作具备复核审批流程
              </div>
            </div>
            <DataTable
              table={roleTable}
              className="gap-0"
              tableAriaLabel="角色权限配置与审批职责矩阵"
              getHeaderRowProps={() => ({ className: 'bg-muted/40 text-xs hover:bg-muted/40' })}
              getHeaderCellProps={(header) => ({
                className:
                  header.id === 'role' ? 'w-[200px] font-medium' :
                  header.id === 'usersCount' ? 'w-[80px] text-center font-medium' :
                  header.id === 'readView' ? 'w-[150px] font-medium' :
                  header.id === 'dataExport' ? 'w-[140px] font-medium' :
                  header.id === 'paramEdit' ? 'w-[170px] font-medium' :
                  header.id === 'directControl' || header.id === 'strategyApprove' ? 'w-[180px] font-medium' :
                  header.id === 'mfaRequirement' ? 'w-[140px] font-medium' :
                  undefined,
              })}
              getRowProps={() => ({ className: 'text-xs hover:bg-muted/30' })}
              getCellProps={(cell) => ({
                className: cell.column.id === 'usersCount' ? 'text-center' : undefined,
              })}
            />
          </div>
        </TabsContent>
      </Tabs>

      {/* 审计日志验签与证据链抽屉 (Inspection Sheet) */}
      <Sheet open={Boolean(selectedLog)} onOpenChange={(open) => !open && setSelectedLog(null)}>
        <SheetContent className="sm:max-w-lg overflow-y-auto">
          <SheetHeader>
            <div className="flex items-center gap-2">
              <Fingerprint className="h-5 w-5 text-primary" />
              <SheetTitle className="text-base font-bold">审计记录详情</SheetTitle>
            </div>
            <SheetDescription className="text-xs">
              包含操作主体、时间戳、网络来源与执行参数详情
            </SheetDescription>
          </SheetHeader>

          {selectedLog && (
            <div className="space-y-4 py-4 text-xs">
              <div className="rounded-md border bg-muted/20 p-3 space-y-2">
                <div className="flex items-center justify-between">
                  <span className="text-muted-foreground font-medium">流水号</span>
                  <span className="font-mono font-bold text-foreground">{selectedLog.id}</span>
                </div>
                <div className="flex items-center justify-between">
                  <span className="text-muted-foreground font-medium">鉴权判定</span>
                  {selectedLog.result === 'SUCCESS' ? (
                    <span className="text-emerald-600 font-semibold flex items-center gap-1">
                      <CheckCircle2 className="h-3.5 w-3.5" /> 已放行下发执行
                    </span>
                  ) : (
                    <span className="text-destructive font-semibold flex items-center gap-1">
                      <AlertOctagon className="h-3.5 w-3.5" /> 越权拦截 (权限校验未通过)
                    </span>
                  )}
                </div>
                <div className="flex items-center justify-between">
                  <span className="text-muted-foreground font-medium">操作时间</span>
                  <span className="font-mono text-muted-foreground">{selectedLog.timestamp}</span>
                </div>
                <div className="flex items-center justify-between">
                  <span className="text-muted-foreground font-medium">操作账号</span>
                  <span className="font-medium text-foreground">{selectedLog.operator}</span>
                </div>
                <div className="flex items-center justify-between">
                  <span className="text-muted-foreground font-medium">来源网络 IP</span>
                  <span className="font-mono text-muted-foreground">{selectedLog.sourceIp}</span>
                </div>
              </div>

              {/* 哈希凭证 */}
              <div className="space-y-1.5">
                <div className="flex items-center justify-between">
                  <span className="font-semibold text-foreground">安全审计哈希 (Hash)</span>
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => copyHashToClipboard(selectedLog.eventHash)}
                    className="h-6 px-1.5 text-[10px] gap-1"
                  >
                    {copiedHash ? <Check className="h-3 w-3 text-emerald-600" /> : <Copy className="h-3 w-3" />}
                    {copiedHash ? '已复制' : '复制哈希'}
                  </Button>
                </div>
                <div className="rounded bg-muted p-2 font-mono text-[10px] break-all select-all text-muted-foreground border">
                  {selectedLog.eventHash}
                </div>
              </div>

              {/* 原始下发参数负载 */}
              <div className="space-y-1.5">
                <span className="font-semibold text-foreground">操作参数明细</span>
                <pre className="rounded bg-muted p-3 font-mono text-[11px] overflow-x-auto text-foreground border max-h-60">
                  {JSON.stringify(selectedLog.payload, null, 2)}
                </pre>
              </div>

              <div className="pt-2">
                <Button variant="outline" className="w-full text-xs" onClick={() => setSelectedLog(null)}>
                  关闭
                </Button>
              </div>
            </div>
          )}
        </SheetContent>
      </Sheet>
    </Main>
  );
}
