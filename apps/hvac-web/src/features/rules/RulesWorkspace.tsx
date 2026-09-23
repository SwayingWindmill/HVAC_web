import { useMemo, useState } from 'react';
import type { ColumnDef } from '@tanstack/react-table';
import { parseAsStringEnum, useQueryState } from 'nuqs';
import {
  ChevronRight,
  MessageSquare,
  Plus,
  RefreshCw,
  Search,
  ShieldCheck,
  Sliders,
  SlidersHorizontal,
  VolumeX,
  AlertTriangle,
  CheckCircle2,
} from 'lucide-react';
import { DataTableBlock } from '@/blocks/data-table';
import { Main } from '@/components/layout/Main';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet';
import { Checkbox } from '@/components/ui/checkbox';
import {
  DataTable,
  DataTableAdvancedToolbar,
  DataTableFilterList,
  DataTablePagination,
  DataTableSortList,
  type DataTableFeatures,
} from '@/components/data-table';
import { StatusBadge } from '@/components/status-badge';
import { useDataTable } from '@/hooks/use-data-table';
import type { ExtendedColumnFilter, JoinOperator } from '@/lib/data-table-types';
import { getFiltersStateParser } from '@/lib/parsers';

export interface RulesWorkspaceProps {
  readonly siteId: string;
}

type RuleSeverity = 'CRITICAL' | 'MAJOR' | 'WARNING' | 'INFO';

interface DetectionRule {
  readonly id: string;
  readonly code: string;
  readonly name: string;
  readonly targetObject: string;
  readonly targetPoint: string;
  readonly conditionExpr: string;
  readonly hysteresis: string;
  readonly debounceSec: number;
  readonly severity: RuleSeverity;
  readonly notificationChannels: readonly string[];
  readonly suppressionWindowMin: number;
  readonly enabled: boolean;
  readonly recentTriggers7d: number;
  readonly statusNote?: string;
}

const DETECTION_RULES: readonly DetectionRule[] = [
  {
    id: 'rule-01',
    code: 'RULE-CH-01-DISCH',
    name: '离心冷机压缩机排气温度超高预警',
    targetObject: '1#~3# 离心冷水机组',
    targetPoint: 'CH-*.DischargeTemp',
    conditionExpr: '排气温度 > 95.0 °C 且持续时长 ≥ 180s',
    hysteresis: '恢复死区: 温度降至 ≤ 88.0 °C 持续 120s',
    debounceSec: 180,
    severity: 'CRITICAL',
    notificationChannels: ['短信紧急通知', '企业微信值班群', '控制台声光'],
    suppressionWindowMin: 15,
    enabled: true,
    recentTriggers7d: 0,
  },
  {
    id: 'rule-02',
    code: 'RULE-CHW-LOW-DT',
    name: '冷冻水大流量小温差异常综合诊断',
    targetObject: '冷冻水供回水总管',
    targetPoint: 'CHW.EnteringTemp - CHW.LeavingTemp',
    conditionExpr: '供回水温差 < 2.5 °C 且总供冷负荷 ≥ 50% 持续 ≥ 20min',
    hysteresis: '恢复死区: 温差回升至 ≥ 3.5 °C',
    debounceSec: 1200,
    severity: 'MAJOR',
    notificationChannels: ['企业微信运维群', '钉钉群机器人'],
    suppressionWindowMin: 30,
    enabled: true,
    recentTriggers7d: 4,
    statusNote: '过去7天触发4次，提示末端存在二通阀常开旁通问题',
  },
  {
    id: 'rule-03',
    code: 'RULE-CT-APPROACH',
    name: '冷却塔近塔逼近度恶化 (Approach Degraded)',
    targetObject: '1#~4# 超低噪冷却塔',
    targetPoint: 'CT.LeavingWaterTemp - Weather.WetBulbTemp',
    conditionExpr: '出水温度与室外湿球温差 > 4.5 °C 持续 ≥ 15min',
    hysteresis: '恢复死区: 逼近度恢复至 ≤ 3.5 °C',
    debounceSec: 900,
    severity: 'WARNING',
    notificationChannels: ['钉钉群机器人', '能效工程师周报'],
    suppressionWindowMin: 60,
    enabled: true,
    recentTriggers7d: 12,
    statusNote: '触发频次较高，建议复核填料脏堵与风机变频限频设定',
  },
  {
    id: 'rule-04',
    code: 'RULE-PMP-VFD-HUNT',
    name: '二次冷冻水泵频率频繁振荡 (Hunting)',
    targetObject: '冷冻二次水输配泵组',
    targetPoint: 'PMP-CHW-*.FrequencyVFD',
    conditionExpr: '5 分钟内频率变化方向反转 ≥ 6 次',
    hysteresis: '恢复死区: 频率稳定在 ±1.0 Hz 带内 ≥ 10min',
    debounceSec: 300,
    severity: 'WARNING',
    notificationChannels: ['企业微信运维群'],
    suppressionWindowMin: 20,
    enabled: true,
    recentTriggers7d: 2,
  },
  {
    id: 'rule-05',
    code: 'RULE-IAQ-CO2-HIGH',
    name: '办公与商铺租户区 CO₂ 浓度超标',
    targetObject: '各楼层主要人员活动区',
    targetPoint: 'ZONE-*.CO2Concentration',
    conditionExpr: '室内二氧化碳浓度 > 1000 ppm 持续 ≥ 10min',
    hysteresis: '恢复死区: 浓度回落至 ≤ 800 ppm',
    debounceSec: 600,
    severity: 'INFO',
    notificationChannels: ['楼宇新风自适应联动', '物业环境监控'],
    suppressionWindowMin: 15,
    enabled: true,
    recentTriggers7d: 8,
  },
];

