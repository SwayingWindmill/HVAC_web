import { useEffect, useMemo, useState } from 'react';
import type { ColumnDef } from '@tanstack/react-table';
import { Download, FileJson, Upload } from 'lucide-react';
import type { Capability, ImportPlan, ImportPlanRequest, Site, SiteAssetModel } from '@/api/generated/platformGateway.gen';
import { importPlanRequestSchema } from '@/api/generated/platformGateway.gen';
import { presentRegistryError, registryAdminApi } from '@/api/registry';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Textarea } from '@/components/ui/textarea';
import { DataTable, DataTablePagination, StatusPillBadge, type DataTableFeatures } from '@/components/data-table';
import { useDataTable } from '@/hooks/use-data-table';
import { buildRegistryExport, canCommitImportPlan, makeRegistryMutationMeta, newRegistryIdempotencyKey, registryExportFileName } from './model';

interface Props {
  site: Site;
  model: SiteAssetModel;
  capabilities: ReadonlySet<Capability>;
  onDirtyChange: (dirty: boolean) => void;
  onRefresh: () => Promise<void>;
}

const EXAMPLE_IMPORT = JSON.stringify({
  namespace: 'site-import-v1',
  rows: [{
    rowNumber: 1,
    resourceType: 'ASSET',
    externalId: 'ahu-01',
    expectedRevision: 0,
    payload: { code: 'ahu-01', displayName: 'AHU 01', assetType: 'AHU', status: 'ACTIVE' },
  }],
}, null, 2);

function planStatusLabel(status: string): string {
  if (status === 'READY') return '可提交';
  if (status === 'COMMITTED') return '已提交';
  if (status === 'CONFLICT') return '存在冲突';
  if (status === 'INVALID') return '无效';
  return '需处理';
}

