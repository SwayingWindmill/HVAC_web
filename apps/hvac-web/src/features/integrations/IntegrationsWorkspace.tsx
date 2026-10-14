import { useMemo, useState, type ComponentType } from 'react';
import { useMutation, useQueries, useQueryClient } from '@tanstack/react-query';
import { getRouteApi } from '@tanstack/react-router';
import { useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { z } from 'zod';
import { Cpu, Globe, Plus, Radio, Router, Search, Server } from 'lucide-react';
import type { Device } from '@/api/generated/platformGateway.gen';
import { flattenRegistryPages, useRegistryDevices, useRegistrySites } from '@/api/registry';
import { createIdleRealtimeStatus, realtimeStatusPresentation } from '@/app/realtime-status';
import { useShellSnapshot } from '@/app/ShellRuntimeContext';
import { PageHeader } from '@/blocks/page-header';
import { Panel, PanelHeader } from '@/blocks/panel';
import { InfoTip } from '@/components/info-tip';
import { Main } from '@/components/layout/Main';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Field, FieldError, FieldLabel } from '@/components/ui/field';
import { Input } from '@/components/ui/input';
import { InputGroup, InputGroupAddon, InputGroupInput } from '@/components/ui/input-group';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Skeleton } from '@/components/ui/skeleton';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { gatewayCredentialApi, gatewayCredentialKey } from '@/features/connectivity/gateway-credentials';
import { CredentialChip, daysLeft, GatewaySheet } from '@/features/connectivity/GatewaySheet';
import { cn } from '@/lib/utils';

const route = getRouteApi('/_app/settings/integrations');
const registrationSchema = z.object({ code: z.string().trim().min(1, '请填写编码'), displayName: z.string().trim().min(1, '请填写名称'), reason: z.string().trim().min(1, '请填写登记原因') });
type Registration = z.infer<typeof registrationSchema>;

type Health = 'good' | 'warn' | 'idle';
const HEALTH_DOT: Record<Health, string> = { good: 'bg-success', warn: 'bg-warning', idle: 'bg-subtle-foreground' };

const PLATFORM_STATE = { checking: '检查中', available: '正常', degraded: '部分降级', unavailable: '不可用' } as const;
const DIRECTORY_STATE = { checking: '读取中', available: '已同步', forbidden: '无权读取', unavailable: '暂时读不到' } as const;
const REGISTRY_STATE = { ACTIVE: null, INACTIVE: '登记已停用', RETIRED: '已退役' } as const;

const date = (value: string) => new Intl.DateTimeFormat('zh-CN', { year: 'numeric', month: '2-digit', day: '2-digit' }).format(new Date(value));

/** One link of the chain from the plant to this page, with its own state. */
function Segment({ icon: Icon, title, value, detail, health }: {
  readonly icon: ComponentType<{ className?: string }>;
  readonly title: string;
  readonly value: string;
  readonly detail: string;
  readonly health: Health;
}) {
  return (
    <div className="flex min-w-0 flex-1 flex-col items-center gap-1.5 text-center">
      <span className="relative flex size-10 items-center justify-center rounded-xl bg-muted text-muted-foreground">
        <Icon className="size-5" aria-hidden="true" />
        <span className={cn('absolute -right-0.5 -bottom-0.5 size-2.5 rounded-full border-2 border-card', HEALTH_DOT[health])} aria-hidden="true" />
      </span>
      <span className="text-sm font-semibold">{title}</span>
      <span className="text-sm">{value}</span>
      <span className="truncate text-xs text-muted-foreground">{detail}</span>
    </div>
  );
}

