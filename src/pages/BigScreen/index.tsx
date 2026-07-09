import { lazy, Suspense, useEffect, useState, type ComponentType } from 'react';
import { useNavigate } from 'react-router-dom';
import { Button, ConfigProvider, Typography, Switch, theme as antdTheme } from 'antd';
import {
  ThunderboltOutlined, ReloadOutlined,
  DollarOutlined, ExperimentOutlined,
  CheckCircleTwoTone, SafetyCertificateOutlined,
  ApiOutlined,
  BulbOutlined,
  FullscreenExitOutlined, DesktopOutlined,
  InfoCircleFilled, ExclamationCircleFilled,
  EnvironmentOutlined,
} from '@ant-design/icons';
import ReactECharts from 'echarts-for-react';
import * as echarts from 'echarts';
import { useTelemetryLive, MOCK_DEVICES } from '@/api';

/* ──────────── lazy-load 3D so three/drei ships only on bigscreen route ──────────── */
const System3D = lazy(() => import('./System3D'));

/* ════════════════ DESIGN TOKENS (forced dark navy command-center) ════════════════ */
/* Reference-image derived palette: PRIMARY accent = ACCENT, NOT teal */
const BG       = '#080c18';
const PANEL_BG = '#0d1424';
const PANEL_BD = '#1a2744';
const TEXT     = '#eaf0fb';
const DIM      = '#7a8baa';

/* Primary accent — the dominant blue used everywhere in the reference image */
const ACCENT   = '#3b82f6';   /* main blue: KPI icons, device cards, links, borders */
const ACCENT_DK= '#2563eb';   /* darker blue: secondary accents, darkened elements */

/* Semantic status colors */
const AMBER    = '#f5a623';
const RED      = '#ef4444';
const GREEN    = '#22c55e';
const RADIUS   = 12; /* shape consistency lock */

const hexA = (hex: string, a: number) => {
  const h = hex.replace('#', '');
  const r = parseInt(h.slice(0, 2), 16);
  const g = parseInt(h.slice(2, 4), 16);
  const b = parseInt(h.slice(4, 6), 16);
  return `rgba(${r},${g},${b},${a})`;
};

/* ═════════════════ REFERENCE-DERIVED DATA ═════════════════ */
/* All numbers sourced directly from the reference screenshot */

type KpiItem = {
  icon: ComponentType<any>;
  label: string;
  value: number;
  unit: string;
  sub: string;
  color: string;
  trend?: number;
  suffix?: string;
  decimals?: number;
  money?: boolean;
  deltaVal?: number;
  deltaUnit?: string;
};

const KPI_DATA: KpiItem[] = [
  { icon: ThunderboltOutlined, label: '今日节电量', value: 12860, unit: 'kWh',
    sub: '较昨日', trend: -5.7, color: ACCENT },
  { icon: ReloadOutlined, label: '本月节能率', value: 18.6, unit: '%',
    sub: '较上月', trend: 2.3, suffix: '个百分点', color: ACCENT },
  { icon: DollarOutlined, label: '累计节省电费', value: 1256780, unit: '',
    sub: '较上月', money: true, deltaVal: 98540, deltaUnit: '', color: ACCENT },
  { icon: ExperimentOutlined, label: '冷站综合COP', value: 6.28, unit: '',
    sub: '较昨日', decimals: 2, trend: 0.16, color: ACCENT },
  { icon: CheckCircleTwoTone, label: '舒适度达标率', value: 98.6, unit: '%',
    sub: '较昨日', trend: 0.8, suffix: '个百分点', color: ACCENT },
  { icon: SafetyCertificateOutlined, label: '系统在线率', value: 99.7, unit: '%',
    sub: '较昨日', trend: -0.2, suffix: '百分点', color: ACCENT },
];

const DEVICE_STATS = [
  { name: '冷机', run: 2, total: 3, power: 320, cop: 6.35, load: 45.4, unit: 'kW' },
  { name: '冷却塔', run: 2, total: 3, power: 38, load: 10.7, unit: 'kW' },
  { name: '冷冻泵', run: 2, total: 3, power: 45.4, freq: 105, load: 201, unit: 'kW' },
  { name: '冷却泵', run: 2, total: 3, power: 30, load: 85, unit: 'kW' },
  { name: '末端', run: 48, total: 52, power: 75, load: 8.2, unit: 'kW' },
];

const HEALTH_SCORES = [
  { label: '冷机', value: 94 },
  { label: '冷冻泵', value: 90 },
  { label: '冷却塔', value: 91 },
  { label: '冷却泵', value: 88 },
  { label: '末端', value: 93 },
];

