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
import ReactECharts from 'echarts-for-react';
import {
  AlertOutlined,
  DashboardOutlined,
  FireOutlined,
  LineChartOutlined,
  ThunderboltOutlined,
} from '@ant-design/icons';
import {
  useBuildingTimeseries,
  useTelemetryLive,
  MOCK_DEVICES,
  type Range,
} from '@/api';
import { BRAND, STATUS } from '@/theme/tokens';
import { numberZh } from '@/utils/format';
import { useUi } from '@/store/ui';
import PageScaffold from '@/components/PageScaffold';
import {
  OperationsChartCard,
  OperationsInsightBand,
  OperationsMetrics,
  OperationsPanelHeading,
} from '@/components/OperationsUI';
import { DEVICE_META, STATUS_INFO, STATUS_MAP, TYPE_LABEL, type DeviceType } from '@/pages/Assets/meta';

// 系统配色：冷机=teal / 冷冻泵=蓝 / 空调末端=amber
const SYS_COLORS: Record<DeviceType, string> = {
  chiller: BRAND.teal,
  pump: '#3b82f6',
  ahu: '#f59e0b',
};

const SYS_ORDER: DeviceType[] = ['chiller', 'pump', 'ahu'];

type DeviceEnergyRow = {
  id: string;
  name: string;
  type: DeviceType;
  zoneName: string;
  ratedPower: number;
  power: number;
  loadRate: number;
  contribution: number;
};

const formatHour = (ts: number) => `${String(new Date(ts).getHours()).padStart(2, '0')}:00`;

