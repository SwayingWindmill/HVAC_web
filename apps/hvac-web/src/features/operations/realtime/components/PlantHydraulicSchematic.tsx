import { useMemo } from 'react';
import { AlertTriangle } from 'lucide-react';
import { Panel, PanelHeader, PanelLink } from '@/blocks/panel';
import { cn } from '@/lib/utils';
import { deviceStatus, ReadingValue } from '../device-presentation';
import {
  buildHydraulicSchematic,
  type HydraulicAnomaly,
  type HydraulicNodeView,
  type HydraulicSchematicModel,
} from '../hydraulic-schematic-model';
import { isRunning, type PlantReading, type PlantView } from '../plant-model';

type LoopTone = 'condenser' | 'chilled';

function LoopPipeBadge({ label, reading, highlight = false }: {
  readonly label: string;
  readonly reading: PlantReading | null;
  readonly highlight?: boolean;
}) {
  return (
    <span className={cn(
      'inline-flex items-center gap-1 rounded-md border px-2 py-0.5 text-[11px] whitespace-nowrap tabular-nums',
      highlight
        ? 'border-warning/40 bg-warning-soft text-warning-text'
        : 'border-border bg-card text-secondary-foreground shadow-xs',
    )}
    >
      <span className="text-muted-foreground">{label}</span>
      <span className="font-semibold text-foreground"><ReadingValue reading={reading} /></span>
    </span>
  );
}

function PipeSegmentConnector({ tone, active, direction }: {
  readonly tone: LoopTone;
  readonly active: boolean;
  readonly direction: 'left' | 'right';
}) {
  const stroke = active
    ? (tone === 'condenser' ? 'var(--chart-2)' : 'var(--chart-1)')
    : 'var(--chart-inactive)';

  return (
    <div className="flex items-center justify-center self-center" aria-hidden="true">
      <svg viewBox="0 0 28 24" className="h-6 w-7 shrink-0 overflow-visible">
        <line
          x1={2}
          y1={12}
          x2={26}
          y2={12}
          stroke={stroke}
          strokeWidth={active ? 2 : 1.5}
          strokeDasharray={active ? undefined : '4 3'}
          strokeLinecap="round"
        />
        {direction === 'right' ? (
          <polyline
            points="17,7 23,12 17,17"
            fill="none"
            stroke={stroke}
            strokeWidth={active ? 2 : 1.5}
            strokeLinecap="round"
            strokeLinejoin="round"
          />
        ) : (
          <polyline
            points="11,7 5,12 11,17"
            fill="none"
            stroke={stroke}
            strokeWidth={active ? 2 : 1.5}
            strokeLinecap="round"
            strokeLinejoin="round"
          />
        )}
      </svg>
    </div>
  );
}

function LoopBusBar({
  tone,
  active,
  direction,
  position,
  metrics,
}: {
  readonly tone: LoopTone;
  readonly active: boolean;
  readonly direction: 'left' | 'right';
  readonly position: 'top' | 'bottom';
  readonly metrics: readonly { readonly label: string; readonly reading: PlantReading | null; readonly highlight?: boolean }[];
}) {
  const stroke = active
    ? (tone === 'condenser' ? 'var(--chart-2)' : 'var(--chart-1)')
    : 'var(--chart-inactive)';
  const lineY = position === 'top' ? 10 : 12;
  const stemY = position === 'top' ? 22 : 0;

  return (
    <div className="relative flex min-h-7 items-center justify-center px-4">
      <svg
        aria-hidden="true"
        viewBox="0 0 320 22"
        preserveAspectRatio="none"
        className="pointer-events-none absolute inset-x-4 top-0 h-[22px] w-[calc(100%-2rem)]"
      >
        <path
          d={`M 10 ${stemY} L 10 ${lineY} L 310 ${lineY} L 310 ${stemY}`}
          fill="none"
          stroke={stroke}
          strokeWidth={active ? 2 : 1.5}
          strokeDasharray={active ? undefined : '5 4'}
          strokeLinecap="round"
          strokeLinejoin="round"
        />
      </svg>
      <div className="relative z-10 flex flex-wrap items-center justify-center gap-1.5">
        <span
          aria-hidden="true"
          className={cn(
            'inline-flex size-4 items-center justify-center rounded-full border bg-card text-[10px] font-bold',
            active
              ? (tone === 'condenser' ? 'border-chart-2/50 text-chart-2' : 'border-chart-1/50 text-chart-1')
              : 'border-border text-muted-foreground',
          )}
        >
          {direction === 'left' ? '←' : '→'}
        </span>
        {metrics.map((metric) => (
          <LoopPipeBadge key={metric.label} label={metric.label} reading={metric.reading} highlight={metric.highlight} />
        ))}
      </div>
    </div>
  );
}

