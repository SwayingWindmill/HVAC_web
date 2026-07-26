import { Card, Typography } from 'antd';
import { CheckCircleOutlined } from '@ant-design/icons';
import { MOCK_DEVICES, useTelemetryLive } from '@/api';

type ControlPoint = {
  label: string;
  set: number;
  actual: number;
  unit: string;
  threshold: number;
};

type DeviationLevel = 'critical' | 'warning' | 'minor';

function levelFor(ratio: number): DeviationLevel {
  if (ratio >= 2.5) return 'critical';
  if (ratio >= 1.2) return 'warning';
  return 'minor';
}

function levelLabel(level: DeviationLevel, delta: number) {
  const direction = delta > 0 ? '偏高' : '偏低';
  if (level === 'critical') return `严重${direction}`;
  if (level === 'warning') return `明显${direction}`;
  return `轻微${direction}`;
}

function formatValue(value: number, unit: string) {
  const decimals = unit === 'MPa' ? 2 : 1;
  return `${value.toFixed(decimals)}${unit}`;
}

export default function SetpointVsActual() {
  const live = useTelemetryLive([MOCK_DEVICES[0]], ['supplyTemp', 'returnTemp']);
  const supply = live.get(MOCK_DEVICES[0], 'supplyTemp') ?? 7.0;
  const rtn = live.get(MOCK_DEVICES[0], 'returnTemp') ?? 12.0;

  const points: ControlPoint[] = [
    { label: '冷冻水供水温度', set: 7, actual: supply, unit: '℃', threshold: 1 },
    { label: '冷冻水回水温度', set: 12, actual: rtn, unit: '℃', threshold: 1 },
    { label: '冷却水供水温度', set: 32, actual: 33.1, unit: '℃', threshold: 1 },
    { label: '末端供水压力', set: 0.45, actual: 0.43, unit: 'MPa', threshold: 0.05 },
  ];

  const deviations = points
    .map((point) => {
      const delta = point.actual - point.set;
      const ratio = Math.abs(delta) / point.threshold;
      return { ...point, delta, ratio, level: levelFor(ratio) };
    })
    .filter((point) => point.ratio > 0.2)
    .sort((a, b) => b.ratio - a.ratio);
  const visible = deviations.slice(0, 2);
  const remaining = deviations.slice(2);
  const healthyCount = points.length - deviations.length;
  const criticalCount = deviations.filter((point) => point.level === 'critical').length;
  const otherDeviationCount = deviations.length - criticalCount;
  const headerText = deviations.length
    ? [criticalCount ? `${criticalCount} 项严重` : '', otherDeviationCount ? `${otherDeviationCount} 项偏离` : ''].filter(Boolean).join(' · ')
    : '全部正常';

  return (
    <Card
      variant="borderless"
      className="dashboard-section-card dashboard-list-card dashboard-control-card"
      title={<Typography.Text strong>关键控制偏差</Typography.Text>}
      extra={<span className={`dashboard-card-state ${criticalCount ? 'is-critical' : deviations.length ? 'is-warning' : 'is-success'}`}>{headerText}</span>}
    >
      <div className="dashboard-control-parameters">
        {visible.length ? visible.map((point) => {
          const decimals = point.unit === 'MPa' ? 2 : 1;
          return (
            <div className={`dashboard-control-parameter is-${point.level}`} key={point.label}>
              <div className="dashboard-control-parameter-head">
                <span className="dashboard-control-parameter-title">{point.label}</span>
                <span className={`dashboard-control-severity is-${point.level}`}>{levelLabel(point.level, point.delta)}</span>
              </div>
              <div className="dashboard-control-values">
                <span className="dashboard-control-value">
                  <small>设定值</small>
                  <strong>{formatValue(point.set, point.unit)}</strong>
                </span>
                <span className="dashboard-control-arrow">→</span>
                <span className="dashboard-control-value">
                  <small>实际值</small>
                  <strong>{formatValue(point.actual, point.unit)}</strong>
                </span>
                <span className={`dashboard-control-delta is-${point.level}`}>
                  <small>偏差</small>
                  <strong>{point.delta > 0 ? '+' : ''}{point.delta.toFixed(decimals)}{point.unit}</strong>
                </span>
              </div>
            </div>
          );
        }) : (
          <div className="dashboard-list-empty"><CheckCircleOutlined /> 关键参数均在控制带内</div>
        )}
      </div>
      <div className="dashboard-list-footer">
        <CheckCircleOutlined />
        <span>{remaining.length ? `另有 ${remaining.length} 项偏离` : '无其他偏离'}</span>
        <span className="dashboard-footer-separator">·</span>
        <span>{healthyCount} 项运行正常</span>
      </div>
    </Card>
  );
}
