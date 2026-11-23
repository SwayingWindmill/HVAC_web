import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { KeyRound, Link2, RefreshCw, RotateCw, ShieldCheck, ShieldX } from 'lucide-react';
import { toast } from 'sonner';
import type {
  Device,
  GatewayCredentialList,
  GatewayCredentialRecord,
  GatewayCredentialStatus,
} from '@/api/generated/platformGateway.gen';
import { Alert, AlertDescription } from '@/components/ui/alert';
import {
  AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle,
} from '@/components/ui/alert-dialog';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '@/components/ui/sheet';
import { Skeleton } from '@/components/ui/skeleton';
import { cn } from '@/lib/utils';
import { GatewayConfigShadowSection } from './GatewayConfigShadowSection';
import { GatewayPacketCaptureSection, GatewayQuarantineSection } from './GatewayDiagnosticsSections';
import { GatewayHostMetricsSection, GatewayLogPullSection } from './GatewayRemoteDiagnosticsSections';
import { gatewayCredentialApi, gatewayCredentialKey, gatewayCredentialListKey } from './gateway-credentials';

type CredentialState = GatewayCredentialStatus['status'];
type CredentialTone = keyof typeof TONE;

/** A gateway's credential in plain words, with the tone it is shown in. */
export const CREDENTIAL: Readonly<Record<CredentialState, { readonly label: string; readonly tone: CredentialTone }>> = {
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

function StatusChip({ label, tone, className }: { readonly label: string; readonly tone: CredentialTone; readonly className?: string }) {
  const style = TONE[tone];
  return (
    <span className={cn('inline-flex h-5.5 shrink-0 items-center gap-1.5 rounded-md px-1.5 text-xs font-medium whitespace-nowrap', style.chip, className)}>
      <span className={cn('size-1.5 rounded-full', style.dot)} aria-hidden="true" />
      {label}
    </span>
  );
}

export function CredentialChip({ state, className }: { readonly state: CredentialState; readonly className?: string }) {
  const presentation = CREDENTIAL[state];
  return <StatusChip label={presentation.label} tone={presentation.tone} className={className} />;
}

const DAY_MS = 86_400_000;
export const daysLeft = (expiresAt: string) => Math.ceil((Date.parse(expiresAt) - Date.now()) / DAY_MS);
const date = (value: string) => new Intl.DateTimeFormat('zh-CN', { year: 'numeric', month: '2-digit', day: '2-digit' }).format(new Date(value));
const time = (value: string) => new Intl.DateTimeFormat('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false }).format(new Date(value));

function resolveCertificateBadge(record: GatewayCredentialRecord): { readonly label: string; readonly tone: CredentialTone } {
  if (record.status === 'REVOKED') return { label: '已吊销', tone: 'danger' };
  if (Date.parse(record.validUntil) <= Date.now()) return { label: '已过期', tone: 'warning' };
  return { label: '有效', tone: 'success' };
}

function formatCertificateIdentifier(record: GatewayCredentialRecord): string {
  return record.serialNumber
    ? `序列号 ${record.serialNumber}`
    : `指纹末 8 位 ${record.fingerprintSha256.slice(-8)}`;
}

function formatRevocationReason(reason: string): string {
  return reason === 'rotated_on_renewal' ? '续期轮换自动吊销' : reason;
}

/** One gateway beside the list: issued certificates, enrollment or key rotation, and stop control. */
export function GatewaySheet({ gateway, siteName, csrfToken, canWrite, onClose }: {
  readonly gateway: Device | null;
  readonly siteName: string;
  readonly csrfToken: string;
  readonly canWrite: boolean;
  readonly onClose: () => void;
}) {
  return (
    <Sheet open={Boolean(gateway)} onOpenChange={(open) => { if (!open) onClose(); }}>
      <SheetContent className="overflow-y-auto data-[side=right]:sm:max-w-[640px]">
        {gateway ? <GatewayDetail key={gateway.id} gateway={gateway} siteName={siteName} csrfToken={csrfToken} canWrite={canWrite} /> : null}
      </SheetContent>
    </Sheet>
  );
}

function CertificateRecordItem({ record, canWrite, working, onRevoke }: {
  readonly record: GatewayCredentialRecord;
  readonly canWrite: boolean;
  readonly working: boolean;
  readonly onRevoke: (record: GatewayCredentialRecord) => void;
}) {
  const badge = resolveCertificateBadge(record);
  const identifier = formatCertificateIdentifier(record);
  return (
    <li className="flex flex-col gap-1.5 rounded-lg border bg-muted/40 px-3.5 py-3">
      <div className="flex items-start justify-between gap-2">
        <span className="min-w-0 break-all font-mono text-xs font-medium tabular-nums">{identifier}</span>
        <StatusChip label={badge.label} tone={badge.tone} />
      </div>
      <div className="text-xs text-muted-foreground">
        签发 <span className="font-mono tabular-nums">{date(record.validFrom)}</span> · 到期 <span className="font-mono tabular-nums">{date(record.validUntil)}</span>
      </div>
      {record.status === 'REVOKED' && (record.revokedAt || record.revocationReason) ? (
        <div className="break-words text-xs text-muted-foreground">
          {record.revokedAt ? <>吊销于 <span className="font-mono tabular-nums">{time(record.revokedAt)}</span></> : null}
          {record.revokedAt && record.revocationReason ? ' · ' : null}
          {record.revocationReason ? <>原因：{formatRevocationReason(record.revocationReason)}</> : null}
        </div>
      ) : null}
      {record.status === 'ACTIVE' ? (
        <div className="pt-1">
          <Button
            variant="outline"
            size="sm"
            className="text-destructive-text"
            disabled={!canWrite || working}
            onClick={() => onRevoke(record)}
          >
            吊销证书…
          </Button>
        </div>
      ) : null}
    </li>
  );
}

function RevokeCertificateDialog({ certificate, reason, pending, working, error, onReasonChange, onClose, onConfirm }: {
  readonly certificate: GatewayCredentialRecord | null;
  readonly reason: string;
  readonly pending: boolean;
  readonly working: boolean;
  readonly error: boolean;
  readonly onReasonChange: (value: string) => void;
  readonly onClose: () => void;
  readonly onConfirm: () => void;
}) {
  return (
    <AlertDialog open={Boolean(certificate)} onOpenChange={(open) => { if (!open && !working) onClose(); }}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>吊销这张网关证书？</AlertDialogTitle>
          <AlertDialogDescription>
            将立即吊销{certificate ? ` ${formatCertificateIdentifier(certificate)}` : '所选证书'}。吊销后仅这张证书立即失效，同网关上其他仍在有效期内的轮换证书会继续工作；如果这是最后一张有效证书，网关将停止上报数据，直到使用新接入码领取证书。
          </AlertDialogDescription>
        </AlertDialogHeader>
        <div className="flex flex-col gap-1.5">
          <label htmlFor="gateway-cert-revoke-reason" className="text-xs font-medium">吊销原因（可选）</label>
          <Input
            id="gateway-cert-revoke-reason"
            value={reason}
            maxLength={512}
            placeholder="例如：证书私钥疑似泄露"
            disabled={working}
            onChange={(event) => onReasonChange(event.target.value)}
            onKeyDown={(event) => {
              if (event.key !== 'Enter') return;
              if (event.nativeEvent.isComposing || event.keyCode === 229) {
                event.preventDefault();
                return;
              }
              event.preventDefault();
            }}
          />
        </div>
        {error ? (
          <Alert variant="destructive">
            <AlertDescription>证书吊销没有完成，请确认权限后重试。</AlertDescription>
          </Alert>
        ) : null}
        <AlertDialogFooter>
          <AlertDialogCancel disabled={working}>取消</AlertDialogCancel>
          <AlertDialogAction
            variant="destructive"
            disabled={working || !certificate}
            onClick={(event) => {
              event.preventDefault();
              onConfirm();
            }}
          >
            {pending ? '正在吊销…' : '确认吊销'}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}

function StopGatewayDialog({ open, gatewayName, pending, working, error, onOpenChange, onConfirm }: {
  readonly open: boolean;
  readonly gatewayName: string;
  readonly pending: boolean;
  readonly working: boolean;
  readonly error: boolean;
  readonly onOpenChange: (open: boolean) => void;
  readonly onConfirm: () => void;
}) {
  return (
    <AlertDialog open={open} onOpenChange={(nextOpen) => { if (!working) onOpenChange(nextOpen); }}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>停用这个网关？</AlertDialogTitle>
          <AlertDialogDescription>
            将作废 {gatewayName} 的全部有效证书和未使用的接入码。它发来的数据会被拒收，也不能再向它发送指令；若需恢复接入，可在上方重新启用并生成接入码。
          </AlertDialogDescription>
        </AlertDialogHeader>
        {error ? (
          <Alert variant="destructive">
            <AlertDescription>停用没有完成，请确认权限后重试。</AlertDescription>
          </Alert>
        ) : null}
        <AlertDialogFooter>
          <AlertDialogCancel disabled={working}>取消</AlertDialogCancel>
          <AlertDialogAction
            variant="destructive"
            disabled={working}
            onClick={(event) => {
              event.preventDefault();
              onConfirm();
            }}
          >
            {pending ? '正在停用…' : '停用此网关'}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
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
  const [certToRevoke, setCertToRevoke] = useState<GatewayCredentialRecord | null>(null);
  const [revokeReason, setRevokeReason] = useState('');

  const status = useQuery({
    queryKey: gatewayCredentialKey(gateway.id),
    queryFn: ({ signal }) => gatewayCredentialApi.status(gateway.id, signal),
    retry: false,
  });
  const credentials = useQuery({
    queryKey: gatewayCredentialListKey(gateway.id),
    queryFn: ({ signal }) => gatewayCredentialApi.listCredentials(gateway.id, signal),
    retry: false,
  });

  const rotate = useMutation({
    mutationFn: (_wasStopped: boolean) => gatewayCredentialApi.rotate(gateway.id, csrfToken),
    gcTime: 0,
    onMutate: () => {
      code.reset();
    },
    onSuccess: (_result, wasStopped) => {
      void queryClient.invalidateQueries({ queryKey: gatewayCredentialKey(gateway.id) });
      void queryClient.invalidateQueries({ queryKey: gatewayCredentialListKey(gateway.id) });
      toast.success(wasStopped ? '网关已重新启用并生成接入码' : '已生成轮换接入码');
    },
  });
  const code = useMutation({
    mutationFn: () => gatewayCredentialApi.generateCode(gateway.id, csrfToken),
    gcTime: 0,
    onMutate: () => {
      rotate.reset();
    },
  });
  const revokeCert = useMutation({
    mutationFn: ({ credentialId, reason }: { readonly credentialId: string; readonly reason: string }) =>
      gatewayCredentialApi.revokeCertificate(gateway.id, credentialId, reason || undefined, csrfToken),
    onSuccess: (updatedRecord) => {
      setCertToRevoke(null);
      setRevokeReason('');
      queryClient.setQueryData<GatewayCredentialList>(gatewayCredentialListKey(gateway.id), (current) => (
        current
          ? { items: current.items.map((item) => (item.id === updatedRecord.id ? updatedRecord : item)) }
          : current
      ));
      void queryClient.invalidateQueries({ queryKey: gatewayCredentialKey(gateway.id) });
      void queryClient.invalidateQueries({ queryKey: gatewayCredentialListKey(gateway.id) });
      toast.success('证书已吊销');
    },
  });
  const stop = useMutation({
    mutationFn: () => gatewayCredentialApi.revoke(gateway.id, csrfToken),
    onSuccess: (result) => {
      code.reset();
      rotate.reset();
      setConfirmingStop(false);
      queryClient.setQueryData(gatewayCredentialKey(gateway.id), result);
      void queryClient.invalidateQueries({ queryKey: gatewayCredentialListKey(gateway.id) });
      toast.success('网关已停用');
    },
  });

  const working = code.isPending || rotate.isPending || stop.isPending || revokeCert.isPending;
  const stopped = status.data?.status === 'REVOKED';
  const activeEnrollmentCode = rotate.data ?? code.data;
  const refreshAll = () => {
    void status.refetch();
    void credentials.refetch();
  };
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
          <span className="font-mono">{gateway.code}</span> · {siteName} · <span className="font-mono tabular-nums">{time(gateway.createdAt)}</span> 登记
        </SheetDescription>
      </SheetHeader>

      <section aria-label="证书" className="flex flex-col gap-3 border-b border-divider px-6 py-4">
        <h3 className="text-sm font-semibold">证书</h3>
        {status.isPending ? <Skeleton className="h-14 rounded-lg" /> : status.data ? (
          <div className="flex items-center gap-3 rounded-lg border bg-muted/40 px-4 py-3">
            {status.data.status === 'ACTIVE' ? <ShieldCheck className="size-5 shrink-0 text-success" aria-hidden="true" />
              : <ShieldX className={cn('size-5 shrink-0', status.data.status === 'NOT_ENROLLED' || stopped ? 'text-muted-foreground' : status.data.status === 'EXPIRED' ? 'text-destructive' : 'text-warning')} aria-hidden="true" />}
            <span className="flex min-w-0 flex-1 flex-col">
              <span className="text-sm font-semibold">{CREDENTIAL[status.data.status].label}</span>
              <span className="text-xs text-muted-foreground">
                {status.data.expiresAt
                  ? <>到期 <span className="font-mono tabular-nums">{date(status.data.expiresAt)}</span> · {daysLeft(status.data.expiresAt) >= 0 ? `还有 ${daysLeft(status.data.expiresAt)} 天` : `已过期 ${-daysLeft(status.data.expiresAt)} 天`}</>
                  : stopped ? '该网关已停用，所有证书和接入码已失效；可用下方“重新启用并生成接入码”恢复接入' : '尚未用接入码领取证书'}
              </span>
            </span>
            <Button variant="outline" size="sm" className="shrink-0" disabled={status.isFetching || credentials.isFetching} onClick={refreshAll}>
              <RefreshCw data-icon="inline-start" />
              刷新状态
            </Button>
          </div>
        ) : null}
        {status.isError ? (
          <Alert variant="destructive">
            <AlertDescription className="flex flex-wrap items-center justify-between gap-2">
              <span>证书状态暂时读不到，请确认权限和服务状态后重试。</span>
              <Button variant="outline" size="sm" disabled={status.isFetching || credentials.isFetching} onClick={refreshAll}>
                <RefreshCw data-icon="inline-start" />
                刷新状态
              </Button>
            </AlertDescription>
          </Alert>
        ) : null}

        <div className="flex flex-col gap-2">
          <h4 className="text-xs font-medium text-muted-foreground">已签发证书</h4>
          {credentials.isPending ? (
            <Skeleton className="h-16 rounded-lg" />
          ) : credentials.isError ? (
            <Alert variant="destructive">
              <AlertDescription className="flex flex-wrap items-center justify-between gap-2">
                <span>已签发证书列表暂时读不到，请确认权限和服务状态后重试。</span>
                <Button variant="outline" size="sm" disabled={credentials.isFetching} onClick={() => void credentials.refetch()}>
                  重试
                </Button>
              </AlertDescription>
            </Alert>
          ) : credentials.data && credentials.data.items.length > 0 ? (
            <ul aria-label="已签发证书" className="flex flex-col gap-2">
              {credentials.data.items.map((record) => (
                <CertificateRecordItem
                  key={record.id}
                  record={record}
                  canWrite={canWrite}
                  working={working}
                  onRevoke={(target) => {
                    revokeCert.reset();
                    setRevokeReason('');
                    setCertToRevoke(target);
                  }}
                />
              ))}
            </ul>
          ) : (
            <p className="rounded-lg border border-dashed px-4 py-3 text-xs text-muted-foreground">
              暂无已签发的网关证书
            </p>
          )}
        </div>
      </section>

      <section aria-label="接入码" className="flex flex-col gap-3 border-b border-divider px-6 py-4">
        <h3 className="text-sm font-semibold">接入码</h3>
        <p className="text-xs text-muted-foreground">
          现场网关用一次性接入码领取证书。接入码只显示一次，使用一次后失效；“轮换密钥”会在保留当前有效证书的同时签发新接入码，便于无中断替换证书。
        </p>
        {activeEnrollmentCode ? (
          <div className="flex flex-col gap-2 rounded-lg border bg-muted/40 p-3">
            <label htmlFor="gateway-enrollment-code" className="text-xs font-medium">一次性接入码</label>
            <Input
              id="gateway-enrollment-code"
              readOnly
              value={activeEnrollmentCode.enrollmentCode}
              autoComplete="off"
              className="font-mono text-xs"
              onFocus={(event) => event.currentTarget.select()}
            />
            <p className="text-xs text-muted-foreground">有效至 <span className="font-mono tabular-nums">{time(activeEnrollmentCode.expiresAt)}</span>。请安全交给现场人员。</p>
          </div>
        ) : null}
        {code.isError || rotate.isError ? (
          <Alert variant="destructive">
            <AlertDescription>接入码没有生成，请确认权限和网关状态后重试。</AlertDescription>
          </Alert>
        ) : null}
        {stopped ? (
          <div>
            <Button size="sm" disabled={!canWrite || working} onClick={() => rotate.mutate(true)}>
              <RotateCw data-icon="inline-start" />
              {rotate.isPending ? '正在重新启用…' : '重新启用并生成接入码'}
            </Button>
          </div>
        ) : (
          <div className="flex flex-wrap gap-2">
            <Button size="sm" disabled={!canWrite || working || !status.data} onClick={() => code.mutate()}>
              <KeyRound data-icon="inline-start" />
              {code.isPending ? '正在生成…' : '生成接入码'}
            </Button>
            <Button variant="outline" size="sm" disabled={!canWrite || working || !status.data} onClick={() => rotate.mutate(false)}>
              <RotateCw data-icon="inline-start" />
              {rotate.isPending ? '正在轮换…' : '轮换密钥'}
            </Button>
          </div>
        )}
      </section>

      <GatewayConfigShadowSection gateway={gateway} csrfToken={csrfToken} canWrite={canWrite} stopped={stopped} />

      <GatewayPacketCaptureSection gatewayId={gateway.id} csrfToken={csrfToken} canWrite={canWrite} />

      <GatewayQuarantineSection gatewayId={gateway.id} />

      <GatewayHostMetricsSection gatewayId={gateway.id} />

      <GatewayLogPullSection gatewayId={gateway.id} csrfToken={csrfToken} canWrite={canWrite} />

      <section aria-label="危险操作" className="flex flex-col gap-3 px-6 py-4">
        <h3 className="text-sm font-semibold">危险操作</h3>
        <div className="flex items-center gap-4 rounded-lg border border-destructive/30 px-4 py-3">
          <span className="flex min-w-0 flex-1 flex-col">
            <span className="text-sm font-semibold">停用这个网关</span>
            <span className="text-xs text-muted-foreground">
              停用后立即作废全部有效证书与未使用的接入码，并阻断数据上报与指令下发；后续可由管理员通过“重新启用并生成接入码”恢复
            </span>
          </span>
          <Button
            variant="outline"
            size="sm"
            className="shrink-0 text-destructive-text"
            disabled={!canWrite || working || !status.data || stopped}
            onClick={() => {
              stop.reset();
              setConfirmingStop(true);
            }}
          >
            停用网关…
          </Button>
        </div>
      </section>

      <StopGatewayDialog
        open={confirmingStop}
        gatewayName={gateway.displayName}
        pending={stop.isPending}
        working={working}
        error={stop.isError}
        onOpenChange={setConfirmingStop}
        onConfirm={() => stop.mutate()}
      />

      <RevokeCertificateDialog
        certificate={certToRevoke}
        reason={revokeReason}
        pending={revokeCert.isPending}
        working={working}
        error={revokeCert.isError}
        onReasonChange={setRevokeReason}
        onClose={() => {
          revokeCert.reset();
          setRevokeReason('');
          setCertToRevoke(null);
        }}
        onConfirm={() => {
          if (certToRevoke) {
            revokeCert.mutate({ credentialId: certToRevoke.id, reason: revokeReason });
          }
        }}
      />
    </>
  );
}
