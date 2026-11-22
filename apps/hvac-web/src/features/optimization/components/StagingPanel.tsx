import { Link } from '@tanstack/react-router';
import { Link2, Lock } from 'lucide-react';
import { Panel, PanelHeader } from '@/blocks/panel';
import { StatusBadge } from '@/components/status-badge';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { SETTLED, type Command } from '@/features/control/command-api';
import { STATUS_PRESENTATION } from '@/features/control/command-presentation';
import type { PendingCommand } from '@/features/control/components/CommandConfirmation';
import type { ControlTarget } from '@/features/control/control-model';
import { MODE_PRESENTATION, type SiteControlMode } from '@/features/control/control-state';
import { formatCompactTime, formatDecimal } from '@/lib/operator-format';
import { KIND_LABEL, type StagingCard, type StagingView } from '../staging-model';

const kw = (value: number) => formatDecimal(value, value >= 100 ? 0 : 1);
const kwh = (value: number) => formatDecimal(value, 0);

function Unit({ children }: { readonly children: string }) {
  return <span className="ml-1 text-xs font-medium tracking-normal text-subtle-foreground">{children}</span>;
}

function CapacityBar({ capacity }: { readonly capacity: NonNullable<StagingCard['capacity']> }) {
  const share = Math.min(100, Math.max(0, (capacity.loadKW / capacity.remainingKW) * 100));
  return (
    <div className="flex max-w-[440px] flex-col gap-1">
      <div className="relative h-2 overflow-hidden rounded-full bg-secondary" role="img" aria-label={`停机后冷负荷占剩余制冷能力 ${Math.round(share)}%`}>
        <span className="absolute inset-y-0 left-0 rounded-full bg-primary" style={{ width: `${share}%` }} />
      </div>
      <div className="flex justify-between text-xs text-muted-foreground">
        <span>冷负荷 <b className="font-semibold text-foreground tabular-nums">{kw(capacity.loadKW)}</b> kW</span>
        <span>停机后余量 <b className="font-semibold text-foreground tabular-nums">{capacity.headroomPercent}%</b></span>
        <span>剩余能力 <b className="font-semibold text-foreground tabular-nums">{kw(capacity.remainingKW)}</b> kW</span>
      </div>
    </div>
  );
}

function Check({ children }: { readonly children: string }) {
  return <span className="inline-flex items-center gap-1.5"><span className="text-success" aria-hidden="true">✓</span>{children}</span>;
}

