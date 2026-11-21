import { useEffect, useId, useMemo, useState } from 'react';
import type {
  Device,
  ReplaceDeviceHardwareRequest,
  ReplaceDeviceHardwareResult,
} from '@/api/generated/platformGateway.gen';
import { presentRegistryError, registryAdminApi } from '@/api/registry';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Textarea } from '@/components/ui/textarea';
import { confirmDiscardRegistryDraft } from './confirmDiscard';
import { makeRegistryMutationMeta, newRegistryIdempotencyKey } from './model';

const KEEP_GATEWAY = '__keep__';
const SOURCE_KEY_PATTERN = /^[A-Za-z][A-Za-z0-9_.:-]{0,127}$/;
const DEFAULT_REPLACE_REASON = '现场设备硬件故障更换';

interface ReplaceHardwareDialogProps {
  open: boolean;
  device: Device | null;
  gatewayDevices: readonly Device[];
  csrfToken: string;
  onOpenChange: (open: boolean) => void;
  onDirtyChange: (dirty: boolean) => void;
  onReplaced: (result: ReplaceDeviceHardwareResult, targetDevice: Device) => Promise<void>;
}

/**
 * Modal dialog for swapping physical field hardware on an existing logical device
 * without changing device, asset, or point IDs.
 */
export function ReplaceHardwareDialog({
  open,
  device,
  gatewayDevices,
  csrfToken,
  onOpenChange,
  onDirtyChange,
  onReplaced,
}: ReplaceHardwareDialogProps) {
  const externalIdInputId = useId();
  const gatewayTriggerId = useId();
  const sourceKeyInputId = useId();
  const protocolNoteInputId = useId();
  const reasonInputId = useId();

  const [newExternalId, setNewExternalId] = useState('');
  const [selectedGatewayId, setSelectedGatewayId] = useState(KEEP_GATEWAY);
  const [newSourceKey, setNewSourceKey] = useState('');
  const [protocolNote, setProtocolNote] = useState('');
  const [reason, setReason] = useState(DEFAULT_REPLACE_REASON);
  const [attemptKey, setAttemptKey] = useState(() => newRegistryIdempotencyKey('replace-hardware'));
  const [dirty, setDirty] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState<unknown>(null);

  useEffect(() => {
    if (!open || !device) {
      setDirty(false);
      onDirtyChange(false);
      return;
    }
    setNewExternalId('');
    setSelectedGatewayId(KEEP_GATEWAY);
    setNewSourceKey('');
    setProtocolNote('');
    setReason(DEFAULT_REPLACE_REASON);
    setAttemptKey(newRegistryIdempotencyKey('replace-hardware'));
    setSubmitError(null);
    setDirty(false);
    onDirtyChange(false);
  }, [open, device, onDirtyChange]);

  const isGatewayDevice = device?.deviceType === 'GATEWAY';

  const sourceKeyError = useMemo(() => {
    const trimmed = newSourceKey.trim();
    if (!trimmed) return null;
    if (!SOURCE_KEY_PATTERN.test(trimmed)) {
      return '采集源标识必须以字母开头，仅包含字母、数字、下划线、点、冒号或短横线。';
    }
    return null;
  }, [newSourceKey]);

  const presentedError = useMemo(() => {
    if (!submitError) return null;
    return presentRegistryError(submitError);
  }, [submitError]);

  const canSubmit = Boolean(
    device
    && newExternalId.trim()
    && reason.trim()
    && !sourceKeyError
    && !submitting,
  );

  const markEdited = () => {
    if (!dirty) {
      setDirty(true);
      onDirtyChange(true);
    }
    setSubmitError(null);
    setAttemptKey(newRegistryIdempotencyKey('replace-hardware'));
  };

  const handleOpenChange = (nextOpen: boolean) => {
    if (submitting) return;
    if (!nextOpen && !confirmDiscardRegistryDraft(dirty)) return;
    if (!nextOpen) {
      setDirty(false);
      onDirtyChange(false);
    }
    onOpenChange(nextOpen);
  };

  const handleConfirm = async () => {
    if (!device || !canSubmit) return;
    setSubmitting(true);
    setSubmitError(null);
    try {
      const payload: ReplaceDeviceHardwareRequest = {
        newExternalId: newExternalId.trim(),
        meta: makeRegistryMutationMeta(device.revision, reason, attemptKey),
      };
      if (!isGatewayDevice && selectedGatewayId !== KEEP_GATEWAY) {
        payload.newGatewayDeviceId = selectedGatewayId;
      }
      const trimmedSourceKey = newSourceKey.trim();
      if (!isGatewayDevice && trimmedSourceKey) {
        payload.newSourceKey = trimmedSourceKey;
      }
      const trimmedNote = protocolNote.trim();
      if (trimmedNote) {
        payload.newProtocolAddress = { endpoint: trimmedNote };
      }

      const result = await registryAdminApi.replaceDeviceHardware(
        device.siteId,
        device.id,
        payload,
        csrfToken,
      );
      setDirty(false);
      onDirtyChange(false);
      await onReplaced(result, device);
    } catch (caught) {
      setSubmitError(caught);
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Dialog open={open && Boolean(device)} onOpenChange={handleOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>更换这台设备的现场硬件？</DialogTitle>
          <DialogDescription>
            替换故障网关、电表或控制器后，系统会保留原有设备与全部点位，历史用电记录、运行趋势和告警规则会继续接续在这台设备名下，不会拆分成两套记录。
          </DialogDescription>
        </DialogHeader>

        {device ? (
          <div className="grid gap-4">
            <div className="rounded-md border bg-muted/40 p-3 text-sm">
              <div className="flex flex-wrap items-baseline justify-between gap-2">
                <strong className="font-medium text-foreground">{device.displayName}</strong>
                <span className="font-mono text-xs text-muted-foreground">{device.code}</span>
              </div>
              <p className="mt-1 text-xs text-muted-foreground">
                当前硬件标识：{device.externalId ?? '尚未登记'}
              </p>
            </div>

            {presentedError ? (
              <Alert variant="destructive">
                <AlertTitle>{presentedError.title}</AlertTitle>
                <AlertDescription>{presentedError.description}</AlertDescription>
              </Alert>
            ) : null}

            <div className="grid gap-1.5 text-sm">
              <label htmlFor={externalIdInputId} className="font-medium text-foreground">
                新硬件序列号 / 外部标识
              </label>
              <Input
                id={externalIdInputId}
                value={newExternalId}
                placeholder="例如：SN-20261118-A02 或现场新表号"
                disabled={submitting}
                onChange={(event) => {
                  setNewExternalId(event.target.value);
                  markEdited();
                }}
              />
            </div>

            {!isGatewayDevice && gatewayDevices.length > 0 ? (
              <div className="grid gap-1.5 text-sm">
                <label htmlFor={gatewayTriggerId} className="font-medium text-foreground">
                  上级接入网关（可选）
                </label>
                <Select
                  value={selectedGatewayId}
                  disabled={submitting}
                  onValueChange={(value) => {
                    setSelectedGatewayId(value);
                    markEdited();
                  }}
                >
                  <SelectTrigger id={gatewayTriggerId} aria-label="上级接入网关">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectGroup>
                      <SelectItem value={KEEP_GATEWAY}>保持当前接入网关</SelectItem>
                      {gatewayDevices.map((gw) => (
                        <SelectItem key={gw.id} value={gw.id}>
                          {gw.displayName} ({gw.code})
                        </SelectItem>
                      ))}
                    </SelectGroup>
                  </SelectContent>
                </Select>
              </div>
            ) : null}

            {!isGatewayDevice ? (
              <div className="grid gap-1.5 text-sm">
                <label htmlFor={sourceKeyInputId} className="font-medium text-foreground">
                  新采集源标识（可选）
                </label>
                <Input
                  id={sourceKeyInputId}
                  value={newSourceKey}
                  placeholder="仅在网关上行键名称变更时填写，留空则沿用原键"
                  disabled={submitting}
                  aria-invalid={Boolean(sourceKeyError)}
                  onChange={(event) => {
                    setNewSourceKey(event.target.value);
                    markEdited();
                  }}
                />
                {sourceKeyError ? (
                  <p className="text-xs text-destructive">{sourceKeyError}</p>
                ) : null}
              </div>
            ) : null}

            <div className="grid gap-1.5 text-sm">
              <label htmlFor={protocolNoteInputId} className="font-medium text-foreground">
                通信地址或端口备注（可选）
              </label>
              <Input
                id={protocolNoteInputId}
                value={protocolNote}
                placeholder="例如：Modbus 从站 04 / RS485-2，留空则沿用原参数"
                disabled={submitting}
                onChange={(event) => {
                  setProtocolNote(event.target.value);
                  markEdited();
                }}
              />
            </div>

            <div className="grid gap-1.5 text-sm">
              <label htmlFor={reasonInputId} className="font-medium text-foreground">
                更换原因
              </label>
              <Textarea
                id={reasonInputId}
                rows={2}
                value={reason}
                disabled={submitting}
                onChange={(event) => {
                  setReason(event.target.value);
                  markEdited();
                }}
              />
            </div>

            <Alert>
              <AlertTitle>并发保护已启用</AlertTitle>
              <AlertDescription>
                保存时会校验当前设备版本并保留操作留痕；若其他管理员刚修改过此设备，请刷新后再试。
              </AlertDescription>
            </Alert>
          </div>
        ) : null}

        <DialogFooter>
          <Button
            type="button"
            variant="outline"
            disabled={submitting}
            onClick={() => handleOpenChange(false)}
          >
            取消
          </Button>
          <Button
            type="button"
            disabled={!canSubmit}
            onClick={() => void handleConfirm()}
          >
            {submitting ? '正在更换…' : '确认更换硬件'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
