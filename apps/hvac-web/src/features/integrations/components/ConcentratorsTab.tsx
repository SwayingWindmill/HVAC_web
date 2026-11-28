import { useEffect, useId, useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Radio, RefreshCw, ShieldAlert } from 'lucide-react';
import { notifySuccess } from '@/lib/toast';
import type {
  ConcentratorEidSlot,
  ConcentratorParamsPatch,
  Device,
} from '@/api/generated/platformGateway.gen';
import { Panel } from '@/blocks/panel';
import { StateCard } from '@/blocks/state/state-card';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet';
import { Skeleton } from '@/components/ui/skeleton';
import {
  gatewayConcentratorApi,
  gatewayConcentratorEidsKey,
  gatewayConcentratorStatusKey,
  gatewayConfigShadowKey,
} from '@/features/connectivity/gateway-credentials';
import {
  CONCENTRATOR_BATTERY_WARNING_MV,
  buildConcentratorChannelRow,
  buildConcentratorSummary,
  describeConcentratorError,
  describeProtectedConcentratorUid,
  formatConcentratorEidDisplay,
  inferDefaultConcentratorUid,
  isProtectedConcentratorUid,
  mergeConcentratorAudits,
  type ConcentratorChannelRow,
} from '../concentrator-model';
import { ConcentratorChannelsSection } from './ConcentratorChannelsSection';
import { ConcentratorEidSection } from './ConcentratorEidSection';
import { ConcentratorParamsSection } from './ConcentratorParamsSection';
import { ConcentratorSummaryStrip } from './ConcentratorSummaryStrip';
import { GovernanceBadge } from './GovernanceShared';

export interface ConcentratorsTabProps {
  readonly siteName: string;
  readonly allDevices: readonly Device[];
  readonly devicesPending: boolean;
  readonly devicesError: boolean;
  readonly canReadDevices: boolean;
  readonly canWrite: boolean;
  readonly csrfToken: string;
}

