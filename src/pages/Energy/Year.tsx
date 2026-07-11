import { useMemo } from 'react';
import { Button, Card, Col, Progress, Row, Table, Tag, Typography, theme } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import ReactECharts from 'echarts-for-react';
import {
  AlertOutlined,
  AreaChartOutlined,
  DollarOutlined,
  LineChartOutlined,
  ThunderboltOutlined,
} from '@ant-design/icons';
import PageScaffold from '@/components/PageScaffold';
import {
  OperationsChartCard,
  OperationsInsightBand,
  OperationsMetrics,
  OperationsPanelHeading,
} from '@/components/OperationsUI';
import { BRAND } from '@/theme/tokens';
import { currencyCny, numberZh, percentText } from '@/utils/format';
import { getAvailableDayCount } from './data';
import { createYearAnalytics, ENERGY_SYSTEM_COLORS, type YearBuildingAnalytics } from './analytics';
import { useEnergySystemContext } from './context';

export default function EnergyYear() {
  const { year, navigateGranularity } = useEnergySystemContext();
  const { token } = theme.useToken();
  const now = useMemo(() => new Date(), []);
  const analytics = useMemo(() => createYearAnalytics(year, now), [now, year]);
  const actualMonths = analytics.months.filter((item) => item.energy !== null);
  const bestSystem = [...analytics.systems].sort((a, b) => a.change - b.change)[0];
  const riskBuilding = [...analytics.buildings].sort((a, b) => b.change - a.change)[0];
  const overTarget = analytics.total - analytics.targetTotal;

  const axisColor = token.colorBorder;
  const labelColor = token.colorTextSecondary;
  const splitColor = token.colorSplit;

  const annualTrendOption = {
    aria: { enabled: true, description: `${year} 年月度能耗、年度目标和去年同期对比图。` },
    grid: { left: 62, right: 54, top: 42, bottom: 32 },
    tooltip: { trigger: 'axis' },
    legend: { top: 0, right: 8, textStyle: { color: labelColor, fontSize: 12 } },
    xAxis: {
      type: 'category',
      data: analytics.months.map((item) => item.label),
      axisLine: { lineStyle: { color: axisColor } },
      axisLabel: { color: labelColor, fontSize: 11 },
    },
    yAxis: [
      {
        type: 'value',
        name: 'kWh',
        splitLine: { lineStyle: { color: splitColor } },
        axisLabel: { color: labelColor, fontSize: 11 },
      },
      {
        type: 'value',
        name: 'COP',
        min: 4.6,
        max: 5.8,
        splitLine: { show: false },
        axisLabel: { color: labelColor, fontSize: 11 },
      },
    ],
    series: [
      {
        name: `${year} 年能耗`,
        type: 'bar',
        barMaxWidth: 28,
        data: analytics.months.map((item) => ({
          value: item.energy,
          itemStyle: {
            color: BRAND.teal,
            opacity: item.status === 'mtd' ? 0.66 : 1,
            borderColor: item.status === 'mtd' ? token.colorTextSecondary : 'transparent',
            borderWidth: item.status === 'mtd' ? 1 : 0,
          },
        })),
      },
      {
        name: '去年同期',
        type: 'line',
        smooth: true,
        symbolSize: 5,
        data: analytics.months.map((item) => item.previous),
        lineStyle: { color: '#94a3b8', type: 'dashed', width: 1.6 },
        itemStyle: { color: '#94a3b8' },
      },
      {
        name: '年度目标',
        type: 'line',
        smooth: true,
        symbol: 'none',
        data: analytics.months.map((item) => item.target),
        lineStyle: { color: '#f59e0b', width: 1.8 },
      },
      {
        name: '加权 COP',
        type: 'line',
        yAxisIndex: 1,
        smooth: true,
        symbolSize: 7,
        data: analytics.months.map((item) => item.cop),
        lineStyle: { color: '#8b5cf6', width: 2 },
        itemStyle: { color: '#8b5cf6' },
      },
    ],
  };

  const systemOption = {
    aria: { enabled: true, description: `${year} 年暖通设备类别能耗与去年同期对比。` },
    grid: { left: 92, right: 24, top: 34, bottom: 26 },
    tooltip: { trigger: 'axis', axisPointer: { type: 'shadow' } },
    legend: { top: 0, right: 8, textStyle: { color: labelColor, fontSize: 12 } },
    xAxis: {
      type: 'value',
      splitLine: { lineStyle: { color: splitColor } },
      axisLabel: { color: labelColor, fontSize: 11 },
    },
    yAxis: {
      type: 'category',
      data: analytics.systems.map((item) => item.label),
      axisLine: { lineStyle: { color: axisColor } },
      axisLabel: { color: labelColor, fontSize: 12 },
    },
    series: [
      {
        name: '本年累计',
        type: 'bar',
        barWidth: 14,
        data: analytics.systems.map((item) => ({ value: item.energy, itemStyle: { color: ENERGY_SYSTEM_COLORS[item.type] } })),
      },
      {
        name: '去年同期',
        type: 'bar',
        barWidth: 10,
        data: analytics.systems.map((item) => ({ value: item.previous, itemStyle: { color: '#94a3b8', opacity: 0.46 } })),
      },
    ],
  };

  const monthEvents = {
    click: (params: { dataIndex?: number }) => {
      if (typeof params.dataIndex !== 'number') return;
      const item = analytics.months[params.dataIndex];
      if (!item || item.status === 'future') return;
      const day = getAvailableDayCount(year, item.month, now);
      navigateGranularity('month', { month: item.month, day: Math.max(1, day), device: null });
    },
  };

  const buildingColumns: ColumnsType<YearBuildingAnalytics> = [
    {
      title: '建筑 / 区域',
      dataIndex: 'name',
      key: 'name',
      render: (value: string, row) => (
        <div className="energy-device-name">
          <Typography.Text strong>{value}</Typography.Text>
          <span className="energy-device-meta">{row.id} · {numberZh(row.area)} m²</span>
        </div>
      ),
    },
    {
      title: '年度能耗',
      dataIndex: 'energy',
      key: 'energy',
      align: 'right',
      sorter: (a, b) => a.energy - b.energy,
      render: (value: number) => `${numberZh(value)} kWh`,
    },
    {
      title: '单位面积能耗',
      dataIndex: 'intensity',
      key: 'intensity',
      align: 'right',
      render: (value: number) => `${value} kWh/m²`,
    },
    {
      title: '目标完成',
      key: 'targetRate',
      width: 150,
      render: (_, row) => {
        const rate = Math.round((row.energy / Math.max(row.target, 1)) * 100);
        return <Progress percent={rate} size="small" status={rate > 100 ? 'exception' : 'active'} />;
      },
    },
    {
      title: '同比',
      dataIndex: 'change',
      key: 'change',
      align: 'right',
      render: (value: number) => (
        <span className={`energy-change ${value <= 0 ? 'is-lower' : 'is-higher'}`}>
          {percentText(value, { signed: true, digits: 1 })}
        </span>
      ),
    },
  ];

  return (
    <PageScaffold
      title="年度能耗分析"
      subtitle="面向经营复盘与年度决策，评估累计能耗、费用、能效、目标完成和建筑绩效。"
      eyebrow="长期绩效与经营目标"
      extra={<Tag color={actualMonths.at(-1)?.status === 'mtd' ? 'processing' : 'success'}>{actualMonths.at(-1)?.status === 'mtd' ? 'YTD / MTD' : '完整年度'}</Tag>}
    >
      <OperationsMetrics
        items={[
          {
            label: 'YTD 累计能耗',
            value: numberZh(analytics.total),
            suffix: 'kWh',
            detail: `${actualMonths.length} 个已计量月份 · ${currencyCny(analytics.cost)}`,
            icon: <ThunderboltOutlined />,
            tone: 'accent',
          },
          {
            label: '同比节能',
            value: Math.abs(analytics.savingRate).toFixed(1),
            suffix: '%',
            detail: `${analytics.savingRate >= 0 ? '低于' : '高于'}去年同期 ${numberZh(Math.abs(analytics.previousTotal - analytics.total))} kWh`,
            icon: <LineChartOutlined />,
            tone: analytics.savingRate >= 0 ? 'positive' : 'warning',
          },
          {
            label: '年度加权 COP',
            value: analytics.weightedCop.toFixed(2),
            detail: '按月度输入电量加权，不采用算术平均',
            icon: <AreaChartOutlined />,
            tone: analytics.weightedCop >= 5.15 ? 'positive' : 'warning',
          },
          {
            label: '累计能源费用',
            value: currencyCny(analytics.cost),
            detail: `折算碳排 ${numberZh(analytics.carbon)} kgCO₂e`,
            icon: <DollarOutlined />,
          },
        ]}
      />

      <OperationsInsightBand
        title="年度经营结论"
        icon={<AlertOutlined />}
        items={[
          {
            key: 'target',
            text: overTarget > 0
              ? `当前累计能耗高于同期目标 ${numberZh(overTarget)} kWh，目标完成率 ${analytics.targetRate.toFixed(1)}%。`
              : `当前累计能耗低于同期目标 ${numberZh(Math.abs(overTarget))} kWh，年度目标保持可控。`,
            tone: overTarget > 0 ? 'warning' : 'positive',
          },
          {
            key: 'system',
            text: `${bestSystem.label}同比变化 ${percentText(bestSystem.change, { signed: true, digits: 1 })}，是当前改善最明显的设备类别。`,
            tone: bestSystem.change <= 0 ? 'positive' : 'warning',
          },
          {
            key: 'building',
            text: `${riskBuilding.name}同比变化 ${percentText(riskBuilding.change, { signed: true, digits: 1 })}，建议进入月度页定位异常月份。`,
            tone: riskBuilding.change > 0 ? 'warning' : 'info',
          },
        ]}
      />

      <Row gutter={[16, 16]} className="ops-chart-row">
        <Col xs={24} xl={16}>
          <OperationsChartCard
            title={`${year} 年能耗、目标与 COP`}
            description="点击已发生月份进入月度分析，继续定位异常日期和设备类别"
            meta={`${actualMonths.length} 个月已计量`}
            extra={<span className="ops-chart-status">目标线 + 去年同期</span>}
            height={340}
            ariaLabel="年度月度能耗、目标、去年同期和加权COP趋势"
            footer={<><span>柱：本年能耗</span><span>线：去年同期 / 目标 / COP</span></>}
          >
            <ReactECharts option={annualTrendOption} onEvents={monthEvents} style={{ height: '100%' }} notMerge />
          </OperationsChartCard>
        </Col>
        <Col xs={24} xl={8}>
          <Card
            variant="borderless"
            className="energy-year-target-card"
            title={<OperationsPanelHeading title="年度目标与碳绩效" meta="经营口径" />}
          >
            <div className="energy-year-target-block">
              <div className="energy-year-target-head">
                <span>同期目标完成率</span>
                <strong>{analytics.targetRate.toFixed(1)}%</strong>
              </div>
              <Progress percent={Math.min(120, Math.round(analytics.targetRate))} status={analytics.targetRate > 100 ? 'exception' : 'active'} />
              <Typography.Paragraph type="secondary">
                目标值 {numberZh(analytics.targetTotal)} kWh；超过 100% 表示实际能耗高于目标。
              </Typography.Paragraph>
            </div>
            <div className="energy-year-target-grid">
              <div><span>碳排放</span><strong>{numberZh(analytics.carbon)} kgCO₂e</strong></div>
              <div><span>综合电价</span><strong>0.78 元/kWh</strong></div>
              <div><span>同比基准</span><strong>{numberZh(analytics.previousTotal)} kWh</strong></div>
              <div><span>节能收益</span><strong>{currencyCny(Math.max(0, analytics.previousTotal - analytics.total) * 0.78)}</strong></div>
            </div>
            <Button block type="primary" onClick={() => navigateGranularity('month')}>进入月度异常定位</Button>
          </Card>
        </Col>
      </Row>

      <Row gutter={[16, 16]} className="ops-chart-row">
        <Col xs={24} lg={10}>
          <OperationsChartCard
            title="设备类别年度贡献"
            description="本年累计与去年同期对比，识别系统级改善或反弹"
            meta={`${analytics.systems.length} 类系统`}
            height={280}
            ariaLabel="年度设备类别能耗与去年同期横向对比图"
            footer={<span>点击年度主图中的月份可继续向下钻取</span>}
          >
            <ReactECharts option={systemOption} style={{ height: '100%' }} notMerge />
          </OperationsChartCard>
        </Col>
        <Col xs={24} lg={14}>
          <Card
            variant="borderless"
            className="energy-table-card"
            title={<OperationsPanelHeading title="建筑年度绩效排名" meta={`${analytics.buildings.length} 个范围`} />}
          >
            <Table<YearBuildingAnalytics>
              rowKey="id"
              size="middle"
              columns={buildingColumns}
              dataSource={analytics.buildings}
              pagination={false}
              scroll={{ x: 760 }}
            />
          </Card>
        </Col>
      </Row>
    </PageScaffold>
  );
}
