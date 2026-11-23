import { useQuery } from '@tanstack/react-query';
import type { ColumnDef } from '@tanstack/react-table';
import { GitCompareArrows, Plus, RotateCcw, ShieldCheck } from 'lucide-react';
import { useMemo, useState } from 'react';
import type { EdgeModelArtifactView, EdgeModelDeploymentView } from '@/api/generated/platformGateway.gen';
import { DataTableBlock } from '@/blocks/data-table';
import { FactTile } from '@/blocks/fact-list';
import { Panel, PanelHeader } from '@/blocks/panel';
import { DataTable, DataTablePagination, type DataTableFeatures } from '@/components/data-table';
import { StatusBadge } from '@/components/status-badge';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Skeleton } from '@/components/ui/skeleton';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { useDataTable } from '@/hooks/use-data-table';
import { formatTime } from '@/lib/operator-format';
import {
  ApproveEdgeModelDialog,
  DeployEdgeModelDialog,
  RegisterEdgeModelDialog,
  RollbackEdgeModelDialog,
  type EdgeGatewayOption,
} from './EdgeModelDialogs';
import {
  buildHyperparamDiff,
  edgeModelWorkspaceQuery,
  formatApprovalStatusLabel,
  formatDeployEnvironmentLabel,
  formatDeployStatusLabel,
  formatDomainLabel,
  formatDriftStatusLabel,
  formatEvalOutcomeLabel,
} from './edge-models-query';

