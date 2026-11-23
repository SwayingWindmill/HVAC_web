import { useEffect, useId, useMemo, useState } from 'react';
import { RotateCcw, Save } from 'lucide-react';
import { Panel, PanelCount, PanelHeader } from '@/blocks/panel';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
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
import { Textarea } from '@/components/ui/textarea';
import type {
  ConcentratorParams,
  ConcentratorParamsPatch,
} from '@/api/generated/platformGateway.gen';
import {
  CONCENTRATOR_PARAM_SPECS,
  buildConcentratorParamsDiff,
  validateConcentratorParamsDraft,
  type ConcentratorParamFieldSpec,
} from '../concentrator-config-model';
import { describeConcentratorError } from '../concentrator-model';

export interface ConcentratorParamsSectionProps {
  readonly params: ConcentratorParams | null;
  readonly loading: boolean;
  readonly canWrite: boolean;
  readonly submitting: boolean;
  readonly submitError: unknown;
  readonly onResetError: () => void;
  readonly onSubmitParams: (
    patch: ConcentratorParamsPatch,
    reason: string,
    stageIfOffline: boolean,
  ) => Promise<void>;
}

function updateParamDraftValue(
  prev: ConcentratorParams,
  key: keyof ConcentratorParams,
  nextValue: number,
): ConcentratorParams {
  switch (key) {
    case 'modbusId':
      return { ...prev, modbusId: nextValue };
    case 'baudRateCode':
      return { ...prev, baudRateCode: nextValue };
    case 'offLineTime':
      return { ...prev, offLineTime: nextValue };
    case 'dataUplinkCycle':
      return { ...prev, dataUplinkCycle: nextValue };
    case 'localNet':
      return { ...prev, localNet: nextValue };
    case 'timeMark':
      return { ...prev, timeMark: nextValue };
    case 'rfTxPower':
      return { ...prev, rfTxPower: nextValue };
    case 'rfFrequency':
      return { ...prev, rfFrequency: nextValue };
    case 'rfBandWidth':
      return { ...prev, rfBandWidth: nextValue };
    case 'rfSf':
      return { ...prev, rfSf: nextValue };
    case 'rfLeadCode':
      return { ...prev, rfLeadCode: nextValue };
    case 'txTimeout':
      return { ...prev, txTimeout: nextValue };
    case 'rxTimeout':
      return { ...prev, rxTimeout: nextValue };
    case 'rssiOut':
      return { ...prev, rssiOut: nextValue };
  }
}

