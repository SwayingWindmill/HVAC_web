import { useEffect, useMemo, useRef } from 'react';
import { Graph } from '@antv/x6';
import { Button } from '@/components/ui/button';
import type { MonitorDevice } from './model';

interface MonitorLinkageGraphProps {
  readonly devices: readonly MonitorDevice[];
  readonly selectedDeviceId: string | null;
  readonly onOpenPlant: () => void;
  readonly onOpenTerminal: () => void;
}

type LinkageGroupKey = 'chiller' | 'chilled-pump' | 'cooling-pump' | 'tower' | 'terminal';

interface LinkageGroup {
  readonly key: LinkageGroupKey;
  readonly label: string;
  readonly devices: readonly MonitorDevice[];
  readonly action: 'plant' | 'terminal';
  readonly x: number;
  readonly y: number;
}

function groupDetail(group: LinkageGroup): string {
  const total = group.devices.length;
  if (total === 0) return '当前未登记设备';
  const online = group.devices.filter((item) => item.state.connection.state === 'ONLINE').length;
  const alarms = group.devices.reduce((sum, item) => sum + item.activeAlarms.length, 0);
  const state = `${online}/${total} 在线`;
  return alarms > 0 ? `${state} · ${alarms} 条活动告警` : state;
}

export function MonitorLinkageGraph({ devices, selectedDeviceId, onOpenPlant, onOpenTerminal }: MonitorLinkageGraphProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const callbacksRef = useRef({ onOpenPlant, onOpenTerminal });
  callbacksRef.current = { onOpenPlant, onOpenTerminal };

  const groups = useMemo<LinkageGroup[]>(() => [
    { key: 'tower', label: '冷却塔', devices: devices.filter((item) => item.kind === 'tower'), action: 'plant', x: 26, y: 250 },
    { key: 'cooling-pump', label: '冷却水泵', devices: devices.filter((item) => item.kind === 'cooling-pump'), action: 'plant', x: 266, y: 250 },
    { key: 'chiller', label: '冷水机组', devices: devices.filter((item) => item.kind === 'chiller'), action: 'plant', x: 266, y: 62 },
    { key: 'chilled-pump', label: '冷冻水泵', devices: devices.filter((item) => item.kind === 'chilled-pump'), action: 'plant', x: 516, y: 62 },
    { key: 'terminal', label: '空调末端', devices: devices.filter((item) => item.kind === 'terminal'), action: 'terminal', x: 766, y: 62 },
  ], [devices]);

  const model = useMemo(() => ({
    nodes: groups.map((group) => {
      const alarmCount = group.devices.reduce((sum, item) => sum + item.activeAlarms.length, 0);
      const selected = Boolean(selectedDeviceId && group.devices.some((item) => item.device.id === selectedDeviceId));
      return {
        ...group,
        detail: groupDetail(group),
        alarmCount,
        selected,
      };
    }),
    edges: [
      { source: 'tower', target: 'cooling-pump', label: '冷却水侧' },
      { source: 'cooling-pump', target: 'chiller', label: '冷却水侧' },
      { source: 'chiller', target: 'chilled-pump', label: '冷冻水侧' },
      { source: 'chilled-pump', target: 'terminal', label: '冷冻水侧' },
    ],
  }), [groups, selectedDeviceId]);

  useEffect(() => {
    const container = containerRef.current;
    if (!container) return undefined;

    const graph = new Graph({
      container,
      width: Math.max(container.clientWidth, 980),
      height: 390,
      background: { color: 'transparent' },
      grid: false,
      panning: false,
      mousewheel: false,
      interacting: false,
    });

    model.nodes.forEach((node) => {
      const alert = node.alarmCount > 0;
      graph.addNode({
        id: node.key,
        x: node.x,
        y: node.y,
        width: 194,
        height: 78,
        shape: 'rect',
        data: { action: node.action },
        attrs: {
          body: {
            rx: 8,
            ry: 8,
            fill: node.selected ? 'color-mix(in oklch, var(--primary) 9%, var(--card))' : alert ? 'color-mix(in oklch, var(--destructive) 7%, var(--card))' : 'var(--card)',
            stroke: node.selected ? 'var(--primary)' : alert ? 'color-mix(in oklch, var(--destructive) 40%, var(--border))' : 'var(--border)',
            strokeWidth: node.selected ? 2 : 1,
            cursor: 'pointer',
          },
          label: {
            text: `${node.label}\n${node.detail}`,
            fill: 'var(--foreground)',
            fontSize: 13,
            fontWeight: 600,
            cursor: 'pointer',
            textWrap: { width: 174, height: 62, ellipsis: true },
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
            stroke: 'color-mix(in oklch, var(--primary) 55%, var(--border))',
            strokeWidth: 3,
            opacity: 0.82,
            targetMarker: { name: 'block', width: 8, height: 8 },
          },
        },
        labels: [{ attrs: { label: { text: edge.label, fill: 'var(--muted-foreground)', fontSize: 11 } } }],
      });
    });

    graph.on('node:click', ({ node }) => {
      const data = node.getData<{ action?: 'plant' | 'terminal' }>();
      node.attr('body/stroke', 'var(--primary)');
      node.attr('body/strokeWidth', 2);
      if (data?.action === 'plant') callbacksRef.current.onOpenPlant();
      if (data?.action === 'terminal') callbacksRef.current.onOpenTerminal();
    });

    return () => graph.dispose();
  }, [model]);

  return (
    <div className="hvac-monitor__linkage-graph" aria-label="HVAC 设备联动关系：冷却塔、冷却水泵、冷水机组、冷冻水泵与空调末端">
      <div ref={containerRef} className="hvac-monitor__linkage-graph-canvas" data-testid="hvac-x6-linkage-graph" />
      <div className="hvac-monitor__linkage-graph-actions">
        <span>系统链路</span>
        <div className="flex flex-wrap items-center gap-1">
          <Button variant="link" size="sm" onClick={onOpenPlant}>冷源系统</Button>
          <Button variant="link" size="sm" onClick={onOpenTerminal}>空调末端</Button>
        </div>
      </div>
    </div>
  );
}