export function EdgeModelsGovernancePanel({
  siteId,
  timeZone,
  gateways,
  active,
  canManage,
  csrfToken,
}: {
  readonly siteId: string;
  readonly timeZone: string;
  readonly gateways: readonly EdgeGatewayOption[];
  readonly active: boolean;
  readonly canManage: boolean;
  readonly csrfToken: string;
}) {
  const workspaceQuery = useQuery(edgeModelWorkspaceQuery(siteId, active));
  const [registerOpen, setRegisterOpen] = useState(false);
  const [approveTarget, setApproveTarget] = useState<EdgeModelArtifactView | null>(null);
  const [deployTarget, setDeployTarget] = useState<EdgeModelArtifactView | null>(null);
  const [rollbackTarget, setRollbackTarget] = useState<EdgeModelDeploymentView | null>(null);
  const [compareBaseId, setCompareBaseId] = useState<string>('');
  const [compareTargetId, setCompareTargetId] = useState<string>('');

  const data = workspaceQuery.data;
  const artifacts = data?.artifacts ?? [];
  const deployments = data?.deployments ?? [];
  const driftObservations = data?.driftObservations ?? [];
  const rollbacks = data?.rollbacks ?? [];

  const resolvedBase = useMemo(
    () => artifacts.find((a) => a.id === compareBaseId) ?? artifacts[1] ?? artifacts[0] ?? null,
    [artifacts, compareBaseId],
  );
  const resolvedTarget = useMemo(
    () => artifacts.find((a) => a.id === compareTargetId) ?? artifacts[0] ?? null,
    [artifacts, compareTargetId],
  );
  const diffRows = useMemo(() => buildHyperparamDiff(resolvedBase, resolvedTarget), [resolvedBase, resolvedTarget]);

  const gatewayLabelMap = useMemo(() => {
    const map = new Map<string, string>();
    for (const gw of gateways) map.set(gw.id, gw.name);
    return map;
  }, [gateways]);

  const columns = useMemo<Array<ColumnDef<DataTableFeatures, EdgeModelArtifactView>>>(() => [
    {
      id: 'model',
      header: '模型与版本',
      cell: ({ row }) => (
        <div className="space-y-0.5">
          <div className="font-medium text-foreground">{row.original.name}</div>
          <div className="text-xs text-muted-foreground">
            {formatDomainLabel(row.original.domain)} · {row.original.modelKey} · <span className="font-mono">{row.original.version}</span>
          </div>
        </div>
      ),
    },
    {
      id: 'checksum',
      header: '校验与签名',
      cell: ({ row }) => (
        <div className="flex items-center gap-2 text-xs">
          <span className="font-mono text-muted-foreground">摘要 {row.original.checksumSha256.slice(0, 8)}</span>
          <Badge variant="outline">{row.original.signed ? '已签名' : '待签名'}</Badge>
        </div>
      ),
    },
    {
      id: 'approval',
      header: '审批状态',
      cell: ({ row }) => {
        const tone = row.original.approvalStatus === 'APPROVED' ? 'success' : row.original.approvalStatus === 'REJECTED' ? 'destructive' : 'warning';
        return <StatusBadge tone={tone}>{formatApprovalStatusLabel(row.original.approvalStatus)}</StatusBadge>;
      },
    },
    {
      id: 'evaluation',
      header: '离线 / 影子验证',
      cell: ({ row }) => (
        <span className="text-xs text-muted-foreground">
          {row.original.latestEvaluation ? formatEvalOutcomeLabel(row.original.latestEvaluation) : '未记录评估'}
        </span>
      ),
    },
    {
      id: 'createdAt',
      header: '登记时间',
      cell: ({ row }) => <span className="font-mono text-xs tabular-nums">{formatTime(row.original.createdAt, timeZone)}</span>,
    },
    {
      id: 'actions',
      header: '治理操作',
      cell: ({ row }) => (
        <div className="flex flex-wrap items-center gap-1.5">
          <Button type="button" size="xs" variant="outline" onClick={() => setCompareTargetId(row.original.id)}>
            对比参数
          </Button>
          {canManage ? (
            <Button type="button" size="xs" variant="outline" onClick={() => setApproveTarget(row.original)}>
              审批
            </Button>
          ) : null}
          {canManage && row.original.approvalStatus === 'APPROVED' ? (
            <Button type="button" size="xs" onClick={() => setDeployTarget(row.original)}>
              下发
            </Button>
          ) : null}
        </div>
      ),
    },
  ], [canManage, timeZone]);

  const artifactTable = useDataTable({
    key: 'edge-model-artifacts',
    data: [...artifacts],
    columns,
    pageSize: 6,
    getRowId: (row) => row.id,
  });

  const activeDeployCount = deployments.filter((d) => d.status === 'ACTIVE').length;
  const approvedCount = artifacts.filter((a) => a.approvalStatus === 'APPROVED').length;
  const breachedCount = driftObservations.filter((d) => d.driftStatus === 'BREACHED').length;

  return (
    <div className="space-y-4">
      <Panel aria-label="端侧模型与超参治理概览">
        <PanelHeader
          title="端侧 AI 模型与控制超参托管"
          caption="托管端侧寻优与故障诊断模型超参；校验硬件物理边界与硬实时安全点位，经审批后通过配置影子热更新并监测影子偏差。"
          action={
            <div className="flex items-center gap-2">
              {canManage ? (
                <Button type="button" size="sm" onClick={() => setRegisterOpen(true)}>
                  <Plus className="size-3.5" aria-hidden="true" />
                  登记候选版本
                </Button>
              ) : null}
            </div>
          }
        />

        {workspaceQuery.isPending ? (
          <div className="p-5"><Skeleton className="h-32 rounded-lg" /></div>
        ) : workspaceQuery.isError ? (
          <div className="p-5">
            <Alert variant="destructive">
              <AlertTitle>加载端侧模型列表失败</AlertTitle>
              <AlertDescription>{workspaceQuery.error instanceof Error ? workspaceQuery.error.message : '服务暂不可用'}</AlertDescription>
            </Alert>
          </div>
        ) : (
          <div className="space-y-5 px-5 py-4">
            <dl className="grid grid-cols-2 gap-x-6 gap-y-3 sm:grid-cols-4">
              <FactTile label="已登记候选版本">{artifacts.length} 个</FactTile>
              <FactTile label="已审批可执行">{approvedCount} 个</FactTile>
              <FactTile label="当前端侧生效">{activeDeployCount} 个</FactTile>
              <FactTile label="影子偏差告警" valueClassName={breachedCount > 0 ? 'font-semibold text-destructive' : undefined}>
                {breachedCount > 0 ? `${breachedCount} 次超限` : '无超限'}
              </FactTile>
            </dl>

            <DataTableBlock title="模型与超参版本清单" description="候选版本登记后需经审批通过方可下发；任意物理越界或受保护联锁点位将被服务端拦截。">
              <DataTable
                table={artifactTable}
                empty="当前站点尚未登记端侧模型或超参版本"
                footer={<DataTablePagination table={artifactTable} totalRows={artifacts.length} />}
              />
            </DataTableBlock>
          </div>
        )}
      </Panel>

      {artifacts.length > 0 ? (
        <Panel aria-label="结构化超参差异对比">
          <PanelHeader
            title="结构化超参差异对比"
            caption="在审批与热更新之前，逐项核对基准版本与候选版本的物理限值与调控点位变化。"
            action={
              <div className="flex flex-wrap items-center gap-2">
                <GitCompareArrows className="size-4 text-muted-foreground" aria-hidden="true" />
                <Select value={resolvedBase?.id ?? ''} onValueChange={setCompareBaseId}>
                  <SelectTrigger className="h-8 w-44"><SelectValue placeholder="基准版本" /></SelectTrigger>
                  <SelectContent>
                    {artifacts.map((art) => (
                      <SelectItem key={art.id} value={art.id}>{art.modelKey} · {art.version}</SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                <span className="text-xs text-muted-foreground">对比</span>
                <Select value={resolvedTarget?.id ?? ''} onValueChange={setCompareTargetId}>
                  <SelectTrigger className="h-8 w-44"><SelectValue placeholder="目标版本" /></SelectTrigger>
                  <SelectContent>
                    {artifacts.map((art) => (
                      <SelectItem key={art.id} value={art.id}>{art.modelKey} · {art.version}</SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
            }
          />
          <Table aria-label="超参差异明细">
            <TableHeader>
              <TableRow>
                <TableHead className="pl-5">超参项目</TableHead>
                <TableHead>基准版本（{resolvedBase?.version ?? '未选'}）</TableHead>
                <TableHead>目标版本（{resolvedTarget?.version ?? '未选'}）</TableHead>
                <TableHead className="pr-5">变更状态</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {diffRows.map((row) => (
                <TableRow key={row.key}>
                  <TableCell className="pl-5 font-medium">{row.label}</TableCell>
                  <TableCell className="font-mono text-xs text-muted-foreground">{row.baseValue}</TableCell>
                  <TableCell className="font-mono text-xs text-foreground">{row.targetValue}</TableCell>
                  <TableCell className="pr-5">
                    {row.changed ? <StatusBadge tone="info">已调整</StatusBadge> : <span className="text-xs text-muted-foreground">保持一致</span>}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </Panel>
      ) : null}

      <Panel aria-label="端侧热更新部署与回退审计">
        <PanelHeader title="端侧热更新部署与影子偏差审计" caption="监测端侧影子并行输出与实际控制之间的最大偏差；超出允许阈值时自动或手动一键回退上一版本。" />
        {deployments.length === 0 ? (
          <div className="flex items-center gap-2 px-5 py-6 text-sm text-muted-foreground">
            <ShieldCheck className="size-4" aria-hidden="true" />
            暂无端侧部署记录，请先审批候选版本后下发。
          </div>
        ) : (
          <Table aria-label="端侧部署记录">
            <TableHeader>
              <TableRow>
                <TableHead className="pl-5">模型版本</TableHead>
                <TableHead>运行模式</TableHead>
                <TableHead>目标节点</TableHead>
                <TableHead>当前状态</TableHead>
                <TableHead>生效时间</TableHead>
                <TableHead className="pr-5">回退控制</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {deployments.map((dep) => {
                const gwLabel = dep.gatewayId ? (gatewayLabelMap.get(dep.gatewayId) ?? '指定边缘网关') : '站点边缘节点组';
                const statusTone = dep.status === 'ACTIVE' ? 'success' : dep.status === 'ROLLED_BACK' ? 'destructive' : 'neutral';
                return (
                  <TableRow key={dep.id}>
                    <TableCell className="pl-5 font-medium">{dep.modelKey} · <span className="font-mono">{dep.version}</span></TableCell>
                    <TableCell>{formatDeployEnvironmentLabel(dep.environment)}</TableCell>
                    <TableCell>{gwLabel}</TableCell>
                    <TableCell><StatusBadge tone={statusTone}>{formatDeployStatusLabel(dep.status)}</StatusBadge></TableCell>
                    <TableCell className="font-mono text-xs tabular-nums">{formatTime(dep.effectiveFrom, timeZone)}</TableCell>
                    <TableCell className="pr-5">
                      {canManage && dep.status === 'ACTIVE' ? (
                        <Button type="button" size="xs" variant="destructive" onClick={() => setRollbackTarget(dep)}>
                          <RotateCcw className="size-3" aria-hidden="true" />
                          一键回退
                        </Button>
                      ) : <span className="text-xs text-muted-foreground">—</span>}
                    </TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        )}

        {driftObservations.length > 0 ? (
          <div className="border-t border-divider px-5 py-3">
            <div className="mb-2 text-xs font-medium text-muted-foreground">最近影子偏差观测</div>
            <div className="flex flex-wrap gap-2">
              {driftObservations.slice(0, 6).map((dr) => (
                <StatusBadge key={dr.id} tone={dr.driftStatus === 'BREACHED' ? 'destructive' : dr.driftStatus === 'WARNING' ? 'warning' : 'success'}>
                  {dr.evidence.modelKey} ({dr.evidence.version}) · {formatDriftStatusLabel(dr.driftStatus)} {dr.observedValue.toFixed(2)}/{dr.threshold.toFixed(2)}
                </StatusBadge>
              ))}
            </div>
          </div>
        ) : null}

        {rollbacks.length > 0 ? (
          <div className="border-t border-divider px-5 py-3 text-xs text-muted-foreground">
            最近回退审计：{rollbacks[0]!.fromVersion || '当前版'} 回退至 {rollbacks[0]!.toVersion || '规则基线'}（原因：{rollbacks[0]!.reason} · {formatTime(rollbacks[0]!.requestedAt, timeZone)}）
          </div>
        ) : null}
      </Panel>

      <RegisterEdgeModelDialog siteId={siteId} gateways={gateways} open={registerOpen} onOpenChange={setRegisterOpen} csrfToken={csrfToken} />
      <ApproveEdgeModelDialog siteId={siteId} artifact={approveTarget} open={Boolean(approveTarget)} onOpenChange={(o) => { if (!o) setApproveTarget(null); }} csrfToken={csrfToken} />
      <DeployEdgeModelDialog siteId={siteId} artifact={deployTarget} gateways={gateways} open={Boolean(deployTarget)} onOpenChange={(o) => { if (!o) setDeployTarget(null); }} csrfToken={csrfToken} />
      <RollbackEdgeModelDialog siteId={siteId} deployment={rollbackTarget} open={Boolean(rollbackTarget)} onOpenChange={(o) => { if (!o) setRollbackTarget(null); }} csrfToken={csrfToken} />
    </div>
  );
}