const RULE_SEVERITY_OPTIONS = [
  { label: '紧急', value: 'CRITICAL' },
  { label: '重要', value: 'MAJOR' },
  { label: '警告', value: 'WARNING' },
  { label: '提示', value: 'INFO' },
] as const;
const RULE_ENABLED_OPTIONS = [
  { label: '已启用', value: 'enabled' },
  { label: '已停用', value: 'disabled' },
] as const;
const RULE_FILTER_COLUMN_IDS = ['severity', 'enabled'] as const;
const RULE_FILTERS_QUERY_KEY = 'ruleFilters';
const RULE_JOIN_OPERATOR_QUERY_KEY = 'ruleJoinOperator';

function matchesRuleAdvancedFilter(
  rule: DetectionRule,
  filter: ExtendedColumnFilter<DetectionRule>,
) {
  const actual = filter.id === 'severity'
    ? rule.severity
    : filter.id === 'enabled'
      ? (rule.enabled ? 'enabled' : 'disabled')
      : undefined;
  if (actual === undefined) return true;
  const values = Array.isArray(filter.value) ? filter.value : [filter.value];

  switch (filter.operator) {
    case 'eq':
    case 'inArray':
      return values.includes(actual);
    case 'ne':
    case 'notInArray':
      return !values.includes(actual);
    case 'isEmpty':
      return false;
    case 'isNotEmpty':
      return true;
    default:
      return false;
  }
}