export function ConcentratorParamsSection({
  params,
  loading,
  canWrite,
  submitting,
  submitError,
  onResetError,
  onSubmitParams,
}: ConcentratorParamsSectionProps) {
  const fieldPrefix = useId();
  const reasonId = useId();
  const stageIfOfflineId = useId();
  const [draft, setDraft] = useState<ConcentratorParams | null>(params);
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [reason, setReason] = useState('');
  const [stageIfOffline, setStageIfOffline] = useState(false);

  useEffect(() => {
    setDraft(params);
  }, [params]);

  const validation = useMemo(
    () => (draft ? validateConcentratorParamsDraft(draft) : null),
    [draft],
  );
  const diff = useMemo(
    () => (params && draft ? buildConcentratorParamsDiff(params, draft) : null),
    [params, draft],
  );

  const serialSpecs = useMemo(
    () => CONCENTRATOR_PARAM_SPECS.filter((item) => item.group === 'serial'),
    [],
  );
  const rfSpecs = useMemo(
    () => CONCENTRATOR_PARAM_SPECS.filter((item) => item.group === 'rf'),
    [],
  );

  const canOpenConfirm =
    canWrite &&
    Boolean(diff?.hasChanges) &&
    Boolean(validation?.valid) &&
    !submitting;

  const renderField = (spec: ConcentratorParamFieldSpec) => {
    if (!draft) return null;
    const inputId = `${fieldPrefix}-${spec.key}`;
    const value = draft[spec.key];
    const errorText = validation?.errors[spec.key];

    return (
      <div key={spec.key} className="flex flex-col gap-1.5">
        <div className="flex items-center justify-between gap-2">
          <Label htmlFor={inputId} className="text-xs font-medium text-foreground">
            {spec.label}
            {spec.unit ? (
              <span className="ml-1 font-normal text-muted-foreground">({spec.unit})</span>
            ) : null}
          </Label>
          <span className="font-mono text-[11px] text-muted-foreground tabular-nums">
            {spec.min}–{spec.max}
          </span>
        </div>

        {spec.options ? (
          <Select
            value={String(value)}
            disabled={!canWrite || submitting}
            onValueChange={(next) => {
              const parsed = Number(next);
              if (!Number.isInteger(parsed)) return;
              setDraft((current) =>
                current ? updateParamDraftValue(current, spec.key, parsed) : current,
              );
            }}
          >
            <SelectTrigger id={inputId} className="w-full font-mono text-sm">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {spec.options.map((opt) => (
                <SelectItem key={opt.value} value={String(opt.value)} className="font-mono text-sm">
                  {opt.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        ) : (
          <Input
            id={inputId}
            type="number"
            min={spec.min}
            max={spec.max}
            disabled={!canWrite || submitting}
            value={Number.isNaN(value) ? '' : String(value)}
            aria-invalid={Boolean(errorText)}
            onChange={(event) => {
              const raw = event.currentTarget.value.trim();
              const parsed = raw === '' ? Number.NaN : Number(raw);
              setDraft((current) =>
                current ? updateParamDraftValue(current, spec.key, parsed) : current,
              );
            }}
            className="font-mono text-sm tabular-nums"
          />
        )}

        {errorText ? (
          <p className="text-xs text-destructive-text">{errorText}</p>
        ) : (
          <p className="text-xs text-muted-foreground">{spec.description}</p>
        )}
      </div>
    );
  };

  const handleConfirmSubmit = async () => {
    const trimmedReason = reason.trim();
    if (!diff?.patch || !validation?.valid || !trimmedReason || submitting) return;
    try {
      await onSubmitParams(diff.patch, trimmedReason, stageIfOffline);
      setConfirmOpen(false);
      setReason('');
      setStageIfOffline(false);
    } catch {
      // Mutation error stays displayed inside the dialog via submitError without leaking an unhandled rejection
    }
  };

  return (
    <Panel aria-label="串口与射频参数">
      <PanelHeader
        title="串口与射频参数"
        caption="共 14 项基础通信寄存器，确认变更后经网关写入 Flash 持久化保存（0x9C41 = 6）"
        badge={
          diff?.hasChanges ? <PanelCount count={diff.changes.length} tone="urgent" /> : undefined
        }
        action={
          <div className="flex items-center gap-2">
            <Button
              variant="outline"
              size="sm"
              disabled={!diff?.hasChanges || submitting}
              onClick={() => setDraft(params)}
            >
              <RotateCcw data-icon="inline-start" />
              还原当前值
            </Button>
            <Button
              size="sm"
              disabled={!canOpenConfirm}
              onClick={() => {
                onResetError();
                setStageIfOffline(false);
                setConfirmOpen(true);
              }}
            >
              <Save data-icon="inline-start" />
              预览参数变更
            </Button>
          </div>
        }
      />

      {loading || !draft ? (
        <div className="grid grid-cols-1 gap-4 p-5 md:grid-cols-2 lg:grid-cols-3">
          <Skeleton className="h-16" />
          <Skeleton className="h-16" />
          <Skeleton className="h-16" />
          <Skeleton className="h-16" />
          <Skeleton className="h-16" />
          <Skeleton className="h-16" />
        </div>
      ) : (
        <div className="flex flex-col gap-5 p-5">
          {validation?.protectedModbusId ? (
            <Alert variant="destructive">
              <AlertDescription>{validation.errors.modbusId}</AlertDescription>
            </Alert>
          ) : null}

          <div className="space-y-3">
            <h3 className="text-sm font-semibold text-foreground">RS485 串口与上报设置</h3>
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
              {serialSpecs.map(renderField)}
            </div>
          </div>

          <div className="border-t border-divider pt-5 space-y-3">
            <h3 className="text-sm font-semibold text-foreground">LoRa 无线射频参数</h3>
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
              {rfSpecs.map(renderField)}
            </div>
          </div>
        </div>
      )}

      <Dialog
        open={confirmOpen}
        onOpenChange={(open) => {
          if (!submitting) setConfirmOpen(open);
        }}
      >
        <DialogContent className="sm:max-w-xl">
          <DialogHeader>
            <DialogTitle>确认下发串口与射频参数到集中器 Flash？</DialogTitle>
            <DialogDescription>
              系统将下发以下 {diff?.changes.length ?? 0} 项参数并通过指令寄存器 0x9C41 = 6 写入 Flash
              永久保存。如修改从站地址或串口波特率，请确认现场网关轮询配置同步调整。
            </DialogDescription>
          </DialogHeader>

          {diff && diff.changes.length > 0 ? (
            <div className="max-h-60 overflow-y-auto rounded-md border">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>参数名称</TableHead>
                    <TableHead>当前值</TableHead>
                    <TableHead>目标值</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {diff.changes.map((item) => (
                    <TableRow key={item.key}>
                      <TableCell className="font-medium">{item.label}</TableCell>
                      <TableCell className="font-mono text-xs text-muted-foreground tabular-nums">
                        {item.beforeDisplay}
                      </TableCell>
                      <TableCell className="font-mono text-xs font-semibold text-foreground tabular-nums">
                        {item.afterDisplay}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          ) : null}

          <div className="flex flex-col gap-2">
            <Label htmlFor={reasonId} className="text-xs font-medium">
              变更原因（必填）
            </Label>
            <Textarea
              id={reasonId}
              value={reason}
              disabled={submitting}
              maxLength={512}
              onChange={(event) => setReason(event.currentTarget.value)}
              placeholder="请填写本次调整参数的原因（如：提升 LoRa 发射功率至 22 dBm 并缩短离线判定）"
            />
          </div>

          <div className="flex items-start gap-2.5 rounded-md border bg-muted/30 px-3 py-2.5">
            <Checkbox
              id={stageIfOfflineId}
              checked={stageIfOffline}
              disabled={submitting}
              onCheckedChange={(checked) => setStageIfOffline(checked === true)}
              className="mt-0.5"
            />
            <Label htmlFor={stageIfOfflineId} className="flex flex-col items-start gap-0.5 text-xs font-normal leading-relaxed">
              <span className="font-medium text-foreground">若网关离线，暂存至配置影子（重连后自动同步）</span>
              <span className="text-muted-foreground">
                勾选后当网关通信通道不可达时不会直接报错，而是写入网关配置影子并在重连后下发到集中器 Flash。
              </span>
            </Label>
          </div>

          {submitError ? (
            <Alert variant="destructive">
              <AlertDescription>
                {describeConcentratorError(
                  submitError,
                  '串口与射频参数写入集中器 Flash 失败，请核对网关连接与参数范围后重试',
                )}
              </AlertDescription>
            </Alert>
          ) : null}

          <DialogFooter>
            <Button
              variant="outline"
              disabled={submitting}
              onClick={() => setConfirmOpen(false)}
            >
              取消
            </Button>
            <Button
              disabled={!canWrite || submitting || reason.trim().length === 0 || !diff?.hasChanges}
              onClick={() => void handleConfirmSubmit()}
            >
              {submitting ? '正在提交…' : stageIfOffline ? '确认写入或离线暂存' : '确认写入 Flash'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </Panel>
  );
}
