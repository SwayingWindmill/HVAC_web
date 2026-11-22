import { useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import type { ColumnDef } from '@tanstack/react-table';
import { Rocket, Upload } from 'lucide-react';
import type {
  Device,
  OTACampaignSummary,
  OTAPackage,
  Site,
} from '@/api/generated/platformGateway.gen';
import { DataTableBlock } from '@/blocks/data-table';
import { Panel } from '@/blocks/panel';
import { StateCard } from '@/blocks/state/state-card';
import { DataTable, DataTablePagination, type DataTableFeatures } from '@/components/data-table';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Skeleton } from '@/components/ui/skeleton';
import { useDataTable } from '@/hooks/use-data-table';
import { isOneOf } from '@/lib/typed';
import {
  OTA_ARCH_LABELS,
  OTA_CAMPAIGN_STATUS_PRESENTATION,
  OTA_COMPONENT_LABELS,
  OTA_PACKAGE_STATUS_PRESENTATION,
  describeOtaError,
  formatByteSize,
  formatOtaPauseReason,
  summarizeOtaWorkspace,
} from '../ota-model';
import { otaCampaignsQuery, otaPackagesQuery } from '../ota-query';
import { GovernanceBadge } from './GovernanceShared';
import { OtaCampaignCreateDialog } from './OtaCampaignCreateDialog';
import { OtaCampaignDetailSheet } from './OtaCampaignDetailSheet';
import { OtaPackageUploadDialog } from './OtaPackageUploadDialog';

const CAMPAIGN_FILTER_VALUES = [
  'ALL',
  'CANARY_RUNNING',
  'ROLLOUT_RUNNING',
  'PAUSED',
  'COMPLETED',
  'ROLLED_BACK',
  'FAILED',
] as const;

type CampaignStatusFilter = (typeof CAMPAIGN_FILTER_VALUES)[number];

const timeFormat = new Intl.DateTimeFormat('zh-CN', {
  month: '2-digit',
  day: '2-digit',
  hour: '2-digit',
  minute: '2-digit',
  hour12: false,
});

function formatTimestamp(iso: string): string {
  const parsed = Date.parse(iso);
  if (Number.isNaN(parsed)) return '—';
  return timeFormat.format(new Date(parsed));
}

export interface OtaTabProps {
  readonly sites: readonly Site[];
  readonly activeSiteId: string | null;
  readonly allDevices: readonly Device[];
  readonly canRead: boolean;
  readonly canWrite: boolean;
  readonly csrfToken: string;
  readonly inspectCampaignId: string | undefined;
  readonly onInspectChange: (campaignId: string | undefined) => void;
}

