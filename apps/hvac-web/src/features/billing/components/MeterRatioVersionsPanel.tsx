import { useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Plus, SlidersHorizontal } from 'lucide-react';
import { uuidV7Schema } from '@/api/generated/platformGateway.gen';
import { registryAssetModelQuery } from '@/api/registry';
import { Panel, PanelHeader } from '@/blocks/panel';
import { StateCard } from '@/blocks/state/state-card';
import { InfoTip } from '@/components/info-tip';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Skeleton } from '@/components/ui/skeleton';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { resolveTelemetryPointLabel } from '@/domain/centralPlantTelemetry';
import { formatTime } from '@/lib/operator-format';
import {
  canRetireRatioVersion,
  deriveRatioLifecycleStatus,
  RATIO_APPLICATION_MODE_SHORT_LABEL,
} from '../accounting-config-model';
import {
  describeAccountingConfigError,
  siteMeterRatioVersionsQuery,
  type MeterRatioVersion,
} from '../query';
import { formatMultiplier, type TelemetryPointOption } from './accounting-config-helpers';
import { CreateMeterRatioDialog } from './CreateMeterRatioDialog';
import { RetireMeterRatioDialog } from './RetireMeterRatioDialog';

export interface MeterRatioVersionsPanelProps {
  readonly siteId: string;
  readonly timezone: string;
  readonly csrfToken: string;
  readonly canManageBilling: boolean;
  readonly now: Date;
  readonly onNotice: (message: string) => void;
}

