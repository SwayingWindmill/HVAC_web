import { useMemo, useState } from 'react';
import {
  Card,
  Col,
  Empty,
  Grid,
  Progress,
  Row,
  Segmented,
  Space,
  Table,
  Tag,
  Typography,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import {
  AlertOutlined,
  CloudOutlined,
  FundOutlined,
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
  OperationsChartCard,
  OperationsInsightBand,
  OperationsMetrics,
  OperationsPanelHeading,
} from '@/components/OperationsUI';
import { currencyCny } from '@/utils/format';
import {
  PRICE, CARBON_FACTOR, INVESTMENT, ANNUAL_TARGET_CNY,
  ANNUAL_SAVING_KWH, ANNUAL_KWH, MONTH_LABELS, ANNUAL_CNY,
} from './meta';

const TOU_PRICE = {
  peak: 1.18,
  flat: 0.85,
  valley: 0.42,
} as const;

type TouBucket = keyof typeof TOU_PRICE;

type SavingRow = {
  id: string;
  title: string;
  device: string;
  status: string;
  savingCny: number;
  savingKwh: number;
  savingCo2: number;
  confidence: number;
  paybackDays?: number;
};

const bucketLabel: Record<TouBucket, string> = {
  peak: '峰时段',
  flat: '平时段',
  valley: '谷时段',
};

const bucketColor: Record<TouBucket, string> = {
  peak: STATUS.warn,
  flat: BRAND.teal,
  valley: STATUS.info,
};

const getTouBucket = (hour: number): TouBucket => {
  if ((hour >= 8 && hour < 11) || (hour >= 18 && hour < 22)) return 'peak';
  if (hour >= 23 || hour < 7) return 'valley';
  return 'flat';
};

const formatCurrency = currencyCny;

export default function Cost() {
  const screens = Grid.useBreakpoint();
  const compactTable = !screens.xl;
  const [range, setRange] = useState<Range>('day');
  const mode = useUi((s) => s.themeMode);
  const dark = mode === 'dark';

  // 历史层（React Query 聚合能耗 kWh）
  const { data: dayData } = useBuildingTimeseries('day');
  const { data: trend, isLoading } = useBuildingTimeseries(range);

  // 实时层（WebSocket 推送，驱动今日电费 / 碳排放末端延伸）
  const live = useTelemetryLive(MOCK_DEVICES, ['power']);
  const totalPower = MOCK_DEVICES.reduce((sum, id) => sum + (live.get(id, 'power') ?? 0), 0);

  // /optimize 已批准 / 已下发建议（联动累计节能收益）
  const suggestions = useOps((s) => s.suggestions);
  const approved = suggestions.filter((s) => s.status === 'approved' || s.status === 'dispatched');
  const liveSavingCny = approved.reduce((sum, s) => sum + s.saving.cny, 0);
  const liveSavingKwh = approved.reduce((sum, s) => sum + s.saving.kwh, 0);
  const liveSavingCo2 = approved.reduce((sum, s) => sum + s.saving.co2, 0);

  const savingRows: SavingRow[] = approved.map((s) => ({
    id: s.id,
    title: s.title,
    device: s.device,
    status: s.status,
    savingCny: s.saving.cny,
    savingKwh: s.saving.kwh,
    savingCo2: s.saving.co2,
    confidence: s.confidence,
    paybackDays: s.paybackDays,
  }));

  // ---- 峰平谷成本派生 ----
  const touCost = useMemo(() => {
    const acc: Record<TouBucket, { kwh: number; cost: number }> = {
      peak: { kwh: 0, cost: 0 },
      flat: { kwh: 0, cost: 0 },
      valley: { kwh: 0, cost: 0 },
    };
    dayData.forEach((point) => {
      const hour = new Date(point.ts).getHours();
      const bucket = getTouBucket(hour);
      acc[bucket].kwh += point.value;
      acc[bucket].cost += point.value * TOU_PRICE[bucket];
    });
    return acc;
  }, [dayData]);

  const todayKwh = dayData.reduce((sum, p) => sum + p.value, 0);
  const todayCost = Object.values(touCost).reduce((sum, item) => sum + item.cost, 0);
  const avgPrice = todayKwh ? todayCost / todayKwh : PRICE;
  const peakCostRatio = todayCost ? Math.round((touCost.peak.cost / todayCost) * 100) : 0;
  const valleyKwhRatio = todayKwh ? Math.round((touCost.valley.kwh / todayKwh) * 100) : 0;

  const cumulativeSavingCny = ANNUAL_CNY + liveSavingCny; // 本年基线 + 实时已批准
  const cumulativeSavingKwh = ANNUAL_KWH + liveSavingKwh;
  const cumulativeCo2T = (cumulativeSavingKwh * CARBON_FACTOR) / 1000;

  const roi = (cumulativeSavingCny / INVESTMENT) * 100; // %
  const paybackYr = INVESTMENT / Math.max(cumulativeSavingCny, 1); // 年
  const achieveRate = Math.min(100, (cumulativeSavingCny / ANNUAL_TARGET_CNY) * 100);

  // ---- 成本趋势（kWh × 峰平谷/均价 = ¥），末端随实时延伸 ----
  const lastTs = trend.length ? trend[trend.length - 1].ts : Date.now();
  const costTrend = trend.map((point) => {
    const hour = new Date(point.ts).getHours();
    const bucket = getTouBucket(hour);
    return { ts: point.ts, value: Math.round(point.value * TOU_PRICE[bucket]) };
  });
  if (totalPower > 0 && Date.now() - lastTs > 0) {
    const nowBucket = getTouBucket(new Date().getHours());
    costTrend.push({ ts: Date.now(), value: Math.round(totalPower * TOU_PRICE[nowBucket]) });
  }

  const fmtX = (ts: number): string => {
    const date = new Date(ts);
    if (range === 'day') return `${String(date.getHours()).padStart(2, '0')}:00`;
    if (range === 'week') return ['日', '一', '二', '三', '四', '五', '六'][date.getDay()];
    return `${date.getMonth() + 1}/${date.getDate()}`;
  };

  // ---- 图表配色 ----
  const axisColor = dark ? '#444' : '#ccc';
  const labelColor = dark ? '#aaa' : '#666';
  const splitColor = dark ? '#222' : '#eee';

  const costOption = {
    aria: { enabled: true, description: '建筑能耗按峰平谷电价折算的成本趋势。' },
    grid: { left: 56, right: 16, top: 34, bottom: 30 },
    tooltip: { trigger: 'axis', valueFormatter: (value: number) => formatCurrency(value) },
    xAxis: {
      type: 'category',
      data: costTrend.map((point) => fmtX(point.ts)),
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
        name: '电费',
        type: 'line',
        smooth: true,
        data: costTrend.map((point) => point.value),
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

  const touOption = {
    aria: { enabled: true, description: '今日峰时、平时和谷时费用占比。' },
    tooltip: { trigger: 'item', formatter: '{b}: ¥{c} ({d}%)' },
    legend: { bottom: 0, textStyle: { color: labelColor, fontSize: 12 } },
    series: [
      {
        type: 'pie',
        radius: ['48%', '72%'],
        center: ['50%', '43%'],
        label: { color: labelColor, formatter: '{b}\n{d}%', fontSize: 11 },
        itemStyle: { borderColor: dark ? '#141414' : '#fff', borderWidth: 2 },
        data: (Object.keys(touCost) as TouBucket[]).map((bucket) => ({
          name: bucketLabel[bucket],
          value: Math.round(touCost[bucket].cost),
          itemStyle: { color: bucketColor[bucket] },
        })),
      },
    ],
  };

  // 年度节能收益（按月，¥）
  const annualSavingCny = ANNUAL_SAVING_KWH.map((kwh) => Math.round(kwh * PRICE));
  const savingOption = {
    aria: { enabled: true, description: '年度逐月节能收益及月均目标。' },
    grid: { left: 56, right: 16, top: 28, bottom: 28 },
    tooltip: { trigger: 'axis', valueFormatter: (value: number) => formatCurrency(value) },
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
        name: '节能收益',
        type: 'bar',
        data: annualSavingCny.map((value, index) => ({
          value,
          itemStyle: { color: index === 11 ? BRAND.tealStrong : BRAND.teal },
        })),
        barWidth: '52%',
        markLine: { data: [{ yAxis: Math.round(ANNUAL_TARGET_CNY / 12), name: '月均目标' }] },
      },
    ],
  };

  // 碳排放趋势（按月，t CO2）
  const carbonOption = {
    aria: { enabled: true, description: '年度逐月节能电量折算的碳减排趋势。' },
    grid: { left: 56, right: 16, top: 28, bottom: 28 },
    tooltip: { trigger: 'axis', valueFormatter: (value: number) => `${value} t` },
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
        name: '减排量',
        type: 'line',
        smooth: true,
        data: ANNUAL_SAVING_KWH.map((kwh) => +(kwh * CARBON_FACTOR / 1000).toFixed(2)),
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

  const anomalyItems = [
    peakCostRatio > 55
      ? { type: 'warning' as const, text: `峰时段费用占比 ${peakCostRatio}%，建议将可延迟负荷转移至平/谷时段。` }
      : { type: 'success' as const, text: `峰时段费用占比 ${peakCostRatio}%，成本结构处于可控区间。` },
    valleyKwhRatio < 18
      ? { type: 'warning' as const, text: `谷时段用电占比仅 ${valleyKwhRatio}%，可评估预冷、蓄冷或错峰策略。` }
      : { type: 'success' as const, text: `谷时段用电占比 ${valleyKwhRatio}%，错峰利用较好。` },
    approved.length === 0
      ? { type: 'info' as const, text: '暂无已批准优化建议，节能收益主要来自年度基线。' }
      : { type: 'success' as const, text: `已批准/下发 ${approved.length} 条优化建议，实时贡献 ${formatCurrency(liveSavingCny)}。` },
  ];

  const columns: ColumnsType<SavingRow> = [
    {
      title: '建议',
      dataIndex: 'title',
      key: 'title',
      width: 260,
      render: (title: string, row) => (
        <Space direction="vertical" size={0}>
          <Typography.Text strong>{title}</Typography.Text>
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>{row.id} · {row.device}</Typography.Text>
        </Space>
      ),
    },
    {
      title: '状态',
      dataIndex: 'status',
      key: 'status',
      width: 100,
      render: (status: string) => <Tag color={status === 'dispatched' ? 'blue' : 'green'}>{status === 'dispatched' ? '已下发' : '已批准'}</Tag>,
    },
    {
      title: '费用收益',
      dataIndex: 'savingCny',
      key: 'savingCny',
      width: 130,
      sorter: (a, b) => a.savingCny - b.savingCny,
      render: (value: number) => <Typography.Text strong style={{ color: STATUS.ok }}>{formatCurrency(value)}</Typography.Text>,
    },
    {
      title: '节电 / 减排',
      key: 'energyCarbon',
      width: 160,
      render: (_, row) => `${row.savingKwh} kWh · ${row.savingCo2} kgCO₂`,
    },
    {
      title: '置信度',
      dataIndex: 'confidence',
      key: 'confidence',
      width: 130,
      render: (confidence: number) => <Progress percent={Math.round(confidence * 100)} size="small" />,
    },
    {
      title: '回收周期',
      dataIndex: 'paybackDays',
      key: 'paybackDays',
      width: 100,
      render: (days?: number) => days ? `${days} 天` : '-',
    },
  ];

  const tableColumns = compactTable
    ? columns.filter((column) => ['title', 'status', 'savingCny', 'confidence'].includes(String(column.key)))
    : columns;
  const rangeLabel = range === 'day' ? '日' : range === 'week' ? '周' : range === 'month' ? '月' : range;

  return (
    <PageScaffold
      title="成本与绩效"
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
      <OperationsMetrics
        items={[
          { label: '今日电费', value: formatCurrency(Math.round(todayCost)), detail: `均价 ${avgPrice.toFixed(2)} ¥/kWh`, tone: 'accent' },
          { label: '累计节能收益', value: formatCurrency(cumulativeSavingCny), detail: '年度基线 + 已批准建议', tone: 'positive' },
          { label: '累计减碳', value: cumulativeCo2T.toFixed(1), suffix: 't CO₂', detail: `因子 ${CARBON_FACTOR} kg/kWh`, icon: <CloudOutlined /> },
          { label: '投资回报率 ROI', value: roi.toFixed(1), suffix: '%', detail: `回收期约 ${paybackYr.toFixed(1)} 年`, icon: <FundOutlined />, tone: roi >= 100 ? 'positive' : 'accent' },
        ]}
      />

      <OperationsInsightBand
        title="成本诊断"
        icon={<AlertOutlined />}
        items={anomalyItems.map((item) => ({
          key: item.text,
          text: item.text,
          tone: item.type === 'warning' ? 'warning' : item.type === 'success' ? 'positive' : 'info',
        }))}
      />

      <Row gutter={[16, 16]} className="ops-chart-row">
        <Col xs={24} lg={15}>
          <OperationsChartCard
            title="能耗成本趋势"
            description="按峰平谷电价将能耗折算为费用，实时功率作为最新末点延伸"
            meta={`${rangeLabel}视图`}
            height={320}
            loading={isLoading && trend.length === 0}
            empty={!isLoading && costTrend.length === 0}
            emptyDescription="当前范围暂无成本趋势数据"
            ariaLabel="建筑能耗成本趋势图"
            footer={<><span>口径：能耗 × 对应时段电价</span><span>实时末点：当前功率折算</span></>}
          >
            <ReactECharts option={costOption} style={{ height: '100%' }} notMerge />
          </OperationsChartCard>
        </Col>
        <Col xs={24} lg={9}>
          <OperationsChartCard
            title="今日峰平谷费用结构"
            description="展示各电价时段的费用贡献，用于判断错峰空间"
            extra={<span className="ops-chart-status">示例电价</span>}
            height={320}
            empty={todayKwh <= 0}
            emptyDescription="暂无今日分时能耗数据"
            ariaLabel="今日峰平谷费用占比环图"
            footer={<><span>峰 {TOU_PRICE.peak} · 平 {TOU_PRICE.flat} · 谷 {TOU_PRICE.valley} ¥/kWh</span><span>峰时费用占比 {peakCostRatio}%</span></>}
          >
            <ReactECharts option={touOption} style={{ height: '100%' }} notMerge />
          </OperationsChartCard>
        </Col>
      </Row>

      <Row gutter={[16, 16]} className="ops-chart-row">
        <Col xs={24} lg={9}>
          <OperationsChartCard
            title="年度节能达标率"
            description="累计节能收益相对年度经营目标的完成进度"
            extra={<span className={`ops-chart-status ${achieveRate >= 100 ? 'is-positive' : 'is-warning'}`}>{achieveRate >= 100 ? '已达标' : '推进中'}</span>}
            height={260}
            ariaLabel="年度节能收益目标完成率"
            footer={<span>目标 {formatCurrency(ANNUAL_TARGET_CNY)} · 当前 {formatCurrency(cumulativeSavingCny)}</span>}
          >
            <div className="ops-progress-focus">
              <Progress
                type="dashboard"
                percent={+achieveRate.toFixed(0)}
                strokeColor={achieveRate >= 100 ? STATUS.ok : BRAND.teal}
                size={180}
                format={(percent) => `${percent}%`}
              />
              <div className="ops-progress-caption">
                距离年度目标还差 {formatCurrency(Math.max(0, ANNUAL_TARGET_CNY - cumulativeSavingCny))}
              </div>
            </div>
          </OperationsChartCard>
        </Col>
        <Col xs={24} lg={15}>
          <Card
            variant="borderless"
            title={<OperationsPanelHeading title="峰平谷成本明细" meta="示例电价口径" />}
          >
            <OperationsMetrics
              className="is-embedded"
              ariaLabel="峰平谷成本明细"
              items={(Object.keys(touCost) as TouBucket[]).map((bucket) => ({
                key: bucket,
                label: bucketLabel[bucket],
                value: formatCurrency(Math.round(touCost[bucket].cost)),
                detail: `${Math.round(touCost[bucket].kwh)} kWh · ${TOU_PRICE[bucket]} ¥/kWh`,
                tone: bucket === 'peak' ? 'warning' : bucket === 'valley' ? 'accent' : 'default',
              }))}
            />
          </Card>
        </Col>
      </Row>

      <Row gutter={[16, 16]} className="ops-chart-row">
        <Col xs={24} lg={12}>
          <OperationsChartCard
            title="年度节能收益"
            description="按月折算节能电量对应的经营收益"
            meta="年度累计"
            height={280}
            empty={annualSavingCny.length === 0}
            emptyDescription="暂无年度节能收益数据"
            ariaLabel="年度逐月节能收益柱状图"
            footer={<span>目标月均 {formatCurrency(Math.round(ANNUAL_TARGET_CNY / 12))}</span>}
          >
            <ReactECharts option={savingOption} style={{ height: '100%' }} notMerge />
          </OperationsChartCard>
        </Col>
        <Col xs={24} lg={12}>
          <OperationsChartCard
            title="碳减排折算"
            description="根据节能电量与碳排放因子折算月度减排量"
            meta={`因子 ${CARBON_FACTOR} kg/kWh`}
            height={280}
            empty={ANNUAL_SAVING_KWH.length === 0}
            emptyDescription="暂无碳减排折算数据"
            ariaLabel="年度逐月碳减排趋势图"
            footer={<span>累计减排 {cumulativeCo2T.toFixed(1)} t CO₂</span>}
          >
            <ReactECharts option={carbonOption} style={{ height: '100%' }} notMerge />
          </OperationsChartCard>
        </Col>
      </Row>

      <Card
        variant="borderless"
        title={<OperationsPanelHeading title="已批准节能建议" meta="联动 /optimize" />}
        extra={
          <span className="ops-chart-status is-positive">
            实时累计 {formatCurrency(liveSavingCny)} · {liveSavingCo2.toLocaleString()} kg CO₂
          </span>
        }
      >
        {savingRows.length === 0 ? (
          <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无已批准建议，前往 /optimize 审批后将自动计入节能收益" />
        ) : (
          <Table<SavingRow>
            rowKey="id"
            size="middle"
            columns={tableColumns}
            dataSource={savingRows}
            pagination={false}
            scroll={{ x: compactTable ? 620 : 880 }}
          />
        )}
      </Card>
    </PageScaffold>
  );
}