const DIAGNOSTICS = [
  { severity: 'error' as const, title: '冷媒液 T-3 电值偏高', time: '监测时间：09-30~08:15', action: '待处理' },
  { severity: 'warn' as const, title: '冷冻泵 P-2 频率异常', time: '监测时间：09-20~07:42', action: '已报警' },
  { severity: 'info' as const, title: '冷却塔 CT-1 排温值偏高', time: '监测时间：07-17~23:16', action: '已处理' },
];

const ALARM_PROGRESS = { total: 34, resolved: 29, processing: 3, overdue: 2, pct: 85 };

const SUGGESTIONS = [
  '降低冷冻水温度设定（建议调整至 7℃）',
  '调整冷却水泵运行数（建议减少 1 台）',
  '优化冷却塔启停策略（建议提前调整）',
];

const COMPOSITION = [
  { name: '制冷', pct: 46.3, kwh: 118760 },
  { name: '冷冻泵', pct: 20.1, kwh: 52430 },
  { name: '冷却泵', pct: 14.6, kwh: 40140 },
  { name: '冷却塔', pct: 10.7, kwh: 27860 },
  { name: '末端', pct: 8.3, kwh: 21520 },
];

/* ═════════════════ SUB-COMPONENTS ═════════════════ */

/* ---- tiny spinner for Suspense fallback ---- */
const Spinner = () => (
  <div style={{
    display: 'flex', alignItems: 'center', justifyContent: 'center',
    flex: 1, minHeight: 200, flexDirection: 'column', gap: 10,
  }}>
    <ReloadOutlined spin style={{ fontSize: 28, color: ACCENT }} />
    <span style={{ color: DIM, fontSize: 13 }}>加载 3D 场景</span>
  </div>
);

/* ---- KPI card (top row) ---- */
function KpiCard({ item }: { item: typeof KPI_DATA[number] }) {
  const Icon = item.icon;
  const valStr = item.money
    ? `${item.value >= 10000 ? '' : '¥'}${item.value.toLocaleString('zh-CN')}`
    : (typeof item.decimals === 'number' ? item.value.toFixed(item.decimals) : item.value.toLocaleString('zh-CN'));
  const displayValue = item.money ? `¥ ${valStr}` : `${valStr}${item.unit}`;

  let subText: string;
  if (item.money) {
    subText = `${item.sub} ↑${item.deltaVal?.toLocaleString() ?? ''}${item.deltaUnit}`;
  } else if (item.trend !== undefined) {
    const arrow = item.trend > 0 ? '↑' : '↓';
    subText = `${item.sub} ${arrow}${Math.abs(item.trend)}${item.suffix ?? '%'}`;
  } else {
    subText = item.sub;
  }
  /* Reference-img color rule: all "good" trends are GREEN; only the slight
     dip in 系统在线率 is flagged AMBER. */
  const trendColor = item.money
    ? GREEN
    : item.trend !== undefined
      ? (item.label.includes('在线率') ? AMBER : GREEN)
      : DIM;

  return (
    <div style={{
      flex: '1 1 0', minWidth: 140,
      background: PANEL_BG, border: `1px solid ${PANEL_BD}`, borderRadius: RADIUS,
      padding: '10px 14px', display: 'flex', gap: 10, alignItems: 'center',
    }}>
      <div style={{
        width: 36, height: 36, borderRadius: 8,
        background: hexA(item.color, 0.12), color: item.color,
        display: 'flex', alignItems: 'center', justifyContent: 'center',
        flexShrink: 0, fontSize: 17,
      }}>
        <Icon />
      </div>
      <div style={{ minWidth: 0 }}>
        <div style={{ color: DIM, fontSize: 12, marginBottom: 2 }}>{item.label}</div>
        <div style={{ color: TEXT, fontSize: 19, fontWeight: 700, lineHeight: 1.2, fontVariantNumeric: 'tabular-nums' }}>
          {displayValue}
        </div>
        <div style={{ color: trendColor, fontSize: 11 }}>{subText}</div>
      </div>
    </div>
  );
}

/* ---- Section panel container ---- */
function Panel({
  title, extra, children, style, accent,
}: {
  title: string; extra?: React.ReactNode;
  children: React.ReactNode; style?: React.CSSProperties; accent?: boolean;
}) {
  return (
    <div style={{
      flex: 1, minHeight: 0,
      background: PANEL_BG,
      border: `1px solid ${accent ? hexA(ACCENT, 0.45) : PANEL_BD}`,
      borderRadius: RADIUS,
      padding: '10px 12px',
      display: 'flex', flexDirection: 'column',
      ...(style || {}),
    }}>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 10 }}>
        <span style={{ color: TEXT, fontSize: 14, fontWeight: 600, letterSpacing: 0.3 }}>{title}</span>
        {extra}
      </div>
      {children}
    </div>
  );
}

