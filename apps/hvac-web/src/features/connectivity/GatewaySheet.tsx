import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { KeyRound, Link2, RefreshCw, ShieldCheck, ShieldX } from 'lucide-react';
import { toast } from 'sonner';
import type { Device, GatewayCredentialStatus } from '@/api/generated/platformGateway.gen';
import { Alert, AlertDescription } from '@/components/ui/alert';
import {
  AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle,
} from '@/components/ui/alert-dialog';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '@/components/ui/sheet';
import { Skeleton } from '@/components/ui/skeleton';
import { cn } from '@/lib/utils';
import { gatewayCredentialApi, gatewayCredentialKey } from './gateway-credentials';

type CredentialState = GatewayCredentialStatus['status'];

/** A gateway's credential in plain words, with the tone it is shown in. */
export const CREDENTIAL: Readonly<Record<CredentialState, { readonly label: string; readonly tone: 'success' | 'warning' | 'danger' | 'neutral' }>> = {
  NOT_ENROLLED: { label: '等待接入', tone: 'neutral' },
  ACTIVE: { label: '证书有效', tone: 'success' },
  EXPIRING: { label: '证书即将到期', tone: 'warning' },
  EXPIRED: { label: '证书已到期', tone: 'danger' },
  REVOKED: { label: '已停用', tone: 'neutral' },
};

const TONE = {
  success: { chip: 'bg-success-soft text-success-text', dot: 'bg-success' },
  warning: { chip: 'bg-warning-soft text-warning-text', dot: 'bg-warning' },
  danger: { chip: 'bg-destructive-soft text-destructive-text', dot: 'bg-destructive' },
  neutral: { chip: 'bg-secondary text-secondary-foreground', dot: 'bg-subtle-foreground' },
} as const;

export function CredentialChip({ state, className }: { readonly state: CredentialState; readonly className?: string }) {
  const tone = TONE[CREDENTIAL[state].tone];
  return (
    <span className={cn('inline-flex h-5.5 items-center gap-1.5 rounded-md px-1.5 text-xs font-medium whitespace-nowrap', tone.chip, className)}>
      <span className={cn('size-1.5 rounded-full', tone.dot)} aria-hidden="true" />
      {CREDENTIAL[state].label}
    </span>
  );
}

