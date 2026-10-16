import type { ReactNode } from 'react';
import { PERIOD_LABEL, type DemandCharge as Charge } from '@/features/tariff/tariff-model';
import { formatDecimal, formatTime } from '@/lib/operator-format';

const chargedPeriods = (charges: readonly Charge[]) => charges.map((charge) => `${PERIOD_LABEL[charge.code]}时段`).join('、');

function Fact({ label, children }: { readonly label: string; readonly children: ReactNode }) {
  return (
    <>
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="font-medium">{children}</dd>
    </>
  );
}

/** This month's demand charge at the Site's Tariff: each billed period's highest quarter-hour times its rate. */
export function DemandCharge({ charges, ranges, timeZone }: {
  readonly charges: readonly Charge[];
  readonly ranges: ReadonlyMap<string, string>;
  readonly timeZone: string;
}) {
  const billed = charges.filter((charge) => charge.charge !== null);
  const total = billed.reduce((sum, charge) => sum + charge.charge!, 0);
  return (
    <div className="grid grid-cols-[minmax(0,1fr)_minmax(0,2fr)] divide-x divide-divider">
      <div className="flex flex-col gap-1 px-5 py-5">
        <span className="text-xs font-medium text-muted-foreground">本月需量电费（估算）</span>
        <span className="text-2xl leading-[29px] font-semibold tracking-[-0.03em] tabular-nums">{billed.length ? `¥${formatDecimal(total, 0)}` : '暂无数据'}</span>
        <span className="text-xs text-muted-foreground">
          {billed.length
            ? billed.map((charge) => `${PERIOD_LABEL[charge.code]}时段最高 ${formatDecimal(charge.peak!.demandKW, 1)} kW × ${formatDecimal(charge.ratePerKW, 0)} 元/kW`).join('，')
            : `${chargedPeriods(charges)}本月还没有完整的 15 分钟`}
        </span>
      </div>
      <dl className="grid grid-cols-[auto_minmax(0,1fr)] content-start gap-x-4 gap-y-2.5 px-5 py-5 text-sm">
        <Fact label="计费方式">按{chargedPeriods(charges)}当月最高的 15 分钟平均功率（需量）计收</Fact>
        <Fact label="计费时段">{charges.map((charge) => ranges.get(charge.code) ?? '今日不适用').join('；')}</Fact>
        <Fact label="出现在">
          {billed.length ? <span className="font-mono">{billed.map((charge) => formatTime(charge.peak!.at, timeZone)).join('；')}</span> : '—'}
        </Fact>
        <Fact label="说明">按电价估算，正式电费以结算为准</Fact>
      </dl>
    </div>
  );
}