export function OtaTab({
  sites,
  activeSiteId,
  allDevices,
  canRead,
  canWrite,
  csrfToken,
  inspectCampaignId,
  onInspectChange,
}: OtaTabProps) {
  const [statusFilter, setStatusFilter] = useState<CampaignStatusFilter>('ALL');
  const [uploadOpen, setUploadOpen] = useState(false);
  const [createOpen, setCreateOpen] = useState(false);

  const packagesQuery = useQuery(otaPackagesQuery(canRead));
  const campaignsQuery = useQuery(otaCampaignsQuery(canRead));

  const packages = packagesQuery.data?.items ?? [];
  const campaigns = campaignsQuery.data?.items ?? [];

  const summary = useMemo(
    () => summarizeOtaWorkspace(packages, campaigns),
    [packages, campaigns],
  );

  const filteredCampaigns = useMemo(
    () =>
      statusFilter === 'ALL'
        ? [...campaigns]
        : campaigns.filter((campaign) => campaign.status === statusFilter),
    [campaigns, statusFilter],
  );

  const campaignColumns = useMemo<ColumnDef<DataTableFeatures, OTACampaignSummary>[]>(
    () => [
      {
        id: 'campaign',
        header: () => '任务与目标版本',
        cell: ({ row }) => {
          const item = row.original;
          return (
            <div className="flex max-w-xs min-w-0 flex-col gap-0.5">
              <button
                type="button"
                className="truncate text-left font-medium outline-none hover:underline focus-visible:underline"
                title={item.name}
                onClick={() => onInspectChange(item.id)}
              >
                {item.name}
              </button>
              <span className="truncate font-mono text-xs text-muted-foreground">
                {OTA_COMPONENT_LABELS[item.component]} v{item.packageVersion} · {item.targetArch}
              </span>
            </div>
          );
        },
      },
      {
        id: 'policy',
        header: () => '灰度与熔断策略',
        cell: ({ row }) => {
          const item = row.original;
          return (
            <div className="text-xs tabular-nums">
              <div className="font-mono">
                灰度 {item.canaryPercent}% · 熔断 {item.failureRateThresholdPct}%
              </div>
              <div className="text-muted-foreground">
                {item.autoPromoteRollout ? '自动放量' : '手动放量'}
              </div>
            </div>
          );
        },
      },
      {
        id: 'progress',
        header: () => '网关进度',
        cell: ({ row }) => {
          const item = row.original;
          return (
            <div className="text-xs tabular-nums">
              <div className="font-mono font-medium">
                成功 {item.succeededCount} / {item.totalCount} 台
              </div>
              <div className="text-muted-foreground">
                失败率 {item.failureRatePct}% · 回滚 {item.rolledBackCount}
              </div>
            </div>
          );
        },
      },
      {
        id: 'status',
        header: () => '状态',
        cell: ({ row }) => {
          const item = row.original;
          const pauseNote = formatOtaPauseReason(item.pauseReason);
          return (
            <div className="flex flex-col items-start gap-1">
              <GovernanceBadge status={OTA_CAMPAIGN_STATUS_PRESENTATION[item.status]} />
              {pauseNote ? (
                <span
                  className={
                    item.pauseReason === 'FAILURE_RATE_THRESHOLD_EXCEEDED'
                      ? 'text-xs text-destructive-text'
                      : 'text-xs text-muted-foreground'
                  }
                >
                  {pauseNote}
                </span>
              ) : null}
            </div>
          );
        },
      },
      {
        id: 'updatedAt',
        header: () => '更新时间',
        cell: ({ row }) => (
          <span className="font-mono text-xs text-muted-foreground tabular-nums">
            {formatTimestamp(row.original.updatedAt)}
          </span>
        ),
      },
      {
        id: 'actions',
        header: () => '操作',
        cell: ({ row }) => (
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() => onInspectChange(row.original.id)}
          >
            查看详情
          </Button>
        ),
      },
    ],
    [onInspectChange],
  );

  const packageRows = useMemo(() => [...packages], [packages]);

  const packageColumns = useMemo<ColumnDef<DataTableFeatures, OTAPackage>[]>(
    () => [
      {
        id: 'componentVersion',
        header: () => '组件与版本',
        cell: ({ row }) => {
          const item = row.original;
          return (
            <div className="flex flex-col gap-0.5">
              <span className="font-mono font-semibold">v{item.version}</span>
              <span className="text-xs text-muted-foreground">
                {OTA_COMPONENT_LABELS[item.component]}
                {item.minCompatibleVersion ? ` · 最低兼容 v${item.minCompatibleVersion}` : ''}
              </span>
            </div>
          );
        },
      },
      {
        id: 'platform',
        header: () => '目标平台与型号',
        cell: ({ row }) => {
          const item = row.original;
          return (
            <div className="text-xs">
              <div className="font-mono">{OTA_ARCH_LABELS[item.targetArch]}</div>
              <div className="text-muted-foreground">
                {item.hardwareModels.length > 0 ? item.hardwareModels.join('、') : '不限硬件型号'}
              </div>
            </div>
          );
        },
      },
      {
        id: 'artifact',
        header: () => '安装包与签名',
        cell: ({ row }) => {
          const item = row.original;
          return (
            <div className="flex max-w-xs min-w-0 flex-col gap-0.5 text-xs">
              <span className="truncate font-mono font-medium" title={item.fileName}>
                {item.fileName}
              </span>
              <span className="truncate font-mono text-muted-foreground tabular-nums">
                {formatByteSize(item.fileSizeBytes)} · 密钥 {item.signingKeyId}
              </span>
            </div>
          );
        },
      },
      {
        id: 'status',
        header: () => '状态',
        cell: ({ row }) => (
          <GovernanceBadge status={OTA_PACKAGE_STATUS_PRESENTATION[row.original.status]} />
        ),
      },
      {
        id: 'createdAt',
        header: () => '上传时间',
        cell: ({ row }) => (
          <span className="font-mono text-xs text-muted-foreground tabular-nums">
            {formatTimestamp(row.original.createdAt)}
          </span>
        ),
      },
    ],
    [],
  );

  const campaignTable = useDataTable({
    key: 'integrations.ota.campaigns',
    data: filteredCampaigns,
    columns: campaignColumns,
    pageSize: 8,
    getRowId: (row) => row.id,
  });

  const packageTable = useDataTable({
    key: 'integrations.ota.packages',
    data: packageRows,
    columns: packageColumns,
    pageSize: 8,
    getRowId: (row) => row.id,
  });

  if (!canRead) {
    return (
      <StateCard
        title={<h2 className="text-base font-semibold">当前账号无权查看 OTA 升级记录</h2>}
        description="查看升级包和发布任务需要设备读取权限，请联系管理员分配相应角色。"
      />
    );
  }

  return (
    <div className="flex flex-col gap-6">
      {!canWrite ? (
        <Alert>
          <AlertDescription>
            当前账号仅有查看权限，无法上传升级包或控制发布任务（暂停、放量与回滚）。
          </AlertDescription>
        </Alert>
      ) : null}

      {packagesQuery.isError || campaignsQuery.isError ? (
        <Alert variant="destructive">
          <AlertDescription className="flex flex-wrap items-center justify-between gap-2">
            <span>
              {describeOtaError(
                campaignsQuery.error ?? packagesQuery.error,
                'OTA 数据暂时无法读取，请稍后重试。',
              )}
            </span>
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => {
                void packagesQuery.refetch();
                void campaignsQuery.refetch();
              }}
            >
              重试
            </Button>
          </AlertDescription>
        </Alert>
      ) : null}

      <Panel aria-label="OTA 升级概览">
        {packagesQuery.isPending || campaignsQuery.isPending ? (
          <div className="grid grid-cols-1 gap-4 p-5 sm:grid-cols-4">
            <Skeleton className="h-16" />
            <Skeleton className="h-16" />
            <Skeleton className="h-16" />
            <Skeleton className="h-16" />
          </div>
        ) : (
          <div className="grid grid-cols-1 divide-y divide-divider sm:grid-cols-4 sm:divide-x sm:divide-y-0">
            <div className="flex flex-col gap-1 px-6 py-4">
              <span className="text-xs font-medium text-muted-foreground">进行中发布</span>
              <div className="flex items-baseline gap-1.5">
                <span className="font-mono text-2xl font-semibold tabular-nums">
                  {summary.activeCampaigns}
                </span>
                <span className="text-xs text-muted-foreground">项</span>
              </div>
              <span className="text-xs text-muted-foreground tabular-nums">
                灰度 {summary.canaryRunning} · 全量 {summary.rolloutRunning}
              </span>
            </div>

            <div className="flex flex-col gap-1 px-6 py-4">
              <span className="text-xs font-medium text-muted-foreground">已暂停与熔断</span>
              <div className="flex items-baseline gap-1.5">
                <span className="font-mono text-2xl font-semibold tabular-nums">
                  {summary.pausedCampaigns}
                </span>
                <span className="text-xs text-muted-foreground">项</span>
              </div>
              <span
                className={
                  summary.circuitBrokenCampaigns > 0
                    ? 'text-xs font-medium text-destructive-text tabular-nums'
                    : 'text-xs text-muted-foreground tabular-nums'
                }
              >
                {summary.circuitBrokenCampaigns > 0
                  ? `其中 ${summary.circuitBrokenCampaigns} 项触发失败率熔断`
                  : '当前无自动熔断任务'}
              </span>
            </div>

            <div className="flex flex-col gap-1 px-6 py-4">
              <span className="text-xs font-medium text-muted-foreground">可用升级包</span>
              <div className="flex items-baseline gap-1.5">
                <span className="font-mono text-2xl font-semibold tabular-nums">
                  {summary.activePackages}
                </span>
                <span className="text-xs text-muted-foreground">个</span>
              </div>
              <span className="text-xs text-muted-foreground tabular-nums">
                累计登记 {summary.totalPackages} 个历史升级包
              </span>
            </div>

            <div className="flex flex-col gap-1 px-6 py-4">
              <span className="text-xs font-medium text-muted-foreground">历史已完成</span>
              <div className="flex items-baseline gap-1.5">
                <span className="font-mono text-2xl font-semibold tabular-nums">
                  {summary.completedCampaigns}
                </span>
                <span className="text-xs text-muted-foreground">项</span>
              </div>
              <span className="text-xs text-muted-foreground">已全量完成健康检查</span>
            </div>
          </div>
        )}
      </Panel>

      <DataTableBlock
        title="发布任务"
        description="按站点与标签灰度发布边缘网关升级包，失败率达阈值自动熔断，支持随时暂停、放量与回滚"
        controls={
          <div className="flex flex-wrap items-center justify-between gap-2">
            <Select
              value={statusFilter}
              onValueChange={(next) => {
                if (isOneOf(CAMPAIGN_FILTER_VALUES, next)) setStatusFilter(next);
              }}
            >
              <SelectTrigger aria-label="发布状态筛选" className="h-8 w-44">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="ALL">全部状态</SelectItem>
                <SelectItem value="CANARY_RUNNING">灰度进行中</SelectItem>
                <SelectItem value="ROLLOUT_RUNNING">全量进行中</SelectItem>
                <SelectItem value="PAUSED">已暂停</SelectItem>
                <SelectItem value="COMPLETED">已完成</SelectItem>
                <SelectItem value="ROLLED_BACK">已回滚</SelectItem>
                <SelectItem value="FAILED">已失败</SelectItem>
              </SelectContent>
            </Select>
          </div>
        }
        actions={
          <Button
            type="button"
            size="sm"
            disabled={!canWrite || summary.activePackages === 0}
            onClick={() => setCreateOpen(true)}
          >
            <Rocket data-icon="inline-start" />
            发起发布任务
          </Button>
        }
      >
        {campaignsQuery.isPending ? (
          <Skeleton className="h-44 w-full rounded-xl" />
        ) : (
          <DataTable
            table={campaignTable}
            tableAriaLabel="OTA 发布任务"
            empty={
              <div className="py-10 text-center text-sm text-muted-foreground">
                {statusFilter === 'ALL' ? '还没有 OTA 发布任务' : '当前筛选状态下没有发布任务'}
              </div>
            }
            getHeaderCellProps={(header) => ({
              className:
                header.id === 'campaign'
                  ? 'pl-5'
                  : header.id === 'actions'
                    ? 'pr-5 text-right'
                    : undefined,
            })}
            getCellProps={(cell) => ({
              className:
                cell.column.id === 'campaign'
                  ? 'pl-5 align-top'
                  : cell.column.id === 'actions'
                    ? 'pr-5 text-right align-top whitespace-nowrap'
                    : 'align-top',
            })}
            footer={
              filteredCampaigns.length > 0 ? (
                <DataTablePagination table={campaignTable} totalRows={filteredCampaigns.length} />
              ) : null
            }
          />
        )}
      </DataTableBlock>

      <DataTableBlock
        title="升级包"
        description="Linux 边缘守护进程与固件签名安装包（最大 64 MiB，自动生成或校验 Ed25519 签名与 SHA-256）"
        actions={
          <Button
            type="button"
            variant="outline"
            size="sm"
            disabled={!canWrite}
            onClick={() => setUploadOpen(true)}
          >
            <Upload data-icon="inline-start" />
            上传升级包
          </Button>
        }
      >
        {packagesQuery.isPending ? (
          <Skeleton className="h-40 w-full rounded-xl" />
        ) : (
          <DataTable
            table={packageTable}
            tableAriaLabel="OTA 升级包"
            empty={
              <div className="py-10 text-center text-sm text-muted-foreground">
                还没有上传 OTA 升级包
              </div>
            }
            getHeaderCellProps={(header) => ({
              className:
                header.id === 'componentVersion'
                  ? 'pl-5'
                  : header.id === 'createdAt'
                    ? 'pr-5 text-right'
                    : undefined,
            })}
            getCellProps={(cell) => ({
              className:
                cell.column.id === 'componentVersion'
                  ? 'pl-5 align-top'
                  : cell.column.id === 'createdAt'
                    ? 'pr-5 text-right align-top whitespace-nowrap'
                    : 'align-top',
            })}
            footer={
              packageRows.length > 0 ? (
                <DataTablePagination table={packageTable} totalRows={packageRows.length} />
              ) : null
            }
          />
        )}
      </DataTableBlock>

      <OtaPackageUploadDialog
        open={uploadOpen}
        csrfToken={csrfToken}
        onOpenChange={setUploadOpen}
      />

      <OtaCampaignCreateDialog
        open={createOpen}
        packages={packages}
        sites={sites}
        activeSiteId={activeSiteId}
        allDevices={allDevices}
        csrfToken={csrfToken}
        onOpenChange={setCreateOpen}
        onCreated={(detail) => onInspectChange(detail.id)}
      />

      <OtaCampaignDetailSheet
        campaignId={inspectCampaignId}
        sites={sites}
        canWrite={canWrite}
        csrfToken={csrfToken}
        onClose={() => onInspectChange(undefined)}
      />
    </div>
  );
}