function NodeCard({
  node,
  selectedDeviceId,
  onInspect,
  accent,
}: {
  readonly node: HydraulicNodeView;
  readonly selectedDeviceId?: string;
  readonly onInspect: (deviceId: string) => void;
  readonly accent?: 'chiller-bridge' | LoopTone;
}) {
  const { device, primaryReading, secondaryReading, branchCode, valves, hasIssue } = node;
  const running = isRunning(device);
  const selected = selectedDeviceId === device.deviceId;
  const status = deviceStatus(device);

  const dotClass = status.tone === 'running'
    ? 'bg-success'
    : status.tone === 'alarm' || status.tone === 'offline'
      ? 'bg-destructive'
      : 'bg-muted-foreground/50';
  const badgeClass = status.tone === 'running'
    ? 'bg-success-soft text-success-text'
    : status.tone === 'alarm' || status.tone === 'offline'
      ? 'bg-destructive-soft text-destructive-text'
      : 'bg-secondary text-secondary-foreground';

  return (
    <div className="flex flex-col gap-1">
      <button
        type="button"
        onClick={() => onInspect(device.deviceId)}
        aria-pressed={selected}
        className={cn(
          'flex min-h-[44px] w-full cursor-pointer flex-col gap-1.5 rounded-lg border px-2.5 py-2 text-left transition-[border-color,background-color,box-shadow] outline-none focus-visible:ring-3 focus-visible:ring-ring-soft',
          selected
            ? 'border-primary bg-primary-soft/50 ring-1 ring-primary'
            : hasIssue
              ? 'border-destructive/40 bg-destructive-soft/20 hover:bg-destructive-soft/30'
              : running
                ? 'border-border bg-card shadow-xs hover:border-input-hover hover:bg-accent/50'
                : 'border-divider bg-muted/50 text-muted-foreground hover:bg-accent/40',
          accent === 'chiller-bridge' && !selected && 'border-r-2 border-r-chart-1 border-l-2 border-l-chart-2',
        )}
      >
        <div className="flex items-center justify-between gap-1.5">
          <span className="flex min-w-0 items-center gap-1.5">
            <span className={cn('size-2 shrink-0 rounded-full', dotClass)} aria-hidden="true" />
            <span className="truncate font-mono text-xs font-semibold text-foreground">{device.name}</span>
          </span>
          <span className={cn('shrink-0 rounded px-1.5 py-0.5 text-[10px] leading-3.5 font-medium', badgeClass)}>
            {status.label}
          </span>
        </div>
        {(primaryReading || secondaryReading || branchCode) ? (
          <div className="flex flex-wrap items-center justify-between gap-x-2 gap-y-0.5 text-[11px] tabular-nums">
            <div className="flex flex-wrap items-center gap-x-2 gap-y-0.5">
              {primaryReading ? (
                <span className="text-foreground"><ReadingValue reading={primaryReading} /></span>
              ) : null}
              {secondaryReading ? (
                <span className="text-secondary-foreground"><ReadingValue reading={secondaryReading} /></span>
              ) : null}
              {!primaryReading && !secondaryReading ? (
                <span className="text-muted-foreground">—</span>
              ) : null}
            </div>
            {branchCode ? (
              <span className="rounded bg-muted px-1 py-0.5 font-mono text-[10px] text-muted-foreground">
                {branchCode}
              </span>
            ) : null}
          </div>
        ) : null}
      </button>
      {valves.length > 0 ? (
        <div className="flex flex-wrap gap-1 px-0.5">
          {valves.map((valve) => {
            const valveSelected = selectedDeviceId === valve.deviceId;
            return (
              <button
                key={valve.deviceId}
                type="button"
                onClick={() => onInspect(valve.deviceId)}
                aria-pressed={valveSelected}
                className={cn(
                  'inline-flex min-h-[24px] cursor-pointer items-center gap-1 rounded border px-1.5 py-0.5 text-[10px] font-medium transition-colors outline-none focus-visible:ring-3 focus-visible:ring-ring-soft',
                  valveSelected
                    ? 'border-primary bg-primary-soft text-primary'
                    : valve.closed
                      ? 'border-destructive/30 bg-destructive-soft text-destructive-text'
                      : valve.open
                        ? 'border-success/25 bg-success-soft text-success-text'
                        : 'border-divider bg-card text-secondary-foreground',
                )}
              >
                <span className="truncate max-w-[96px]">{valve.name}</span>
                <span>{valve.label}</span>
              </button>
            );
          })}
        </div>
      ) : null}
    </div>
  );
}