function RecommendationRow({ item, target, sent, siteId, timeZone, paused, onConfirm }: {
  readonly item: StagingCard;
  readonly target: ControlTarget | undefined;
  readonly sent: Command | undefined;
  readonly siteId: string;
  readonly timeZone: string;
  readonly paused: boolean;
  readonly onConfirm: (pending: PendingCommand) => void;
}) {
  const wholeUnit = item.kind !== 'IDLE_AUXILIARY_STOP';
  const action = target?.actions.find((candidate) => candidate.pointId === item.pointId);
  const sequence = action?.capability === 'STOP_UNIT_SEQUENCE';
  const confirm = () => {
    if (!target || !action) return;
    onConfirm({
      target,
      title: `停 ${item.assetName}？`,
      label: action.label,
      pointId: action.pointId,
      parameters: {},
      facts: [
        { label: '预计省', value: `${kw(item.savingKW)} kW · 本班约 ${kwh(item.shiftKWh)} kWh` },
        ...(item.capacity ? [{ label: '停机后', value: `剩余能力 ${kw(item.capacity.remainingKW)} kW，余量 ${item.capacity.headroomPercent}%` }] : []),
        ...(item.idleMinutes !== null ? [{ label: '已空转', value: `${formatDecimal(item.idleMinutes, 0)} 分钟` }] : []),
        { label: '风险', value: item.requiresSupervisorReview ? '高风险 · 主管批准后下发' : '高风险' },
      ],
      requiresCode: item.requiresTotp,
      acknowledgement: '我已和现场确认可以停',
      submitLabel: '提交停机',
    });
  };
  return (
    <li className="grid grid-cols-[minmax(0,1fr)_176px] gap-4 border-b border-divider p-4 last:border-b-0">
      <div className="flex min-w-0 flex-col gap-2.5">
        <div className="flex items-center gap-2">
          <StatusBadge tone={wholeUnit ? 'info' : 'neutral'} label={KIND_LABEL[item.kind]} />
          <h3 className="text-base leading-[22px] font-semibold tracking-[-0.02em]">停 {item.assetName}</h3>
          {item.extrapolated ? <StatusBadge tone="warning" label="■ 超出近 14 天见过的负荷" /> : null}
        </div>
        {item.capacity ? <CapacityBar capacity={item.capacity} /> : null}
        <dl className="flex flex-wrap gap-6">
          {item.cop ? <div className="flex flex-col"><dt className="text-xs text-muted-foreground">冷站能效</dt><dd className="text-sm font-semibold tabular-nums">{formatDecimal(item.cop.before, 1)} → {formatDecimal(item.cop.after, 1)}</dd></div> : null}
          {item.idleMinutes !== null ? <div className="flex flex-col"><dt className="text-xs text-muted-foreground">对应主机已停</dt><dd className="text-sm font-semibold tabular-nums">{formatDecimal(item.idleMinutes, 0)} 分钟</dd></div> : null}
        </dl>
        <div className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-secondary-foreground" aria-label="下发前提">
          {item.capacity ? <Check>{`剩余能力够用，余量 ${item.capacity.headroomPercent}%`}</Check> : null}
          {item.idleMinutes !== null ? <Check>对应主机已停，这台还在转</Check> : null}
          {item.kind === 'OFF_HOURS_STOP' ? <Check>运行计划里现在是停机时段</Check> : null}
          <Check>{wholeUnit ? `${item.assetName}的设备都没锁定` : '这台设备没锁定'}</Check>
        </div>
        {sequence ? (
          <span className="flex items-center gap-1.5 text-xs text-muted-foreground"><Link2 className="size-3" aria-hidden="true" />一键顺序停：主机先停，对应冷却塔、冷却泵、冷冻泵随后</span>
        ) : null}
      </div>
      <div className="flex flex-col items-end gap-0.5 text-right">
        <span className="text-[24px] leading-[29px] font-semibold tracking-[-0.03em] text-success-text tabular-nums">−{kw(item.savingKW)}<Unit>kW</Unit></span>
        <span className="text-xs text-muted-foreground">本班约 {kwh(item.shiftKWh)} kWh</span>
        <div className="mt-auto flex flex-col items-end gap-1.5 pt-3">
          {sent ? (
            <>
              <StatusBadge tone={SETTLED.has(sent.status) ? 'neutral' : 'warning'} label={`已提交 · ${STATUS_PRESENTATION[sent.status].label}`} />
              <span className="font-mono text-xs text-muted-foreground">{formatCompactTime(sent.createdAt, timeZone)}</span>
              <Link to="/operations/control" search={{ site: siteId, inspect: sent.commandId }} className="text-xs font-medium text-primary hover:underline">在运行控制查看 ›</Link>
            </>
          ) : (
            <>
              <Button size="sm" disabled={paused || !action} title={!action ? '没有登记对应的停机控制点' : undefined} onClick={confirm}>确认停机…</Button>
              <span className="text-xs text-muted-foreground">{[item.requiresSupervisorReview ? '需主管审批' : '', item.requiresTotp ? '动态口令' : ''].filter(Boolean).join(' · ')}</span>
            </>
          )}
        </div>
      </div>
    </li>
  );
}