export function ConcentratorsTab({
  siteName,
  allDevices,
  devicesPending,
  devicesError,
  canReadDevices,
  canWrite,
  csrfToken,
}: ConcentratorsTabProps) {
  const gatewaySelectId = useId();
  const unitIdInputId = useId();
  const queryClient = useQueryClient();

  const gateways = useMemo(
    () => allDevices.filter((device) => device.deviceType === 'GATEWAY'),
    [allDevices],
  );
  const defaultUnitId = useMemo(() => inferDefaultConcentratorUid(allDevices), [allDevices]);

  const [selectedGatewayId, setSelectedGatewayId] = useState<string>('');
  const [unitIdRaw, setUnitIdRaw] = useState<string>(() => String(defaultUnitId));
  const [unitIdCustomized, setUnitIdCustomized] = useState<boolean>(false);
  const [inspectedChannelNum, setInspectedChannelNum] = useState<number | null>(null);

  useEffect(() => {
    if (unitIdCustomized) return;
    const nextDefault = String(defaultUnitId);
    setUnitIdRaw((prev) => (prev === nextDefault ? prev : nextDefault));
  }, [defaultUnitId, unitIdCustomized]);

  useEffect(() => {
    if (gateways.length === 0) {
      if (selectedGatewayId !== '') setSelectedGatewayId('');
      return;
    }
    if (!gateways.some((gw) => gw.id === selectedGatewayId)) {
      setSelectedGatewayId(gateways[0]!.id);
    }
  }, [gateways, selectedGatewayId]);

  const parsedUnitId = useMemo(() => {
    const trimmed = unitIdRaw.trim();
    if (!trimmed) return null;
    const num = Number(trimmed);
    if (!Number.isInteger(num) || num < 1 || num > 247) return null;
    return num;
  }, [unitIdRaw]);

  const isUnitProtected = parsedUnitId !== null && isProtectedConcentratorUid(parsedUnitId);
  const queryEnabled =
    canReadDevices &&
    Boolean(selectedGatewayId) &&
    parsedUnitId !== null &&
    !isUnitProtected;

  const statusQuery = useQuery({
    queryKey: gatewayConcentratorStatusKey(selectedGatewayId, parsedUnitId ?? 1),
    queryFn: ({ signal }) =>
      gatewayConcentratorApi.status(selectedGatewayId, parsedUnitId ?? 1, signal),
    enabled: queryEnabled,
    retry: false,
  });

  const eidsQuery = useQuery({
    queryKey: gatewayConcentratorEidsKey(selectedGatewayId, parsedUnitId ?? 1),
    queryFn: ({ signal }) =>
      gatewayConcentratorApi.eids(selectedGatewayId, parsedUnitId ?? 1, signal),
    enabled: queryEnabled,
    retry: false,
  });

  const paramsMutation = useMutation({
    mutationFn: async (payload: {
      readonly patch: ConcentratorParamsPatch;
      readonly reason: string;
      readonly stageIfOffline: boolean;
    }) => {
      if (!selectedGatewayId || parsedUnitId === null) {
        throw new Error('缺少网关或集中器从站地址');
      }
      return gatewayConcentratorApi.updateParams(
        selectedGatewayId,
        parsedUnitId,
        {
          params: payload.patch,
          reason: payload.reason,
          ...(payload.stageIfOffline ? { stageIfOffline: true } : {}),
        },
        csrfToken,
      );
    },
    onSuccess: (result) => {
      if (!selectedGatewayId || parsedUnitId === null) return;
      void queryClient.invalidateQueries({
        queryKey: gatewayConfigShadowKey(selectedGatewayId),
      });
      if (result.staged) {
        queryClient.setQueryData(
          gatewayConcentratorStatusKey(selectedGatewayId, parsedUnitId),
          (current: typeof statusQuery.data) =>
            current
              ? {
                  ...current,
                  recentAudits: [result.audit, ...current.recentAudits],
                }
              : current,
        );
        notifySuccess(
          `网关当前离线，参数变更已暂存至配置影子（期望修订 v${result.desiredRevision ?? '?'}），待重连后自动下发`,
        );
        return;
      }
      const nextUnitId = result.params.modbusId;
      if (nextUnitId !== parsedUnitId) {
        setUnitIdRaw(String(nextUnitId));
      }
      queryClient.setQueryData(
        gatewayConcentratorStatusKey(selectedGatewayId, nextUnitId),
        (current: typeof statusQuery.data) =>
          current
            ? {
                ...current,
                unitId: nextUnitId,
                params: result.params,
                recentAudits: [result.audit, ...current.recentAudits],
              }
            : current,
      );
      void queryClient.invalidateQueries({
        queryKey: gatewayConcentratorStatusKey(selectedGatewayId, nextUnitId),
      });
      void queryClient.invalidateQueries({
        queryKey: gatewayConcentratorEidsKey(selectedGatewayId, nextUnitId),
      });
      notifySuccess(`已将 ${result.writtenCount} 项参数写入集中器 Flash`);
    },
  });

  const eidsMutation = useMutation({
    mutationFn: async (payload: {
      readonly slots: readonly ConcentratorEidSlot[];
      readonly reason: string;
      readonly stageIfOffline: boolean;
    }) => {
      if (!selectedGatewayId || parsedUnitId === null) {
        throw new Error('缺少网关或集中器从站地址');
      }
      return gatewayConcentratorApi.updateEids(
        selectedGatewayId,
        parsedUnitId,
        {
          slots: [...payload.slots],
          reason: payload.reason,
          ...(payload.stageIfOffline ? { stageIfOffline: true } : {}),
        },
        csrfToken,
      );
    },
    onSuccess: (result) => {
      if (!selectedGatewayId || parsedUnitId === null) return;
      void queryClient.invalidateQueries({
        queryKey: gatewayConfigShadowKey(selectedGatewayId),
      });
      if (result.staged) {
        queryClient.setQueryData(
          gatewayConcentratorEidsKey(selectedGatewayId, parsedUnitId),
          (current: typeof eidsQuery.data) =>
            current
              ? {
                  ...current,
                  recentAudits: [result.audit, ...current.recentAudits],
                }
              : current,
        );
        notifySuccess(
          `网关当前离线，EID 映射变更已暂存至配置影子（期望修订 v${result.desiredRevision ?? '?'}），待重连后自动下发`,
        );
        return;
      }
      queryClient.setQueryData(
        gatewayConcentratorEidsKey(selectedGatewayId, parsedUnitId),
        (current: typeof eidsQuery.data) =>
          current
            ? {
                ...current,
                slots: result.slots,
                recentAudits: [result.audit, ...current.recentAudits],
              }
            : current,
      );
      void queryClient.invalidateQueries({
        queryKey: gatewayConcentratorStatusKey(selectedGatewayId, parsedUnitId),
      });
      void queryClient.invalidateQueries({
        queryKey: gatewayConcentratorEidsKey(selectedGatewayId, parsedUnitId),
      });
      notifySuccess(
        `已分 ${result.chunkCount} 段更新 ${result.updatedSlots} 个槽位并写入 Flash`,
      );
    },
  });

  const channelRows = useMemo<readonly ConcentratorChannelRow[]>(
    () => (statusQuery.data?.channels ?? []).map(buildConcentratorChannelRow),
    [statusQuery.data?.channels],
  );

  const summary = useMemo(
    () => (statusQuery.data ? buildConcentratorSummary(channelRows) : null),
    [statusQuery.data, channelRows],
  );

  const mergedAudits = useMemo(
    () => mergeConcentratorAudits(statusQuery.data?.recentAudits, eidsQuery.data?.recentAudits),
    [statusQuery.data?.recentAudits, eidsQuery.data?.recentAudits],
  );

  const inspectedChannel = useMemo(
    () => channelRows.find((row) => row.channel === inspectedChannelNum) ?? null,
    [channelRows, inspectedChannelNum],
  );

  const inspectedSlot = useMemo(
    () =>
      inspectedChannelNum !== null
        ? eidsQuery.data?.slots.find((slot) => slot.slot === inspectedChannelNum) ?? null
        : null,
    [eidsQuery.data?.slots, inspectedChannelNum],
  );

  if (!canReadDevices) {
    return (
      <StateCard
        tone="warning"
        icon={ShieldAlert}
        title={<h2 className="text-base font-semibold">当前账号无权读取无线集中器</h2>}
        description="查看现场无线集中器实时通道、串口射频参数与 200 槽 EID 映射需要设备读取权限（device.read），请联系管理员分配相应角色。"
      />
    );
  }

  if (devicesPending && gateways.length === 0) {
    return (
      <div className="flex flex-col gap-4">
        <Skeleton className="h-20 w-full rounded-xl" />
        <Skeleton className="h-56 w-full rounded-xl" />
      </div>
    );
  }

  if (devicesError) {
    return (
      <StateCard
        tone="danger"
        title={<h2 className="text-base font-semibold">无法读取站点网关登记信息</h2>}
        description="请稍后刷新页面或确认平台服务状态后重试。"
      />
    );
  }

  if (gateways.length === 0) {
    return (
      <StateCard
        icon={Radio}
        title={<h2 className="text-base font-semibold">当前站点尚未登记现场网关</h2>}
        description="请先在「现场网关」页签登记网关设备并完成接入，随后可在此管理所挂接的无线温度集中器。"
      />
    );
  }

  const readError = statusQuery.error ?? eidsQuery.error;
  const activeGateway = gateways.find((gw) => gw.id === selectedGatewayId) ?? gateways[0]!;

  return (
    <div className="flex flex-col gap-6">
      {!canWrite ? (
        <Alert>
          <AlertDescription>
            当前账号仅有查看权限（缺少 device.write），可查看无线集中器通道读数、串口射频参数与
            200 槽 EID 映射；所有参数修改与 Flash 写入操作已禁用。
          </AlertDescription>
        </Alert>
      ) : null}

      <Panel aria-label="集中器选择与通信控制">
        <div className="flex flex-wrap items-end justify-between gap-4 px-5 py-4">
          <div className="flex flex-wrap items-end gap-4">
            <div className="flex flex-col gap-1.5">
              <Label htmlFor={gatewaySelectId} className="text-xs font-medium">
                所属现场网关
              </Label>
              <Select
                value={selectedGatewayId}
                onValueChange={(next) => {
                  setSelectedGatewayId(next);
                  setInspectedChannelNum(null);
                }}
              >
                <SelectTrigger id={gatewaySelectId} className="min-w-64">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {gateways.map((gw) => (
                    <SelectItem key={gw.id} value={gw.id}>
                      {gw.displayName}（{gw.code}）
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>

            <div className="flex flex-col gap-1.5">
              <Label htmlFor={unitIdInputId} className="text-xs font-medium">
                RS485 从站地址 (Unit ID, 1–247)
              </Label>
              <div className="flex items-center gap-2">
                <Input
                  id={unitIdInputId}
                  type="number"
                  min={1}
                  max={247}
                  value={unitIdRaw}
                  aria-invalid={parsedUnitId === null || isUnitProtected}
                  onChange={(event) => {
                    setUnitIdRaw(event.currentTarget.value);
                    setUnitIdCustomized(event.currentTarget.value.trim() !== String(defaultUnitId));
                    setInspectedChannelNum(null);
                  }}
                  className="h-8 w-28 font-mono text-sm tabular-nums"
                />
                {unitIdRaw !== String(defaultUnitId) ? (
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() => {
                      setUnitIdRaw(String(defaultUnitId));
                      setUnitIdCustomized(false);
                      setInspectedChannelNum(null);
                    }}
                  >
                    恢复 UID {defaultUnitId}
                  </Button>
                ) : null}
              </div>
            </div>
          </div>

          <div className="flex items-center gap-2">
            <Button
              variant="outline"
              size="sm"
              disabled={!queryEnabled || statusQuery.isFetching || eidsQuery.isFetching}
              onClick={() => {
                void statusQuery.refetch();
                void eidsQuery.refetch();
              }}
            >
              <RefreshCw
                data-icon="inline-start"
                className={statusQuery.isFetching || eidsQuery.isFetching ? 'motion-safe:animate-spin' : undefined}
              />
              刷新集中器读数
            </Button>
          </div>
        </div>
      </Panel>

      {parsedUnitId === null ? (
        <Alert variant="destructive">
          <AlertDescription>请输入 1 到 247 之间的整数作为集中器 RS485 从站地址。</AlertDescription>
        </Alert>
      ) : null}

      {isUnitProtected && parsedUnitId !== null ? (
        <Alert variant="destructive">
          <AlertDescription>{describeProtectedConcentratorUid(parsedUnitId)}</AlertDescription>
        </Alert>
      ) : null}

      {queryEnabled && readError ? (
        <StateCard
          tone="danger"
          title={<h2 className="text-base font-semibold">无法读取集中器实时状态或 EID 映射</h2>}
          description={describeConcentratorError(readError)}
        >
          <Button
            variant="outline"
            size="sm"
            onClick={() => {
              void statusQuery.refetch();
              void eidsQuery.refetch();
            }}
          >
            重试读取
          </Button>
        </StateCard>
      ) : null}

      {!isUnitProtected && parsedUnitId !== null && !readError ? (
        <>
          <ConcentratorSummaryStrip
            summary={summary}
            params={statusQuery.data?.params ?? null}
            respondedAt={statusQuery.data?.respondedAt ?? null}
            loading={statusQuery.isPending}
          />

          <ConcentratorChannelsSection
            rows={channelRows}
            loading={statusQuery.isPending}
            onInspectChannel={setInspectedChannelNum}
          />

          <ConcentratorParamsSection
            params={statusQuery.data?.params ?? null}
            loading={statusQuery.isPending}
            canWrite={canWrite}
            submitting={paramsMutation.isPending}
            submitError={paramsMutation.error}
            onResetError={() => paramsMutation.reset()}
            onSubmitParams={async (patch, reason, stageIfOffline) => {
              await paramsMutation.mutateAsync({ patch, reason, stageIfOffline });
            }}
          />

          <ConcentratorEidSection
            slots={eidsQuery.data?.slots ?? null}
            channelRows={channelRows}
            audits={mergedAudits}
            loading={eidsQuery.isPending}
            canWrite={canWrite}
            submitting={eidsMutation.isPending}
            submitError={eidsMutation.error}
            onResetError={() => eidsMutation.reset()}
            onSubmitEids={async (slots, reason, stageIfOffline) => {
              await eidsMutation.mutateAsync({ slots, reason, stageIfOffline });
            }}
          />
        </>
      ) : null}

      <Sheet
        open={Boolean(inspectedChannel)}
        onOpenChange={(open) => {
          if (!open) setInspectedChannelNum(null);
        }}
      >
        <SheetContent className="overflow-y-auto data-[side=right]:sm:max-w-[480px]">
          {inspectedChannel ? (
            <>
              <SheetHeader className="gap-2 border-b border-divider px-6 pt-5 pb-4">
                <div className="flex items-center gap-2 pr-8">
                  <GovernanceBadge status={inspectedChannel.overallStatus} />
                </div>
                <SheetTitle className="font-mono tabular-nums">
                  通道 CH {String(inspectedChannel.channel).padStart(2, '0')} ·{' '}
                  {inspectedChannel.sensorModel || '无线测温探头'}
                </SheetTitle>
                <SheetDescription>
                  {siteName || '当前站点'} · {activeGateway.displayName} · UID {parsedUnitId ?? 1}
                </SheetDescription>
              </SheetHeader>

              <div className="flex flex-col gap-5 px-6 py-5 text-sm">
                <section aria-label="通道实时读数" className="grid grid-cols-2 gap-4">
                  <div className="rounded-lg border bg-muted/30 p-3">
                    <div className="text-xs text-muted-foreground">实时测温</div>
                    <div className="mt-1 font-mono text-xl font-semibold tabular-nums">
                      {inspectedChannel.temperatureC !== null
                        ? `${inspectedChannel.temperatureC.toFixed(1)} °C`
                        : '—'}
                    </div>
                  </div>
                  <div className="rounded-lg border bg-muted/30 p-3">
                    <div className="text-xs text-muted-foreground">电池电压</div>
                    <div className="mt-1 font-mono text-xl font-semibold tabular-nums">
                      {inspectedChannel.voltageMv !== null
                        ? `${inspectedChannel.voltageMv} mV`
                        : '—'}
                    </div>
                  </div>
                </section>

                <section aria-label="硬件绑定与信号参数" className="space-y-3 border-t border-divider pt-4">
                  <h3 className="text-sm font-semibold text-foreground">探头绑定与寄存器信息</h3>
                  <dl className="grid grid-cols-2 gap-y-2.5 text-xs">
                    <dt className="text-muted-foreground">当前通道 EID</dt>
                    <dd className="font-mono text-right text-foreground tabular-nums">
                      {formatConcentratorEidDisplay(inspectedChannel.eid)}
                    </dd>
                    <dt className="text-muted-foreground">映射表槽位 #{inspectedChannel.channel}</dt>
                    <dd className="font-mono text-right text-foreground tabular-nums">
                      {inspectedSlot ? formatConcentratorEidDisplay(inspectedSlot.eid) : '—'}
                    </dd>
                    <dt className="text-muted-foreground">无线信号强度 (RSSI)</dt>
                    <dd className="font-mono text-right text-foreground tabular-nums">
                      {inspectedChannel.rssiDbm !== null ? `${inspectedChannel.rssiDbm} dBm` : '—'}
                    </dd>
                    <dt className="text-muted-foreground">传感器类型代码</dt>
                    <dd className="font-mono text-right text-foreground tabular-nums">
                      {inspectedChannel.sensorType}
                    </dd>
                    <dt className="text-muted-foreground">Modbus FC04 寄存器偏移</dt>
                    <dd className="font-mono text-right text-foreground tabular-nums">
                      0x{((inspectedChannel.channel - 1) * 5).toString(16).toUpperCase().padStart(4, '0')}
                    </dd>
                  </dl>
                </section>

                {inspectedChannel.voltageMv !== null &&
                inspectedChannel.voltageMv < CONCENTRATOR_BATTERY_WARNING_MV ? (
                  <Alert variant="destructive">
                    <AlertDescription>
                      该无线探头电池电压低于 {CONCENTRATOR_BATTERY_WARNING_MV} mV（当前{' '}
                      {inspectedChannel.voltageMv} mV），建议尽快安排现场巡检更换电池，防止信号断线。
                    </AlertDescription>
                  </Alert>
                ) : null}
              </div>
            </>
          ) : null}
        </SheetContent>
      </Sheet>
    </div>
  );
}