function StageColumn({
  title,
  nodes,
  selectedDeviceId,
  onInspect,
  accent,
  footer,
  emptyLabel = '暂无登记设备',
}: {
  readonly title: string;
  readonly nodes: readonly HydraulicNodeView[];
  readonly selectedDeviceId?: string;
  readonly onInspect: (deviceId: string) => void;
  readonly accent?: 'chiller-bridge' | LoopTone;
  readonly footer?: React.ReactNode;
  readonly emptyLabel?: string;
}) {
  return (
    <div className="flex min-w-0 flex-col gap-1.5">
      <div className="flex items-center justify-between px-0.5">
        <span className="text-[11px] font-medium text-muted-foreground">{title}</span>
        {nodes.length > 1 ? (
          <span className="font-mono text-[10px] text-subtle-foreground">{nodes.length} 台</span>
        ) : null}
      </div>
      {nodes.length === 0 ? (
        <div className="flex min-h-[44px] items-center justify-center rounded-lg border border-dashed border-divider bg-muted/30 px-2.5 py-2 text-xs text-muted-foreground">
          {emptyLabel}
        </div>
      ) : (
        <div className="flex flex-col gap-1.5">
          {nodes.map((node) => (
            <NodeCard
              key={node.device.deviceId}
              node={node}
              selectedDeviceId={selectedDeviceId}
              onInspect={onInspect}
              accent={accent}
            />
          ))}
        </div>
      )}
      {footer}
    </div>
  );
}

function WeatherFootnote({
  schematic,
  selectedDeviceId,
  onInspect,
}: {
  readonly schematic: HydraulicSchematicModel;
  readonly selectedDeviceId?: string;
  readonly onInspect: (deviceId: string) => void;
}) {
  const { weather, ambient } = schematic;
  if (!weather && !ambient.outdoorDryBulb && !ambient.outdoorWetBulb) return null;

  const content = (
    <div className="flex flex-wrap items-center justify-between gap-x-2 gap-y-0.5 text-[11px] tabular-nums">
      <span className="text-muted-foreground">室外</span>
      <span className="flex items-center gap-1.5 text-secondary-foreground">
        {ambient.outdoorDryBulb ? (
          <span>干 <ReadingValue reading={ambient.outdoorDryBulb} /></span>
        ) : null}
        {ambient.outdoorWetBulb ? (
          <span>湿 <ReadingValue reading={ambient.outdoorWetBulb} /></span>
        ) : null}
      </span>
    </div>
  );

  if (weather) {
    const selected = selectedDeviceId === weather.deviceId;
    return (
      <button
        type="button"
        onClick={() => onInspect(weather.deviceId)}
        aria-pressed={selected}
        className={cn(
          'w-full cursor-pointer rounded-md border px-2 py-1 text-left transition-colors outline-none focus-visible:ring-3 focus-visible:ring-ring-soft',
          selected ? 'border-primary bg-primary-soft/50' : 'border-divider bg-card/80 hover:bg-accent/50',
        )}
      >
        {content}
      </button>
    );
  }
  return <div className="rounded-md border border-divider bg-card/80 px-2 py-1">{content}</div>;
}