/* ---- Device stat card (floating near 3D scene) ---- */
function DevCard({ d, style }: { d: typeof DEVICE_STATS[number]; style?: React.CSSProperties }) {
  return (
    <div style={{
      position: 'absolute', padding: '8px 12px', borderRadius: 8,
      background: hexA(BG, 0.88), backdropFilter: 'blur(10px)',
      border: `1px solid ${hexA(ACCENT, 0.3)}`,
      color: TEXT, fontSize: 11.5, whiteSpace: 'nowrap',
      boxShadow: `0 2px 16px ${hexA('#000', 0.5)}`,
      ...style,
    }}>
      <div style={{ color: ACCENT, fontWeight: 700, fontSize: 12, marginBottom: 3 }}>{d.name}</div>
      <div style={{ color: DIM, fontSize: 10.5, lineHeight: 1.6 }}>
        运行 <span style={{ color: TEXT }}>{d.run}</span> / {d.total} 台&nbsp;&nbsp;
        功率 <span style={{ color: TEXT }}>{d.power}{d.unit}</span>
        {d.cop && <><span style={{ margin: '0 4px' }}>|</span>COP <span style={{ color: ACCENT }}>{d.cop}</span></>}
        {d.freq && <><span style={{ margin: '0 4px' }}>|</span>频率 <span style={{ color: TEXT }}>{d.freq}Hz</span></>}
        <br />
        负载点比 <span style={{ color: d.load > 100 ? AMBER : ACCENT, fontWeight: 600 }}>{d.load}%</span>
      </div>
    </div>
  );
}

/* ---- Alarm item (diagnostics list) ---- */
function AlarmRow({ a }: { a: typeof DIAGNOSTICS[number] }) {
  const cfg = {
    error: { color: RED, IconComp: ExclamationCircleFilled },
    warn:  { color: AMBER, IconComp: ExclamationCircleFilled },
    info:  { color: ACCENT, IconComp: InfoCircleFilled },
  }[a.severity] ?? { color: DIM, IconComp: InfoCircleFilled };
  const IconComp = cfg.IconComp;
  const btnStyle = a.action === '待处理'
    ? { borderColor: hexA(AMBER, 0.6), color: AMBER }
    : a.action === '已报警'
      ? { borderColor: hexA(RED, 0.5), color: RED }
      : { borderColor: hexA(GREEN, 0.5), color: GREEN };
  return (
    <div style={{
      display: 'flex', alignItems: 'center', gap: 8, padding: '6px 0',
      borderBottom: `1px solid ${hexA(PANEL_BD, 0.4)}`,
    }}>
      <IconComp style={{ color: cfg.color, fontSize: 14, flexShrink: 0 }} />
      <div style={{ flex: 1, minWidth: 0 }}>
        <div style={{ color: TEXT, fontSize: 12 }}>{a.title}</div>
        <div style={{ color: DIM, fontSize: 10 }}>{a.time}</div>
      </div>
      <Button size="small" type="primary" ghost
        style={{ height: 22, fontSize: 10, paddingInline: 8, ...btnStyle }}
      >
        {a.action}
      </Button>
    </div>
  );
}

/* ═════════════════ ECHARTS OPTION BUILDERS ═════════════════ */

/* Energy comparison: baseline vs actual (dual line) */
const compareOpt = () => ({
  tooltip: { trigger: 'axis', backgroundColor: '#141c30', borderColor: PANEL_BD, textStyle: { color: TEXT, fontSize: 11 }, axisPointer: { lineStyle: { color: hexA(PANEL_BD, 0.8) } } },
  legend: { top: 0, right: 0, textStyle: { color: DIM, fontSize: 10 }, itemWidth: 14, itemHeight: 3, itemGap: 12, data: ['基准能耗', '实际能耗'] },
  grid: { left: 48, right: 12, top: 26, bottom: 28 },
  xAxis: { type: 'category', data: ['05-14','05-15','05-16','05-17','05-18','05-19','05-20'], axisLine: { lineStyle: { color: hexA(PANEL_BD, 0.6) } }, axisLabel: { color: DIM, fontSize: 10 } },
  yAxis: { type: 'value', max: 50000, splitLine: { lineStyle: { color: hexA(PANEL_BD, 0.3), type: 'dashed' } }, axisLabel: { color: DIM, fontSize: 10, formatter: '{value}' } },
  series: [
    { name: '基准能耗', type: 'line', smooth: true, symbol: 'none', lineStyle: { color: DIM, width: 1.5, type: 'dashed' }, data: [42000,43000,42800,43200,44100,43900,45000] },
    { name: '实际能耗', type: 'line', smooth: true, symbol: 'none', areaStyle: { color: new echarts.graphic.LinearGradient(0, 0, 0, 1, [{ offset: 0, color: hexA(ACCENT, 0.25) }, { offset: 1, color: hexA(ACCENT, 0.01) }]) }, lineStyle: { color: ACCENT, width: 2 }, data: [40000,41000,39500,38000,37000,36800,36000] },
  ],
});