export function MeterRatioVersionsPanel({
  siteId,
  timezone,
  csrfToken,
  canManageBilling,
  now,
  onNotice,
}: MeterRatioVersionsPanelProps) {
  const [includeSuperseded, setIncludeSuperseded] = useState(false);
  const [createRatioOpen, setCreateRatioOpen] = useState(false);
  const [retiringRatio, setRetiringRatio] = useState<MeterRatioVersion | null>(null);

  const ratiosQuery = useQuery(siteMeterRatioVersionsQuery(siteId, { includeSuperseded }));
  const assetModelQuery = useQuery({
    ...registryAssetModelQuery(siteId, uuidV7Schema.safeParse(siteId).success),
    retry: false,
  });

  const { pointOptions, pointById } = useMemo<{
    readonly pointOptions: readonly TelemetryPointOption[];
    readonly pointById: ReadonlyMap<string, TelemetryPointOption>;
  }>(() => {
    const model = assetModelQuery.data;
    if (!model) {
      return { pointOptions: [], pointById: new Map() };
    }
    const deviceNames = new Map(model.devices.map((device) => [device.id, device.displayName]));
    const options: TelemetryPointOption[] = model.telemetryPoints
      .filter((point) => point.status === 'ACTIVE')
      .map((point) => {
        const deviceName = deviceNames.get(point.reportingDeviceId) ?? '';
        const pointLabel = resolveTelemetryPointLabel(point);
        return {
          id: point.id,
          label: deviceName ? `${deviceName} · ${pointLabel}` : pointLabel,
          sublabel: point.unit ? `${point.pointCode} · ${point.unit}` : point.pointCode,
          isCounter: point.pointType === 'COUNTER',
        };
      })
      .sort((left, right) => {
        if (left.isCounter !== right.isCounter) return left.isCounter ? -1 : 1;
        return left.label.localeCompare(right.label, 'zh-CN');
      });

    return {
      pointOptions: options,
      pointById: new Map(options.map((option) => [option.id, option])),
    };
  }, [assetModelQuery.data]);

  const resolvePointDisplay = (
    ratio: MeterRatioVersion,
  ): { title: string; subtitle: string | null } => {
    if (ratio.pointId) {
      const matched = pointById.get(ratio.pointId);
      if (matched) {
        return { title: matched.label, subtitle: matched.sublabel };
      }
      const isUuid = uuidV7Schema.safeParse(ratio.pointId).success;
      return {
        title: isUuid ? '已绑定计量测点' : ratio.pointId,
        subtitle: ratio.version !== null ? `版本 v${ratio.version}` : null,
      };
    }
    return {
      title: '站点电表配置',
      subtitle: ratio.version !== null ? `版本 v${ratio.version}` : null,
    };
  };

  const ratioVersions = ratiosQuery.data ?? [];

  return (
    <>
      <Panel role="region" aria-label="电表 CT/PT 倍率版本">
        <PanelHeader
          title={
            <span className="inline-flex items-center gap-1.5">
              电表 CT/PT 倍率版本
              <InfoTip>
                按测点与生效时间段维护电流互感器（CT）、电压互感器（PT）变比及计量单位；同一测点的生效时间段不允许重叠。
              </InfoTip>
            </span>
          }
          className="h-auto min-h-12 flex-wrap py-2"
          action={
            <div className="flex flex-wrap items-center gap-3">
              <label
                htmlFor="ratio-include-superseded"
                className="inline-flex cursor-pointer items-center gap-2 py-1 text-xs font-medium text-muted-foreground select-none hover:text-foreground"
              >
                <Checkbox
                  id="ratio-include-superseded"
                  checked={includeSuperseded}
                  onCheckedChange={(checked) => setIncludeSuperseded(checked === true)}
                />
                <span>显示历史/已失效版本</span>
              </label>

              {canManageBilling ? (
                <Button
                  type="button"
                  size="sm"
                  variant="outline"
                  onClick={() => setCreateRatioOpen(true)}
                >
                  <Plus data-icon="inline-start" />
                  新增倍率版本
                </Button>
              ) : null}
            </div>
          }
        />

        {ratiosQuery.isError ? (
          <StateCard
            tone="danger"
            title={<h3 className="text-base font-semibold">无法读取电表倍率版本</h3>}
            description={describeAccountingConfigError(
              ratiosQuery.error,
              '请检查网络连接或稍后重试',
            )}
          >
            <Button variant="outline" size="sm" onClick={() => void ratiosQuery.refetch()}>
              重试
            </Button>
          </StateCard>
        ) : ratiosQuery.isPending ? (
          <div className="p-5">
            <Skeleton className="h-44 w-full rounded-lg" />
          </div>
        ) : ratioVersions.length === 0 ? (
          <StateCard
            icon={SlidersHorizontal}
            title={
              <h3 className="text-sm font-medium text-foreground">
                {includeSuperseded ? '尚未配置任何电表倍率版本' : '暂无当前生效的电表倍率版本'}
              </h3>
            }
            description={
              includeSuperseded
                ? '新增后将按生效时段自动折算电量'
                : '可勾选上方开关查看历史版本'
            }
          >
            {canManageBilling ? (
              <Button
                type="button"
                size="sm"
                variant="outline"
                onClick={() => setCreateRatioOpen(true)}
              >
                <Plus data-icon="inline-start" />
                新增倍率版本
              </Button>
            ) : null}
          </StateCard>
        ) : (
          <Table aria-label="电表倍率版本">
            <TableHeader>
              <TableRow className="hover:bg-transparent">
                <TableHead className="pl-5">测点与设备</TableHead>
                <TableHead>CT / PT / 附加乘数</TableHead>
                <TableHead className="text-right">云端生效乘数</TableHead>
                <TableHead>生效时段</TableHead>
                <TableHead>状态</TableHead>
                {canManageBilling ? <TableHead className="pr-5 text-right">操作</TableHead> : null}
              </TableRow>
            </TableHeader>
            <TableBody>
              {ratioVersions.map((version) => {
                const pointInfo = resolvePointDisplay(version);
                const lifecycle = deriveRatioLifecycleStatus(version, now);
                const canRetire = canRetireRatioVersion(version);
                return (
                  <TableRow key={version.id}>
                    <TableCell className="pl-5 align-top">
                      <div className="font-medium text-foreground">{pointInfo.title}</div>
                      {pointInfo.subtitle ? (
                        <div className="mt-0.5 text-xs text-muted-foreground">
                          {pointInfo.subtitle}
                        </div>
                      ) : null}
                    </TableCell>
                    <TableCell className="align-top font-mono text-xs tabular-nums whitespace-nowrap">
                      <div>
                        CT {formatMultiplier(version.ctRatio)} · PT{' '}
                        {formatMultiplier(version.ptRatio)}
                      </div>
                      <div className="mt-0.5 text-muted-foreground">
                        附加乘数 {formatMultiplier(version.meterMultiplier)}
                      </div>
                    </TableCell>
                    <TableCell className="text-right align-top tabular-nums whitespace-nowrap">
                      <div className="font-mono font-semibold text-foreground">
                        ×{formatMultiplier(version.cloudMultiplier)}
                      </div>
                      <div className="mt-0.5 text-xs text-muted-foreground">
                        {RATIO_APPLICATION_MODE_SHORT_LABEL[version.ratioApplicationMode]} ·{' '}
                        {version.readingUnit}
                      </div>
                    </TableCell>
                    <TableCell className="align-top font-mono text-xs tabular-nums whitespace-nowrap">
                      <div>{formatTime(version.effectiveFrom, timezone)} 起</div>
                      <div className="mt-0.5 font-sans text-muted-foreground">
                        {version.effectiveTo
                          ? `至 ${formatTime(version.effectiveTo, timezone)}`
                          : '长期有效'}
                      </div>
                    </TableCell>
                    <TableCell className="align-top whitespace-nowrap">
                      <Badge variant={lifecycle.variant}>{lifecycle.label}</Badge>
                    </TableCell>
                    {canManageBilling ? (
                      <TableCell className="pr-5 text-right align-top whitespace-nowrap">
                        {canRetire ? (
                          <Button
                            type="button"
                            size="sm"
                            variant="outline"
                            onClick={() => setRetiringRatio(version)}
                          >
                            终止生效
                          </Button>
                        ) : (
                          <span className="text-xs text-muted-foreground">—</span>
                        )}
                      </TableCell>
                    ) : null}
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        )}
      </Panel>

      <CreateMeterRatioDialog
        open={createRatioOpen}
        onOpenChange={setCreateRatioOpen}
        siteId={siteId}
        timezone={timezone}
        csrfToken={csrfToken}
        now={now}
        pointOptions={pointOptions}
        onNotice={onNotice}
      />

      <RetireMeterRatioDialog
        retiringRatio={retiringRatio}
        onClose={() => setRetiringRatio(null)}
        siteId={siteId}
        timezone={timezone}
        csrfToken={csrfToken}
        now={now}
        onNotice={onNotice}
      />
    </>
  );
}