export function RegistryImportExportWorkbench({ site, model, capabilities, onDirtyChange, onRefresh }: Props) {
  const [source, setSource] = useState(EXAMPLE_IMPORT);
  const [dirty, setDirty] = useState(false);
  const [plan, setPlan] = useState<ImportPlan | null>(null);
  const [working, setWorking] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [commitMessage, setCommitMessage] = useState<string | null>(null);
  const allowed = capabilities.has('registry.import');

  useEffect(() => onDirtyChange(dirty), [dirty, onDirtyChange]);

  const dryRun = async () => {
    setWorking(true);
    setError(null);
    setCommitMessage(null);
    try {
      const parsed = importPlanRequestSchema.parse(JSON.parse(source)) as ImportPlanRequest;
      const nextPlan = await registryAdminApi.planImport(site.id, parsed);
      setPlan(nextPlan);
      setDirty(false);
    } catch (reason) {
      setError(reason);
    } finally {
      setWorking(false);
    }
  };

  const commit = async () => {
    if (!plan || !canCommitImportPlan(plan)) return;
    setWorking(true);
    setError(null);
    try {
      const result = await registryAdminApi.commitImport(site.id, {
        plan,
        meta: makeRegistryMutationMeta(0, '提交已复核的登记导入计划', newRegistryIdempotencyKey('registry-import-commit')),
      });
      const committed = result.results.filter((row) => row.status === 'COMMITTED').length;
      setCommitMessage(`导入完成，共提交 ${committed} 条登记记录。`);
      setPlan(null);
      await onRefresh();
    } catch (reason) {
      setError(reason);
    } finally {
      setWorking(false);
    }
  };

  const readFile = async (file: File | undefined) => {
    if (!file) return;
    try {
      setSource(await file.text());
      setDirty(true);
      setPlan(null);
    } catch (reason) {
      setError(reason);
    }
  };

  const exportRegistry = () => {
    const payload = buildRegistryExport(site, model, new Date().toISOString());
    const blob = new Blob([JSON.stringify(payload, null, 2)], { type: 'application/json' });
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement('a');
    anchor.href = url;
    anchor.download = registryExportFileName(site);
    anchor.click();
    URL.revokeObjectURL(url);
  };

  type ImportPlanRow = ImportPlan['results'][number];

  const planColumns = useMemo<Array<ColumnDef<DataTableFeatures, ImportPlanRow>>>(() => [
    { id: 'rowNumber', header: '行', cell: ({ row }) => <span className="font-mono tabular-nums">{row.original.rowNumber}</span> },
    { id: 'resourceType', header: '资源类型', cell: ({ row }) => <span className="font-medium text-foreground">{row.original.resourceType}</span> },
    { id: 'externalId', header: '外部标识', cell: ({ row }) => <span className="font-mono text-xs">{row.original.externalId}</span> },
    {
      id: 'status',
      header: '状态',
      cell: ({ row }) => (
        <StatusPillBadge
          label={planStatusLabel(row.original.status)}
          tone={row.original.status === 'READY' || row.original.status === 'COMMITTED' ? 'success' : 'warning'}
        />
      ),
    },
    {
      id: 'message',
      header: '说明',
      cell: ({ row }) => <span className="text-[11px] text-muted-foreground">{row.original.message || (row.original.status === 'READY' ? '校验通过' : '需要复核')}</span>,
    },
  ], []);

  const planTable = useDataTable({
    key: 'registry-import-plan-results',
    data: [...(plan?.results ?? [])],
    columns: planColumns,
    pageSize: 10,
    getRowId: (row) => String(row.rowNumber),
  });

  const presentedError = error ? presentRegistryError(error) : null;

  return (
    <div className="space-y-4">
      {presentedError ? <Alert variant="destructive"><AlertTitle>{presentedError.title}</AlertTitle><AlertDescription>{presentedError.description}</AlertDescription></Alert> : null}
      {commitMessage ? <Alert><AlertTitle>导入成功</AlertTitle><AlertDescription>{commitMessage}</AlertDescription></Alert> : null}
      <div className="grid gap-4 xl:grid-cols-[1.5fr_.8fr]">
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2"><Upload className="size-4" />受控导入</CardTitle>
            <CardDescription>先生成服务端校验计划，所有记录可提交后才能正式写入。</CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            <Alert><AlertTitle>导入不会直接写入拓扑</AlertTitle><AlertDescription>文件会先经过服务端校验。若登记版本已经变化或存在不合法记录，必须重新复核后再提交。</AlertDescription></Alert>
            <div className="flex flex-wrap items-center gap-3">
              <label className="inline-flex cursor-pointer items-center gap-2 rounded-md border px-3 py-2 text-sm hover:bg-muted/50">
                <FileJson className="size-4" />选择 JSON 文件
                <input className="sr-only" type="file" accept="application/json,.json" disabled={!allowed} onChange={(event) => void readFile(event.currentTarget.files?.[0])} />
              </label>
              <Badge variant="outline">{allowed ? '允许导入' : '当前只读'}</Badge>
            </div>
            <Textarea value={source} onChange={(event) => { setSource(event.target.value); setDirty(true); setPlan(null); }} rows={16} className="font-mono text-xs" disabled={!allowed} aria-label="登记导入 JSON" />
            <div className="flex flex-wrap gap-2">
              <Button disabled={!allowed || working} onClick={() => void dryRun()}>{working ? '正在校验…' : '校验导入内容'}</Button>
              <Button variant="destructive" disabled={!allowed || working || !canCommitImportPlan(plan)} onClick={() => void commit()}>提交已复核计划</Button>
            </div>
          </CardContent>
        </Card>
        <Card>
          <CardHeader><CardTitle className="flex items-center gap-2"><Download className="size-4" />受控导出</CardTitle><CardDescription>导出当前站点的规范化登记快照。</CardDescription></CardHeader>
          <CardContent className="space-y-4">
            <p className="text-sm leading-6 text-muted-foreground">导出只包含规范化身份、拓扑关系、版本和测点采集语义，不包含凭据、秘密或自由格式的敏感元数据。</p>
            <dl className="grid gap-2 text-sm">
              <div className="flex justify-between gap-3"><dt className="text-muted-foreground">站点</dt><dd className="font-medium">{site.displayName}</dd></div>
              <div className="flex justify-between gap-3"><dt className="text-muted-foreground">空间</dt><dd className="font-medium">{model.counts.spaces}</dd></div>
              <div className="flex justify-between gap-3"><dt className="text-muted-foreground">设备资产</dt><dd className="font-medium">{model.counts.assets}</dd></div>
              <div className="flex justify-between gap-3"><dt className="text-muted-foreground">设备端点</dt><dd className="font-medium">{model.counts.deviceEndpoints}</dd></div>
              <div className="flex justify-between gap-3"><dt className="text-muted-foreground">测点</dt><dd className="font-medium">{model.counts.points}</dd></div>
            </dl>
            <Button variant="outline" className="w-full" onClick={exportRegistry}><Download />导出登记快照</Button>
          </CardContent>
        </Card>
      </div>

      {plan ? (
        <Card>
          <CardHeader><CardTitle>导入校验结果</CardTitle><CardDescription>只有全部记录均显示“可提交”时，才会开放正式提交。</CardDescription></CardHeader>
          <CardContent className="p-0">
            <DataTable
              table={planTable}
              className="gap-0"
              tableAriaLabel="登记导入校验结果"
              getHeaderRowProps={() => ({ className: 'bg-muted/30 text-xs' })}
              getHeaderCellProps={(header) => ({
                className:
                  header.id === 'rowNumber' ? 'w-16' :
                  header.id === 'status' ? 'w-32 text-center' :
                  undefined,
              })}
              getRowProps={() => ({ className: 'text-xs hover:bg-muted/40' })}
              getCellProps={(cell) => ({
                className: cell.column.id === 'status' ? 'text-center' : undefined,
              })}
              footer={(
                <DataTablePagination
                  table={planTable}
                  totalRows={plan.results.length}
                />
              )}
            />
          </CardContent>
        </Card>
      ) : null}
    </div>
  );
}