/* Monthly saving trend: bar (MWh) + line overlay (%) */
const monthOpt = () => ({
  tooltip: { trigger: 'axis', backgroundColor: '#141c30', borderColor: PANEL_BD, textStyle: { color: TEXT, fontSize: 11 } },
  legend: { top: 0, right: 0, textStyle: { color: DIM, fontSize: 10 }, data: ['节能量(MWh)', '节能率'] },
  grid: { left: 46, right: 46, top: 26, bottom: 26 },
  xAxis: { type: 'category', data: Array.from({ length: 12 }, (_, i) => `${i + 1}月`), axisLine: { lineStyle: { color: hexA(PANEL_BD, 0.6) } }, axisLabel: { color: DIM, fontSize: 10, interval: 1 } },
  yAxis: [{ type: 'value', name: '(MWh)', max: 100000, splitLine: { lineStyle: { color: hexA(PANEL_BD, 0.3) } }, axisLabel: { color: DIM, fontSize: 10 } },
           { type: 'value', name: '(%)', max: 40, splitLine: { show: false }, axisLabel: { color: DIM, fontSize: 10, formatter: '{value}' } }],
  series: [
    { name: '节能量(MWh)', type: 'bar', barWidth: 14, itemStyle: { color: new echarts.graphic.LinearGradient(0, 0, 0, 1, [{ offset: 0, color: ACCENT }, { offset: 1, color: hexA(ACCENT, 0.35) }]), borderRadius: [3, 3, 0, 0] },
      data: [42000, 48000, 55000, 62000, 75000, 85000, 90000, 88000, 72000, 65000, 58000, 52000] },
    { name: '节能率', type: 'line', yAxisIndex: 1, smooth: true, symbol: 'circle', symbolSize: 4, lineStyle: { color: AMBER, width: 1.5 }, itemStyle: { color: AMBER }, data: [12, 14, 16, 19, 22, 25, 28, 27, 23, 21, 18, 15] },
  ],
});

/* Device energy composition donut */
const composeOpt = () => ({
  tooltip: { trigger: 'item', formatter: '{b}: {c} kWh ({d}%)', backgroundColor: '#141c30', borderColor: PANEL_BD, textStyle: { color: TEXT, fontSize: 11 } },
  legend: {
    orient: 'vertical', right: 0, top: 'middle',
    textStyle: { color: DIM, fontSize: 11 },
    itemWidth: 10, itemHeight: 10, itemGap: 10,
    formatter: (name: string) => {
      const item = COMPOSITION.find((c) => c.name === name);
      return `${name}  ${item?.pct}%  ${(item?.kwh ?? 0).toLocaleString()}`;
    },
  },
  series: [{
    type: 'pie', radius: ['52%', '72%'], center: ['35%', '50%'],
    avoidLabelOverlap: false, label: { show: false },
    emphasis: { label: { show: true, fontSize: 13, fontWeight: 'bold', color: TEXT } },
    data: COMPOSITION.map((c, i) => ({ name: c.name, value: c.kwh, itemStyle: { color: [ACCENT, '#2563eb', '#1d4ed8', '#1e40af', '#1e3a8a'][i % 5] } })),
  }],
});

