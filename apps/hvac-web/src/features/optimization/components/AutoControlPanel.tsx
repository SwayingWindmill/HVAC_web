import { useQuery } from '@tanstack/react-query';
import { ShieldCheck, ShieldOff, TriangleAlert } from 'lucide-react';
import { useState } from 'react';
import { FactTile } from '@/blocks/fact-list';
import { Panel, PanelHeader } from '@/blocks/panel';
import { StatusBadge } from '@/components/status-badge';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { useNow } from '@/hooks/use-now';
import { formatDecimal, formatTime } from '@/lib/operator-format';
import {
  AutoControlRequestError,
  siteAutoControlLeaseQuery,
  siteExecutionMonitorsQuery,
} from '../auto-control-api';
import {
  formatAutoControlCapability,
  formatAutoControlExecutionMode,
  formatAutoControlTripReason,
  LEASE_STATUS_PRESENTATION,
  resolveActiveLeaseBackoff,
  resolveAutoControlExecutionMode,
} from '../auto-control-model';
import { ArmLeaseDialog } from './ArmLeaseDialog';
import { ExecutionMonitorsTable } from './ExecutionMonitorsTable';
import { RevokeLeaseDialog } from './RevokeLeaseDialog';

export function AutoControlPanel({
  siteId,
  timeZone,
  csrfToken,
  canRead,
  canManage,
}: {
  readonly siteId: string;
  readonly timeZone: string;
  readonly csrfToken: string;
  readonly canRead: boolean;
  readonly canManage: boolean;
}) {
  const [armOpen, setArmOpen] = useState(false);
  const [revokeOpen, setRevokeOpen] = useState(false);
  const now = useNow(30_000);

  const leaseQuery = useQuery({
    ...siteAutoControlLeaseQuery(siteId),
    enabled: canRead,
  });
  const monitorsQuery = useQuery({
    ...siteExecutionMonitorsQuery(siteId),
    enabled: canRead,
  });

  if (!canRead) return null;

  const monitorItems = monitorsQuery.data?.items ?? [];
  const leaseState = leaseQuery.data;
  const isAuto = leaseState?.confirmationMode === 'AUTO_WITH_LEASE' && leaseState.activeLease !== null;
  const activeLease = leaseState?.activeLease ?? null;
  const latestLease = leaseState?.latestLease ?? null;
  const leaseExecutionMode = resolveAutoControlExecutionMode(activeLease);
  const isShadowLease = isAuto && leaseExecutionMode === 'SHADOW';
  const backoff = isAuto && activeLease ? resolveActiveLeaseBackoff(activeLease, now) : null;
  const deadbandDigits = activeLease?.deadbandC !== undefined
    && Math.abs(activeLease.deadbandC * 10 - Math.round(activeLease.deadbandC * 10)) > 0.001
    ? 2
    : 1;

  return (
    <div className="flex w-full min-w-0 flex-col gap-4">
      <Panel aria-label="自动控制租约">
        <PanelHeader
          className="h-auto min-h-12 flex-wrap py-2.5 sm:h-12 sm:py-0"
          title={(
            <span className="flex items-center gap-1.5">
              {isAuto ? (
                <ShieldCheck
                  className={isShadowLease ? 'size-4 text-primary' : 'size-4 text-success'}
                  aria-hidden="true"
                />
              ) : (
                <ShieldOff className="size-4 text-muted-foreground" aria-hidden="true" />
              )}
              自动控制租约
            </span>
          )}
          badge={
            leaseState ? (
              <StatusBadge
                tone={isShadowLease ? 'info' : isAuto ? 'success' : 'neutral'}
                label={isShadowLease ? '影子跟跑中' : isAuto ? '限时自动控制租约生效中' : '人工确认下发'}
              />
            ) : undefined
          }
          caption={isAuto && activeLease ? `有效期至 ${formatTime(activeLease.expiresAt, timeZone)}` : undefined}
          action={
            leaseState ? (
              canManage ? (
                <div className="flex flex-wrap items-center gap-2">
                  <Button
                    type="button"
                    size="sm"
                    variant="outline"
                    onClick={() => setArmOpen(true)}
                  >
                    {isAuto ? '调整租约…' : '开启自动控制租约…'}
                  </Button>
                  {isAuto ? (
                    <Button
                      type="button"
                      size="sm"
                      variant="destructive"
                      onClick={() => setRevokeOpen(true)}
                    >
                      撤回租约
                    </Button>
                  ) : null}
                </div>
              ) : (
                <span className="text-xs text-muted-foreground">只读，需运行排班管理权限才能授权</span>
              )
            ) : undefined
          }
        />

        {leaseQuery.isPending ? (
          <div className="flex flex-col gap-2 p-4">
            <Skeleton className="h-14 rounded-lg" />
          </div>
        ) : leaseQuery.isError ? (
          <div className="flex flex-col items-start gap-2 px-5 py-6">
            <p className="text-sm font-semibold">自动控制租约状态暂时读不到</p>
            <p className="text-xs text-muted-foreground">
              {leaseQuery.error instanceof AutoControlRequestError ? leaseQuery.error.message : '请稍后重试'}
            </p>
            <Button variant="outline" size="sm" onClick={() => void leaseQuery.refetch()}>
              重试
            </Button>
          </div>
        ) : isAuto && activeLease ? (
          <div className="flex flex-col gap-3 px-5 py-4">
            {backoff ? (
              <Alert role="status" className="border-warning/40 bg-warning-soft text-warning-text">
                <TriangleAlert className="size-4" aria-hidden="true" />
                <AlertTitle>AI 自动调节退避保护中</AlertTitle>
                <AlertDescription className="text-warning-text">
                  {[
                    backoff.reasonText ?? '检测到人工干预设定值，当前暂停自动下发调温指令',
                    backoff.until ? `退避至 ${formatTime(backoff.until, timeZone)}` : null,
                  ]
                    .filter(Boolean)
                    .join('；')}
                </AlertDescription>
              </Alert>
            ) : null}
            <div className="rounded-lg border border-divider bg-muted/20 p-4">
              <dl className="grid grid-cols-2 gap-x-6 gap-y-3 sm:grid-cols-3 lg:grid-cols-4">
                <FactTile
                  label="运行模式"
                  valueClassName={
                    isShadowLease
                      ? 'text-sm font-semibold text-primary'
                      : 'text-sm font-semibold text-foreground'
                  }
                >
                  {formatAutoControlExecutionMode(leaseExecutionMode)}
                </FactTile>
                <FactTile
                  label="供水温度范围"
                  valueClassName="text-sm font-semibold text-foreground"
                >
                  {`${formatDecimal(activeLease.supplyTempMinC, 1)} 至 ${formatDecimal(activeLease.supplyTempMaxC, 1)} °C`}
                </FactTile>
                <FactTile
                  label="单次最大步长"
                  valueClassName="text-sm font-semibold text-foreground"
                >
                  {`≤ ${formatDecimal(activeLease.maxSetpointStepC, 1)} °C`}
                </FactTile>
                <FactTile
                  label="室内温度上限"
                  valueClassName="text-sm font-semibold text-foreground"
                >
                  {`≤ ${formatDecimal(activeLease.zoneTempMaxC, 1)} °C`}
                </FactTile>
                <FactTile
                  label="最少运行冷机"
                  valueClassName="text-sm font-semibold text-foreground"
                >
                  {`${activeLease.minRunningChillers} 台`}
                </FactTile>
                <FactTile
                  label="每小时指令上限"
                  valueClassName="text-sm font-semibold text-foreground"
                >
                  {`${activeLease.maxCommandsPerHour} 次`}
                </FactTile>
                {(activeLease.allowedCapabilities.includes('SET_FREQUENCY')
                  || activeLease.pumpFreqMinHz !== undefined
                  || activeLease.pumpFreqMaxHz !== undefined) ? (
                  <FactTile
                    label="水泵变频边界"
                    valueClassName="text-sm font-semibold text-foreground tabular-nums"
                  >
                    {`${formatDecimal(activeLease.pumpFreqMinHz ?? 28.0, 1)} 至 ${formatDecimal(activeLease.pumpFreqMaxHz ?? 50.0, 1)} Hz · 步长 ≤ ${formatDecimal(activeLease.maxFreqStepHz ?? 2.5, 1)} Hz`}
                  </FactTile>
                ) : null}
                {(activeLease.allowedCapabilities.includes('SET_FAN_SPEED')
                  || activeLease.fanSpeedMinPct !== undefined
                  || activeLease.fanSpeedMaxPct !== undefined) ? (
                  <FactTile
                    label="风机转速边界"
                    valueClassName="text-sm font-semibold text-foreground tabular-nums"
                  >
                    {`${formatDecimal(activeLease.fanSpeedMinPct ?? 25.0, 1)}% 至 ${formatDecimal(activeLease.fanSpeedMaxPct ?? 100.0, 1)}% · 步长 ≤ ${formatDecimal(activeLease.maxFanStepPct ?? 10.0, 1)}%`}
                  </FactTile>
                ) : null}
                <div className="col-span-2 sm:col-span-3 lg:col-span-2">
                  <FactTile
                    label="允许能力"
                    valueClassName="text-sm font-semibold text-foreground"
                  >
                    <span className="flex flex-wrap gap-1.5 pt-0.5">
                      {activeLease.allowedCapabilities.map((cap) => (
                        <Badge key={cap} variant="secondary">
                          {formatAutoControlCapability(cap)}
                        </Badge>
                      ))}
                    </span>
                  </FactTile>
                </div>
              </dl>
            </div>
            <div className="flex flex-col gap-1 border-t border-divider pt-2.5 text-xs text-muted-foreground">
              <p>
                授权时段 <span className="font-mono">{formatTime(activeLease.armedAt, timeZone)}</span> 至{' '}
                <span className="font-mono">{formatTime(activeLease.expiresAt, timeZone)}</span>
                {activeLease.deadbandC !== undefined
                  ? ` · 调节死区 ${formatDecimal(activeLease.deadbandC, deadbandDigits)} °C`
                  : null}
              </p>
              {isShadowLease ? (
                <p className="font-medium text-primary">
                  当前租约处于影子跟跑模式：每周期执行完整物理护栏校验，仅记录推荐与限幅对照轨迹，不下发设备指令。
                </p>
              ) : null}
            </div>
          </div>
        ) : (
          <div className="flex flex-col gap-3 px-5 py-3.5">
            <dl className="flex flex-wrap items-center gap-x-6 gap-y-1.5 text-xs">
              <div className="flex items-baseline gap-1.5">
                <dt className="text-muted-foreground">执行方式</dt>
                <dd className="font-medium text-foreground">人工确认下发</dd>
              </div>
              <div className="flex items-baseline gap-1.5">
                <dt className="text-muted-foreground">防冻下限</dt>
                <dd className="font-medium text-foreground tabular-nums">5.0 °C</dd>
              </div>
              <div className="flex items-baseline gap-1.5">
                <dt className="text-muted-foreground">安全监护</dt>
                <dd className="font-medium text-foreground">30 分钟巡检与异常回退</dd>
              </div>
            </dl>
            {latestLease ? (
              <div className="flex flex-wrap items-center gap-2 rounded-lg border border-divider bg-muted px-3.5 py-2.5 text-xs text-secondary-foreground">
                <StatusBadge
                  tone={LEASE_STATUS_PRESENTATION[latestLease.status].tone}
                  label={`上次租约：${LEASE_STATUS_PRESENTATION[latestLease.status].label}`}
                />
                {latestLease.executionMode ? (
                  <Badge variant={latestLease.executionMode === 'SHADOW' ? 'default' : 'secondary'}>
                    {formatAutoControlExecutionMode(latestLease.executionMode)}
                  </Badge>
                ) : null}
                <span className="font-mono text-muted-foreground">
                  {formatTime(latestLease.updatedAt, timeZone)}
                </span>
                {latestLease.tripReason ? (
                  <span className="text-foreground">
                    原因：{formatAutoControlTripReason(latestLease.tripReason)}
                  </span>
                ) : null}
              </div>
            ) : null}
          </div>
        )}
      </Panel>

      <ExecutionMonitorsTable
        siteId={siteId}
        timeZone={timeZone}
        monitors={monitorItems}
        isPending={monitorsQuery.isPending}
        isError={monitorsQuery.isError}
        onRetry={() => void monitorsQuery.refetch()}
      />

      {leaseState ? (
        <>
          <ArmLeaseDialog
            siteId={siteId}
            csrfToken={csrfToken}
            leaseState={leaseState}
            open={armOpen}
            onOpenChange={setArmOpen}
          />
          <RevokeLeaseDialog
            siteId={siteId}
            csrfToken={csrfToken}
            timeZone={timeZone}
            leaseState={leaseState}
            open={revokeOpen}
            onOpenChange={setRevokeOpen}
          />
        </>
      ) : null}
    </div>
  );
}