export function IntegrationsWorkspace() {
  const snapshot = useShellSnapshot();
  const principal = snapshot.principal!;
  const search = route.useSearch();
  const navigate = route.useNavigate();
  const sitesQuery = useRegistrySites();
  const sites = flattenRegistryPages(sitesQuery.data);
  const siteId = search.site ?? sites[0]?.id ?? null;
  const siteName = sites.find((site) => site.id === siteId)?.displayName ?? '';
  const devicesQuery = useRegistryDevices(siteId);
  const allDevices = flattenRegistryPages(devicesQuery.data);
  const gateways = allDevices.filter((device) => device.deviceType === 'GATEWAY');
  const fieldDevices = allDevices.filter((device) => device.deviceType !== 'GATEWAY' && device.status === 'ACTIVE');
  const canWrite = principal.authorization.capabilities.includes('device.write');
  const [query, setQuery] = useState('');
  const [registering, setRegistering] = useState(false);
  const [selected, setSelected] = useState<Device | null>(null);
  const queryClient = useQueryClient();

  const credentials = useQueries({
    queries: gateways.map((gateway) => ({
      queryKey: gatewayCredentialKey(gateway.id),
      queryFn: ({ signal }: { signal: AbortSignal }) => gatewayCredentialApi.status(gateway.id, signal),
      enabled: gateway.status === 'ACTIVE',
    })),
  });
  const credentialOf = new Map(gateways.map((gateway, index) => [gateway.id, credentials[index]]));

  const form = useForm<Registration>({ resolver: zodResolver(registrationSchema), defaultValues: { code: '', displayName: '', reason: '' } });
  const registration = useMutation({
    mutationFn: (values: Registration) => gatewayCredentialApi.register(siteId!, values),
    onSuccess: async (device) => {
      await queryClient.invalidateQueries({ queryKey: ['registry', 'sites', siteId, 'devices'] });
      setRegistering(false); form.reset(); setSelected(device);
    },
  });

  // The chain from the plant to this page, each link as its owner reports it.
  const platform = snapshot.platform?.status;
  const platformState = platform ? (platform.status === 'ok' ? '正常' : '部分降级') : PLATFORM_STATE[snapshot.platform?.state ?? 'checking'];
  const realtime = realtimeStatusPresentation(snapshot.realtime ?? createIdleRealtimeStatus());
  const activeGateways = gateways.filter((gateway) => gateway.status === 'ACTIVE');
  const validCertificates = activeGateways.filter((gateway) => credentialOf.get(gateway.id)?.data?.status === 'ACTIVE').length;
  const attention = activeGateways.filter((gateway) => ['EXPIRING', 'EXPIRED'].includes(credentialOf.get(gateway.id)?.data?.status ?? '')).length;
  const credentialsReady = credentials.every((result) => !result.isPending || result.fetchStatus === 'idle');

  const shown = useMemo(() => {
    const needle = query.trim().toLowerCase();
    return needle ? gateways.filter((gateway) => `${gateway.displayName} ${gateway.code}`.toLowerCase().includes(needle)) : gateways;
  }, [gateways, query]);

  return (
    <Main>
      <PageHeader
        title="集成管理"
        description="现场网关与数据接入"
        actions={(
          <>
            <Select value={siteId ?? ''} onValueChange={(site) => { setSelected(null); void navigate({ search: { site } }); }}>
              <SelectTrigger aria-label="站点" className="h-8 w-48"><SelectValue placeholder="选择站点" /></SelectTrigger>
              <SelectContent>{sites.map((site) => <SelectItem key={site.id} value={site.id}>{site.displayName}</SelectItem>)}</SelectContent>
            </Select>
            {sitesQuery.hasNextPage ? <Button variant="outline" size="sm" disabled={sitesQuery.isFetchingNextPage} onClick={() => void sitesQuery.fetchNextPage()}>加载更多站点</Button> : null}
          </>
        )}
      />

      {sitesQuery.isError || devicesQuery.isError ? <Alert variant="destructive"><AlertDescription>登记信息暂时读不到，请确认当前站点权限和服务状态。</AlertDescription></Alert> : null}

      <Panel aria-label="数据链路">
        <PanelHeader title="数据链路" caption="从现场到页面的每一段连接" />
        <div className="flex items-start px-6 py-5">
          {[
            <Segment key="devices" icon={Cpu} title="现场设备" value={devicesQuery.isPending ? '读取中' : `${fieldDevices.length} 台已登记`} detail={siteName || '—'} health={fieldDevices.length > 0 ? 'good' : 'idle'} />,
            <Segment
              key="gateways"
              icon={Router}
              title="现场网关"
              value={activeGateways.length === 0 ? '尚未登记' : credentialsReady ? `${validCertificates} / ${activeGateways.length} 证书有效` : '读取中'}
              detail={attention > 0 ? `${attention} 台证书需处理` : activeGateways.length > 0 ? '证书均有效' : '登记后生成接入码'}
              health={activeGateways.length === 0 ? 'idle' : attention > 0 || validCertificates < activeGateways.length ? 'warn' : 'good'}
            />,
            <Segment key="platform" icon={Server} title="平台服务" value={platformState} detail={platform?.version ? `版本 ${platform.version}` : '等待状态响应'} health={platform?.status === 'ok' ? 'good' : platform ? 'warn' : 'idle'} />,
            <Segment key="directory" icon={Globe} title="站点目录" value={DIRECTORY_STATE[snapshot.sites?.state ?? 'checking']} detail={`${snapshot.sites?.items?.length ?? 0} 个授权站点`} health={snapshot.sites?.state === 'available' ? 'good' : 'warn'} />,
            <Segment key="realtime" icon={Radio} title="实时推送" value={realtime.label} detail="打开站点页面后建立连接" health={realtime.code === 'CONNECTED' ? 'good' : realtime.code === 'RECONNECTING' || realtime.code === 'DEGRADED' ? 'warn' : 'idle'} />,
          ].flatMap((segment, index) => (index === 0 ? [segment] : [
            <span key={`link-${index}`} className="mt-5 h-px min-w-6 flex-[0.4] border-t-2 border-dashed border-border" aria-hidden="true" />,
            segment,
          ]))}
        </div>
      </Panel>

      <Panel aria-label="现场网关">
        <PanelHeader
          title={<span className="flex items-center gap-1.5">现场网关<InfoTip>接入码只显示一次；停用后，这个网关的数据与远程指令都会被拒绝</InfoTip></span>}
          caption={siteName}
          action={(
            <>
              <InputGroup className="h-8 w-56">
                <InputGroupAddon><Search /></InputGroupAddon>
                <InputGroupInput aria-label="搜索网关" placeholder="网关名称或编码" value={query} onChange={(event) => setQuery(event.target.value)} />
              </InputGroup>
              <Button size="sm" disabled={!canWrite || !siteId} onClick={() => { registration.reset(); setRegistering(true); }}>
                <Plus data-icon="inline-start" />
                登记网关
              </Button>
            </>
          )}
        />
        <Table aria-label="现场网关">
          <TableHeader>
            <TableRow>
              <TableHead className="pl-5">网关</TableHead>
              <TableHead>站点</TableHead>
              <TableHead>状态</TableHead>
              <TableHead>登记时间</TableHead>
              <TableHead className="pr-5">证书到期</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {devicesQuery.isPending ? (
              <TableRow className="hover:bg-transparent"><TableCell colSpan={5} className="p-4"><Skeleton className="h-10" /></TableCell></TableRow>
            ) : shown.length === 0 ? (
              <TableRow className="hover:bg-transparent">
                <TableCell colSpan={5} className="py-10 text-center text-muted-foreground">
                  {devicesQuery.isError || sitesQuery.isError ? '登记信息暂时读不到' : query ? '没有符合条件的网关' : '当前站点还没有登记网关'}
                </TableCell>
              </TableRow>
            ) : shown.map((gateway) => {
              const credential = credentialOf.get(gateway.id);
              const expiresAt = credential?.data?.expiresAt;
              const left = expiresAt ? daysLeft(expiresAt) : null;
              return (
                <TableRow key={gateway.id} className="cursor-pointer" onClick={() => setSelected(gateway)}>
                  <TableCell className="pl-5">
                    <button type="button" className="block text-left font-semibold outline-none hover:underline focus-visible:underline" onClick={(event) => { event.stopPropagation(); setSelected(gateway); }}>
                      {gateway.displayName}
                    </button>
                    <span className="font-mono text-xs text-muted-foreground">{gateway.code}</span>
                  </TableCell>
                  <TableCell>{siteName}</TableCell>
                  <TableCell>
                    {REGISTRY_STATE[gateway.status] ? <span className="text-xs text-muted-foreground">{REGISTRY_STATE[gateway.status]}</span>
                      : credential?.data ? <CredentialChip state={credential.data.status} />
                        : credential?.isError ? <span className="text-xs text-muted-foreground">证书状态读不到</span> : <Skeleton className="h-5 w-16" />}
                  </TableCell>
                  <TableCell className="font-mono text-xs">{date(gateway.createdAt)}</TableCell>
                  <TableCell className="pr-5">
                    {expiresAt ? (
                      <span className="flex flex-col">
                        <span className="font-mono text-xs">{date(expiresAt)}</span>
                        <span className={cn('text-xs', left !== null && left < 30 ? 'text-warning-text' : 'text-muted-foreground')}>{left !== null && left >= 0 ? `还有 ${left} 天` : '已过期'}</span>
                      </span>
                    ) : <span className="text-muted-foreground">—</span>}
                  </TableCell>
                </TableRow>
              );
            })}
          </TableBody>
        </Table>
        {devicesQuery.hasNextPage ? (
          <div className="flex justify-center border-t border-divider p-3">
            <Button variant="outline" size="sm" disabled={devicesQuery.isFetchingNextPage} onClick={() => void devicesQuery.fetchNextPage()}>加载更多网关</Button>
          </div>
        ) : null}
      </Panel>

      <Dialog open={registering} onOpenChange={(open) => { if (!registration.isPending) { setRegistering(open); if (!open) form.reset(); } }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>登记网关</DialogTitle>
            <DialogDescription>登记到 {siteName}，然后生成一次性接入码供现场领取证书。</DialogDescription>
          </DialogHeader>
          <form className="space-y-4" onSubmit={form.handleSubmit((values) => registration.mutate(values))}>
            {(['code', 'displayName', 'reason'] as const).map((field) => (
              <Field key={field} data-invalid={Boolean(form.formState.errors[field])}>
                <FieldLabel htmlFor={`gateway-${field}`}>{{ code: '业务编码', displayName: '网关名称', reason: '登记原因' }[field]}</FieldLabel>
                <Input id={`gateway-${field}`} {...form.register(field)} aria-invalid={Boolean(form.formState.errors[field])} />
                <FieldError errors={[form.formState.errors[field]]} />
              </Field>
            ))}
            {registration.isError ? <Alert variant="destructive"><AlertDescription>登记没有完成，请检查业务编码是否重复以及当前权限。</AlertDescription></Alert> : null}
            <div className="flex justify-end"><Button type="submit" disabled={registration.isPending}>{registration.isPending ? '正在登记…' : '保存登记'}</Button></div>
          </form>
        </DialogContent>
      </Dialog>
      <GatewaySheet gateway={selected} siteName={siteName} csrfToken={principal.session.csrfToken} canWrite={canWrite} onClose={() => setSelected(null)} />
    </Main>
  );
}