/* Health score gauge (semi-circle) */
const healthGaugeOpt = () => ({
  series: [{
    type: 'gauge', startAngle: 200, endAngle: -20, min: 0, max: 100,
    radius: '100%', center: ['50%', '62%'],
    pointer: { show: false },
    progress: { show: true, overlap: false, roundCap: true,
      clip: false, itemStyle: { color: ACCENT } },
    axisLine: { roundCap: true, lineStyle: { width: 14, color: [[1, hexA(PANEL_BD, 0.5)]] } },
    axisTick: { show: false },
    splitLine: { length: 4, distance: -8, lineStyle: { width: 2, color: hexA(TEXT, 0.15) } },
    axisLabel: { show: false },
    detail: {
      valueAnimation: true,
      offsetCenter: [0, '20%'], fontSize: 32, fontWeight: 'bold', color: TEXT,
      formatter: '{value}', fontFamily: '"DIN Alternate", "Roboto Mono", monospace',
    },
    data: [{ value: 92 }],
  }],
  graphic: [{ type: 'text', left: 'center', top: '70%', style: { text: '健康状态: 优', fill: GREEN, fontSize: 13, textAlign: 'center' } }],
});

/* Alarm progress circular gauge */
const alarmGaugeOpt = () => ({
  series: [{
    type: 'gauge', startAngle: 200, endAngle: -20, min: 0, max: 100,
    radius: '95%', center: ['50%', '58%'],
    pointer: { show: false },
    progress: { show: true, roundCap: true, clip: false, itemStyle: { color: GREEN } },
    axisLine: { roundCap: true, lineStyle: { width: 12, color: [[1, hexA(PANEL_BD, 0.5)]] } },
    axisTick: { show: false },
    splitLine: { length: 3, distance: -6, lineStyle: { width: 2, color: hexA(TEXT, 0.12) } },
    axisLabel: { show: false },
    detail: { valueAnimation: true, offsetCenter: [0, '18%'], fontSize: 26, fontWeight: 'bold', color: TEXT, formatter: '{value}%' },
    data: [{ value: 85 }],
  }],
  graphic: [{ type: 'text', left: 'center', top: '66%', style: { text: '良好', fill: GREEN, fontSize: 12, textAlign: 'center' } }],
});

/* ═════════════════ MAIN COMPONENT ═════════════════ */

