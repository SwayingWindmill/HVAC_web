import { useState } from 'react';
import { Card, Col, Row, Statistic, Segmented, Typography, List, Tag, Empty, Progress } from 'antd';
import {
  CheckCircleOutlined,
} from '@ant-design/icons';
import ReactECharts from 'echarts-for-react';
import {
  useBuildingTimeseries,
  useTelemetryLive,
  MOCK_DEVICES,
  type Range,
} from '@/api';
import { BRAND, STATUS } from '@/theme/tokens';
import { useUi } from '@/store/ui';
import { useOps } from '@/store/ops';
import PageScaffold from '@/components/PageScaffold';
import {
  PRICE, CARBON_FACTOR, INVESTMENT, ANNUAL_TARGET_CNY,
  ANNUAL_SAVING_KWH, ANNUAL_KWH, MONTH_LABELS, ANNUAL_CNY,
} from './meta';

export default function Cost() {
  const [range, setRange] = useState<Range>('day');
  const mode = useUi((s) => s.themeMode);
  const dark = mode === 'dark';

  // 历史层（React Query 聚合能耗 kWh）
  const { data: dayData } = useBuildingTimeseries('day');
  const { data: trend } = useBuildingTimeseries(range);

  // 实时层（WebSocket 推送，驱动今日电费 / 碳排放末端延伸）
  const live = useTelemetryLive(MOCK_DEVICES, ['power']);
  const totalPower = MOCK_DEVICES.reduce((s, id) => s + (live.get(id, 'power') ?? 0), 0);

  // /optimize 已批准 / 已下发建议（联动累计节能收益）
  const suggestions = useOps((s) => s.suggestions);
  const approved = suggestions.filter((s) => s.status === 'approved' || s.status === 'dispatched');
  const liveSavingCny = approved.reduce((a, s) => a + s.saving.cny, 0);
  const liveSavingKwh = approved.reduce((a, s) => a + s.saving.kwh, 0);
  const liveSavingCo2 = approved.reduce((a, s) => a + s.saving.co2, 0);

  // ---- KPI 派生 ----
  const todayKwh = dayData.reduce((s, p) => s + p.value, 0);
  const todayCost = Math.round(todayKwh * PRICE);

  const cumulativeSavingCny = ANNUAL_CNY + liveSavingCny; // 本年基线 + 实时已批准
  const cumulativeSavingKwh = ANNUAL_KWH + liveSavingKwh;
  const cumulativeCo2T = (cumulativeSavingKwh * CARBON_FACTOR) / 1000;

  const roi = (ANNUAL_CNY / INVESTMENT) * 100; // %
  const paybackYr = INVESTMENT / ANNUAL_CNY; // 年
  const achieveRate = Math.min(100, (ANNUAL_CNY / ANNUAL_TARGET_CNY) * 100);

  // ---- 成本趋势（kWh × 电价 = ¥），末端随实时延伸 ----
  const lastTs = trend.length ? trend[trend.length - 1].ts : Date.now();
  const extended = [...trend];
  if (totalPower > 0 && Date.now() - lastTs > 0) {
    extended.push({ ts: Date.now(), value: Math.round(totalPower * PRICE) });
  }
  const fmtX = (ts: number): string => {
    const d = new Date(ts);
    if (range === 'day') return `${String(d.getHours()).padStart(2, '0')}:00`;
    if (range === 'week') return ['日', '一', '二', '三', '四', '五', '六'][d.getDay()];
    return `${d.getMonth() + 1}/${d.getDate()}`;
  };
  const costTrend = extended.map((p) => ({
    ts: p.ts,
    value: Math.round(p.value * PRICE), // 能耗→电费
  }));

  // ---- 图表配色 ----
  const axisColor = dark ? '#444' : '#ccc';
  const labelColor = dark ? '#aaa' : '#666';
  const splitColor = dark ? '#222' : '#eee';

  const costOption = {
    grid: { left: 56, right: 16, top: 24, bottom: 28 },
    tooltip: { trigger: 'axis', valueFormatter: (v: number) => `¥${v}` },
    xAxis: {
      type: 'category',
      data: costTrend.map((p) => fmtX(p.ts)),
      axisLine: { lineStyle: { color: axisColor } },
      axisLabel: { color: labelColor, fontSize: 11 },
    },
    yAxis: {
      type: 'value',
      name: '¥',
      splitLine: { lineStyle: { color: splitColor } },
      axisLabel: { color: labelColor, fontSize: 11 },
    },
    series: [
      {
        type: 'line',
        smooth: true,
        data: costTrend.map((p) => p.value),
        symbol: 'none',
        lineStyle: { color: BRAND.teal, width: 2 },
        areaStyle: {
          color: {
            type: 'linear', x: 0, y: 0, x2: 0, y2: 1,
            colorStops: [
              { offset: 0, color: 'rgba(15,181,174,0.35)' },
              { offset: 1, color: 'rgba(15,181,174,0.02)' },
            ],
          },
        },
      },
    ],
  };

  // 年度节能收益（按月，¥）
  const annualSavingCny = ANNUAL_SAVING_KWH.map((k) => Math.round(k * PRICE));
  const savingOption = {
    grid: { left: 56, right: 16, top: 24, bottom: 28 },
    tooltip: { trigger: 'axis', valueFormatter: (v: number) => `¥${v}` },
    xAxis: {
      type: 'category',
      data: MONTH_LABELS,
      axisLine: { lineStyle: { color: axisColor } },
      axisLabel: { color: labelColor, fontSize: 11 },
    },
    yAxis: {
      type: 'value',
      name: '¥',
      splitLine: { lineStyle: { color: splitColor } },
      axisLabel: { color: labelColor, fontSize: 11 },
    },
    series: [
      {
        type: 'bar',
        data: annualSavingCny.map((v, i) => ({
          value: v,
          itemStyle: { color: i === 11 ? BRAND.tealStrong : BRAND.teal },
        })),
        barWidth: '52%',
      },
    ],
  };

  // 碳排放趋势（按月，t CO2）
  const carbonOption = {
    grid: { left: 56, right: 16, top: 24, bottom: 28 },
    tooltip: { trigger: 'axis', valueFormatter: (v: number) => `${v} t` },
    xAxis: {
      type: 'category',
      data: MONTH_LABELS,
      axisLine: { lineStyle: { color: axisColor } },
      axisLabel: { color: labelColor, fontSize: 11 },
    },
    yAxis: {
      type: 'value',
      name: 't CO₂',
      splitLine: { lineStyle: { color: splitColor } },
      axisLabel: { color: labelColor, fontSize: 11 },
    },
    series: [
      {
        type: 'line',
        smooth: true,
        data: ANNUAL_SAVING_KWH.map((k) => +(k * CARBON_FACTOR / 1000).toFixed(2)),
        symbol: 'circle',
        symbolSize: 6,
        lineStyle: { color: STATUS.ok, width: 2 },
        areaStyle: {
          color: {
            type: 'linear', x: 0, y: 0, x2: 0, y2: 1,
            colorStops: [
              { offset: 0, color: 'rgba(22,163,74,0.30)' },
              { offset: 1, color: 'rgba(22,163,74,0.02)' },
            ],
          },
        },
      },
    ],
  };

  return (
    <PageScaffold
      title="成本与绩效"
      subtitle="能耗成本、节能收益、碳排与 ROI（联动 /optimize 已批准建议，数据接入实时层）"
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
      {/* KPI 行：成本 / 节能 / 碳排 / ROI */}
      <Row gutter={[16, 16]}>
        <Col xs={12} sm={12} md={6}>
          <Card variant="borderless">
            <Statistic
              title="今日电费"
              value={todayCost}
              prefix="¥"
              valueStyle={{ color: BRAND.teal, fontWeight: 600 }}
            />
            <Typography.Text type="secondary" style={{ fontSize: 12 }}>
              实时功率 × 电价 {PRICE} ¥/kWh
            </Typography.Text>
          </Card>
        </Col>
        <Col xs={12} sm={12} md={6}>
          <Card variant="borderless">
            <Statistic
              title="累计节能收益"
              value={cumulativeSavingCny}
              prefix="¥"
              valueStyle={{ color: STATUS.ok, fontWeight: 600 }}
            />
            <Typography.Text type="secondary" style={{ fontSize: 12 }}>
              本年基线 + 已批准建议 {approved.length} 项
            </Typography.Text>
          </Card>
        </Col>
        <Col xs={12} sm={12} md={6}>
          <Card variant="borderless">
            <Statistic
              title="累计减碳"
              value={+cumulativeCo2T.toFixed(1)}
              suffix="t CO₂"
              valueStyle={{ fontWeight: 600 }}
            />
            <Typography.Text type="secondary" style={{ fontSize: 12 }}>
              因子 {CARBON_FACTOR} kg/kWh
            </Typography.Text>
          </Card>
        </Col>
        <Col xs={12} sm={12} md={6}>
          <Card variant="borderless">
            <Statistic
              title="投资回报率 ROI"
              value={+roi.toFixed(1)}
              suffix="%"
              valueStyle={{ color: BRAND.tealStrong, fontWeight: 600 }}
            />
            <Typography.Text type="secondary" style={{ fontSize: 12 }}>
              回收期 ≈ {paybackYr.toFixed(1)} 年
            </Typography.Text>
          </Card>
        </Col>
      </Row>

      {/* 成本趋势 + 达标率仪表盘 */}
      <Row gutter={[16, 16]}>
        <Col xs={24} lg={15}>
          <Card
            variant="borderless"
            title={<Typography.Text strong>能耗成本趋势</Typography.Text>}
            styles={{ body: { paddingTop: 8 } }}
          >
            <ReactECharts option={costOption} style={{ height: 300 }} notMerge />
          </Card>
        </Col>
        <Col xs={24} lg={9}>
          <Card
            variant="borderless"
            title={<Typography.Text strong>年度节能达标率</Typography.Text>}
            styles={{ body: { paddingTop: 8 } }}
          >
            <div style={{ textAlign: 'center', paddingTop: 8 }}>
              <Progress
                type="dashboard"
                percent={+achieveRate.toFixed(0)}
                strokeColor={achieveRate >= 100 ? STATUS.ok : BRAND.teal}
                size={180}
                format={(p) => `${p}%`}
              />
              <div style={{ color: labelColor, fontSize: 13, marginTop: 8 }}>
                目标 ¥{ANNUAL_TARGET_CNY.toLocaleString()} / 本年 ¥{ANNUAL_CNY.toLocaleString()}
              </div>
            </div>
          </Card>
        </Col>
      </Row>

      {/* 年度节能收益 + 碳排放趋势 */}
      <Row gutter={[16, 16]}>
        <Col xs={24} lg={12}>
          <Card
            variant="borderless"
            title={<Typography.Text strong>年度节能收益（按月）</Typography.Text>}
            styles={{ body: { paddingTop: 8 } }}
          >
            <ReactECharts option={savingOption} style={{ height: 260 }} notMerge />
          </Card>
        </Col>
        <Col xs={24} lg={12}>
          <Card
            variant="borderless"
            title={<Typography.Text strong>碳排放趋势（按月）</Typography.Text>}
            styles={{ body: { paddingTop: 8 } }}
          >
            <ReactECharts option={carbonOption} style={{ height: 260 }} notMerge />
          </Card>
        </Col>
      </Row>

      {/* 已批准建议联动（来自 /optimize） */}
      <Row gutter={[16, 16]}>
        <Col xs={24}>
          <Card
            variant="borderless"
            title={<Typography.Text strong>已批准节能建议（联动 /optimize）</Typography.Text>}
            extra={
              <Typography.Text style={{ fontSize: 13 }}>
                实时累计：<Typography.Text strong style={{ color: STATUS.ok }}>¥{liveSavingCny.toLocaleString()}</Typography.Text>
                {' / '}
                <Typography.Text strong>{liveSavingCo2.toLocaleString()} kg CO₂</Typography.Text>
              </Typography.Text>
            }
            styles={{ body: { paddingTop: 8 } }}
          >
            {approved.length === 0 ? (
              <Empty
                image={Empty.PRESENTED_IMAGE_SIMPLE}
                description="暂无已批准建议，前往 /optimize 审批后将自动计入节能收益"
              />
            ) : (
              <List
                dataSource={approved}
                renderItem={(s) => (
                  <List.Item>
                    <List.Item.Meta
                      avatar={<CheckCircleOutlined style={{ color: STATUS.ok, fontSize: 20 }} />}
                      title={
                        <span>
                          {s.title}
                          <Tag color="green" style={{ marginLeft: 8 }}>
                            {s.status === 'dispatched' ? '已下发' : '已批准'}
                          </Tag>
                        </span>
                      }
                      description={`${s.device} · 置信度 ${(s.confidence * 100).toFixed(0)}%`}
                    />
                    <div style={{ textAlign: 'right' }}>
                      <div style={{ color: STATUS.ok, fontWeight: 600 }}>¥{s.saving.cny}</div>
                      <div style={{ color: labelColor, fontSize: 12 }}>{s.saving.kwh} kWh · {s.saving.co2} kgCO₂</div>
                    </div>
                  </List.Item>
                )}
              />
            )}
          </Card>
        </Col>
      </Row>
    </PageScaffold>
  );
}