function AnomalyStrip({ anomalies, onInspect }: {
  readonly anomalies: readonly HydraulicAnomaly[];
  readonly onInspect: (deviceId: string) => void;
}) {
  if (anomalies.length === 0) return null;
  return (
    <div className="flex flex-wrap gap-2 border-b border-divider bg-muted/40 px-4 py-2.5" role="region" aria-label="水力异常提示">
      {anomalies.map((anomaly) => (
        <button
          key={`${anomaly.code}-${anomaly.deviceId}`}
          type="button"
          onClick={() => onInspect(anomaly.deviceId)}
          className={cn(
            'inline-flex min-h-[28px] cursor-pointer items-center gap-1.5 rounded-md border px-2.5 py-1 text-left text-xs transition-colors outline-none focus-visible:ring-3 focus-visible:ring-ring-soft',
            anomaly.severity === 'destructive'
              ? 'border-destructive/30 bg-destructive-soft text-destructive-text hover:bg-destructive-soft/80'
              : 'border-warning/35 bg-warning-soft text-warning-text hover:bg-warning-soft/80',
          )}
        >
          <AlertTriangle className="size-3.5 shrink-0" aria-hidden="true" />
          <span className="font-semibold">{anomaly.title}</span>
          <span className="text-[11px] opacity-90">{anomaly.detail}</span>
        </button>
      ))}
    </div>
  );
}