export default function BigScreen() {
  const navigate = useNavigate();

  const [cursorHidden, setCursorHidden] = useState(false);
  const [clock, setClock] = useState(() => new Date());

  useEffect(() => {
    const id = setInterval(() => setClock(new Date()), 1000);
    return () => clearInterval(id);
  }, []);

  /* hide cursor after idle */
  useEffect(() => {
    let t: number;
    const reset = () => { setCursorHidden(false); clearTimeout(t); t = window.setTimeout(() => setCursorHidden(true), 3000); };
    document.addEventListener('mousemove', reset);
    document.addEventListener('keydown', reset);
    t = window.setTimeout(() => setCursorHidden(true), 3000);
    return () => { document.removeEventListener('mousemove', reset); document.removeEventListener('keydown', reset); clearTimeout(t); };
  }, []);

  const clockStr = clock.toLocaleTimeString('zh-CN', { hour12: false });
  const dateStr = clock.toLocaleDateString('zh-CN', { year: 'numeric', month: 'long', day: 'numeric', weekday: 'long' });

  /* ───── 实时数据开关：默认关，保留精确参考图数字；开启后接 TelemetryClient ───── */
  const [realData, setRealData] = useState(false);
  const { get } = useTelemetryLive(MOCK_DEVICES, ['power', 'cop', 'load']);
  const live = (i: number) => ({
    power: get(MOCK_DEVICES[i], 'power') ?? 0,
    cop: get(MOCK_DEVICES[i], 'cop') ?? 0,
    load: get(MOCK_DEVICES[i], 'load') ?? 0,
  });
  // 6 台模拟设备 → 5 组（冷机/冷却塔/冷冻泵/冷却泵/末端）
  const deviceStats = realData
    ? [
        { name: '冷机', run: 2, total: 3, power: Math.round(live(0).power + live(1).power), cop: Math.round(((live(0).cop + live(1).cop) / 2) * 100) / 100, load: Math.round((live(0).load + live(1).load) / 2), unit: 'kW' },
        { name: '冷却塔', run: 2, total: 3, power: Math.round(live(2).power), load: Math.round(live(2).load), unit: 'kW' },
        { name: '冷冻泵', run: 2, total: 3, power: Math.round(live(3).power), load: Math.round(live(3).load), freq: 105, unit: 'kW' },
        { name: '冷却泵', run: 2, total: 3, power: Math.round(live(4).power), load: Math.round(live(4).load), unit: 'kW' },
        { name: '末端', run: 48, total: 52, power: Math.round(live(5).power), load: Math.round(live(5).load), unit: 'kW' },
      ]
    : DEVICE_STATS;
  const liveCop = realData ? Math.round(((live(0).cop + live(1).cop) / 2) * 100) / 100 : 6.28;
  const liveLoad = realData ? Math.round((live(0).load + live(1).load) / 2) : 45.4;

  return (
    <ConfigProvider theme={{ algorithm: antdTheme.darkAlgorithm, token: { colorPrimary: ACCENT, borderRadius: RADIUS } }}>
      <div
        style={{
          position: 'fixed', inset: 0, background: BG, color: TEXT,
          fontFamily: 'system-ui, -apple-system, "PingFang SC", "Microsoft YaHei", sans-serif',
          cursor: cursorHidden ? 'none' : 'default', display: 'flex', flexDirection: 'column',
          overflow: 'hidden',
        }}
        onKeyDown={(e) => e.key === 'Escape' && navigate('/dashboard')}
      >
        {/* ───── TITLE BAR ───── */}
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', padding: '10px 20px', flexShrink: 0 }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
            <DesktopOutlined style={{ fontSize: 20, color: ACCENT }} />
            <Typography.Text style={{ color: TEXT, fontSize: 18, fontWeight: 700, letterSpacing: 0.5 }}>
              商业建筑智慧能源驾驶舱
            </Typography.Text>
          </div>
          <div style={{ display: 'flex', alignItems: 'center', gap: 16, color: DIM, fontSize: 13 }}>
            <span style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 12 }}>
              <ApiOutlined style={{ color: realData ? ACCENT : undefined }} />
              实时数据
              <Switch size="small" checked={realData} onChange={setRealData}
                style={{ background: realData ? ACCENT : undefined }} />
            </span>
            <span style={{ display: 'flex', alignItems: 'center', gap: 4 }}>
              <EnvironmentOutlined /> 28℃
            </span>
            <span style={{ fontVariantNumeric: 'tabular-nums' }}>{dateStr}</span>
            <span style={{ color: ACCENT, fontVariantNumeric: 'tabular-nums', fontWeight: 600 }}>{clockStr}</span>
          </div>
        </div>

        {/* ───── KPI ROW (6 cards) ───── */}
        <div style={{ display: 'flex', gap: 10, padding: '0 20px 10px', flexShrink: 0 }}>
          {KPI_DATA.map((k, i) => <KpiCard key={i} item={k} />)}
        </div>

        {/* ───── MAIN GRID: left | CENTER 3D | right ───── */}
        <div style={{ flex: 1, minHeight: 0, display: 'flex', gap: 10, padding: '0 20px 8px' }}>

          {/* ═══════ LEFT COLUMN (flexible ~22%) ═══════ */}
          <div style={{ flex: '0 0 22%', minWidth: 200, maxWidth: 300, display: 'flex', flexDirection: 'column', gap: 8 }}>

            {/* 能耗对比 */}
            <Panel title="能耗对比" style={{ flex: 0, minHeight: 0 }}>
              <ReactECharts option={compareOpt()} style={{ flex: 1, minHeight: 140 }}
                opts={{ renderer: 'canvas' }} notMerge={true} />
              <div style={{ display: 'flex', gap: 16, marginTop: 6, paddingTop: 8, borderTop: `1px solid ${hexA(PANEL_BD, 0.4)}` }}>
                <div>
                  <span style={{ color: DIM, fontSize: 10 }}>今日节电量</span><br />
                  <span style={{ color: ACCENT, fontSize: 16, fontWeight: 700, fontVariantNumeric: 'tabular-nums' }}>12,860</span>
                  <span style={{ color: DIM, fontSize: 10 }}> kWh</span>
                </div>
                <div>
                  <span style={{ color: DIM, fontSize: 10 }}>节能率</span><br />
                  <span style={{ color: ACCENT, fontSize: 16, fontWeight: 700, fontVariantNumeric: 'tabular-nums' }}>18.6%</span>
                </div>
              </div>
            </Panel>

            {/* 月度节能趋势 */}
            <Panel title="月度节能趋势" style={{ flex: 1, minHeight: 0 }}>
              <ReactECharts option={monthOpt()} style={{ flex: 1, minHeight: 180 }}
                opts={{ renderer: 'canvas' }} notMerge={true} />
            </Panel>

            {/* 设备能耗构成 */}
            <Panel title="设备能耗构成（本月）" style={{ flex: 1, minHeight: 0 }}>
              <ReactECharts option={composeOpt()} style={{ flex: 1, minHeight: 170 }}
                opts={{ renderer: 'canvas' }} notMerge={true} />
            </Panel>
          </div>

          {/* ═══════ CENTER: 3D FOCAL POINT (~52%) ═══════ */}
          <div style={{ flex: 1, minWidth: 0, display: 'flex', flexDirection: 'column' }}>
            <Panel title="冷站系统总览" accent style={{ flex: 1, minHeight: 0, position: 'relative', overflow: 'hidden', padding: 0 }}>
              {/* Header badges inside panel */}
              <div style={{
                display: 'flex', alignItems: 'center', gap: 16, padding: '10px 15px',
                borderBottom: `1px solid ${hexA(PANEL_BD, 0.5)}`,
                background: 'linear-gradient(to right, rgba(59,130,246,0.04), transparent)',
                zIndex: 2, position: 'relative',
              }}>
                <span style={{ color: DIM, fontSize: 11, display: 'flex', alignItems: 'center', gap: 4 }}>
                  <BulbOutlined /> 运行模式 · <span style={{ color: ACCENT }}>节能优先</span>
                </span>
                <span style={{ color: DIM, fontSize: 11, display: 'flex', alignItems: 'center', gap: 4 }}>
                  <EnvironmentOutlined /> 室外温度：<span style={{ color: TEXT }}>28℃</span>
                </span>
                <span style={{ color: DIM, fontSize: 11 }}>
                  供回水温度：<span style={{ color: ACCENT }}>7℃</span> / <span style={{ color: AMBER }}>12℃</span>
                </span>
              </div>

              {/* 3D Scene area */}
              <div style={{ flex: 1, minHeight: 0, position: 'relative' }}>
                <Suspense fallback={<Spinner />}>
                  <System3D
                    cop={liveCop} chillerRun={2} chillerTotal={2}
                    towerRun={2} towerTotal={2}
                    pumpRun={3} pumpTotal={3}
                    load={liveLoad}
                    style={{ position: 'absolute', inset: 0 }}
                  />
                </Suspense>

                {/* Device stat cards overlaid on 3D — positioned per reference layout (percent-based for responsiveness) */}
                <DevCard d={deviceStats[0]} style={{ left: '1%', top: '18%' }} />     {/* 冷机 - left */}
                <DevCard d={deviceStats[1]} style={{ right: '1%', top: '16%' }} />   {/* 冷却塔 - right */}
                <DevCard d={deviceStats[2]} style={{ left: '1%', bottom: '20%' }} />  {/* 冷冻泵 - bottom-left */}
                <DevCard d={deviceStats[3]} style={{ right: '8%', bottom: '18%' }} /> {/* 冷却泵 - bottom-right inner */}
                <DevCard d={deviceStats[4]} style={{ right: '1%', bottom: '16%' }} />  {/* 末端 - far right */}
              </div>

              {/* Pipe legend at bottom */}
              <div style={{
                display: 'flex', gap: 16, padding: '7px 15px',
                borderTop: `1px solid ${hexA(PANEL_BD, 0.4)}`,
                fontSize: 10, color: DIM, zIndex: 2,
                background: hexA(BG, 0.7),
              }}>
                <span style={{ display: 'flex', alignItems: 'center', gap: 4 }}>
                  <span style={{ width: 18, height: 2, background: ACCENT, display: 'inline-block' }} /> 冷冻水供水
                </span>
                <span style={{ display: 'flex', alignItems: 'center', gap: 4 }}>
                  <span style={{ width: 18, height: 2, background: ACCENT_DK, display: 'inline-block' }} /> 冷冻水回水
                </span>
                <span style={{ display: 'flex', alignItems: 'center', gap: 4 }}>
                  <span style={{ width: 18, height: 2, background: GREEN, display: 'inline-block' }} /> 冷却水供水
                </span>
                <span style={{ display: 'flex', alignItems: 'center', gap: 4 }}>
                  <span style={{ width: 18, height: 2, background: hexA(GREEN, 0.55), display: 'inline-block' }} /> 冷却水回水
                </span>
                <span style={{ display: 'flex', alignItems: 'center', gap: 4 }}>
                  <span style={{ width: 18, height: 2, background: hexA(DIM, 0.4), borderStyle: 'dashed', borderWidth: 1, borderBottom: `1px dashed ${DIM}` }}>冷却塔进风</span>
                </span>
                <span style={{ marginLeft: 'auto', color: ACCENT, cursor: 'pointer', fontSize: 11, display: 'flex', alignItems: 'center', gap: 3 }}>
                  查看系统详情 <span>›</span>
                </span>
              </div>
            </Panel>
          </div>

          {/* ═══════ RIGHT COLUMN (flexible ~22%) ═══════ */}
          <div style={{ flex: '0 0 22%', minWidth: 200, maxWidth: 300, display: 'flex', flexDirection: 'column', gap: 8 }}>

            {/* 设备健康评分 */}
            <Panel title="设备健康评分" style={{ flex: 0, minHeight: 0 }}>
              <div style={{ display: 'flex', gap: 14 }}>
                <div style={{ width: 110 }}>
                  <ReactECharts option={healthGaugeOpt()} style={{ width: 110, height: 130 }}
                    opts={{ renderer: 'canvas' }} notMerge={true} />
                </div>
                <div style={{ flex: 1, display: 'flex', flexDirection: 'column', justifyContent: 'center', gap: 5 }}>
                  {HEALTH_SCORES.map((h) => (
                    <div key={h.label} style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', fontSize: 12 }}>
                      <span style={{ color: DIM }}>{h.label}</span>
                      <span style={{ color: h.value >= 92 ? ACCENT : h.value >= 89 ? AMBER : RED, fontWeight: 600, fontVariantNumeric: 'tabular-nums' }}>{h.value}</span>
                    </div>
                  ))}
                </div>
              </div>
            </Panel>

            {/* 异常诊断 */}
            <Panel title="异常诊断" style={{ flex: '0 0 auto' }}>
              {DIAGNOSTICS.map((a, i) => <AlarmRow key={i} a={a} />)}
            </Panel>

            {/* 告警闭环进度 */}
            <Panel title="告警闭环进度" style={{ flex: 0, minHeight: 0 }}>
              <div style={{ display: 'flex', gap: 12 }}>
                <div style={{ width: 100 }}>
                  <ReactECharts option={alarmGaugeOpt()} style={{ width: 100, height: 105 }}
                    opts={{ renderer: 'canvas' }} notMerge={true} />
                </div>
                <div style={{ flex: 1, display: 'flex', flexDirection: 'column', justifyContent: 'center', gap: 4 }}>
                  {[
                    { label: '告警总数', value: ALARM_PROGRESS.total },
                    { label: '已处理', value: ALARM_PROGRESS.resolved, color: GREEN },
                    { label: '处理中', value: ALARM_PROGRESS.processing, color: AMBER },
                    { label: '超时未处理', value: ALARM_PROGRESS.overdue, color: RED },
                  ].map((s) => (
                    <div key={s.label} style={{ display: 'flex', justifyContent: 'space-between', fontSize: 11 }}>
                      <span style={{ color: DIM }}>{s.label}</span>
                      <span style={{ color: s.color ?? TEXT, fontWeight: 600, fontVariantNumeric: 'tabular-nums' }}>{s.value}</span>
                    </div>
                  ))}
                </div>
              </div>
            </Panel>

            {/* 建议优化动作 */}
            <Panel title="建议优化动作" style={{ flex: 1, minHeight: 0 }}>
              <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
                {SUGGESTIONS.map((t, i) => (
                  <div key={i} style={{
                    display: 'flex', alignItems: 'center', justifyContent: 'space-between',
                    padding: '6px 9px', borderRadius: 8, background: hexA(ACCENT, 0.07),
                    border: `1px solid ${hexA(ACCENT, 0.15)}`,
                  }}>
                    <span style={{ color: TEXT, fontSize: 11, flex: 1 }}>{t}</span>
                    <Button size="small" type="primary"
                      style={{ height: 22, fontSize: 10, paddingInline: 10, marginLeft: 8 }}
                    >
                      去执行
                    </Button>
                  </div>
                ))}
              </div>
            </Panel>
          </div>
        </div>

        {/* ───── BOTTOM STRIP (always visible) ───── */}
        <div style={{
          display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 24,
          padding: '6px 20px', flexShrink: 0, color: DIM, fontSize: 11,
          borderTop: `1px solid ${hexA(PANEL_BD, 0.3)}`,
        }}>
          <span style={{ display: 'flex', alignItems: 'center', gap: 4 }}><ThunderboltOutlined /> 智慧能源</span>
          <span style={{ display: 'flex', alignItems: 'center', gap: 4 }}><ApiOutlined /> 精益运行</span>
          <span style={{ display: 'flex', alignItems: 'center', gap: 4 }}><SafetyCertificateOutlined /> 绿色低碳</span>
          <span style={{ display: 'flex', alignItems: 'center', gap: 4 }}><BulbOutlined /> 价值创造</span>
          <Button size="small" ghost icon={<FullscreenExitOutlined />} onClick={() => navigate('/dashboard')}
            style={{ marginLeft: 'auto', color: DIM, borderColor: hexA(PANEL_BD, 0.5), height: 24 }}
          >
            退出大屏
          </Button>
        </div>
      </div>
    </ConfigProvider>
  );
}
