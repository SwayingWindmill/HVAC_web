import { useEffect, useMemo, useRef } from 'react';
import { Graph } from '@antv/x6';
import { register } from '@antv/x6-react-shape';
import type { DashboardOverview } from '@/api/dashboard-overview';
import { AHU_ART, CHILLED_PUMP_ART, CHILLER_ART, COOLING_PUMP_ART, FCU_ART, TOWER_ART } from './reference-equipment-art';
import { deviceStateLabel, monitorRunState, pointMetric, type MonitorDevice } from './model';

type TopologyKind = 'tower' | 'cooling-pump' | 'chilled-pump' | 'chiller' | 'ahu' | 'fcu';

interface TopologyNodeData {
  readonly kind: TopologyKind;
  readonly label: string;
  readonly countText: string;
  readonly status: 'running' | 'idle' | 'fault' | 'unknown';
  readonly statusText: string;
  readonly metrics: readonly string[];
  readonly deviceId?: string;
  readonly zoneKey?: string;
  readonly selected?: boolean;
  readonly alarmCount?: number;
  readonly onActivate?: () => void;
}

export interface MonitorTopologyZone {
  readonly key: string;
  readonly label: string;
}

interface MonitorX6TopologyProps {
  readonly overview: DashboardOverview | undefined;
  readonly devices: readonly MonitorDevice[];
  readonly zones?: readonly MonitorTopologyZone[];
  readonly selectedDeviceId: string | null;
  readonly selectedZoneKey?: string | null;
  readonly anomalyMode?: boolean;
  readonly onOpenDevice: (deviceId: string) => void;
  readonly onOpenPlant: () => void;
  readonly onOpenTerminal: (zoneKey?: string) => void;
}

let registered = false;

const equipmentArtByKind: Readonly<Record<TopologyKind, string>> = {
  tower: TOWER_ART,
  'cooling-pump': COOLING_PUMP_ART,
  'chilled-pump': CHILLED_PUMP_ART,
  chiller: CHILLER_ART,
  ahu: AHU_ART,
  fcu: FCU_ART,
};

function X6EquipmentNode({ node }: { readonly node: { getData: <T>() => T } }) {
  const data = node.getData<TopologyNodeData>();
  return (
    <div
      className={`hvac-x6-node hvac-x6-node--${data.kind} is-${data.status}${data.selected ? ' is-selected' : ''}`}
      data-device-id={data.deviceId}
      data-zone-key={data.zoneKey}
      role={data.onActivate ? 'button' : undefined}
      tabIndex={data.onActivate ? 0 : undefined}
      onClick={(event) => { event.stopPropagation(); data.onActivate?.(); }}
      onKeyDown={(event) => {
        if (!data.onActivate || (event.key !== 'Enter' && event.key !== ' ')) return;
        event.preventDefault();
        data.onActivate();
      }}
    >
      <div className="hvac-x6-node__art"><img src={equipmentArtByKind[data.kind]} alt="" aria-hidden="true" draggable={false} /></div>
      <div className="hvac-x6-node__copy">
        <div className="hvac-x6-node__title"><strong>{data.label}</strong><b className="hvac-x6-node__count">{data.countText}</b>{data.alarmCount ? <b className="hvac-x6-node__alarm">{data.alarmCount}</b> : null}</div>
        <span className="hvac-x6-node__state"><i />{data.statusText}</span>
        <div className="hvac-x6-node__metrics">
          {data.metrics.map((metric) => <small key={metric}>{metric}</small>)}
        </div>
      </div>
    </div>
  );
}

function ensureRegistered() {
  if (registered) return;
  register({
    shape: 'hvac-monitor-equipment',
    width: 220,
    height: 128,
    component: X6EquipmentNode,
    effect: ['data'],
  });
  registered = true;
}

function equipmentStatus(item: MonitorDevice | undefined): TopologyNodeData['status'] {
  const run = item ? monitorRunState(item.state.points) : null;
  if (run === 'FAULT') return 'fault';
  if (run === 'RUNNING') return 'running';
  if (run === 'STOPPED') return 'idle';
  return 'unknown';
}

function aggregateStatus(summary: DashboardOverview['topology'][number] | undefined): TopologyNodeData['status'] {
  if (summary?.running == null || summary.total == null || summary.total <= 0) return 'unknown';
  return summary.running > 0 ? 'running' : 'idle';
}

function statusText(item: MonitorDevice | undefined, summary: DashboardOverview['topology'][number] | undefined): string {
  const run = item ? monitorRunState(item.state.points) : null;
  if (run === 'RUNNING') return '运行';
  if (run === 'STOPPED') return '停机';
  if (run === 'FAULT') return '故障';
  if (summary?.running != null && summary.total != null) return `运行 ${summary.running} / ${summary.total} 台`;
  return item ? deviceStateLabel(item.state) : '状态待确认';
}