export default function Energy() {
  const screens = Grid.useBreakpoint();
  const compactTable = !screens.xl;
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

  const deviceRows = useMemo<DeviceEnergyRow[]>(() => {
    const raw = MOCK_DEVICES
      .map((id) => {
        const meta = DEVICE_META[id];
        if (!meta) return null;
        const power = Math.round(live.get(id, 'power') ?? 0);
        return {
          id,
          name: meta.name,
          type: meta.type,
          zoneName: meta.zoneName,
          ratedPower: meta.ratedPower,
          power,
          loadRate: meta.ratedPower ? Math.round((power / meta.ratedPower) * 100) : 0,
          contribution: 0,
        };
      })
      .filter(Boolean) as DeviceEnergyRow[];
    const total = raw.reduce((sum, row) => sum + row.power, 0);
    return raw.map((row) => ({ ...row, contribution: total ? Math.round((row.power / total) * 100) : 0 }));
  }, [live]);

  const totalPower = deviceRows.reduce((sum, row) => sum + row.power, 0);
  const totalRatedPower = deviceRows.reduce((sum, row) => sum + row.ratedPower, 0);
  const buildingLoadRate = totalRatedPower ? Math.round((totalPower / totalRatedPower) * 100) : 0;

  // KPI 派生（power 为瞬时功率 kW，按 1h 采样近似 kWh）
  const todayKwh = dayData.reduce((sum, p) => sum + p.value, 0);
  const monthKwh = monthData.reduce((sum, p) => sum + p.value, 0);
  const peakPoint = dayData.reduce((max, p) => (p.value > max.value ? p : max), { ts: 0, value: 0 });
  const valleyPoint = dayData.reduce((min, p) => (min.value === 0 || p.value < min.value ? p : min), { ts: 0, value: 0 });
  const peakValleyGap = Math.round(Math.max(0, peakPoint.value - valleyPoint.value));

  // 趋势末端随实时延伸：把最新实时总功率作为末点追加
  const lastTs = trend.length ? trend[trend.length - 1].ts : Date.now();
  const extended = [...trend];
  if (totalPower > 0 && Date.now() - lastTs > 0) {
    extended.push({ ts: Date.now(), value: Math.round(totalPower) });
  }

  // 系统能耗分解（按设备类型分组实时功率）
  const sysPower: Record<DeviceType, number> = { chiller: 0, pump: 0, ahu: 0 };
  deviceRows.forEach((row) => {
    sysPower[row.type] += row.power;
  });
  const pieData = SYS_ORDER.map((type) => ({
    name: TYPE_LABEL[type],
    value: Math.round(sysPower[type]),
    itemStyle: { color: SYS_COLORS[type] },
  }));

  // 近 7 日对比（week 聚合为每日能耗和）
  const weekKwh = weekData.map((p) => p.value);
  const last7 = weekKwh[weekKwh.length - 1] ?? 0;
  const prev6 = weekKwh.length > 1 ? weekKwh.slice(0, -1).reduce((sum, value) => sum + value, 0) / (weekKwh.length - 1) : 0;
  const weekDrift = prev6 > 0 ? Math.round(((last7 - prev6) / prev6) * 100) : 0;

  const baseline = extended.map((point, index) => {
    const wave = 1 + (index % 5) * 0.012;
    return Math.round(point.value * 1.08 * wave);
  });
  const actual = extended.map((point) => Math.round(point.value));
  const savedVsBaseline = Math.max(0, baseline.reduce((sum, value, index) => sum + (value - (actual[index] ?? value)), 0));

  const anomalyItems = [
    peakValleyGap > 300
      ? { type: 'warning' as const, text: `今日峰谷差 ${peakValleyGap} kW，建议关注尖峰负荷转移。` }
      : { type: 'success' as const, text: '今日峰谷差处于可控区间。' },
    weekDrift > 8
      ? { type: 'error' as const, text: `近 7 日末日能耗高于均值 ${weekDrift}%，建议核查日程和异常设备。` }
      : { type: 'success' as const, text: `近 7 日能耗趋势稳定，末日环比 ${weekDrift > 0 ? '+' : ''}${weekDrift}%。` },
    buildingLoadRate > 85
      ? { type: 'warning' as const, text: `实时负荷率 ${buildingLoadRate}%，接近高负荷运行区间。` }
      : { type: 'info' as const, text: `实时负荷率 ${buildingLoadRate}%，仍有调节余量。` },
  ];

  const axisColor = dark ? '#444' : '#ccc';
  const labelColor = dark ? '#aaa' : '#666';
  const splitColor = dark ? '#222' : '#eee';

  const fmtX = (ts: number): string => {
    const date = new Date(ts);
    if (range === 'day') return `${String(date.getHours()).padStart(2, '0')}:00`;
    if (range === 'week') return ['日', '一', '二', '三', '四', '五', '六'][date.getDay()];
    return `${date.getMonth() + 1}/${date.getDate()}`;
  };

  const trendOption = {
    aria: { enabled: true, description: '建筑能耗实际值与模拟基线趋势，支持按日、周、月查看。' },
    grid: { left: 52, right: 16, top: 36, bottom: 30 },
    tooltip: { trigger: 'axis' },
    legend: { top: 0, right: 8, textStyle: { color: labelColor, fontSize: 12 } },
    xAxis: {
      type: 'category',
      data: extended.map((p) => fmtX(p.ts)),
      axisLine: { lineStyle: { color: axisColor } },
      axisLabel: { color: labelColor, fontSize: 11 },
    },
    yAxis: {
      type: 'value',
      name: 'kWh / kW',
      splitLine: { lineStyle: { color: splitColor } },
      axisLabel: { color: labelColor, fontSize: 11 },
    },
    series: [
      {
        name: '基线能耗',
        type: 'line',
        smooth: true,
        data: baseline,
        symbol: 'none',
        lineStyle: { color: '#94a3b8', width: 1.5, type: 'dashed' },
      },
      {
        name: '实际能耗',
        type: 'line',
        smooth: true,
        data: actual,
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
        markPoint: range === 'day' ? { data: [{ type: 'max', name: '峰值' }, { type: 'min', name: '谷值' }] } : undefined,
      },
    ],
  };

  const pieOption = {
    aria: { enabled: true, description: '冷机、冷冻泵和空调末端实时功率占比。' },
    tooltip: { trigger: 'item', formatter: '{b}: {c} kW ({d}%)' },
    legend: {
      bottom: 0,
      textStyle: { color: labelColor, fontSize: 12 },
      data: SYS_ORDER.map((type) => TYPE_LABEL[type]),
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
    aria: { enabled: true, description: '近七日建筑能耗柱状对比，并标记前期均值。' },
    grid: { left: 52, right: 16, top: 28, bottom: 28 },
    tooltip: { trigger: 'axis' },
    xAxis: {
      type: 'category',
      data: weekKwh.map((_, index) => `D${index + 1}`),
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
        name: '日能耗',
        type: 'bar',
        data: weekKwh.map((value, index) => ({
          value: Math.round(value),
          itemStyle: { color: index === weekKwh.length - 1 ? BRAND.tealStrong : BRAND.teal },
        })),
        barWidth: '52%',
        markLine: prev6 ? { data: [{ yAxis: Math.round(prev6), name: '前期均值' }] } : undefined,
      },
    ],
  };

  const loadCurveOption = {
    aria: { enabled: true, description: '今日逐小时负荷柱状图，峰值时段使用风险色标记。' },
    grid: { left: 52, right: 16, top: 28, bottom: 28 },
    tooltip: { trigger: 'axis' },
    xAxis: {
      type: 'category',
      data: dayData.map((point) => formatHour(point.ts)),
      axisLine: { lineStyle: { color: axisColor } },
      axisLabel: { color: labelColor, fontSize: 11 },
    },
    yAxis: {
      type: 'value',
      name: 'kW',
      splitLine: { lineStyle: { color: splitColor } },
      axisLabel: { color: labelColor, fontSize: 11 },
    },
    series: [
      {
        name: '日内负荷',
        type: 'bar',
        data: dayData.map((point) => ({
          value: Math.round(point.value),
          itemStyle: { color: point.value === peakPoint.value ? STATUS.warn : BRAND.teal },
        })),
        barWidth: '58%',
      },
    ],
  };

  const columns: ColumnsType<DeviceEnergyRow> = [
    {
      title: '设备',
      dataIndex: 'name',
      key: 'name',
      width: 180,
      render: (name: string, row) => (
        <Space direction="vertical" size={0}>
          <Typography.Text strong>{name}</Typography.Text>
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>{row.id} · {row.zoneName}</Typography.Text>
        </Space>
      ),
    },
    {
      title: '系统',
      dataIndex: 'type',
      key: 'type',
      width: 110,
      render: (type: DeviceType) => <Tag color={SYS_COLORS[type]}>{TYPE_LABEL[type]}</Tag>,
    },
    {
      title: '实时功率',
      dataIndex: 'power',
      key: 'power',
      width: 120,
      sorter: (a, b) => a.power - b.power,
      render: (power: number) => <Typography.Text strong>{power} kW</Typography.Text>,
    },
    {
      title: '负荷率',
      dataIndex: 'loadRate',
      key: 'loadRate',
      width: 150,
      sorter: (a, b) => a.loadRate - b.loadRate,
      render: (loadRate: number) => <Progress percent={loadRate} size="small" status={loadRate > 90 ? 'exception' : 'active'} />,
    },
    {
      title: '贡献度',
      dataIndex: 'contribution',
      key: 'contribution',
      width: 110,
      render: (contribution: number) => `${contribution}%`,
    },
    {
      title: '状态',
      key: 'status',
      width: 100,
      render: (_, row) => {
        const status = STATUS_MAP[row.id];
        const info = status ? STATUS_INFO[status] : undefined;
        return info ? <Tag color={info.color}>{info.label}</Tag> : <Tag>未知</Tag>;
      },
    },
  ];

  const tableColumns = compactTable
    ? columns.filter((column) => ['name', 'power', 'loadRate', 'status'].includes(String(column.key)))
    : columns;
  const rangeLabel = range === 'day' ? '日' : range === 'week' ? '周' : range === 'month' ? '月' : range;

  return (
    <PageScaffold
      title="能耗分析"
      subtitle="建筑级趋势、基线对比、系统分解、峰谷负荷与设备贡献分析。"
      eyebrow="能源与负荷"
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
          { label: '今日能耗', value: numberZh(Math.round(todayKwh)), suffix: 'kWh', detail: `本月累计 ${numberZh(Math.round(monthKwh))} kWh`, icon: <ThunderboltOutlined />, tone: 'accent' },
          { label: '实时总功率', value: numberZh(Math.round(totalPower)), suffix: 'kW', detail: `实时聚合 ${deviceRows.length} 台设备`, icon: <DashboardOutlined /> },
          { label: '基线节省', value: numberZh(Math.round(savedVsBaseline)), suffix: 'kWh', detail: '相对模拟基线', icon: <LineChartOutlined />, tone: 'positive' },
          { label: '峰谷差', value: peakValleyGap, suffix: 'kW', detail: `峰 ${peakPoint.ts ? formatHour(peakPoint.ts) : '-'} · 谷 ${valleyPoint.ts ? formatHour(valleyPoint.ts) : '-'}`, icon: <FireOutlined />, tone: peakValleyGap > 300 ? 'warning' : 'default' },
        ]}
      />

      <OperationsInsightBand
        title="能耗诊断"
        icon={<AlertOutlined />}
        items={anomalyItems.map((item) => ({
          key: item.text,
          text: item.text,
          tone: item.type === 'error' ? 'critical' : item.type === 'warning' ? 'warning' : item.type === 'success' ? 'positive' : 'info',
        }))}
      />

      <Row gutter={[16, 16]} className="ops-chart-row">
        <Col xs={24} lg={15}>
          <OperationsChartCard
            title="建筑能耗趋势"
            description="实际能耗与模拟基线对比，实时功率作为最新末点延伸"
            meta={`${rangeLabel}视图`}
            extra={<span className="ops-chart-status">模拟基线</span>}
            height={320}
            loading={isLoading && trend.length === 0}
            empty={!isLoading && extended.length === 0}
            emptyDescription="当前范围暂无能耗趋势数据"
            ariaLabel="建筑能耗实际值与模拟基线趋势图"
            footer={<><span>口径：历史能耗聚合 + 实时遥测末点</span><span>单位：kWh / kW</span></>}
          >
            <ReactECharts option={trendOption} style={{ height: '100%' }} notMerge />
          </OperationsChartCard>
        </Col>
        <Col xs={24} lg={9}>
          <OperationsChartCard
            title="系统能耗分解"
            description="按冷机、冷冻泵与空调末端聚合实时功率"
            meta={`${deviceRows.length} 台设备`}
            height={320}
            empty={deviceRows.length === 0 || totalPower <= 0}
            emptyDescription="暂无设备实时功率数据"
            ariaLabel="暖通系统实时功率分解图"
            footer={<><span>实时总功率 {numberZh(Math.round(totalPower))} kW</span><span>来源：设备遥测</span></>}
          >
            <ReactECharts option={pieOption} style={{ height: '100%' }} notMerge />
          </OperationsChartCard>
        </Col>
      </Row>

      <Row gutter={[16, 16]} className="ops-chart-row">
        <Col xs={24} lg={12}>
          <OperationsChartCard
            title="近 7 日能耗对比"
            description="末日能耗与前期均值比较，用于识别短期漂移"
            extra={
              <span className={`ops-chart-status ${weekDrift <= 0 ? 'is-positive' : 'is-warning'}`}>
                末日环比 {weekDrift > 0 ? '+' : ''}{weekDrift}%
              </span>
            }
            height={260}
            empty={weekKwh.length === 0}
            emptyDescription="暂无近 7 日能耗数据"
            ariaLabel="近七日建筑能耗对比柱状图"
            footer={<span>比较基准：除末日外的前期日均值</span>}
          >
            <ReactECharts option={weekOption} style={{ height: '100%' }} notMerge />
          </OperationsChartCard>
        </Col>
        <Col xs={24} lg={12}>
          <OperationsChartCard
            title="今日峰谷负荷"
            description="日内负荷分布与峰值时段，用于判断削峰移谷空间"
            extra={
              <span className={`ops-chart-status ${peakValleyGap > 300 ? 'is-warning' : 'is-positive'}`}>
                峰谷差 {peakValleyGap} kW
              </span>
            }
            height={260}
            empty={dayData.length === 0}
            emptyDescription="暂无今日负荷数据"
            ariaLabel="今日逐小时峰谷负荷柱状图"
            footer={<span>峰 {peakPoint.ts ? formatHour(peakPoint.ts) : '-'} · 谷 {valleyPoint.ts ? formatHour(valleyPoint.ts) : '-'}</span>}
          >
            <ReactECharts option={loadCurveOption} style={{ height: '100%' }} notMerge />
          </OperationsChartCard>
        </Col>
      </Row>

      <Card variant="borderless" title={<OperationsPanelHeading title="设备能耗贡献" meta={`${deviceRows.length} 台设备`} />}>
        <Table<DeviceEnergyRow>
          rowKey="id"
          size="middle"
          columns={tableColumns}
          dataSource={[...deviceRows].sort((a, b) => b.power - a.power)}
          pagination={false}
          scroll={{ x: compactTable ? 550 : 780 }}
          locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无设备能耗数据" /> }}
        />
      </Card>
    </PageScaffold>
  );
}