export function RulesWorkspace({ siteId: _siteId }: RulesWorkspaceProps) {
  const [selectedRule, setSelectedRule] = useState<DetectionRule | null>(null);
  const [searchQuery, setSearchQuery] = useState('');
  const [advancedFilters] = useQueryState(
    RULE_FILTERS_QUERY_KEY,
    getFiltersStateParser<DetectionRule>([...RULE_FILTER_COLUMN_IDS]).withDefault([]),
  );
  const [joinOperator] = useQueryState(
    RULE_JOIN_OPERATOR_QUERY_KEY,
    parseAsStringEnum<JoinOperator>(['and', 'or']).withDefault('and'),
  );
  const filteredRules = useMemo(() => {
    return DETECTION_RULES.filter((rule) => {
      const q = searchQuery.trim().toLowerCase();
      const matchesSearch = !q || (
        rule.name.toLowerCase().includes(q) ||
        rule.code.toLowerCase().includes(q) ||
        rule.targetObject.toLowerCase().includes(q)
      );
      const filterMatches = advancedFilters.map((filter) => matchesRuleAdvancedFilter(rule, filter));
      const matchesAdvancedFilters = advancedFilters.length === 0 || (joinOperator === 'or'
        ? filterMatches.some(Boolean)
        : filterMatches.every(Boolean));
      return matchesSearch && matchesAdvancedFilters;
    });
  }, [advancedFilters, joinOperator, searchQuery]);

  const columns = useMemo<Array<ColumnDef<DataTableFeatures, DetectionRule>>>(() => [
    {
      id: 'select',
      header: ({ table }) => (
        <Checkbox
          checked={table.getIsAllPageRowsSelected() ? true : table.getIsSomePageRowsSelected() ? 'indeterminate' : false}
          onCheckedChange={(checked) => table.toggleAllPageRowsSelected(Boolean(checked))}
          aria-label="选择全部规则"
        />
      ),
      cell: ({ row }) => (
        <Checkbox
          checked={row.getIsSelected()}
          onCheckedChange={(checked) => row.toggleSelected(Boolean(checked))}
          aria-label={`选择 ${row.original.name}`}
          onClick={(event) => event.stopPropagation()}
        />
      ),
      enableSorting: false,
      enableHiding: false,
    },
    { id: 'code', accessorFn: (row) => row.code, meta: { label: '规则代码' }, header: '规则代码', cell: ({ row }) => <span className="font-mono font-semibold text-foreground">{row.original.code}</span> },
    {
      id: 'name',
      accessorFn: (row) => row.name,
      meta: { label: '规则名称' },
      header: '规则名称与监控对象',
      cell: ({ row }) => <div><div className="font-medium text-foreground">{row.original.name}</div><div className="font-mono text-[11px] text-muted-foreground">{row.original.targetObject}</div></div>,
    },
    {
      id: 'condition',
      accessorFn: (row) => row.debounceSec,
      meta: { label: '防抖延时' },
      header: '触发逻辑与防抖延时',
      cell: ({ row }) => <div><div className="text-xs font-medium text-foreground">{row.original.conditionExpr}</div><div className="mt-0.5 text-[10px] text-muted-foreground">防抖确认: <span className="font-mono tabular-nums">{row.original.debounceSec}s</span></div></div>,
    },
    { id: 'hysteresis', header: '恢复死区', cell: ({ row }) => <span className="text-[11px] text-muted-foreground">{row.original.hysteresis}</span> },
    {
      id: 'severity',
      accessorFn: (row) => row.severity,
      enableColumnFilter: true,
      meta: { label: '级别', variant: 'select', options: [...RULE_SEVERITY_OPTIONS] },
      header: '级别',
      cell: ({ row }) => (
        <StatusBadge
          tone={row.original.severity === 'CRITICAL' ? 'destructive' : row.original.severity === 'MAJOR' || row.original.severity === 'WARNING' ? 'warning' : 'info'}
          pulse={row.original.severity === 'CRITICAL' || row.original.severity === 'MAJOR'}
          label={row.original.severity === 'CRITICAL' ? '紧急' : row.original.severity === 'MAJOR' ? '重要' : row.original.severity === 'WARNING' ? '警告' : '提示'}
        />
      ),
    },
    {
      id: 'channels',
      header: '通知渠道',
      cell: ({ row }) => <div className="flex flex-wrap gap-1">{row.original.notificationChannels.map((channel) => <span key={channel} className="rounded bg-muted px-1.5 py-0.5 text-[10px] text-muted-foreground">{channel}</span>)}</div>,
    },
    { id: 'suppression', accessorFn: (row) => row.suppressionWindowMin, meta: { label: '防雪崩窗口' }, header: '防雪崩窗口', cell: ({ row }) => <span className="font-mono text-[11px] text-muted-foreground tabular-nums">{row.original.suppressionWindowMin} 分钟</span> },
    { id: 'enabled', accessorFn: (row) => row.enabled ? 'enabled' : 'disabled', enableColumnFilter: true, meta: { label: '启用状态', variant: 'select', options: [...RULE_ENABLED_OPTIONS] }, header: '状态', cell: ({ row }) => <StatusBadge tone={row.original.enabled ? 'success' : 'neutral'} label={row.original.enabled ? '已启用' : '已停用'} /> },
    {
      id: 'actions',
      header: '操作',
      cell: ({ row }) => (
        <Button variant="ghost" size="sm" className="h-7 gap-0.5 px-2 text-xs" onClick={(event) => { event.stopPropagation(); setSelectedRule(row.original); }}>
          配置<ChevronRight className="h-3 w-3" />
        </Button>
      ),
      enableSorting: false,
    },
  ], []);

  const table = useDataTable({
    key: 'surface-30-rules',
    data: [...filteredRules],
    columns,
    pageSize: 10,
    getRowId: (row) => row.id,
    meta: {
      queryKeys: {
        page: 'rulePage',
        perPage: 'rulePerPage',
        sort: 'ruleSort',
        filters: RULE_FILTERS_QUERY_KEY,
        joinOperator: RULE_JOIN_OPERATOR_QUERY_KEY,
      },
    },
  });


  return (
    <Main className="space-y-6">
      {/* 1. Surface Page Header */}
      <div className="flex flex-col gap-3 md:flex-row md:items-center md:justify-between border-b pb-4">
        <div>
          <div className="flex items-center gap-2">
            
            <span className="rounded bg-muted px-2 py-0.5 font-mono text-xs text-muted-foreground">
              {DETECTION_RULES.length} 条已配置规则
            </span>
          </div>
        </div>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" className="gap-1.5" onClick={() => window.location.reload()}>
            <RefreshCw className="h-3.5 w-3.5" />
            重载规则引擎
          </Button>
          <Button size="sm" className="gap-1.5">
            <Plus className="h-3.5 w-3.5" />
            新建检测规则
          </Button>
        </div>
      </div>

      {/* 2. Detection & Notification Posture Cards */}
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
        {/* 1. 已启用规则 */}
        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-xs font-medium text-muted-foreground">已启用检测规则</CardTitle>
            <ShieldCheck className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="flex items-baseline gap-2">
              <span className="text-2xl font-bold tabular-nums tracking-tight text-foreground">46</span>
              <span className="text-xs text-muted-foreground">条活跃引擎规则</span>
            </div>
            <div className="mt-2 space-y-1 text-xs border-t pt-2">
              <div className="flex items-center justify-between text-muted-foreground">
                <span>覆盖系统领域</span>
                <span className="text-foreground font-medium">冷源 / 水系统 / IAQ</span>
              </div>
              <div className="flex items-center justify-between text-[11px] text-muted-foreground">
                <span>死区滞后约束</span>
                <span className="text-foreground font-medium">100% 规则配备死区</span>
              </div>
            </div>
          </CardContent>
        </Card>

        {/* 2. 防抖与收敛 */}
        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-xs font-medium text-muted-foreground">防抖与防雪崩收敛</CardTitle>
            <VolumeX className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="flex items-baseline gap-2">
              <span className="text-2xl font-bold tabular-nums tracking-tight text-foreground">128</span>
              <span className="text-xs text-muted-foreground">次瞬态闪烁阻断</span>
            </div>
            <div className="mt-2 space-y-1 text-xs border-t pt-2">
              <div className="flex items-center justify-between text-muted-foreground">
                <span>统计窗口</span>
                <span className="text-foreground font-medium">近 7 天累计拦截</span>
              </div>
              <div className="flex items-center justify-between text-[11px] text-muted-foreground">
                <span>运维价值</span>
                <span className="text-foreground font-medium">消除 94% 值班疲劳</span>
              </div>
            </div>
          </CardContent>
        </Card>

        {/* 3. 已绑定渠道 */}
        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-xs font-medium text-muted-foreground">已绑定通知渠道</CardTitle>
            <MessageSquare className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="flex items-baseline gap-2">
              <span className="text-2xl font-bold tabular-nums tracking-tight text-foreground">5</span>
              <span className="text-xs text-muted-foreground">类分级推送通道</span>
            </div>
            <div className="mt-2 space-y-1 text-xs border-t pt-2">
              <div className="flex items-center justify-between text-muted-foreground">
                <span>多通道路由</span>
                <span className="text-[11px] text-foreground font-medium truncate max-w-[140px]">企微/钉钉/短信/声光</span>
              </div>
              <div className="flex items-center justify-between text-[11px] text-muted-foreground">
                <span>心跳联通性</span>
                <span className="text-foreground font-medium">全通道在线可用</span>
              </div>
            </div>
          </CardContent>
        </Card>

        {/* 4. 待调优高频规则 */}
        <Card className="shadow-xs">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-xs font-medium text-muted-foreground">待调优高频规则</CardTitle>
            <Sliders className="size-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="flex items-baseline gap-2">
              <span className="text-2xl font-bold tabular-nums tracking-tight text-foreground">1</span>
              <span className="text-xs text-muted-foreground">条建议放宽死区</span>
            </div>
            <div className="mt-2 space-y-1 text-xs border-t pt-2">
              <div className="flex items-center justify-between text-muted-foreground">
                <span>高频震荡点</span>
                <span className="text-[11px] text-foreground font-medium truncate max-w-[140px]">冷却塔逼近度恶化</span>
              </div>
              <div className="flex items-center justify-between text-[11px] text-muted-foreground">
                <span>建议行动</span>
                <span className="text-foreground font-medium">调增回落死区 +0.5℃</span>
              </div>
            </div>
          </CardContent>
        </Card>
      </div>

      {/* 3. Rules Ledger */}
      <DataTableBlock
        title="告警规则"
      >

        <DataTable
          table={table}
          tableAriaLabel="告警规则"
          empty="没有符合条件的检测规则"
          getHeaderCellProps={(header) => ({
            className:
              header.id === 'select' ? 'w-10' :
              header.id === 'code' ? 'w-[150px]' :
              header.id === 'condition' ? 'w-[260px]' :
              header.id === 'hysteresis' ? 'w-[200px]' :
              header.id === 'severity' ? 'w-[120px] text-center' :
              header.id === 'channels' ? 'w-[160px]' :
              header.id === 'suppression' || header.id === 'enabled' ? 'w-[100px] text-center' :
              header.id === 'actions' ? 'w-[80px] text-right' :
              undefined,
          })}
          getRowProps={(row) => ({
            className: 'cursor-pointer',
            onClick: () => setSelectedRule(row.original),
          })}
          getCellProps={(cell) => ({
            className:
              cell.column.id === 'severity' || cell.column.id === 'suppression' || cell.column.id === 'enabled'
                ? 'text-center'
                : cell.column.id === 'actions'
                  ? 'text-right'
                  : undefined,
          })}
          footer={(
            <DataTablePagination
              table={table}
              totalRows={filteredRules.length}
            />
          )}
        >
          <DataTableAdvancedToolbar table={table}>
            <div className="relative w-48 sm:w-60">
              <Search className="absolute left-2.5 top-2.5 h-3.5 w-3.5 text-muted-foreground" />
              <Input
                placeholder="搜索规则名称 / 代码 / 监控对象..."
                value={searchQuery}
                onChange={(e) => {
                  setSearchQuery(e.target.value);
                  table.setPageIndex(0);
                }}
                className="h-8 bg-background pl-8 text-xs"
              />
            </div>
            <DataTableSortList table={table} />
            <DataTableFilterList table={table} />
          </DataTableAdvancedToolbar>
        </DataTable>
      </DataTableBlock>

      {/* 4. Rule Configuration Sheet */}
      <Sheet open={Boolean(selectedRule)} onOpenChange={(open) => !open && setSelectedRule(null)}>
        <SheetContent className="sm:max-w-xl!">
          <SheetHeader>
            <div className="flex items-center gap-2">
              <span className="font-mono text-xs font-semibold text-muted-foreground">
                {selectedRule?.code}
              </span>
              <span className="rounded bg-muted px-2 py-0.5 text-[10px] font-medium text-muted-foreground">
                {selectedRule?.targetObject}
              </span>
            </div>
            <SheetTitle className="text-base font-bold text-foreground mt-1">
              {selectedRule?.name}
            </SheetTitle>
            <SheetDescription className="text-xs">
              监控点位: {selectedRule?.targetPoint}
            </SheetDescription>
          </SheetHeader>

          {selectedRule && (
            <div className="min-h-0 flex-1 overflow-y-auto px-4 pb-4 space-y-5 pt-4 text-xs">
              {/* Threshold & Hysteresis Facts */}
              <div className="rounded-lg border border-border/80 bg-muted/10 p-3.5 space-y-2">
                <div className="flex items-center gap-1.5 text-xs font-semibold text-foreground">
                  <SlidersHorizontal className="h-3.5 w-3.5 text-primary" />
                  触发阈值与死区滞后逻辑 (Hysteresis Guard)
                </div>
                <div className="space-y-1.5 pt-1 text-[11px]">
                  <div>
                    <span className="text-muted-foreground">判定条件：</span>
                    <span className="font-mono font-bold text-foreground">{selectedRule.conditionExpr}</span>
                  </div>
                  <div>
                    <span className="text-muted-foreground">回落消除门限：</span>
                    <span className="font-medium text-foreground">{selectedRule.hysteresis}</span>
                  </div>
                  <div>
                    <span className="text-muted-foreground">防抖延时要求：</span>
                    <span className="text-foreground"><span className="tabular-nums font-medium">{selectedRule.debounceSec}</span> 秒采样连续确认</span>
                  </div>
                  <div>
                    <span className="text-muted-foreground">抑制去重静音窗：</span>
                    <span className="text-foreground"><span className="tabular-nums font-medium">{selectedRule.suppressionWindowMin}</span> 分钟内重复触发不升级广播</span>
                  </div>
                </div>
              </div>

              {/* Status Note if exists */}
              {selectedRule.statusNote && (
                <div className="rounded-lg border bg-muted/40 p-3.5 space-y-1">
                  <div className="flex items-center gap-1.5 text-xs font-semibold text-foreground">
                    <AlertTriangle className="h-3.5 w-3.5 text-amber-500" />
                    规则调优工程建议
                  </div>
                  <p className="text-muted-foreground text-[11px] leading-relaxed">
                    {selectedRule.statusNote}
                  </p>
                </div>
              )}

              {/* Notification Targets */}
              <div className="space-y-2">
                <div className="text-xs font-semibold text-foreground flex items-center gap-1.5">
                  <MessageSquare className="h-3.5 w-3.5 text-primary" />
                  已配置通知路由通道
                </div>
                <div className="space-y-1.5">
                  {selectedRule.notificationChannels.map((ch, idx) => (
                    <div key={idx} className="flex items-center justify-between rounded-md border border-border/80 p-2 text-xs bg-background">
                      <span className="font-medium text-foreground">{ch}</span>
                      <span className="text-[10px] font-mono text-emerald-600 flex items-center gap-1">
                        <CheckCircle2 className="h-3 w-3" /> 联通正常
                      </span>
                    </div>
                  ))}
                </div>
              </div>

              {/* Action Buttons */}
              <div className="pt-3 border-t flex items-center justify-end gap-2">
                <Button variant="outline" size="sm" onClick={() => setSelectedRule(null)}>
                  关闭
                </Button>
                <Button size="sm">
                  编辑规则参数
                </Button>
              </div>
            </div>
          )}
        </SheetContent>
      </Sheet>
    </Main>
  );
}
