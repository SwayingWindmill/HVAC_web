import { Panel, PanelHeader } from '@/blocks/panel';
import { InfoTip } from '@/components/info-tip';
import { Badge } from '@/components/ui/badge';
import { Skeleton } from '@/components/ui/skeleton';
import type { Breakdown } from '@/features/breakdown/breakdown-model';
import { formatDecimal } from '@/lib/operator-format';
import { cn } from '@/lib/utils';
import {
  computeLayeredEfficiency,
  computeMeterBalance,
  METER_BALANCE_LABEL,
  type MeterBalanceStatus,
} from '../layered-efficiency';
import type { EnergySummary } from '../model';

interface BalancePresentation {
  readonly badgeText: string;
  readonly variant: 'success' | 'warning' | 'destructive' | 'outline';
  readonly diffClassName: string;
}

const BALANCE_PRESENTATION: Readonly<Record<MeterBalanceStatus, BalancePresentation>> = {
  BALANCED: {
    badgeText: '● 平衡',
    variant: 'success',
    diffClassName: 'text-success-text',
  },
  UNMETERED_GAP: {
    badgeText: '■ 未计量差额偏大',
    variant: 'warning',
    diffClassName: 'text-warning-text',
  },
  SUBMETER_EXCEEDS_MAIN: {
    badgeText: '▲ 设备合计高于主计量',
    variant: 'destructive',
    diffClassName: 'text-destructive-text',
  },
  NO_MAIN_METER: {
    badgeText: '○ 暂无冷站主计量',
    variant: 'outline',
    diffClassName: 'text-muted-foreground',
  },
};

function formatEnergy(value: number): string {
  return Math.abs(value) >= 10_000
    ? `${formatDecimal(value / 1000, 2)} MWh`
    : `${formatDecimal(value, 1)} kWh`;
}

function formatSignedEnergy(value: number): string {
  const roundedZero = Math.abs(value) < 0.05 ? 0 : value;
  const prefix = roundedZero > 0 ? '+' : '';
  return `${prefix}${formatEnergy(roundedZero)}`;
}

function formatSignedRatio(ratio: number): string {
  const pct = ratio * 100;
  const roundedZero = Math.abs(pct) < 0.05 ? 0 : pct;
  const prefix = roundedZero > 0 ? '+' : '';
  return `${prefix}${formatDecimal(roundedZero, 1)}%`;
}

function EfficiencyTile({
  label,
  value,
  unit,
  note,
  swatch,
}: {
  readonly label: string;
  readonly value: string | null;
  readonly unit?: string;
  readonly note: string;
  readonly swatch?: string;
}) {
  return (
    <div className="flex min-w-0 flex-col gap-1 px-5 py-4">
      <span className="flex items-center gap-1.5 text-xs font-medium text-muted-foreground">
        {swatch ? (
          <span
            className="size-2 shrink-0 rounded-[2px]"
            style={{ background: swatch }}
            aria-hidden="true"
          />
        ) : null}
        <span className="truncate">{label}</span>
      </span>
      <span className="text-[22px] leading-7 font-semibold tracking-[-0.02em] tabular-nums">
        {value === null ? <span className="text-muted-foreground">—</span> : value}
        {value !== null && unit ? (
          <span className="ml-1 text-xs font-medium tracking-normal text-subtle-foreground">
            {unit}
          </span>
        ) : null}
      </span>
      <span className="truncate text-xs text-muted-foreground" title={note}>
        {note}
      </span>
    </div>
  );
}

/**
 * Displays layered HVAC plant efficiency (plant COP, chiller COP, pump WTFs, cooling tower share)
 * alongside main-vs-submeter balance verification.
 */
