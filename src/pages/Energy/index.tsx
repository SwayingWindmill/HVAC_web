import { useState } from 'react';
import { Card, Col, Row, Statistic, Segmented, Typography, Spin } from 'antd';
import ReactECharts from 'echarts-for-react';
import {
  useBuildingTimeseries,
  useTelemetryLive,
  MOCK_DEVICES,
  type Range,
} from '@/api';
import { BRAND, STATUS } from '@/theme/tokens';
import { useUi } from '@/store/ui';
import PageScaffold from '@/components/PageScaffold';
import { DEVICE_META, TYPE_LABEL, type DeviceType } from '@/pages/Assets/meta';

// 系统配色：冷机=teal / 冷冻泵=蓝 / 空调末端=amber
const SYS_COLORS: Record<DeviceType, string> = {
  chiller: BRAND.teal,
  pump: '#3b82f6',
  ahu: '#f59e0b',
};

const SYS_ORDER: DeviceType[] = ['chiller', 'pump', 'ahu'];

export default function Energy() {
  const [range, setRange] = useState<Range>('day');
  const mode = useUi((s) => s.themeMode);
  const dark = mode === 'dark';

  // 历史层（React Query 聚合）
  const { data: trend, isLoading } = useBuildingTimeseries(range);
  const { data: dayData } = useBuildingTimeseries('day');
  const { data: monthData } = useBuildingTimeseries('month');
  const { data: weekData } = useBuildingTimeseries('week');

  // 实时层（WebSocket 推送，驱动 KPI 与分解环图）
  const live = useTelemetryLive(MOCK_DEVICES, ['power']);
  const totalPower = MOCK_DEVICES.reduce((s, id) => s + (live.get(id, 'power') ?? 0), 0);

  // KPI 派生（power 为瞬时功率 kW，按 1h 采样近似 kWh）
  const todayKwh = dayData.reduce((s, p) => s + p.value, 0);
  const monthKwh = monthData.reduce((s, p) => s + p.value, 0);
  const peak = dayData.length ? Math.max(...dayData.map((p) => p.value)) : 0;
  const valley = dayData.length ? Math.min(...dayData.map((p) => p.value)) : 0;
  const peakValleyGap = Math.round(peak - valley);

  // 趋势末端随实时延伸：把最新实时总功率作为末点追加
  const lastTs = trend.length ? trend[trend.length - 1].ts : Date.now();
  const extended = [...trend];
  if (totalPower > 0 && Date.now() - lastTs > 0) {
    extended.push({ ts: Date.now(), value: Math.round(totalPower) });
  }

  // 系统能耗分解（按设备类型分组实时功率）
  const sysPower: Record<DeviceType, number> = { chiller: 0, pump: 0, ahu: 0 };
  MOCK_DEVICES.forEach((id) => {
    const t = DEVICE_META[id]?.type;
    if (t) sysPower[t] += live.get(id, 'power') ?? 0;
  });
  const pieData = SYS_ORDER.map((t) => ({
    name: TYPE_LABEL[t],
    value: Math.round(sysPower[t]),
    itemStyle: { color: SYS_COLORS[t] },
  }));

  // 近 7 日对比（week 聚合为每日能耗和）
  const weekKwh = weekData.map((p) => p.value);
  const last7 = weekKwh[weekKwh.length - 1] ?? 0;
  const prev6 = weekKwh.length > 1 ? weekKwh.slice(0, -1).reduce((s, v) => s + v, 0) / (weekKwh.length - 1) : 0;
  const downdrift = prev6 > 0 ? Math.round(((last7 - prev6) / prev6) * 100) : 0;

  const axisColor = dark ? '#444' : '#ccc';
  const labelColor = dark ? '#aaa' : '#666';
  const splitColor = dark ? '#222' : '#eee';

  const fmtX = (ts: number): string => {
    const d = new Date(ts);
    if (range === 'day') return `${String(d.getHours()).padStart(2, '0')}:00`;
    if (range === 'week') return ['日', '一', '二', '三', '四', '五', '六'][d.getDay()];
    return `${d.getMonth() + 1}/${d.getDate()}`;
  };

  const trendOption = {
    grid: { left: 52, right: 16, top: 24, bottom: 28 },
    tooltip: { trigger: 'axis' },
    xAxis: {
      type: 'category',
      data: extended.map((p) => fmtX(p.ts)),
      axisLine: { lineStyle: { color: axisColor } },
      axisLabel: { color: labelColor, fontSize: 11 },
    },
    yAxis: {
      type: 'value',
      name: 'kWh',
      splitLine: { lineStyle: { color: splitColor } },
      axisLabel: { color: labelColor, fontSize: 11 },
    },
    series: [
      {
        type: 'line',
        smooth: true,
        data: extended.map((p) => p.value),
        symbol: 'none',
        lineStyle: { color: BRAND.teal, width: 2 },
        areaStyle: {
          color: {
            type: 'linear',
            x: 0,
            y: 0,
            x2: 0,
            y2: 1,
            colorStops: [
              { offset: 0, color: 'rgba(15,181,174,0.35)' },
              { offset: 1, color: 'rgba(15,181,174,0.02)' },
            ],
          },
        },
      },
    ],
  };

  const pieOption = {
    tooltip: { trigger: 'item', formatter: '{b}: {c} kW ({d}%)' },
    legend: {
      bottom: 0,
      textStyle: { color: labelColor, fontSize: 12 },
      data: SYS_ORDER.map((t) => TYPE_LABEL[t]),
    },
    series: [
      {
        type: 'pie',
        radius: ['45%', '70%'],
        center: ['50%', '44%'],
        avoidLabelOverlap: true,
        itemStyle: { borderColor: dark ? '#141414' : '#fff', borderWidth: 2 },
        label: { show: true, color: labelColor, formatter: '{b}\n{d}%', fontSize: 11 },
        data: pieData,
      },
    ],
  };

  const weekOption = {
    grid: { left: 52, right: 16, top: 28, bottom: 28 },
    tooltip: { trigger: 'axis' },
    xAxis: {
      type: 'category',
      data: weekKwh.map((_, i) => `D${i + 1}`),
      axisLine: { lineStyle: { color: axisColor } },
      axisLabel: { color: labelColor, fontSize: 11 },
    },
    yAxis: {
      type: 'value',
      name: 'kWh',
      splitLine: { lineStyle: { color: splitColor } },
      axisLabel: { color: labelColor, fontSize: 11 },
    },
    series: [
      {
        type: 'bar',
        data: weekKwh.map((v, i) => ({
          value: Math.round(v),
          itemStyle: { color: i === weekKwh.length - 1 ? BRAND.tealStrong : BRAND.teal },
        })),
        barWidth: '52%',
      },
    ],
  };

  return (
    <PageScaffold
      title="能耗分析"
      subtitle="建筑级能耗趋势、系统分解与环比对比（数据接入实时层）"
      extra={
        <Segmented<Range>
          value={range}
          onChange={setRange}
          options={[
            { label: '日', value: 'day' },
            { label: '周', value: 'week' },
            { label: '月', value: 'month' },
          ]}
        />
      }
    >
      {/* KPI 行 */}
      <Row gutter={[16, 16]}>
        <Col xs={12} sm={12} md={6}>
          <Card variant="borderless">
            <Statistic
              title="今日能耗"
              value={Math.round(todayKwh)}
              suffix="kWh"
              valueStyle={{ color: BRAND.teal, fontWeight: 600 }}
            />
            <Typography.Text type="secondary" style={{ fontSize: 12 }}>
              按 1h 采样近似累计
            </Typography.Text>
          </Card>
        </Col>
        <Col xs={12} sm={12} md={6}>
          <Card variant="borderless">
            <Statistic
              title="实时总功率"
              value={Math.round(totalPower)}
              suffix="kW"
              valueStyle={{ color: BRAND.tealStrong, fontWeight: 600 }}
            />
            <Typography.Text type="secondary" style={{ fontSize: 12 }}>
              6 台设备实时求和
            </Typography.Text>
          </Card>
        </Col>
        <Col xs={12} sm={12} md={6}>
          <Card variant="borderless">
            <Statistic
              title="本月累计能耗"
              value={Math.round(monthKwh)}
              suffix="kWh"
              valueStyle={{ fontWeight: 600 }}
            />
          </Card>
        </Col>
        <Col xs={12} sm={12} md={6}>
          <Card variant="borderless">
            <Statistic
              title="峰谷差"
              value={peakValleyGap}
              suffix="kW"
              valueStyle={{ color: STATUS.warn, fontWeight: 600 }}
            />
            <Typography.Text type="secondary" style={{ fontSize: 12 }}>
              今日峰值 − 谷值
            </Typography.Text>
          </Card>
        </Col>
      </Row>

      {/* 趋势 + 分解 */}
      <Row gutter={[16, 16]}>
        <Col xs={24} lg={15}>
          <Card
            variant="borderless"
            title={<Typography.Text strong>建筑能耗趋势</Typography.Text>}
            styles={{ body: { paddingTop: 8 } }}
          >
            {isLoading && trend.length === 0 ? (
              <div style={{ height: 300, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
                <Spin />
              </div>
            ) : (
              <ReactECharts option={trendOption} style={{ height: 300 }} notMerge />
            )}
          </Card>
        </Col>
        <Col xs={24} lg={9}>
          <Card
            variant="borderless"
            title={<Typography.Text strong>系统能耗分解</Typography.Text>}
            styles={{ body: { paddingTop: 8 } }}
          >
            <ReactECharts option={pieOption} style={{ height: 300 }} notMerge />
          </Card>
        </Col>
      </Row>

      {/* 近 7 日环比 */}
      <Row gutter={[16, 16]}>
        <Col xs={24}>
          <Card
            variant="borderless"
            title={<Typography.Text strong>近 7 日能耗对比</Typography.Text>}
            extra={
              <Typography.Text style={{ fontSize: 13 }}>
                末日环比：
                <Typography.Text
                  strong
                  style={{ color: downdrift <= 0 ? STATUS.ok : STATUS.err }}
                >
                  {downdrift > 0 ? '+' : ''}
                  {downdrift}%
                </Typography.Text>
              </Typography.Text>
            }
            styles={{ body: { paddingTop: 8 } }}
          >
            <ReactECharts option={weekOption} style={{ height: 240 }} notMerge />
          </Card>
        </Col>
      </Row>
    </PageScaffold>
  );
}
