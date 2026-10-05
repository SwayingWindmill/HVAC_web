import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import type { Device } from '@/api/generated/platformGateway.gen';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle } from '@/components/ui/alert-dialog';
import { StatusBadge } from '@/components/status-badge';
import { gatewayCredentialApi, gatewayCredentialKey } from './gateway-credentials';

const labels = { NOT_ENROLLED: '未接入', ACTIVE: '有效', EXPIRING: '即将到期', EXPIRED: '已到期', REVOKED: '已吊销' } as const;
export function GatewayCredentialPanel({ gateway, csrfToken, canWrite, onClose }: {
  gateway: Device; csrfToken: string; canWrite: boolean; onClose: () => void;
}) {
  const queryClient = useQueryClient();
  const [confirmingRevoke, setConfirmingRevoke] = useState(false);
  const status = useQuery({ queryKey: gatewayCredentialKey(gateway.id), queryFn: ({ signal }) => gatewayCredentialApi.status(gateway.id, signal) });
  const code = useMutation({ mutationFn: () => gatewayCredentialApi.generateCode(gateway.id, csrfToken), gcTime: 0 });
  const revoke = useMutation({
    mutationFn: () => gatewayCredentialApi.revoke(gateway.id, csrfToken),
    onSuccess: (result) => { code.reset(); setConfirmingRevoke(false); queryClient.setQueryData(gatewayCredentialKey(gateway.id), result); },
  });
  const close = () => { code.reset(); onClose(); };
  const working = code.isPending || revoke.isPending;
  const revoked = status.data?.status === 'REVOKED';
  return <Dialog open onOpenChange={(open) => { if (!open) close(); }}>
    <DialogContent className="sm:max-w-lg">
      <DialogHeader><DialogTitle>{gateway.displayName} · 接入凭据</DialogTitle><DialogDescription>管理网关接入身份。吊销会停止接收数据并禁止向该网关下发命令。</DialogDescription></DialogHeader>
      {status.isPending ? <p className="text-sm text-muted-foreground">正在读取凭据…</p> : null}
      {status.data ? <dl className="grid grid-cols-[auto_1fr] items-center gap-x-6 gap-y-3 text-sm">
        <dt className="text-muted-foreground">凭据状态</dt><dd><StatusBadge tone={revoked || status.data.status === 'EXPIRED' ? 'destructive' : status.data.status === 'EXPIRING' ? 'warning' : status.data.status === 'ACTIVE' ? 'success' : 'neutral'}>{labels[status.data.status]}</StatusBadge></dd>
        {status.data.expiresAt ? <><dt className="text-muted-foreground">到期时间</dt><dd>{new Date(status.data.expiresAt).toLocaleString('zh-CN')}</dd></> : null}
      </dl> : null}
      {revoked ? <p className="text-sm">该网关身份已永久吊销。如需重新接入，请登记新的网关。</p> : null}
      {status.isError || code.isError || revoke.isError ? <Alert variant="destructive"><AlertDescription>凭据操作未完成。请确认当前权限和服务状态后重试。</AlertDescription></Alert> : null}
      {code.data ? <section className="space-y-2 rounded-md border p-3" aria-label="一次性接入码">
        <label htmlFor="gateway-enrollment-code" className="text-sm font-medium">一次性接入码</label>
        <Input id="gateway-enrollment-code" readOnly value={code.data.enrollmentCode} autoComplete="off" className="font-mono text-xs" onFocus={(event) => event.currentTarget.select()} />
        <p className="text-xs text-muted-foreground">仅本次显示，有效至 {new Date(code.data.expiresAt).toLocaleString('zh-CN')}，使用一次后失效。请安全交给现场网关。</p>
      </section> : null}
      <div className="flex justify-end gap-2">
        <Button variant="ghost" disabled={status.isFetching} onClick={() => void status.refetch()}>刷新状态</Button>
        <Button variant="outline" disabled={!canWrite || working || !status.data || revoked} onClick={() => setConfirmingRevoke(true)}>吊销网关身份</Button>
        <Button disabled={!canWrite || working || !status.data || revoked} onClick={() => code.mutate()}>{code.isPending ? '正在生成…' : '生成接入码'}</Button>
      </div>
      <AlertDialog open={confirmingRevoke} onOpenChange={(open) => { if (!working) setConfirmingRevoke(open); }}>
        <AlertDialogContent><AlertDialogHeader><AlertDialogTitle>永久吊销网关身份</AlertDialogTitle><AlertDialogDescription>将永久吊销 {gateway.displayName} 的全部接入证书和未使用接入码。数据会被隔离，远程命令将被拒绝；该身份不能恢复。</AlertDialogDescription></AlertDialogHeader>
          <AlertDialogFooter><AlertDialogCancel disabled={working}>取消</AlertDialogCancel><AlertDialogAction variant="destructive" disabled={working} onClick={(event) => { event.preventDefault(); revoke.mutate(); }}>{revoke.isPending ? '正在吊销…' : '永久吊销此网关'}</AlertDialogAction></AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </DialogContent>
  </Dialog>;
}
