import type { CSSProperties, ReactNode } from 'react';
import { Button } from 'antd';
import {
  ExclamationCircleFilled,
  InfoCircleFilled,
  ReloadOutlined,
} from '@ant-design/icons';
import type { DeviceStat, DiagnosticItem, KpiItem } from './data';
import { ACCENT, AMBER, BG, DIM, GREEN, PANEL_BD, PANEL_BG, RED, RADIUS, TEXT, hexA } from './theme';

export const Spinner = () => (
  <div style={{
    display: 'flex', alignItems: 'center', justifyContent: 'center',
    flex: 1, minHeight: 200, flexDirection: 'column', gap: 10,
  }}>
    <ReloadOutlined spin style={{ fontSize: 28, color: ACCENT }} />
    <span style={{ color: DIM, fontSize: 13 }}>加载 3D 场景</span>
  </div>
);

export function KpiCard({ item }: { item: KpiItem }) {
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

export function Panel({
  title, extra, children, style, accent,
}: {
  title: string; extra?: ReactNode;
  children: ReactNode; style?: CSSProperties; accent?: boolean;
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

export function DevCard({ d, style }: { d: DeviceStat; style?: CSSProperties }) {
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

export function AlarmRow({ a }: { a: DiagnosticItem }) {
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
      <Button size="small" type="primary" ghost style={{ height: 22, fontSize: 10, paddingInline: 8, ...btnStyle }}>
        {a.action}
      </Button>
    </div>
  );
}
