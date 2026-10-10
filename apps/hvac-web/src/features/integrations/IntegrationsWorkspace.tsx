import { useMemo, useState } from 'react';
import { PageHeader } from '@/blocks/page-header';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { getRouteApi } from '@tanstack/react-router';
import type { ColumnDef } from '@tanstack/react-table';
import { useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { z } from 'zod';
import { Plus } from 'lucide-react';
import type { Device } from '@/api/generated/platformGateway.gen';
import { flattenRegistryPages, useRegistrySites, useRegistryDevices } from '@/api/registry';
import { useShellSnapshot } from '@/app/ShellRuntimeContext';
import { Main } from '@/components/layout/Main';
import { DataTableBlock } from '@/blocks/data-table';
import { DataTable, DataTablePagination, type DataTableFeatures } from '@/components/data-table';
import { useDataTable } from '@/hooks/use-data-table';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Field, FieldLabel, FieldError } from '@/components/ui/field';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { GatewayCredentialPanel } from '@/features/connectivity/GatewayCredentialPanel';
import { gatewayCredentialApi } from '@/features/connectivity/gateway-credentials';

const route = getRouteApi('/_app/settings/integrations');
const registrationSchema = z.object({ code: z.string().trim().min(1, '请填写编码'), displayName: z.string().trim().min(1, '请填写名称'), reason: z.string().trim().min(1, '请填写登记原因') });
type Registration = z.infer<typeof registrationSchema>;

export function IntegrationsWorkspace() {
  const principal = useShellSnapshot().principal!;
  const search = route.useSearch();
  const navigate = route.useNavigate();
  const sitesQuery = useRegistrySites();
  const sites = flattenRegistryPages(sitesQuery.data);
  const siteId = search.site ?? sites[0]?.id ?? null;
  const devicesQuery = useRegistryDevices(siteId);
  const devices = flattenRegistryPages(devicesQuery.data).filter((device) => device.deviceType === 'GATEWAY');
  const canWrite = principal.authorization.capabilities.includes('device.write');
  const [registering, setRegistering] = useState(false);
  const [selected, setSelected] = useState<Device | null>(null);
  const queryClient = useQueryClient();
  const form = useForm<Registration>({ resolver: zodResolver(registrationSchema), defaultValues: { code: '', displayName: '', reason: '' } });
  const registration = useMutation({
    mutationFn: (values: Registration) => gatewayCredentialApi.register(siteId!, values),
    onSuccess: async (device) => {
      await queryClient.invalidateQueries({ queryKey: ['registry', 'sites', siteId, 'devices'] });
      setRegistering(false); form.reset(); setSelected(device);
    },
  });
  const columns = useMemo<Array<ColumnDef<DataTableFeatures, Device>>>(() => [
    { id: 'name', header: '网关名称', cell: ({ row }) => <span className="font-medium">{row.original.displayName}</span> },
    { id: 'code', header: '业务编码', cell: ({ row }) => row.original.code },
    { id: 'status', header: '登记状态', cell: ({ row }) => ({ ACTIVE: '已登记', INACTIVE: '已停用', RETIRED: '已退役' })[row.original.status] },
    { id: 'actions', header: '操作', cell: ({ row }) => <Button size="sm" variant="outline" disabled={row.original.status !== 'ACTIVE'} onClick={() => setSelected(row.original)}>接入凭据</Button>, enableSorting: false, enableHiding: false },
  ], []);
  const table = useDataTable({ key: 'registered-gateways', data: devices, columns, pageSize: 10, getRowId: (row) => row.id });
  return <Main className="space-y-6">
    <PageHeader
      title="集成管理"
      description="登记现场网关，管理接入身份与证书生命周期。"
      actions={<Button disabled={!canWrite || !siteId} onClick={() => { registration.reset(); setRegistering(true); }}><Plus />登记网关</Button>}
    />
    <div className="flex items-center gap-3"><label htmlFor="gateway-site" className="text-sm font-medium">站点</label>
      <Select value={siteId ?? ''} onValueChange={(site) => { setSelected(null); void navigate({ search: { site } }); }}>
        <SelectTrigger id="gateway-site" className="w-64"><SelectValue placeholder="选择站点" /></SelectTrigger>
        <SelectContent>{sites.map((site) => <SelectItem key={site.id} value={site.id}>{site.displayName}</SelectItem>)}</SelectContent>
      </Select>
      {sitesQuery.hasNextPage ? <Button variant="outline" disabled={sitesQuery.isFetchingNextPage} onClick={() => void sitesQuery.fetchNextPage()}>加载更多站点</Button> : null}
    </div>
    {sitesQuery.isError || devicesQuery.isError ? <Alert variant="destructive"><AlertDescription>无法读取登记信息，请确认当前站点权限和服务状态。</AlertDescription></Alert> : null}
    <DataTableBlock title="现场网关" description="接入码仅显示一次；吊销身份后，该网关的数据与远程命令都会被拒绝。">
      <DataTable table={table} tableAriaLabel="现场网关" empty={devicesQuery.isError || sitesQuery.isError ? '登记信息暂不可用' : devicesQuery.isPending ? '正在读取网关…' : '当前站点尚未登记网关'} />
      <DataTablePagination table={table} totalRows={devices.length} />
    </DataTableBlock>
    {devicesQuery.hasNextPage ? <Button variant="outline" disabled={devicesQuery.isFetchingNextPage} onClick={() => void devicesQuery.fetchNextPage()}>加载更多网关</Button> : null}
    <Dialog open={registering} onOpenChange={(open) => { if (!registration.isPending) { setRegistering(open); if (!open) form.reset(); } }}>
      <DialogContent><DialogHeader><DialogTitle>登记网关</DialogTitle><DialogDescription>登记到 {sites.find((site) => site.id === siteId)?.displayName}，然后生成一次性接入码供现场领证。</DialogDescription></DialogHeader>
        <form className="space-y-4" onSubmit={form.handleSubmit((values) => registration.mutate(values))}>
          {(['code', 'displayName', 'reason'] as const).map((field) => <Field key={field} data-invalid={Boolean(form.formState.errors[field])}><FieldLabel htmlFor={`gateway-${field}`}>{{ code: '业务编码', displayName: '网关名称', reason: '登记原因' }[field]}</FieldLabel><Input id={`gateway-${field}`} {...form.register(field)} aria-invalid={Boolean(form.formState.errors[field])} /><FieldError errors={[form.formState.errors[field]]} /></Field>)}
          {registration.isError ? <Alert variant="destructive"><AlertDescription>登记未完成，请检查业务编码是否重复及当前权限。</AlertDescription></Alert> : null}
          <div className="flex justify-end"><Button type="submit" disabled={registration.isPending}>{registration.isPending ? '正在登记…' : '保存登记'}</Button></div>
        </form>
      </DialogContent>
    </Dialog>
    {selected ? <GatewayCredentialPanel gateway={selected} csrfToken={principal.session.csrfToken} canWrite={canWrite} onClose={() => setSelected(null)} /> : null}
  </Main>;
}