const DAY_MS = 86_400_000;
export const daysLeft = (expiresAt: string) => Math.ceil((Date.parse(expiresAt) - Date.now()) / DAY_MS);
const date = (value: string) => new Intl.DateTimeFormat('zh-CN', { year: 'numeric', month: '2-digit', day: '2-digit' }).format(new Date(value));
const time = (value: string) => new Intl.DateTimeFormat('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false }).format(new Date(value));

/** One gateway beside the list: its certificate, a one-time enrollment code, and stopping it for good. */
export function GatewaySheet({ gateway, siteName, csrfToken, canWrite, onClose }: {
  readonly gateway: Device | null;
  readonly siteName: string;
  readonly csrfToken: string;
  readonly canWrite: boolean;
  readonly onClose: () => void;
}) {
  return (
    <Sheet open={Boolean(gateway)} onOpenChange={(open) => { if (!open) onClose(); }}>
      <SheetContent className="overflow-y-auto data-[side=right]:sm:max-w-[480px]">
        {gateway ? <GatewayDetail key={gateway.id} gateway={gateway} siteName={siteName} csrfToken={csrfToken} canWrite={canWrite} /> : null}
      </SheetContent>
    </Sheet>
  );
}

function GatewayDetail({ gateway, siteName, csrfToken, canWrite }: {
  readonly gateway: Device;
  readonly siteName: string;
  readonly csrfToken: string;
  readonly canWrite: boolean;
}) {
  const queryClient = useQueryClient();
  const [confirmingStop, setConfirmingStop] = useState(false);
  const status = useQuery({ queryKey: gatewayCredentialKey(gateway.id), queryFn: ({ signal }) => gatewayCredentialApi.status(gateway.id, signal) });
  const code = useMutation({ mutationFn: () => gatewayCredentialApi.generateCode(gateway.id, csrfToken), gcTime: 0 });
  const stop = useMutation({
    mutationFn: () => gatewayCredentialApi.revoke(gateway.id, csrfToken),
    onSuccess: (result) => {
      code.reset();
      setConfirmingStop(false);
      queryClient.setQueryData(gatewayCredentialKey(gateway.id), result);
      toast.success('网关已停用');
    },
  });
  const working = code.isPending || stop.isPending;
  const stopped = status.data?.status === 'REVOKED';
  const copyLink = () => {
    void navigator.clipboard.writeText(window.location.href).then(() => toast.success('链接已复制'), () => toast.error('无法复制链接'));
  };

  return (
    <>
      <SheetHeader className="gap-2 border-b border-divider px-6 pt-5 pb-4">
        <div className="flex items-center gap-2 pr-8 text-xs">
          {status.data ? <CredentialChip state={status.data.status} /> : <Skeleton className="h-5 w-16" />}
          <Button variant="ghost" size="icon-sm" className="ml-auto text-muted-foreground" aria-label="复制链接" onClick={copyLink}><Link2 className="size-3.5" /></Button>
        </div>
        <SheetTitle>{gateway.displayName}</SheetTitle>
        <SheetDescription>
          <span className="font-mono">{gateway.code}</span> · {siteName} · <span className="font-mono">{time(gateway.createdAt)}</span> 登记
        </SheetDescription>
      </SheetHeader>

      <section aria-label="证书" className="flex flex-col gap-3 border-b border-divider px-6 py-4">
        <h3 className="text-sm font-semibold">证书</h3>
        {status.isPending ? <Skeleton className="h-14 rounded-lg" /> : status.data ? (
          <div className="flex items-center gap-3 rounded-lg border bg-muted/40 px-4 py-3">
            {status.data.status === 'ACTIVE' ? <ShieldCheck className="size-5 shrink-0 text-success" aria-hidden="true" />
              : <ShieldX className={cn('size-5 shrink-0', status.data.status === 'NOT_ENROLLED' || stopped ? 'text-muted-foreground' : 'text-warning')} aria-hidden="true" />}
            <span className="flex min-w-0 flex-1 flex-col">
              <span className="text-sm font-semibold">{CREDENTIAL[status.data.status].label}</span>
              <span className="text-xs text-muted-foreground">
                {status.data.expiresAt
                  ? <>到期 <span className="font-mono">{date(status.data.expiresAt)}</span> · {daysLeft(status.data.expiresAt) >= 0 ? `还有 ${daysLeft(status.data.expiresAt)} 天` : `已过期 ${-daysLeft(status.data.expiresAt)} 天`}</>
                  : stopped ? '该网关已永久停用，如需重新接入请登记新的网关' : '尚未用接入码领取证书'}
              </span>
            </span>
            <Button variant="outline" size="sm" disabled={status.isFetching} onClick={() => void status.refetch()}>
              <RefreshCw data-icon="inline-start" />
              刷新状态
            </Button>
          </div>
        ) : null}
        {status.isError ? <Alert variant="destructive"><AlertDescription>证书状态暂时读不到，请确认权限和服务状态后重试。</AlertDescription></Alert> : null}
      </section>

      <section aria-label="接入码" className="flex flex-col gap-3 border-b border-divider px-6 py-4">
        <h3 className="text-sm font-semibold">接入码</h3>
        <p className="text-xs text-muted-foreground">现场网关用一次性接入码领取证书。接入码只显示一次，使用一次后失效。</p>
        {code.data ? (
          <div className="flex flex-col gap-2 rounded-lg border bg-muted/40 p-3">
            <label htmlFor="gateway-enrollment-code" className="text-xs font-medium">一次性接入码</label>
            <Input id="gateway-enrollment-code" readOnly value={code.data.enrollmentCode} autoComplete="off" className="font-mono text-xs" onFocus={(event) => event.currentTarget.select()} />
            <p className="text-xs text-muted-foreground">有效至 <span className="font-mono">{time(code.data.expiresAt)}</span>。请安全交给现场人员。</p>
          </div>
        ) : null}
        {code.isError ? <Alert variant="destructive"><AlertDescription>接入码没有生成，请确认权限后重试。</AlertDescription></Alert> : null}
        <div>
          <Button size="sm" disabled={!canWrite || working || !status.data || stopped} onClick={() => code.mutate()}>
            <KeyRound data-icon="inline-start" />
            {code.isPending ? '正在生成…' : '生成接入码'}
          </Button>
        </div>
      </section>

      <section aria-label="危险操作" className="flex flex-col gap-3 px-6 py-4">
        <h3 className="text-sm font-semibold">危险操作</h3>
        <div className="flex items-center gap-4 rounded-lg border border-destructive/30 px-4 py-3">
          <span className="flex min-w-0 flex-1 flex-col">
            <span className="text-sm font-semibold">停用这个网关</span>
            <span className="text-xs text-muted-foreground">停用后不再接收它的数据，也不能再向它发送指令；无法恢复，需重新登记</span>
          </span>
          <Button variant="outline" size="sm" className="text-destructive-text" disabled={!canWrite || working || !status.data || stopped} onClick={() => setConfirmingStop(true)}>
            停用网关…
          </Button>
        </div>
        {stop.isError ? <Alert variant="destructive"><AlertDescription>停用没有完成，请确认权限后重试。</AlertDescription></Alert> : null}
      </section>

      <AlertDialog open={confirmingStop} onOpenChange={(open) => { if (!working) setConfirmingStop(open); }}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>永久停用这个网关？</AlertDialogTitle>
            <AlertDialogDescription>
              将永久作废 {gateway.displayName} 的全部证书和未使用的接入码。它发来的数据会被拒收，也不能再向它发送指令；该身份不能恢复。
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={working}>取消</AlertDialogCancel>
            <AlertDialogAction variant="destructive" disabled={working} onClick={(event) => { event.preventDefault(); stop.mutate(); }}>
              {stop.isPending ? '正在停用…' : '永久停用此网关'}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  );
}
