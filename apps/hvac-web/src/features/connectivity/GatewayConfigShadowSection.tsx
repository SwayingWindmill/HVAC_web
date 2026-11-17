import { useId, useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { AlertTriangle, Plus, RefreshCw, Settings2, Trash2 } from 'lucide-react';
import { toast } from 'sonner';
import type { Device, GatewayConfigShadow } from '@/api/generated/platformGateway.gen';
import { Alert, AlertDescription } from '@/components/ui/alert';
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
import { Label } from '@/components/ui/label';
import { Skeleton } from '@/components/ui/skeleton';
import { Textarea } from '@/components/ui/textarea';
import { cn } from '@/lib/utils';
import {
  CONFIG_SHADOW_APPLIED_LABELS,
  CONFIG_SHADOW_REASON_LABELS,
  CONFIG_SHADOW_SECTION_LABELS,
  CONFIG_SHADOW_SYNC_PRESENTATION,
  describeGatewayConfigShadowError,
} from '@/features/integrations/integrations-model';
import {
  buildValidatedShadowUpdateRequest,
  createShadowEditorDraft,
  formatPointCopyTargets,
  summarizeConcentratorShadowUnits,
  type GatewayConfigShadowEditorDraft,
} from './gateway-config-shadow-model';
import { gatewayConfigShadowApi, gatewayConfigShadowKey } from './gateway-credentials';

const SHADOW_TONE_CLASSES = {
  neutral: { chip: 'bg-secondary text-secondary-foreground', dot: 'bg-subtle-foreground' },
  warning: { chip: 'bg-warning-soft text-warning-text', dot: 'bg-warning' },
  success: { chip: 'bg-success-soft text-success-text', dot: 'bg-success' },
  danger: { chip: 'bg-destructive-soft text-destructive-text', dot: 'bg-destructive' },
} as const;

const SHADOW_DATETIME = new Intl.DateTimeFormat('zh-CN', {
  month: '2-digit',
  day: '2-digit',
  hour: '2-digit',
  minute: '2-digit',
  hour12: false,
});

function formatShadowTimestamp(value: string): string {
  const parsed = Date.parse(value);
  if (Number.isNaN(parsed)) return value;
  return SHADOW_DATETIME.format(parsed);
}

export function GatewayConfigShadowSection({
  gateway,
  csrfToken,
  canWrite,
  stopped,
}: {
  readonly gateway: Device;
  readonly csrfToken: string;
  readonly canWrite: boolean;
  readonly stopped: boolean;
}) {
  const queryClient = useQueryClient();
  const reasonId = useId();
  const [editorOpen, setEditorOpen] = useState(false);
  const [draft, setDraft] = useState<GatewayConfigShadowEditorDraft>(() =>
    createShadowEditorDraft(undefined),
  );
  const [validationError, setValidationError] = useState<string | null>(null);

  const shadowQuery = useQuery({
    queryKey: gatewayConfigShadowKey(gateway.id),
    queryFn: ({ signal }) => gatewayConfigShadowApi.get(gateway.id, signal),
    retry: false,
  });

  const updateMutation = useMutation({
    mutationFn: async (currentDraft: GatewayConfigShadowEditorDraft) => {
      const built = buildValidatedShadowUpdateRequest(currentDraft, shadowQuery.data);
      if (!built.ok) {
        throw new Error(built.error);
      }
      return gatewayConfigShadowApi.update(gateway.id, built.request, csrfToken);
    },
    onSuccess: (updated) => {
      queryClient.setQueryData(gatewayConfigShadowKey(gateway.id), updated);
      setValidationError(null);
      setEditorOpen(false);
      toast.success(
        updated.desired
          ? `配置影子已保存（期望修订 v${updated.desired.desiredRevision}）`
          : '配置影子已更新',
      );
    },
  });

  const shadow = shadowQuery.data;
  const concentratorUnits = useMemo(
    () => summarizeConcentratorShadowUnits(shadow?.desired?.concentratorConfigs),
    [shadow?.desired?.concentratorConfigs],
  );

  const openEditor = () => {
    setDraft(createShadowEditorDraft(shadow));
    setValidationError(null);
    updateMutation.reset();
    setEditorOpen(true);
  };

  const handleSave = async () => {
    const built = buildValidatedShadowUpdateRequest(draft, shadow);
    if (!built.ok) {
      setValidationError(built.error);
      return;
    }
    setValidationError(null);
    try {
      await updateMutation.mutateAsync(draft);
    } catch {
      // Displayed inside the dialog Alert via updateMutation.error
    }
  };

  const syncPresentation = shadow
    ? CONFIG_SHADOW_SYNC_PRESENTATION[shadow.syncStatus]
    : null;
  const toneClass = syncPresentation ? SHADOW_TONE_CLASSES[syncPresentation.tone] : null;
  const editDisabled = !canWrite || stopped || shadowQuery.isPending;

  return (
    <section aria-label="配置影子" className="flex flex-col gap-3 border-b border-divider px-6 py-4">
      <div className="flex items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <h3 className="text-sm font-semibold">配置影子</h3>
          {syncPresentation && toneClass ? (
            <span
              className={cn(
                'inline-flex h-5.5 shrink-0 items-center gap-1.5 rounded-md px-1.5 text-xs font-medium whitespace-nowrap',
                toneClass.chip,
              )}
            >
              <span className={cn('size-1.5 rounded-full', toneClass.dot)} aria-hidden="true" />
              {syncPresentation.label}
            </span>
          ) : null}
        </div>
        <div className="flex items-center gap-1.5">
          <Button
            variant="outline"
            size="sm"
            disabled={shadowQuery.isFetching}
            onClick={() => void shadowQuery.refetch()}
          >
            <RefreshCw data-icon="inline-start" />
            刷新影子
          </Button>
          <Button
            variant="outline"
            size="sm"
            disabled={editDisabled}
            onClick={openEditor}
          >
            <Settings2 data-icon="inline-start" />
            {shadow?.syncStatus === 'UNCONFIGURED' ? '初始化配置影子' : '编辑配置影子'}
          </Button>
        </div>
      </div>

      {shadowQuery.isPending ? (
        <Skeleton className="h-24 rounded-lg" />
      ) : shadowQuery.isError ? (
        <Alert variant="destructive">
          <AlertDescription className="flex items-center justify-between gap-3">
            <span>{describeGatewayConfigShadowError(shadowQuery.error)}</span>
            <Button variant="outline" size="sm" onClick={() => void shadowQuery.refetch()}>
              重试
            </Button>
          </AlertDescription>
        </Alert>
      ) : shadow ? (
        <ShadowSummaryCard
          shadow={shadow}
          concentratorUnits={concentratorUnits}
          stopped={stopped}
        />
      ) : null}

      <Dialog
        open={editorOpen}
        onOpenChange={(nextOpen) => {
          if (!updateMutation.isPending) setEditorOpen(nextOpen);
        }}
      >
        <DialogContent className="max-h-[85vh] overflow-y-auto sm:max-w-[640px]">
          <DialogHeader>
            <DialogTitle>编辑网关配置影子</DialogTitle>
            <DialogDescription>
              修改点位映射与采集周期后保存为期望配置，边缘网关在线或重连后自动拉取并回传生效状态。
            </DialogDescription>
          </DialogHeader>

          <div className="flex flex-col gap-5 py-2 text-sm">
            <section aria-label="点位映射设置" className="flex flex-col gap-2.5">
              <div className="flex items-center justify-between">
                <h4 className="text-xs font-semibold text-foreground">
                  点位映射（{draft.pointMappings.length} 条）
                </h4>
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  disabled={updateMutation.isPending}
                  onClick={() =>
                    setDraft((current) => ({
                      ...current,
                      pointMappings: [
                        ...current.pointMappings,
                        {
                          sourceChannel: '',
                          primaryDeviceKey: '',
                          primaryPointCode: '',
                          copyTargetsText: '',
                        },
                      ],
                    }))
                  }
                >
                  <Plus data-icon="inline-start" />
                  新增点位映射
                </Button>
              </div>
              {draft.pointMappings.length === 0 ? (
                <p className="rounded-md border border-dashed px-3 py-2 text-xs text-muted-foreground">
                  暂无点位映射条目；点击「新增点位映射」添加边缘通道到标准设备点位的映射。
                </p>
              ) : (
                <div className="flex flex-col gap-2.5">
                  {draft.pointMappings.map((row, index) => (
                    <div
                      key={index}
                      className="flex flex-col gap-2 rounded-lg border bg-muted/30 p-3"
                    >
                      <div className="flex items-center justify-between">
                        <span className="text-xs font-medium text-muted-foreground">
                          映射 #{index + 1}
                        </span>
                        <Button
                          type="button"
                          variant="ghost"
                          size="icon-xs"
                          aria-label={`删除点位映射 ${index + 1}`}
                          disabled={updateMutation.isPending}
                          onClick={() =>
                            setDraft((current) => ({
                              ...current,
                              pointMappings: current.pointMappings.filter((_, i) => i !== index),
                            }))
                          }
                        >
                          <Trash2 className="size-3.5" />
                        </Button>
                      </div>
                      <div className="grid grid-cols-1 gap-2 sm:grid-cols-3">
                        <label className="grid gap-1 text-xs">
                          <span className="text-muted-foreground">源通道</span>
                          <Input
                            value={row.sourceChannel}
                            placeholder="rs485-u1:reg1"
                            disabled={updateMutation.isPending}
                            className="h-8 font-mono text-xs"
                            onChange={(event) => {
                              const value = event.currentTarget.value;
                              setDraft((current) => ({
                                ...current,
                                pointMappings: current.pointMappings.map((item, i) =>
                                  i === index ? { ...item, sourceChannel: value } : item,
                                ),
                              }));
                            }}
                          />
                        </label>
                        <label className="grid gap-1 text-xs">
                          <span className="text-muted-foreground">主设备标识</span>
                          <Input
                            value={row.primaryDeviceKey}
                            placeholder="chiller-01"
                            disabled={updateMutation.isPending}
                            className="h-8 font-mono text-xs"
                            onChange={(event) => {
                              const value = event.currentTarget.value;
                              setDraft((current) => ({
                                ...current,
                                pointMappings: current.pointMappings.map((item, i) =>
                                  i === index ? { ...item, primaryDeviceKey: value } : item,
                                ),
                              }));
                            }}
                          />
                        </label>
                        <label className="grid gap-1 text-xs">
                          <span className="text-muted-foreground">主点位编码</span>
                          <Input
                            value={row.primaryPointCode}
                            placeholder="supply_temp"
                            disabled={updateMutation.isPending}
                            className="h-8 font-mono text-xs"
                            onChange={(event) => {
                              const value = event.currentTarget.value;
                              setDraft((current) => ({
                                ...current,
                                pointMappings: current.pointMappings.map((item, i) =>
                                  i === index ? { ...item, primaryPointCode: value } : item,
                                ),
                              }));
                            }}
                          />
                        </label>
                      </div>
                      <label className="grid gap-1 text-xs">
                        <span className="text-muted-foreground">
                          复用目标（可选，逗号分隔，格式 deviceKey:pointCode）
                        </span>
                        <Input
                          value={row.copyTargetsText}
                          placeholder="chiller-02:supply_temp"
                          disabled={updateMutation.isPending}
                          className="h-8 font-mono text-xs"
                          onChange={(event) => {
                            const value = event.currentTarget.value;
                            setDraft((current) => ({
                              ...current,
                              pointMappings: current.pointMappings.map((item, i) =>
                                i === index ? { ...item, copyTargetsText: value } : item,
                              ),
                            }));
                          }}
                        />
                      </label>
                    </div>
                  ))}
                </div>
              )}
            </section>

            <section aria-label="采集周期设置" className="flex flex-col gap-2.5">
              <div className="flex items-center justify-between">
                <h4 className="text-xs font-semibold text-foreground">
                  采集周期（{draft.samplingPolicies.length} 条）
                </h4>
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  disabled={updateMutation.isPending}
                  onClick={() =>
                    setDraft((current) => ({
                      ...current,
                      samplingPolicies: [
                        ...current.samplingPolicies,
                        {
                          deviceKey: '',
                          pointCode: '',
                          sampleIntervalMs: '5000',
                          publishIntervalMs: '5000',
                        },
                      ],
                    }))
                  }
                >
                  <Plus data-icon="inline-start" />
                  新增采集周期
                </Button>
              </div>
              {draft.samplingPolicies.length === 0 ? (
                <p className="rounded-md border border-dashed px-3 py-2 text-xs text-muted-foreground">
                  暂无采集周期规则；点击「新增采集周期」设置设备或点位的采样与上报频率。
                </p>
              ) : (
                <div className="flex flex-col gap-2.5">
                  {draft.samplingPolicies.map((row, index) => (
                    <div
                      key={index}
                      className="flex flex-col gap-2 rounded-lg border bg-muted/30 p-3"
                    >
                      <div className="flex items-center justify-between">
                        <span className="text-xs font-medium text-muted-foreground">
                          采集规则 #{index + 1}
                        </span>
                        <Button
                          type="button"
                          variant="ghost"
                          size="icon-xs"
                          aria-label={`删除采集周期 ${index + 1}`}
                          disabled={updateMutation.isPending}
                          onClick={() =>
                            setDraft((current) => ({
                              ...current,
                              samplingPolicies: current.samplingPolicies.filter((_, i) => i !== index),
                            }))
                          }
                        >
                          <Trash2 className="size-3.5" />
                        </Button>
                      </div>
                      <div className="grid grid-cols-1 gap-2 sm:grid-cols-4">
                        <label className="grid gap-1 text-xs">
                          <span className="text-muted-foreground">设备标识</span>
                          <Input
                            value={row.deviceKey}
                            placeholder="chiller-01"
                            disabled={updateMutation.isPending}
                            className="h-8 font-mono text-xs"
                            onChange={(event) => {
                              const value = event.currentTarget.value;
                              setDraft((current) => ({
                                ...current,
                                samplingPolicies: current.samplingPolicies.map((item, i) =>
                                  i === index ? { ...item, deviceKey: value } : item,
                                ),
                              }));
                            }}
                          />
                        </label>
                        <label className="grid gap-1 text-xs">
                          <span className="text-muted-foreground">点位编码（可选）</span>
                          <Input
                            value={row.pointCode}
                            placeholder="全部点位"
                            disabled={updateMutation.isPending}
                            className="h-8 font-mono text-xs"
                            onChange={(event) => {
                              const value = event.currentTarget.value;
                              setDraft((current) => ({
                                ...current,
                                samplingPolicies: current.samplingPolicies.map((item, i) =>
                                  i === index ? { ...item, pointCode: value } : item,
                                ),
                              }));
                            }}
                          />
                        </label>
                        <label className="grid gap-1 text-xs">
                          <span className="text-muted-foreground">采样间隔 (ms)</span>
                          <Input
                            type="number"
                            min={100}
                            max={86400000}
                            value={row.sampleIntervalMs}
                            disabled={updateMutation.isPending}
                            className="h-8 font-mono text-xs tabular-nums"
                            onChange={(event) => {
                              const value = event.currentTarget.value;
                              setDraft((current) => ({
                                ...current,
                                samplingPolicies: current.samplingPolicies.map((item, i) =>
                                  i === index ? { ...item, sampleIntervalMs: value } : item,
                                ),
                              }));
                            }}
                          />
                        </label>
                        <label className="grid gap-1 text-xs">
                          <span className="text-muted-foreground">上报间隔 (ms)</span>
                          <Input
                            type="number"
                            min={100}
                            max={86400000}
                            value={row.publishIntervalMs}
                            disabled={updateMutation.isPending}
                            className="h-8 font-mono text-xs tabular-nums"
                            onChange={(event) => {
                              const value = event.currentTarget.value;
                              setDraft((current) => ({
                                ...current,
                                samplingPolicies: current.samplingPolicies.map((item, i) =>
                                  i === index ? { ...item, publishIntervalMs: value } : item,
                                ),
                              }));
                            }}
                          />
                        </label>
                      </div>
                    </div>
                  ))}
                </div>
              )}
            </section>

            {concentratorUnits.length > 0 ? (
              <p className="rounded-md border bg-muted/30 px-3 py-2 text-xs text-muted-foreground">
                本次保存会完整保留已暂存的 {concentratorUnits.length} 个无线集中器单元配置（UID{' '}
                {concentratorUnits.map((unit) => unit.unitId).join('、')}）；集中器参数与 EID 槽位请在「无线测温集中器」页编辑。
              </p>
            ) : null}

            <div className="flex flex-col gap-2">
              <Label htmlFor={reasonId} className="text-xs font-medium">
                变更原因（必填）
              </Label>
              <Textarea
                id={reasonId}
                value={draft.reason}
                disabled={updateMutation.isPending}
                maxLength={1000}
                onChange={(event) => {
                  const value = event.currentTarget.value;
                  setDraft((current) => ({ ...current, reason: value }));
                }}
                placeholder="请说明本次调整点位映射或采集周期的原因（例如：新增 1 号冷机回水温度复用映射并调整采集周期为 5 秒）"
              />
            </div>

            {validationError || updateMutation.error ? (
              <Alert variant="destructive">
                <AlertDescription className="flex flex-col gap-2">
                  <span>
                    {validationError ?? describeGatewayConfigShadowError(updateMutation.error)}
                  </span>
                  {updateMutation.error ? (
                    <Button
                      type="button"
                      variant="outline"
                      size="sm"
                      className="self-start"
                      onClick={() => {
                        void shadowQuery.refetch().then((refetched) => {
                          if (refetched.data) {
                            setDraft((current) => ({
                              ...createShadowEditorDraft(refetched.data),
                              reason: current.reason,
                            }));
                            updateMutation.reset();
                            setValidationError(null);
                          }
                        });
                      }}
                    >
                      加载最新影子修订
                    </Button>
                  ) : null}
                </AlertDescription>
              </Alert>
            ) : null}
          </div>

          <DialogFooter>
            <Button
              variant="outline"
              disabled={updateMutation.isPending}
              onClick={() => setEditorOpen(false)}
            >
              取消
            </Button>
            <Button
              disabled={updateMutation.isPending || !canWrite || stopped}
              onClick={() => void handleSave()}
            >
              {updateMutation.isPending ? '正在保存…' : '保存期望配置'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </section>
  );
}

function ShadowSummaryCard({
  shadow,
  concentratorUnits,
  stopped,
}: {
  readonly shadow: GatewayConfigShadow;
  readonly concentratorUnits: ReturnType<typeof summarizeConcentratorShadowUnits>;
  readonly stopped: boolean;
}) {
  const desired = shadow.desired;
  const reported = shadow.reported;

  if (shadow.syncStatus === 'UNCONFIGURED' && !desired && !reported) {
    return (
      <div className="rounded-lg border border-dashed px-3.5 py-3 text-xs text-muted-foreground">
        该网关尚未配置云边配置影子；可点击「初始化配置影子」下发点位映射与采集周期，或在「无线测温集中器」页面离线暂存参数与 EID。
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-3 rounded-lg border bg-muted/30 p-3.5 text-xs">
      <dl className="grid grid-cols-2 gap-x-4 gap-y-2.5">
        <div>
          <dt className="text-muted-foreground">期望修订</dt>
          <dd className="mt-0.5 font-mono font-semibold text-foreground tabular-nums">
            {desired ? `v${desired.desiredRevision}` : '未设置'}
          </dd>
          {desired ? (
            <dd className="mt-0.5 text-[11px] text-muted-foreground">
              更新于 <span className="font-mono tabular-nums">{formatShadowTimestamp(desired.updatedAt)}</span>
            </dd>
          ) : null}
        </div>
        <div>
          <dt className="text-muted-foreground">边缘已上报修订</dt>
          <dd className="mt-0.5 font-mono font-semibold text-foreground tabular-nums">
            {reported
              ? `v${reported.reportedRevision} · ${CONFIG_SHADOW_APPLIED_LABELS[reported.appliedStatus]}`
              : '等待网关上报'}
          </dd>
          {reported ? (
            <dd className="mt-0.5 text-[11px] text-muted-foreground">
              上报于 <span className="font-mono tabular-nums">{formatShadowTimestamp(reported.reportedAt)}</span>
            </dd>
          ) : null}
        </div>
      </dl>

      <div className="flex flex-wrap gap-2 border-t border-divider pt-2.5 text-[11px]">
        <span className="rounded border bg-background px-2 py-0.5 font-mono tabular-nums">
          点位映射 {desired?.pointMappings.length ?? 0} 条
        </span>
        <span className="rounded border bg-background px-2 py-0.5 font-mono tabular-nums">
          采集周期 {desired?.samplingPolicies.length ?? 0} 条
        </span>
        <span className="rounded border bg-background px-2 py-0.5 font-mono tabular-nums">
          集中器暂存 {concentratorUnits.length} 个单元
        </span>
      </div>

      {desired && desired.pointMappings.length > 0 ? (
        <ul className="flex flex-col gap-1 border-t border-divider pt-2 text-[11px]">
          {desired.pointMappings.slice(0, 3).map((item, idx) => (
            <li key={`${item.sourceChannel}-${idx}`} className="flex items-center justify-between gap-2">
              <span className="shrink-0 font-mono text-foreground">{item.sourceChannel}</span>
              <span className="min-w-0 truncate font-mono text-muted-foreground">
                {item.primaryDeviceKey}:{item.primaryPointCode}
                {item.copyTargets?.length
                  ? ` (+ ${formatPointCopyTargets(item.copyTargets)})`
                  : ''}
              </span>
            </li>
          ))}
          {desired.pointMappings.length > 3 ? (
            <li className="text-muted-foreground">其余 {desired.pointMappings.length - 3} 条可在「编辑配置影子」中查看</li>
          ) : null}
        </ul>
      ) : null}

      {concentratorUnits.length > 0 ? (
        <div className="flex flex-wrap gap-1.5 border-t border-divider pt-2 text-[11px]">
          {concentratorUnits.map((unit) => (
            <span
              key={unit.unitId}
              className="rounded bg-secondary px-2 py-0.5 font-mono text-secondary-foreground tabular-nums"
            >
              UID {unit.unitId}
              {unit.hasParams ? ' · 参数' : ''}
              {unit.boundEidCount > 0 ? ` · ${unit.boundEidCount} 槽 EID` : ''}
            </span>
          ))}
        </div>
      ) : null}

      {reported?.errorMessage ? (
        <Alert variant="destructive">
          <AlertTriangle className="size-4" aria-hidden="true" />
          <AlertDescription>{reported.errorMessage}</AlertDescription>
        </Alert>
      ) : null}

      {shadow.drift.length > 0 ? (
        <div className="flex flex-col gap-1.5 border-t border-divider pt-2.5">
          <span className="font-medium text-destructive-text">
            漂移明细（{shadow.drift.length} 项）
          </span>
          <ul className="flex flex-col gap-1.5">
            {shadow.drift.map((item, index) => (
              <li
                key={`${item.section}-${item.target}-${index}`}
                className="rounded border border-destructive/30 bg-destructive-soft/50 px-2.5 py-2"
              >
                <div className="flex items-center justify-between gap-2 font-medium text-destructive-text">
                  <span>
                    {CONFIG_SHADOW_SECTION_LABELS[item.section]} ·{' '}
                    <span className="font-mono">{item.target}</span>
                  </span>
                  <span className="shrink-0 text-[11px]">
                    {CONFIG_SHADOW_REASON_LABELS[item.reason]}
                  </span>
                </div>
                <p className="mt-0.5 text-[11px] text-foreground">{item.detail}</p>
              </li>
            ))}
          </ul>
        </div>
      ) : null}

      {stopped ? (
        <p className="text-[11px] text-muted-foreground">
          该网关当前已停用，配置影子暂不可编辑或下发。
        </p>
      ) : null}
    </div>
  );
}