function countText(summary: DashboardOverview['topology'][number] | undefined, devices: readonly MonitorDevice[]): string {
  const total = summary?.total ?? devices.length;
  return total > 0 ? `${total} 台` : '—';
}

function metricFor(item: MonitorDevice | undefined, label: string, keys: readonly string[]): string {
  if (!item) return `${label} —`;
  return `${label} ${pointMetric(item.state.points, keys)}`;
}

function numericTemperature(value: number | null | undefined): string {
  return value == null ? '—' : `${value.toFixed(1)} °C`;
}

export function MonitorX6Topology({ overview, devices, zones = [], selectedDeviceId, selectedZoneKey = null, anomalyMode = false, onOpenDevice, onOpenPlant, onOpenTerminal }: MonitorX6TopologyProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const callbacksRef = useRef({ onOpenDevice, onOpenPlant, onOpenTerminal });
  callbacksRef.current = { onOpenDevice, onOpenPlant, onOpenTerminal };

  const model = useMemo(() => {
    const topology = (key: string) => overview?.topology.find((item) => item.key === key);
    const text = (item: MonitorDevice) => `${item.device.code} ${item.device.displayName} ${item.device.deviceType}`.toUpperCase();
    const terminalDevices = devices.filter((item) => item.kind === 'terminal');
    const ahuDevices = terminalDevices.filter((item) => /AHU|AIR[_ -]?HANDLING|空调箱/.test(text(item)));
    const fcuDevices = terminalDevices.filter((item) => /FCU|FAN[_ -]?COIL|风机盘管/.test(text(item)));
    return {
      towerSummary: topology('cooling-tower'),
      cwpSummary: topology('cw-pump'),
      chillerSummary: topology('chiller'),
      chwpSummary: topology('chw-pump'),
      ahuSummary: topology('ahu'),
      fcuSummary: topology('vav-fcu'),
      towerDevices: devices.filter((item) => item.kind === 'tower'),
      cwpDevices: devices.filter((item) => item.kind === 'cooling-pump'),
      chillerDevices: devices.filter((item) => item.kind === 'chiller'),
      chwpDevices: devices.filter((item) => item.kind === 'chilled-pump'),
      ahuDevices,
      fcuDevices,
      zone: zones[0],
    };
  }, [devices, overview, zones]);

  useEffect(() => {
    const container = containerRef.current;
    if (!container) return undefined;
    ensureRegistered();

    const graphWidth = Math.max(container.clientWidth, 980);
    const graph = new Graph({
      container,
      width: graphWidth,
      height: 463,
      background: { color: 'transparent' },
      grid: false,
      panning: false,
      mousewheel: false,
      interacting: {
        nodeMovable: false,
        edgeMovable: false,
        edgeLabelMovable: false,
        arrowheadMovable: false,
        vertexMovable: false,
        magnetConnectable: false,
      },
    });

    const addEquipment = (id: string, data: TopologyNodeData, x: number, y: number, width = 220, height = 126) => {
      graph.addNode({ id, x, y, width, height, zIndex: 3, shape: 'hvac-monitor-equipment', data });
    };

    const chiller = model.chillerDevices[0];
    const chwp = model.chwpDevices[0];
    const cwp = model.cwpDevices[0];
    const tower = model.towerDevices[0];
    const ahu = model.ahuDevices[0];
    const fcu = model.fcuDevices[0];
    const coolingOutput = overview?.kpis.totalLoadKW ?? overview?.loadSummary.currentKW ?? null;

    addEquipment('chiller', {
      kind: 'chiller',
      label: '冷机组',
      countText: countText(model.chillerSummary, model.chillerDevices),
      status: chiller ? equipmentStatus(chiller) : aggregateStatus(model.chillerSummary),
      statusText: statusText(chiller, model.chillerSummary),
      metrics: [
        `冷负荷 ${coolingOutput != null ? `${Math.round(coolingOutput).toLocaleString('zh-CN')} kW` : '—'}`,
        `出水温度 ${numericTemperature(overview?.waterTemperatures.supplyC)}`,
        `输入功率 ${model.chillerSummary?.powerKW != null ? `${model.chillerSummary.powerKW.toFixed(0)} kW` : '—'}`,
        chiller ? metricFor(chiller, 'COP', ['COP', 'cop']) : 'COP —',
      ],
      deviceId: chiller?.device.id,
      selected: chiller?.device.id === selectedDeviceId,
      alarmCount: chiller?.activeAlarms.length,
      onActivate: () => chiller ? callbacksRef.current.onOpenDevice(chiller.device.id) : callbacksRef.current.onOpenPlant(),
    }, 96, 69, 216, 146);

    addEquipment('chwp', {
      kind: 'chilled-pump',
      label: '冷冻水泵',
      countText: countText(model.chwpSummary, model.chwpDevices),
      status: chwp ? equipmentStatus(chwp) : aggregateStatus(model.chwpSummary),
      statusText: statusText(chwp, model.chwpSummary),
      metrics: [
        metricFor(chwp, '流量', ['流量', 'flow']),
        `输入功率 ${model.chwpSummary?.powerKW != null ? `${model.chwpSummary.powerKW.toFixed(0)} kW` : '—'}`,
        metricFor(chwp, '扬程', ['扬程', '压差', 'head']),
        metricFor(chwp, '频率', ['频率', 'frequency']),
      ],
      deviceId: chwp?.device.id,
      selected: chwp?.device.id === selectedDeviceId,
      alarmCount: chwp?.activeAlarms.length,
      onActivate: () => chwp ? callbacksRef.current.onOpenDevice(chwp.device.id) : callbacksRef.current.onOpenPlant(),
    }, 360, 69, 210, 146);

    addEquipment('ahu', {
      kind: 'ahu',
      label: 'AHU',
      countText: countText(model.ahuSummary, model.ahuDevices),
      status: ahu ? equipmentStatus(ahu) : aggregateStatus(model.ahuSummary),
      statusText: statusText(ahu, model.ahuSummary),
      metrics: [
        metricFor(ahu, '送风温度', ['送风温度', 'supply_air_temperature']),
        metricFor(ahu, '风量', ['风量', 'airflow']),
        `输入功率 ${model.ahuSummary?.powerKW != null ? `${model.ahuSummary.powerKW.toFixed(0)} kW` : '—'}`,
        metricFor(ahu, '负荷率', ['负荷率', 'load']),
      ],
      deviceId: ahu?.device.id,
      zoneKey: model.zone?.key,
      selected: ahu?.device.id === selectedDeviceId,
      alarmCount: ahu?.activeAlarms.length,
      onActivate: () => ahu ? callbacksRef.current.onOpenDevice(ahu.device.id) : callbacksRef.current.onOpenTerminal(model.zone?.key),
    }, 640, 18, 264, 126);

    addEquipment('fcu', {
      kind: 'fcu',
      label: 'FCU',
      countText: countText(model.fcuSummary, model.fcuDevices),
      status: fcu ? equipmentStatus(fcu) : aggregateStatus(model.fcuSummary),
      statusText: statusText(fcu, model.fcuSummary),
      metrics: [
        metricFor(fcu, '送风温度', ['送风温度', 'supply_air_temperature']),
        metricFor(fcu, '风量', ['风量', 'airflow']),
        `输入功率 ${model.fcuSummary?.powerKW != null ? `${model.fcuSummary.powerKW.toFixed(0)} kW` : '—'}`,
        metricFor(fcu, '负荷率', ['负荷率', 'load']),
      ],
      deviceId: fcu?.device.id,
      zoneKey: model.zone?.key,
      selected: fcu?.device.id === selectedDeviceId || model.zone?.key === selectedZoneKey,
      alarmCount: fcu?.activeAlarms.length,
      onActivate: () => fcu ? callbacksRef.current.onOpenDevice(fcu.device.id) : callbacksRef.current.onOpenTerminal(model.zone?.key),
    }, 640, 159, 264, 126);

    addEquipment('cwp', {
      kind: 'cooling-pump',
      label: '冷却水泵',
      countText: countText(model.cwpSummary, model.cwpDevices),
      status: cwp ? equipmentStatus(cwp) : aggregateStatus(model.cwpSummary),
      statusText: statusText(cwp, model.cwpSummary),
      metrics: [
        metricFor(cwp, '流量', ['流量', 'flow']),
        `输入功率 ${model.cwpSummary?.powerKW != null ? `${model.cwpSummary.powerKW.toFixed(0)} kW` : '—'}`,
        metricFor(cwp, '扬程', ['扬程', '压差', 'head']),
        metricFor(cwp, '频率', ['频率', 'frequency']),
      ],
      deviceId: cwp?.device.id,
      selected: cwp?.device.id === selectedDeviceId,
      alarmCount: cwp?.activeAlarms.length,
      onActivate: () => cwp ? callbacksRef.current.onOpenDevice(cwp.device.id) : callbacksRef.current.onOpenPlant(),
    }, 256, 312, 248, 132);

    addEquipment('tower', {
      kind: 'tower',
      label: '冷却塔',
      countText: countText(model.towerSummary, model.towerDevices),
      status: tower ? equipmentStatus(tower) : aggregateStatus(model.towerSummary),
      statusText: statusText(tower, model.towerSummary),
      metrics: [
        `送水温度 ${numericTemperature(overview?.coolingWaterTemperatures?.supplyC)}`,
        `出水温度 ${numericTemperature(overview?.coolingWaterTemperatures?.returnC)}`,
        `风机功率 ${model.towerSummary?.powerKW != null ? `${model.towerSummary.powerKW.toFixed(0)} kW` : '—'}`,
        metricFor(tower, '负荷率', ['负荷率', 'load']),
      ],
      deviceId: tower?.device.id,
      selected: tower?.device.id === selectedDeviceId,
      alarmCount: tower?.activeAlarms.length,
      onActivate: () => tower ? callbacksRef.current.onOpenDevice(tower.device.id) : callbacksRef.current.onOpenPlant(),
    }, 600, 312, 302, 132);

    const addEdge = (id: string, source: { x: number; y: number }, target: { x: number; y: number }, vertices: { x: number; y: number }[], color: string, label?: string, position = 0.5) => {
      graph.addEdge({
        id,
        zIndex: 1,
        source,
        target,
        vertices,
        attrs: { line: { stroke: color, strokeWidth: 3.1, strokeLinecap: 'round', strokeLinejoin: 'round', targetMarker: { name: 'block', width: 8, height: 6 } } },
        labels: label ? [{ position, attrs: { label: { text: label, fill: color, fontSize: 12, fontWeight: 650 }, body: { fill: '#f8fbff', stroke: 'none', rx: 3, ry: 3 } } }] : undefined,
      });
    };

    const chilled = '#1677ff';
    const cooling = '#12a86b';
    const chilledSupply = overview?.waterTemperatures.supplyC != null ? `${overview.waterTemperatures.supplyC.toFixed(1)} °C（供水）` : '冷冻水供水';
    const chilledReturn = overview?.waterTemperatures.returnC != null ? `${overview.waterTemperatures.returnC.toFixed(1)} °C（回水）` : '冷冻水回水';
    const coolingSupply = overview?.coolingWaterTemperatures?.supplyC != null ? `${overview.coolingWaterTemperatures.supplyC.toFixed(1)} °C` : '冷却水供水';
    const coolingReturn = overview?.coolingWaterTemperatures?.returnC != null ? `${overview.coolingWaterTemperatures.returnC.toFixed(1)} °C（回水）` : '冷却水回水';

    addEdge('chilled-chiller-pump', { x: 312, y: 142 }, { x: 360, y: 142 }, [], chilled, chilledSupply, 0.55);
    addEdge('chilled-pump-branch', { x: 570, y: 142 }, { x: 600, y: 142 }, [], chilled);
    addEdge('chilled-branch-ahu', { x: 600, y: 142 }, { x: 640, y: 80 }, [{ x: 600, y: 80 }], chilled);
    addEdge('chilled-branch-fcu', { x: 600, y: 142 }, { x: 640, y: 222 }, [{ x: 600, y: 222 }], chilled);
    addEdge('chilled-return-vertical', { x: 1015, y: 293 }, { x: 1015, y: 60 }, [], chilled);
    addEdge('chilled-return-ahu', { x: 1015, y: 60 }, { x: 904, y: 60 }, [], chilled, chilledReturn, 0.5);
    addEdge('chilled-return-fcu', { x: 1015, y: 206 }, { x: 904, y: 206 }, [], chilled);

    addEdge('cooling-chiller-pump', { x: 140, y: 215 }, { x: 256, y: 361 }, [{ x: 140, y: 361 }], cooling);
    addEdge('cooling-pump-tower', { x: 504, y: 361 }, { x: 600, y: 361 }, [], cooling, coolingSupply, 0.55);
    addEdge('cooling-return-vertical', { x: 1015, y: 293 }, { x: 1015, y: 361 }, [], cooling);
    addEdge('cooling-tower-return', { x: 1015, y: 361 }, { x: 902, y: 361 }, [], cooling, coolingReturn, 0.5);

    graph.on('node:mouseenter', () => { container.style.cursor = 'pointer'; });
    graph.on('node:mouseleave', () => { container.style.cursor = 'default'; });

    const resizeObserver = new ResizeObserver(() => {
      graph.resize(Math.max(container.clientWidth, 980), 463);
    });
    resizeObserver.observe(container);

    return () => {
      resizeObserver.disconnect();
      graph.dispose();
    };
  }, [anomalyMode, model, overview, selectedDeviceId, selectedZoneKey]);

  return (
    <div className={`hvac-x6-topology${anomalyMode ? ' is-anomaly' : ''}`} data-testid="hvac-x6-topology" aria-label="HVAC 系统交互拓扑">
      <div className="hvac-x6-system-label is-chilled"><strong>冷冻水系统</strong><span>（供冷）</span></div>
      <div className="hvac-x6-system-label is-cooling"><strong>冷却水系统</strong><span>（散热）</span></div>
      <div ref={containerRef} className="hvac-x6-topology__canvas" />
    </div>
  );
}
