import { memo, useMemo } from 'react';
import { Panel, PanelHeader, PanelLink } from '@/blocks/panel';
import { cn } from '@/lib/utils';
import { ReadingValue } from '../device-presentation';
import { buildHydraulicSchematic } from '../hydraulic-schematic-model';
import { type PlantView } from '../plant-model';
import {
  AnomalyStrip,
  LoopBusBar,
  LoopPipeBadge,
  NodeCard,
  PipeSegmentConnector,
  StageColumn,
  WeatherFootnote,
} from './HydraulicSchematicPrimitives';

export const PlantHydraulicSchematic = memo(function PlantHydraulicSchematic({
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
                          key={node.device.unitId}
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
                        key={node.device.unitId}
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
});
