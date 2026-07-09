import { Card, Col, Row, Statistic, Typography } from 'antd';
import { ArrowDownOutlined, ArrowUpOutlined } from '@ant-design/icons';
import { useTelemetryLive, MOCK_DEVICES, MOCK_KPI } from '@/api';
import { BRAND, COP_GOOD } from '@/theme/tokens';

function Trend({ val, invert = false }: { val: number; invert?: boolean }) {
  // invert=true => 下降是好事（如能耗下降）
  const good = invert ? val < 0 : val > 0;
  const color = good ? '#16A34A' : '#DC2626';
  const Arrow = val >= 0 ? ArrowUpOutlined : ArrowDownOutlined;
  return (
    <span style={{ color, fontSize: 12 }}>
      <Arrow /> {Math.abs(val)}%
    </span>
  );
}

function KpiCard({ title, value, unit, trend, invert }: {
  title: string; value: number | string; unit?: string; trend: number; invert?: boolean;
}) {
  return (
    <Card variant="borderless" styles={{ body: { padding: 16 } }}>
      <Typography.Text type="secondary" style={{ fontSize: 13 }}>{title}</Typography.Text>
      <Statistic
        value={value}
        suffix={unit}
        valueStyle={{ fontSize: 26, fontWeight: 700 }}
        style={{ marginTop: 4 }}
      />
      <div style={{ marginTop: 4 }}><Trend val={trend} invert={invert} /></div>
    </Card>
  );
}

// COP 主视觉卡：放大 + teal 渐变描边，强调系统效率（C 变体的聚焦思路）。
function CopHero({ cop }: { cop: number }) {
  const good = cop >= COP_GOOD;
  return (
    <Card
      variant="borderless"
      styles={{ body: { padding: 16, borderLeft: `4px solid ${BRAND.teal}`, background: 'linear-gradient(135deg, #E6FAF9 0%, #ffffff 60%)' } }}
    >
      <Typography.Text type="secondary" style={{ fontSize: 13 }}>综合 COP</Typography.Text>
      <Statistic
        value={cop}
        precision={1}
        valueStyle={{ fontSize: 34, fontWeight: 800, color: BRAND.tealStrong }}
      />
      <div style={{ marginTop: 2, fontSize: 12, color: good ? '#16A34A' : '#DC2626' }}>
        {good ? '✓ 高于健康阈值 ' + COP_GOOD : '低于健康阈值 ' + COP_GOOD}
      </div>
    </Card>
  );
}

export default function KpiCards() {
  // 实时层：初始快照来自 React Query（useBatch 缓存），之后由 TelemetryClient 推送覆盖。
  const { get } = useTelemetryLive(MOCK_DEVICES, ['power', 'cop', 'load']);

  const powerNow = MOCK_DEVICES.reduce((s, d) => s + (get(d, 'power') ?? 0), 0);
  const cops = MOCK_DEVICES.map((d) => get(d, 'cop')).filter((v): v is number => v != null);
  const loads = MOCK_DEVICES.map((d) => get(d, 'load')).filter((v): v is number => v != null);
  const cop = cops.length ? Math.round((cops.reduce((a, b) => a + b, 0) / cops.length) * 10) / 10 : 0;
  const load = loads.length ? Math.round(loads.reduce((a, b) => a + b, 0) / loads.length) : 0;

  const k = MOCK_KPI;
  return (
    <Row gutter={[16, 16]}>
      <Col xs={12} md={6}><KpiCard title="今日能耗" value={k.energyToday} unit="kWh" trend={k.trends.energy} invert /></Col>
      <Col xs={12} md={6}><KpiCard title="实时功率" value={powerNow} unit="kW" trend={k.trends.power} /></Col>
      <Col xs={12} md={6}><CopHero cop={cop} /></Col>
      <Col xs={12} md={6}><KpiCard title="综合负荷率" value={load} unit="%" trend={k.trends.load} invert /></Col>
      <Col xs={12} md={6}><KpiCard title="今日节能" value={k.savingToday} unit="kWh" trend={k.trends.saving} /></Col>
    </Row>
  );
}
