import type { ReactNode } from 'react';
import { ReloadOutlined } from '@ant-design/icons';
import type { DeviceStat, DiagnosticItem, KpiItem } from './data';
import { ACCENT, AMBER, GREEN, RED } from './theme';

export const Spinner = () => (
  <div style={{
    display: 'flex', alignItems: 'center', justifyContent: 'center',
    flex: 1, minHeight: 180, flexDirection: 'column', gap: 10,
  }}>
    <ReloadOutlined spin style={{ fontSize: 24, color: ACCENT }} />
    <span style={{ color: '#718199', fontSize: 11 }}>正在加载冷站系统模型</span>
  </div>
);

export function KpiCard({ item }: { item: KpiItem }) {
  const Icon = item.icon;
  const value = item.money
    ? `¥ ${item.value.toLocaleString('zh-CN')}`
    : `${typeof item.decimals === 'number' ? item.value.toFixed(item.decimals) : item.value.toLocaleString('zh-CN')}${item.unit}`;

  let subText = item.sub;
  if (item.money) {
    subText = `${item.sub} ↑${item.deltaVal?.toLocaleString('zh-CN') ?? ''}${item.deltaUnit ?? ''}`;
  } else if (item.trend !== undefined) {
    subText = `${item.sub} ${item.trend > 0 ? '↑' : '↓'}${Math.abs(item.trend)}${item.suffix ?? '%'}`;
  }

  return (
    <div className="bigscreen-kpi-card">
      <span className="bigscreen-kpi-icon"><Icon /></span>
      <div className="bigscreen-kpi-copy">
        <div className="bigscreen-kpi-label">{item.label}</div>
        <div className="bigscreen-kpi-value">{value}</div>
        <div className="bigscreen-kpi-sub">{subText}</div>
      </div>
    </div>
  );
}

export function Panel({
  title,
  eyebrow,
  extra,
  children,
  accent = false,
  className = '',
}: {
  title: string;
  eyebrow?: string;
  extra?: ReactNode;
  children: ReactNode;
  accent?: boolean;
  className?: string;
}) {
  const classes = ['bigscreen-panel', accent ? 'is-accent' : '', className].filter(Boolean).join(' ');
  return (
    <section className={classes}>
      <header className="bigscreen-panel-header">
        <div className="bigscreen-panel-heading">
          {eyebrow && <span className="bigscreen-panel-eyebrow">{eyebrow}</span>}
          <span className="bigscreen-panel-title">{title}</span>
        </div>
        {extra && <div className="bigscreen-panel-extra">{extra}</div>}
      </header>
      <div className="bigscreen-panel-body">{children}</div>
    </section>
  );
}

export function ChartFrame({ children }: { children: ReactNode }) {
  return <div className="bigscreen-chart-frame">{children}</div>;
}

export function DeviceStatusRail({ items }: { items: DeviceStat[] }) {
  return (
    <div className="bigscreen-device-rail" data-testid="bigscreen-device-rail">
      {items.map((item) => (
        <div key={item.name} className={`bigscreen-device-item${item.load > 100 ? ' is-warning' : ''}`}>
          <div className="bigscreen-device-head">
            <span>{item.name}</span>
            <i aria-hidden="true" />
          </div>
          <div className="bigscreen-device-data">
            <span>运行</span><strong>{item.run}/{item.total}</strong>
            <span>功率</span><strong>{item.power}{item.unit}</strong>
            {item.cop !== undefined && <><span>COP</span><strong>{item.cop}</strong></>}
            {item.freq !== undefined && <><span>频率</span><strong>{item.freq}Hz</strong></>}
            <span>负载</span><strong>{item.load}%</strong>
          </div>
        </div>
      ))}
    </div>
  );
}

export function HealthSummary({ items }: { items: { label: string; value: number }[] }) {
  const average = Math.round(items.reduce((sum, item) => sum + item.value, 0) / Math.max(items.length, 1));
  return (
    <div className="bigscreen-health-summary">
      <div className="bigscreen-health-score">
        <strong>{average}</strong>
        <span>综合健康 · 优</span>
      </div>
      <div className="bigscreen-health-bars">
        {items.map((item) => (
          <div key={item.label} className="bigscreen-health-row">
            <span>{item.label}</span>
            <div className="bigscreen-health-track"><i className="bigscreen-health-fill" style={{ width: `${item.value}%` }} /></div>
            <strong>{item.value}</strong>
          </div>
        ))}
      </div>
    </div>
  );
}

export function AlarmRow({ item }: { item: DiagnosticItem }) {
  const color = item.severity === 'error' ? RED : item.severity === 'warn' ? AMBER : GREEN;
  return (
    <div className="bigscreen-alarm-row">
      <i className="bigscreen-alarm-dot" style={{ background: color }} aria-hidden="true" />
      <div className="bigscreen-alarm-copy">
        <strong>{item.title}</strong>
        <span>{item.time}</span>
      </div>
      <span className="bigscreen-alarm-state" style={{ color }}>{item.action}</span>
    </div>
  );
}

export function AlarmProgressSummary({
  total,
  resolved,
  active,
  overdue,
}: {
  total: number;
  resolved: number;
  active: number;
  overdue: number;
}) {
  const pct = total > 0 ? Math.round((resolved / total) * 100) : 0;
  const stats = [
    { label: '总数', value: total },
    { label: '已闭环', value: resolved },
    { label: '处理中', value: active },
    { label: '逾期', value: overdue },
  ];
  return (
    <div className="bigscreen-progress-summary">
      <div className="bigscreen-progress-head"><strong>{pct}%</strong><span>闭环率稳定</span></div>
      <div className="bigscreen-progress-track"><i className="bigscreen-progress-fill" style={{ width: `${pct}%` }} /></div>
      <div className="bigscreen-progress-stats">
        {stats.map((item) => (
          <div key={item.label} className="bigscreen-progress-stat">
            <span>{item.label}</span>
            <strong>{item.value}</strong>
          </div>
        ))}
      </div>
    </div>
  );
}
