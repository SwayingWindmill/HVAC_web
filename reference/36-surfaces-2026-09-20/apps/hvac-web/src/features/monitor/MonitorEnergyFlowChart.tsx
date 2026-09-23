import { useEffect, useMemo, useRef } from 'react';
import { Graph } from '@antv/x6';
import { Button } from '@/components/ui/button';

interface MonitorEnergyFlowZone {
  readonly key: string;
  readonly label: string;
}

interface MonitorEnergyFlowChartProps {
  readonly coolingValue: number;
  readonly unit: string;
  readonly pumpLabel: string;
  readonly terminalLabel: string;
  readonly zones?: readonly MonitorEnergyFlowZone[];
  readonly onOpenChiller?: () => void;
  readonly onOpenPump?: () => void;
  readonly onOpenTerminal: (zoneKey?: string) => void;
}

function formatFlow(value: number, unit: string): string {
  return `${value.toLocaleString('zh-CN', { maximumFractionDigits: 1 })} ${unit}`;
}

export function MonitorEnergyFlowChart({
  coolingValue,
  unit,
  pumpLabel,
  terminalLabel,
  zones = [],
  onOpenChiller,
  onOpenPump,
  onOpenTerminal,
}: MonitorEnergyFlowChartProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const callbacksRef = useRef({ onOpenChiller, onOpenPump, onOpenTerminal });
  callbacksRef.current = { onOpenChiller, onOpenPump, onOpenTerminal };
  const flowValue = formatFlow(coolingValue, unit);

  const model = useMemo(() => {
    const visibleZones = zones.slice(0, 5);
    const hasZones = visibleZones.length > 0;
    const nodes = [
      {
        id: 'chiller-output',
        x: 26,
        y: 126,
        width: 188,
        height: 82,
        label: '冷机群输出',
        detail: flowValue,
        action: 'chiller',
        actionable: Boolean(onOpenChiller),
      },
      {
        id: 'chw-transport',
        x: 286,
        y: 126,
        width: 188,
        height: 82,
        label: '冷冻水输配',
        detail: pumpLabel,
        action: 'pump',
        actionable: Boolean(onOpenPump),
      },
      hasZones
        ? {
            id: 'terminal-distribution',
            x: 546,
            y: 126,
            width: 188,
            height: 82,
            label: '末端区域分配',
            detail: '分区冷量待计量',
            action: 'terminal',
            actionable: true,
          }
        : {
            id: 'terminal-load',
            x: 546,
            y: 126,
            width: 188,
            height: 82,
            label: '空调末端',
            detail: terminalLabel,
            action: 'terminal',
            actionable: true,
          },
      ...visibleZones.map((zone, index) => ({
        id: `zone-${zone.key}`,
        x: 786,
        y: 22 + index * 66,
        width: 170,
        height: 56,
        label: zone.label,
        detail: '分项冷量未提供',
        action: 'zone',
        actionable: true,
        zoneKey: zone.key,
      })),
    ];
    const edges = [
      { source: 'chiller-output', target: 'chw-transport', label: flowValue, measured: true },
      { source: 'chw-transport', target: hasZones ? 'terminal-distribution' : 'terminal-load', label: flowValue, measured: true },
      ...visibleZones.map((zone) => ({ source: 'terminal-distribution', target: `zone-${zone.key}`, label: '', measured: false })),
    ];
    return { nodes, edges, hasZones };
  }, [flowValue, onOpenChiller, onOpenPump, pumpLabel, terminalLabel, zones]);

  useEffect(() => {
    const container = containerRef.current;
    if (!container) return undefined;

    const graph = new Graph({
      container,
      width: Math.max(container.clientWidth, model.hasZones ? 980 : 760),
      height: model.hasZones ? 360 : 270,
      background: { color: 'transparent' },
      grid: false,
      panning: false,
      mousewheel: false,
      interacting: false,
    });

    model.nodes.forEach((node) => {
      graph.addNode({
        id: node.id,
        x: node.x,
        y: node.y,
        width: node.width,
        height: node.height,
        shape: 'rect',
        data: { action: node.action, zoneKey: 'zoneKey' in node ? node.zoneKey : undefined, actionable: node.actionable },
        attrs: {
          body: {
            rx: 8,
            ry: 8,
            fill: 'var(--card)',
            stroke: 'var(--border)',
            strokeWidth: 1,
            cursor: node.actionable ? 'pointer' : 'default',
          },
          label: {
            text: `${node.label}\n${node.detail}`,
            fill: 'var(--foreground)',
            fontSize: 13,
            fontWeight: 600,
            cursor: node.actionable ? 'pointer' : 'default',
            textWrap: { width: node.width - 20, height: node.height - 14, ellipsis: true },
          },
        },
      });
    });

    model.edges.forEach((edge) => {
      graph.addEdge({
        source: edge.source,
        target: edge.target,
        attrs: {
          line: {
            stroke: edge.measured ? 'var(--primary)' : 'var(--border)',
            strokeWidth: edge.measured ? 6 : 2,
            strokeDasharray: edge.measured ? undefined : '5 4',
            opacity: edge.measured ? 0.68 : 0.82,
            targetMarker: { name: 'block', width: 8, height: 8 },
          },
        },
        labels: edge.label ? [{ attrs: { label: { text: edge.label, fill: 'var(--muted-foreground)', fontSize: 12 } } }] : [],
      });
    });

    graph.on('node:click', ({ node }) => {
      const data = node.getData<{ action?: string; zoneKey?: string; actionable?: boolean }>();
      if (!data?.actionable) return;
      node.attr('body/stroke', 'var(--primary)');
      node.attr('body/strokeWidth', 2);
      if (data.action === 'chiller') callbacksRef.current.onOpenChiller?.();
      if (data.action === 'pump') callbacksRef.current.onOpenPump?.();
      if (data.action === 'terminal') callbacksRef.current.onOpenTerminal();
      if (data.action === 'zone') callbacksRef.current.onOpenTerminal(data.zoneKey);
    });

    return () => graph.dispose();
  }, [model]);

  return (
    <div className="hvac-monitor__g6-flow" aria-label={zones.length > 0 ? `冷量流向：冷机群输出 ${flowValue}，经冷冻水输配到 ${zones.length} 个末端区域；分区冷量未计量` : `冷量流向：冷机群输出 ${flowValue}，经冷冻水输配到空调末端`}>
      <div ref={containerRef} className="hvac-monitor__g6-flow-canvas" data-testid="hvac-x6-energy-flow" />
      <div className="hvac-monitor__g6-flow-actions" aria-label="能流节点操作">
        <span>查看节点</span>
        <div className="flex flex-wrap items-center gap-1">
          {onOpenChiller ? <Button variant="link" size="sm" onClick={onOpenChiller}>冷机群</Button> : null}
          {onOpenPump ? <Button variant="link" size="sm" onClick={onOpenPump}>冷冻水输配</Button> : null}
          <Button variant="link" size="sm" onClick={() => onOpenTerminal()}>空调末端</Button>
          {zones.slice(0, 5).map((zone) => <Button key={zone.key} variant="link" size="sm" onClick={() => onOpenTerminal(zone.key)}>{zone.label}</Button>)}
        </div>
      </div>
    </div>
  );
}