export function PlantHydraulicSchematic({
  plant,
  siteId,
  selectedDeviceId,
  onInspect,
}: {
  readonly plant: PlantView;
  readonly siteId: string;
  readonly selectedDeviceId?: string;
  readonly onInspect: (deviceId: string) => void;
}) {
  const schematic = useMemo(() => buildHydraulicSchematic(plant), [plant]);
  if (!schematic.hasTopology) return null;

  const hasLowDeltaT = schematic.anomalies.some((item) => item.code === 'LOW_DELTA_T_HIGH_FLOW');

  const condenserTopMetrics = [
    { label: '进塔', reading: schematic.condenser.enteringTowerTemp },
    ...(schematic.condenser.approachTemp ? [{ label: '与湿球差', reading: schematic.condenser.approachTemp }] : []),
    ...(schematic.condenser.differentialPressure ? [{ label: '压差', reading: schematic.condenser.differentialPressure }] : []),
  ];
  const condenserBottomMetrics = [
    { label: '出塔', reading: schematic.condenser.leavingTowerTemp },
    ...(schematic.condenser.flowRate ? [{ label: '流量', reading: schematic.condenser.flowRate }] : []),
  ];
  const chilledTopMetrics = [
    { label: '供水', reading: schematic.chilled.supplyTemp },
    { label: '流量', reading: schematic.chilled.flowRate },
    ...(schematic.chilled.differentialPressure ? [{ label: '压差', reading: schematic.chilled.differentialPressure }] : []),
  ];
  const chilledBottomMetrics = [
    { label: '回水', reading: schematic.chilled.returnTemp },
    { label: '温差', reading: schematic.chilled.deltaT, highlight: hasLowDeltaT },
  ];

  const weatherFooter = (
    <WeatherFootnote schematic={schematic} selectedDeviceId={selectedDeviceId} onInspect={onInspect} />
  );
  const zoneEmptyFallback = schematic.ambient.indoorTemp ? (
    <div className="flex flex-col gap-1 rounded-lg border border-divider bg-card px-2.5 py-2 text-xs">
      <span className="text-[11px] text-muted-foreground">室内</span>
      <span className="font-semibold"><ReadingValue reading={schematic.ambient.indoorTemp} /></span>
    </div>
  ) : (
    <div className="flex min-h-[44px] items-center justify-center rounded-lg border border-divider bg-card px-2.5 py-2 text-xs text-muted-foreground">
      空调末端
    </div>
  );

  return (
    <Panel role="region" aria-label="冷站水力回路">
      <PanelHeader
        title="冷站水力回路"
        action={(
          <div className="flex items-center gap-3">
            <div className="hidden items-center gap-3 text-xs text-muted-foreground sm:flex">
              {schematic.hasCondenserLoop ? (
                <span className="inline-flex items-center gap-1.5">
                  <i className={cn('size-2 rounded-full', schematic.coolingFlowActive ? 'bg-chart-2' : 'bg-chart-inactive')} aria-hidden="true" />
                  <span>冷却水 {schematic.coolingFlowActive ? '循环中' : '停止'}</span>
                </span>
              ) : null}
              <span className="inline-flex items-center gap-1.5">
                <i className={cn('size-2 rounded-full', schematic.chilledFlowActive ? 'bg-chart-1' : 'bg-chart-inactive')} aria-hidden="true" />
                <span>冷冻水 {schematic.chilledFlowActive ? '循环中' : '停止'}</span>
              </span>
            </div>
            <PanelLink to="/operations/trends" search={{ site: siteId, preset: 'pump-hydraulics' }}>
              水力趋势
            </PanelLink>
          </div>
        )}
      />

      <AnomalyStrip anomalies={schematic.anomalies} onInspect={onInspect} />

      <div className="bg-muted/25 p-4">
        {/* Desktop 5-stage (or 3-stage air-cooled) horizontal 2D hydraulic flow diagram */}
        <div className="hidden flex-col gap-2 @4xl/main:flex">
          {schematic.hasCondenserLoop ? (
            <>
              <div className="grid grid-cols-2 gap-6 px-3">
                <LoopBusBar
                  tone="condenser"
                  active={schematic.coolingFlowActive}
                  direction="left"
                  position="top"
                  metrics={condenserTopMetrics}
                />
                <LoopBusBar
                  tone="chilled"
                  active={schematic.chilledFlowActive}
                  direction="right"
                  position="top"
                  metrics={chilledTopMetrics}
                />
              </div>

              <div className="grid grid-cols-[minmax(0,1fr)_24px_minmax(0,1fr)_24px_minmax(0,1.15fr)_24px_minmax(0,1fr)_24px_minmax(0,0.9fr)] items-stretch gap-1">
                <StageColumn
                  title="冷却塔"
                  nodes={schematic.towers}
                  selectedDeviceId={selectedDeviceId}
                  onInspect={onInspect}
                  footer={weatherFooter}
                />
                <PipeSegmentConnector tone="condenser" active={schematic.coolingFlowActive} direction="right" />
                <StageColumn
                  title="冷却水泵"
                  nodes={schematic.coolingPumps}
                  selectedDeviceId={selectedDeviceId}
                  onInspect={onInspect}
                />
                <PipeSegmentConnector tone="condenser" active={schematic.coolingFlowActive} direction="right" />
                <StageColumn
                  title="冷水机组"
                  nodes={schematic.chillers}
                  selectedDeviceId={selectedDeviceId}
                  onInspect={onInspect}
                  accent="chiller-bridge"
                />
                <PipeSegmentConnector tone="chilled" active={schematic.chilledFlowActive} direction="right" />
                <StageColumn
                  title="冷冻水泵"
                  nodes={schematic.chilledPumps}
                  selectedDeviceId={selectedDeviceId}
                  onInspect={onInspect}
                />
                <PipeSegmentConnector tone="chilled" active={schematic.chilledFlowActive} direction="right" />
                <div className="flex min-w-0 flex-col gap-1.5">
                  <span className="px-0.5 text-[11px] font-medium text-muted-foreground">末端负荷</span>
                  {schematic.zones.length > 0 ? (
                    <div className="flex flex-col gap-1.5">
                      {schematic.zones.map((node) => (
                        <NodeCard
                          key={node.device.deviceId}
                          node={node}
                          selectedDeviceId={selectedDeviceId}
                          onInspect={onInspect}
                        />
                      ))}
                    </div>
                  ) : zoneEmptyFallback}
                </div>
              </div>

              <div className="grid grid-cols-2 gap-6 px-3">
                <LoopBusBar
                  tone="condenser"
                  active={schematic.coolingFlowActive}
                  direction="right"
                  position="bottom"
                  metrics={condenserBottomMetrics}
                />
                <LoopBusBar
                  tone="chilled"
                  active={schematic.chilledFlowActive}
                  direction="left"
                  position="bottom"
                  metrics={chilledBottomMetrics}
                />
              </div>
            </>
          ) : (
            <>
              <div className="px-3">
                <LoopBusBar
                  tone="chilled"
                  active={schematic.chilledFlowActive}
                  direction="right"
                  position="top"
                  metrics={chilledTopMetrics}
                />
              </div>
              <div className="grid grid-cols-[minmax(0,1.15fr)_24px_minmax(0,1fr)_24px_minmax(0,1fr)] items-stretch gap-1">
                <StageColumn
                  title="冷水机组"
                  nodes={schematic.chillers}
                  selectedDeviceId={selectedDeviceId}
                  onInspect={onInspect}
                  accent="chiller-bridge"
                  footer={weatherFooter}
                />
                <PipeSegmentConnector tone="chilled" active={schematic.chilledFlowActive} direction="right" />
                <StageColumn
                  title="冷冻水泵"
                  nodes={schematic.chilledPumps}
                  selectedDeviceId={selectedDeviceId}
                  onInspect={onInspect}
                />
                <PipeSegmentConnector tone="chilled" active={schematic.chilledFlowActive} direction="right" />
                <div className="flex min-w-0 flex-col gap-1.5">
                  <span className="px-0.5 text-[11px] font-medium text-muted-foreground">末端负荷</span>
                  {schematic.zones.length > 0 ? (
                    <div className="flex flex-col gap-1.5">
                      {schematic.zones.map((node) => (
                        <NodeCard
                          key={node.device.deviceId}
                          node={node}
                          selectedDeviceId={selectedDeviceId}
                          onInspect={onInspect}
                        />
                      ))}
                    </div>
                  ) : zoneEmptyFallback}
                </div>
              </div>
              <div className="px-3">
                <LoopBusBar
                  tone="chilled"
                  active={schematic.chilledFlowActive}
                  direction="left"
                  position="bottom"
                  metrics={chilledBottomMetrics}
                />
              </div>
            </>
          )}
        </div>

        {/* Stacked loop layout for narrow containers (e.g., 1280px with AI Assistant open) */}
        <div className="flex flex-col gap-3 @4xl/main:hidden">
          {schematic.hasCondenserLoop ? (
            <div className="flex flex-col gap-2.5 rounded-lg border border-divider bg-card/70 p-3">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <span className="inline-flex items-center gap-1.5 text-xs font-semibold">
                  <i className={cn('size-2 rounded-full', schematic.coolingFlowActive ? 'bg-chart-2' : 'bg-chart-inactive')} aria-hidden="true" />
                  冷却水环路
                </span>
                <div className="flex flex-wrap items-center gap-1.5">
                  {[...condenserTopMetrics, ...condenserBottomMetrics].map((metric) => (
                    <LoopPipeBadge key={metric.label} label={metric.label} reading={metric.reading} />
                  ))}
                </div>
              </div>
              <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
                <StageColumn
                  title="冷却塔"
                  nodes={schematic.towers}
                  selectedDeviceId={selectedDeviceId}
                  onInspect={onInspect}
                  footer={weatherFooter}
                />
                <StageColumn
                  title="冷却水泵"
                  nodes={schematic.coolingPumps}
                  selectedDeviceId={selectedDeviceId}
                  onInspect={onInspect}
                />
                <StageColumn
                  title="冷水机组"
                  nodes={schematic.chillers}
                  selectedDeviceId={selectedDeviceId}
                  onInspect={onInspect}
                  accent="chiller-bridge"
                />
              </div>
            </div>
          ) : null}

          <div className="flex flex-col gap-2.5 rounded-lg border border-divider bg-card/70 p-3">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <span className="inline-flex items-center gap-1.5 text-xs font-semibold">
                <i className={cn('size-2 rounded-full', schematic.chilledFlowActive ? 'bg-chart-1' : 'bg-chart-inactive')} aria-hidden="true" />
                冷冻水环路
              </span>
              <div className="flex flex-wrap items-center gap-1.5">
                {[...chilledTopMetrics, ...chilledBottomMetrics].map((metric) => (
                  <LoopPipeBadge
                    key={metric.label}
                    label={metric.label}
                    reading={metric.reading}
                    highlight={'highlight' in metric && Boolean(metric.highlight)}
                  />
                ))}
              </div>
            </div>
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
              {!schematic.hasCondenserLoop ? (
                <StageColumn
                  title="冷水机组"
                  nodes={schematic.chillers}
                  selectedDeviceId={selectedDeviceId}
                  onInspect={onInspect}
                  accent="chiller-bridge"
                  footer={weatherFooter}
                />
              ) : null}
              <StageColumn
                title="冷冻水泵"
                nodes={schematic.chilledPumps}
                selectedDeviceId={selectedDeviceId}
                onInspect={onInspect}
              />
              <div className="flex min-w-0 flex-col gap-1.5">
                <span className="px-0.5 text-[11px] font-medium text-muted-foreground">末端负荷</span>
                {schematic.zones.length > 0 ? (
                  <div className="flex flex-col gap-1.5">
                    {schematic.zones.map((node) => (
                      <NodeCard
                        key={node.device.deviceId}
                        node={node}
                        selectedDeviceId={selectedDeviceId}
                        onInspect={onInspect}
                      />
                    ))}
                  </div>
                ) : zoneEmptyFallback}
              </div>
            </div>
          </div>
        </div>
      </div>
    </Panel>
  );
}