/** Chillers the plant can do without and pumps or towers still running for a stopped chiller, each confirmed by a person. */
export function StagingPanel({ siteId, timeZone, state, view, evaluatedAt, mode, targets, commands, onConfirm, onRetry }: {
  readonly siteId: string;
  readonly timeZone: string;
  readonly state: 'pending' | 'error' | 'ready';
  readonly view: StagingView | null;
  readonly evaluatedAt: string | null;
  readonly mode: SiteControlMode;
  readonly targets: readonly ControlTarget[];
  readonly commands: readonly Command[];
  readonly onConfirm: (pending: PendingCommand) => void;
  readonly onRetry: () => void;
}) {
  const paused = mode !== 'REMOTE_AUTO';
  const targetsById = new Map(targets.map((target) => [target.assetId, target]));
  const sentFor = (item: StagingCard) => (item.pointId && evaluatedAt
    ? commands.find((command) => command.pointId === item.pointId && (!SETTLED.has(command.status) || Date.parse(command.createdAt) >= Date.parse(evaluatedAt)))
    : undefined);
  return (
    <Panel aria-label="减台与辅机停机">
      <PanelHeader
        title="减台与辅机停机"
        badge={<StatusBadge tone="neutral" label="人工确认后下发" />}
        action={evaluatedAt ? <span className="text-xs text-muted-foreground">评估于 <span className="font-mono">{formatCompactTime(evaluatedAt, timeZone)}</span></span> : undefined}
      />
      {paused ? (
        <div role="status" className="flex items-center gap-2.5 border-b border-divider bg-warning-soft px-4 py-2.5 text-sm font-medium text-warning-text">
          <span aria-hidden="true">{MODE_PRESENTATION[mode].glyph}</span>
          {MODE_PRESENTATION[mode].label}中，停机建议已暂停
          <Link to="/operations/control" search={{ site: siteId }} className="ml-auto text-xs font-medium text-primary hover:underline">去运行控制 ›</Link>
        </div>
      ) : null}
      {state === 'pending' ? (
        <div className="flex flex-col gap-3 p-4"><Skeleton className="h-16" /><Skeleton className="h-28" /></div>
      ) : state === 'error' ? (
        <div className="flex flex-col items-start gap-2 px-4 py-6">
          <p className="text-sm font-semibold">减台建议暂时读不到</p>
          <Button variant="outline" size="sm" onClick={onRetry}>重试</Button>
        </div>
      ) : !view ? (
        <p className="px-4 py-8 text-center text-sm text-muted-foreground">这个站点还没有做过减台评估</p>
      ) : (
        <>
          {view.totals ? (
            <dl className="grid grid-cols-3 border-b border-divider">
              <div className="flex flex-col gap-0.5 border-r border-divider px-4 py-3.5">
                <dt className="text-xs text-muted-foreground">可省功率</dt>
                <dd className="text-[24px] leading-[29px] font-semibold tracking-[-0.03em] text-success-text tabular-nums">{kw(view.totals.savingKW)}<Unit>kW</Unit></dd>
              </div>
              <div className="flex flex-col gap-0.5 border-r border-divider px-4 py-3.5">
                <dt className="text-xs text-muted-foreground">本班可省 · {formatDecimal(view.totals.shiftHours, 0)} 小时</dt>
                <dd className="text-[24px] leading-[29px] font-semibold tracking-[-0.03em] text-success-text tabular-nums">{kwh(view.totals.shiftKWh)}<Unit>kWh</Unit></dd>
              </div>
              <div className="flex flex-col gap-0.5 px-4 py-3.5">
                <dt className="text-xs text-muted-foreground">冷站总功率</dt>
                <dd className="text-[24px] leading-[29px] font-semibold tracking-[-0.03em] tabular-nums">
                  {view.totals.powerBeforeKW !== null ? (
                    <>{kw(view.totals.powerBeforeKW)}<span className="mx-1.5 font-medium text-subtle-foreground">→</span>{kw(view.totals.powerBeforeKW - view.totals.savingKW)}<Unit>kW</Unit></>
                  ) : '—'}
                </dd>
              </div>
            </dl>
          ) : null}
          {view.cards.length > 0 ? (
            <ul aria-label="停机建议">
              {view.cards.map((item) => (
                <RecommendationRow
                  key={item.id}
                  item={item}
                  target={targetsById.get(item.assetId)}
                  sent={sentFor(item)}
                  siteId={siteId}
                  timeZone={timeZone}
                  paused={paused}
                  onConfirm={onConfirm}
                />
              ))}
            </ul>
          ) : (
            <div className="flex flex-col items-center gap-1 px-6 py-8 text-center">
              <p className="text-sm font-semibold">现在没有可以停的设备</p>
              {view.stageDownIdle ? <p className="text-xs text-muted-foreground">{view.stageDownIdle}</p> : null}
            </div>
          )}
          {view.paused.length > 0 ? (
            <div aria-label="暂停的建议" className="flex flex-col gap-2 border-t border-divider bg-muted px-4 py-3">
              <span className="text-xs font-medium text-secondary-foreground">暂停 {view.paused.length} 条</span>
              {view.paused.map((item) => (
                <div key={item.key} className="flex items-center gap-2.5 text-sm">
                  {item.controlFix ? <Lock className="size-3.5 shrink-0 text-muted-foreground" aria-hidden="true" /> : <span className="w-3.5 shrink-0 text-center text-muted-foreground" aria-hidden="true">○</span>}
                  <span className="font-semibold">{item.name}</span>
                  <span className="min-w-0 truncate text-xs text-muted-foreground">{item.why}</span>
                  {item.controlFix ? <Link to="/operations/control" search={{ site: siteId }} className="ml-auto shrink-0 text-xs font-medium text-primary hover:underline">去运行控制 ›</Link> : null}
                </div>
              ))}
            </div>
          ) : null}
        </>
      )}
    </Panel>
  );
}