export function LayeredEfficiencyStrip({
  summary,
  breakdown,
  isPending = false,
}: {
  readonly summary: EnergySummary;
  readonly breakdown: Breakdown | null | undefined;
  readonly isPending?: boolean;
}) {
  if (isPending) {
    return <Skeleton className="h-[176px] rounded-xl" />;
  }

  const efficiency = computeLayeredEfficiency({ summary, breakdown });
  const balance = computeMeterBalance({ breakdown });
  const balanceMeta = BALANCE_PRESENTATION[balance.status];
  const hasCooling = summary.coolingKWh !== null && summary.coolingKWh >= 1;
  const categoryMetered = (category: Breakdown['categories'][number]['category']) =>
    (breakdown?.categories.find((entry) => entry.category === category)?.metered ?? 0) > 0;
  const subMeterNote = (
    category: Breakdown['categories'][number]['category'],
    available: boolean,
    formulaNote: string,
  ) => {
    if (!breakdown) return '暂无设备计量数据';
    if (!categoryMetered(category)) return '未单独计量';
    if (hasCooling && !available) return '读数超出合理范围';
    return formulaNote;
  };

  const tiles = [
    {
      key: 'chillerCop',
      label: '冷机 COP',
      value: efficiency.chillerCop === null ? null : formatDecimal(efficiency.chillerCop, 2),
      unit: 'COP',
      swatch: 'var(--chart-1)',
      note: subMeterNote('CHILLER', efficiency.chillerCop !== null, '供冷量 ÷ 冷水机组用电'),
    },
    {
      key: 'chilledPumpWtf',
      label: '冷冻泵输送系数 WTF',
      value: efficiency.chilledPumpWtf === null ? null : formatDecimal(efficiency.chilledPumpWtf, 1),
      swatch: 'var(--chart-2)',
      note: subMeterNote('CHILLED_WATER_PUMP', efficiency.chilledPumpWtf !== null, '供冷量 ÷ 冷冻水泵用电'),
    },
    {
      key: 'coolingPumpWtf',
      label: '冷却泵输送系数 WTF',
      value: efficiency.coolingPumpWtf === null ? null : formatDecimal(efficiency.coolingPumpWtf, 1),
      swatch: 'var(--chart-3)',
      note: subMeterNote('COOLING_WATER_PUMP', efficiency.coolingPumpWtf !== null, '供冷量 ÷ 冷却水泵用电'),
    },
    {
      key: 'towerPowerShare',
      label: '冷却塔电耗占比',
      value: efficiency.towerPowerShare === null
        ? null
        : formatDecimal(efficiency.towerPowerShare * 100, 1),
      unit: '%',
      swatch: 'var(--chart-4)',
      note: !breakdown
        ? '暂无设备计量数据'
        : !categoryMetered('COOLING_TOWER')
          ? '未单独计量'
          : '冷却塔用电 ÷ 整站空调用电',
    },
  ] as const;

  return (
    <Panel aria-label="系统能效分层与计量校核">
      <PanelHeader
        title={(
          <span className="flex items-center gap-1.5">
            系统能效分层与计量校核
            <InfoTip>
              分层查看冷机、水泵、冷却塔的运行能效，并核对冷站主计量与各设备计量之和的偏差（超过 ±5% 时提示核对）
            </InfoTip>
          </span>
        )}
        badge={<Badge variant={balanceMeta.variant}>{balanceMeta.badgeText}</Badge>}
        caption={METER_BALANCE_LABEL[balance.status]}
      />
      <div className="grid grid-cols-2 divide-x divide-y divide-divider border-b border-divider lg:grid-cols-4 lg:divide-y-0">
        {tiles.map((tile) => (
          <EfficiencyTile
            key={tile.key}
            label={tile.label}
            value={tile.value}
            unit={'unit' in tile ? tile.unit : undefined}
            swatch={'swatch' in tile ? tile.swatch : undefined}
            note={tile.note}
          />
        ))}
      </div>
      <div className="flex flex-wrap items-center justify-between gap-x-6 gap-y-2 bg-muted/30 px-5 py-3 text-xs text-muted-foreground tabular-nums">
        <div className="flex flex-wrap items-center gap-x-5 gap-y-1">
          <span>
            冷站主计量{' '}
            <strong className="font-medium text-foreground">
              {balance.mainMeterKWh !== null ? formatEnergy(balance.mainMeterKWh) : '暂无数据'}
            </strong>
          </span>
          <span>
            设备计量合计{' '}
            <strong className="font-medium text-foreground">
              {formatEnergy(balance.subMeteredKWh)}
            </strong>
          </span>
        </div>
        <div>
          {balance.diffKWh !== null && balance.diffRatio !== null ? (
            <span>
              差额{' '}
              <strong className={cn('font-medium', balanceMeta.diffClassName)}>
                {formatSignedEnergy(balance.diffKWh)}（{formatSignedRatio(balance.diffRatio)}）
              </strong>
            </span>
          ) : (
            <span>待冷站主计量读数接入后核对差额</span>
          )}
        </div>
      </div>
    </Panel>
  );
}
