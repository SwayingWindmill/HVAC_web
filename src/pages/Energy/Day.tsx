import { useMemo } from 'react';
import { Button, Card, Col, Empty, Row, Table, Tag, Typography, theme } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import ReactECharts from 'echarts-for-react';
import { useNavigate, useSearchParams } from 'react-router-dom';
import {
  AlertOutlined,
  ClockCircleOutlined,
  DashboardOutlined,
  LineChartOutlined,
  ThunderboltOutlined,
} from '@ant-design/icons';
import PageScaffold from '@/components/PageScaffold';
import {
  OperationsChartCard,
  OperationsInsightBand,
  OperationsMetrics,
  OperationsPanelHeading,
  OperationsTimeline,
  useOperationsDetailFocus,
} from '@/components/OperationsUI';
import DeviceDrawer from '@/pages/Assets/DeviceDrawer';
import { DEVICE_META, TYPE_LABEL } from '@/pages/Assets/meta';
import { BRAND, STATUS } from '@/theme/tokens';
import { currencyCny, numberZh } from '@/utils/format';
import {
  createDayAnalytics,
  ENERGY_SYSTEM_COLORS,
  ENERGY_SYSTEM_ORDER,
  type DayDeviceTimeline,
  type DayEvent,
  type TariffBand,
} from './analytics';
import { useEnergySystemContext } from './context';

const EVENT_META: Record<DayEvent['type'], { label: string; color: string }> = {
  operation: { label: '运行', color: '#64748b' },
  alarm: { label: '告警', color: STATUS.err },
  fdd: { label: '诊断', color: STATUS.warn },
  optimize: { label: '优化', color: BRAND.teal },
};

const TARIFF_COLORS: Record<TariffBand, string> = {
  谷: '#3b82f6',
  平: '#0fb5ae',
  峰: '#f59e0b',
};

const formatClock = (value: number) => {
  const hour = Math.floor(value);
  const minute = Math.round((value - hour) * 60);
  return `${String(hour).padStart(2, '0')}:${String(minute).padStart(2, '0')}`;
};

export default function EnergyDay() {
  const { date, navigateGranularity, updateParams } = useEnergySystemContext();
  const [searchParams] = useSearchParams();
  const navigate = useNavigate();
  const { token } = theme.useToken();
  const detailFocus = useOperationsDetailFocus();
  const now = useMemo(() => new Date(), []);
  const analytics = useMemo(() => createDayAnalytics(date, now), [date, now]);
  const deviceParam = searchParams.get('device');
  const selectedDeviceId = deviceParam && deviceParam in DEVICE_META ? deviceParam : null;
  const latestMeasured = [...analytics.hours].reverse().find((item) => item.totalPower !== null);
  const topDevice = analytics.devices[0];
  const cyclingDevice = analytics.devices.find((item) => item.status === 'warning');
  const peakTariffEnergy = analytics.tariffEnergy.峰;
  const peakTariffRate = analytics.totalEnergy ? (peakTariffEnergy / analytics.totalEnergy) * 100 : 0;

  const axisColor = token.colorBorder;
  const labelColor = token.colorTextSecondary;
  const splitColor = token.colorSplit;

  const loadOption = {
    aria: { enabled: true, description: `${date} 逐小时总功率与累计能耗双轴趋势。` },
    grid: { left: 58, right: 62, top: 42, bottom: 30 },
    tooltip: { trigger: 'axis' },
    legend: { top: 0, right: 8, textStyle: { color: labelColor, fontSize: 12 } },
    xAxis: {
      type: 'category',
      data: analytics.hours.map((item) => item.label),
      axisLine: { lineStyle: { color: axisColor } },
      axisLabel: { color: labelColor, fontSize: 10, interval: 1 },
    },
    yAxis: [
      {
        type: 'value',
        name: 'kW',
        splitLine: { lineStyle: { color: splitColor } },
        axisLabel: { color: labelColor, fontSize: 11 },
      },
      {
        type: 'value',
        name: 'kWh',
        splitLine: { show: false },
        axisLabel: { color: labelColor, fontSize: 11 },
      },
    ],
    series: [
      {
        name: '总功率',
        type: 'line',
        smooth: true,
        symbol: 'none',
        data: analytics.hours.map((item) => item.totalPower),
        lineStyle: { color: BRAND.teal, width: 2.2 },
        areaStyle: { color: 'rgba(15,181,174,0.12)' },
        markArea: {
          silent: true,
          itemStyle: { opacity: 0.055 },
          data: [
            [{ xAxis: '00:00', itemStyle: { color: TARIFF_COLORS.谷 } }, { xAxis: '07:00' }],
            [{ xAxis: '10:00', itemStyle: { color: TARIFF_COLORS.峰 } }, { xAxis: '15:00' }],
            [{ xAxis: '23:00', itemStyle: { color: TARIFF_COLORS.谷 } }, { xAxis: '23:00' }],
          ],
        },
        markPoint: analytics.peakPower ? {
          data: [{ coord: [analytics.peakHour, analytics.peakPower], value: analytics.peakPower, name: '峰值' }],
          itemStyle: { color: STATUS.warn },
        } : undefined,
      },
      {
        name: '累计能耗',
        type: 'line',
        yAxisIndex: 1,
        smooth: true,
        symbol: 'none',
        data: analytics.hours.map((item) => item.cumulativeEnergy),
        lineStyle: { color: '#8b5cf6', width: 1.8 },
      },
    ],
  };

  const systemOption = {
    aria: { enabled: true, description: `${date} 冷水机组、冷冻泵和空调机组逐小时功率堆叠。` },
    color: ENERGY_SYSTEM_ORDER.map((type) => ENERGY_SYSTEM_COLORS[type]),
    grid: { left: 56, right: 18, top: 38, bottom: 30 },
    tooltip: { trigger: 'axis' },
    legend: { top: 0, right: 8, textStyle: { color: labelColor, fontSize: 12 } },
    xAxis: {
      type: 'category',
      data: analytics.hours.map((item) => item.label),
      axisLine: { lineStyle: { color: axisColor } },
      axisLabel: { color: labelColor, fontSize: 10, interval: 2 },
    },
    yAxis: {
      type: 'value',
      name: 'kW',
      splitLine: { lineStyle: { color: splitColor } },
      axisLabel: { color: labelColor, fontSize: 11 },
    },
    series: ENERGY_SYSTEM_ORDER.map((type) => ({
      name: TYPE_LABEL[type],
      type: 'line',
      stack: 'power',
      smooth: true,
      symbol: 'none',
      areaStyle: { opacity: 0.22 },
      lineStyle: { width: 1.5 },
      data: analytics.hours.map((item) => item[type]),
    })),
  };

  const copOption = {
    aria: { enabled: true, description: `${date} 逐小时综合 COP 与效率下限。` },
    grid: { left: 52, right: 18, top: 32, bottom: 28 },
    tooltip: { trigger: 'axis' },
    xAxis: {
      type: 'category',
      data: analytics.hours.map((item) => item.label),
      axisLine: { lineStyle: { color: axisColor } },
      axisLabel: { color: labelColor, fontSize: 10, interval: 2 },
    },
    yAxis: {
      type: 'value',
      name: 'COP',
      min: 4,
      max: 6,
      splitLine: { lineStyle: { color: splitColor } },
      axisLabel: { color: labelColor, fontSize: 11 },
    },
    series: [{
      name: '综合 COP',
      type: 'line',
      smooth: true,
      connectNulls: false,
      symbol: 'none',
      data: analytics.hours.map((item) => item.cop),
      lineStyle: { color: '#8b5cf6', width: 2 },
      areaStyle: { color: 'rgba(139,92,246,0.10)' },
      markLine: {
        symbol: 'none',
        label: { formatter: '效率下限 4.6', color: labelColor },
        lineStyle: { color: STATUS.warn, type: 'dashed' },
        data: [{ yAxis: 4.6 }],
      },
    }],
  };

  const tariffOption = {
    aria: { enabled: true, description: `${date} 峰平谷分时电量构成。` },
    tooltip: { trigger: 'item', formatter: '{b}: {c} kWh ({d}%)' },
    legend: { bottom: 0, textStyle: { color: labelColor, fontSize: 12 } },
    series: [{
      type: 'pie',
      radius: ['48%', '72%'],
      center: ['50%', '43%'],
      itemStyle: { borderColor: token.colorBgContainer, borderWidth: 2 },
      label: { color: labelColor, formatter: '{b}\n{d}%', fontSize: 11 },
      data: (['峰', '平', '谷'] as TariffBand[]).map((band) => ({
        name: `${band}时电量`,
        value: analytics.tariffEnergy[band],
        itemStyle: { color: TARIFF_COLORS[band] },
      })),
    }],
  };

  const openDevice = (id: string, trigger: HTMLElement) => {
    detailFocus.captureTrigger(trigger, id);
    updateParams({ device: id });
  };
  const closeDevice = () => {
    updateParams({ device: null }, true);
    detailFocus.restoreFocus();
  };

  const deviceColumns: ColumnsType<DayDeviceTimeline> = [
    {
      title: '设备',
      dataIndex: 'name',
      key: 'name',
      render: (value: string, row) => (
        <div className="energy-device-name">
          <Button
            type="link"
            className="energy-device-link"
            data-ops-detail-trigger={row.id}
            onClick={(event) => openDevice(row.id, event.currentTarget)}
          >
            {value}
          </Button>
          <span className="energy-device-meta">{row.id} · {TYPE_LABEL[row.type]}</span>
        </div>
      ),
    },
    { title: '启动', dataIndex: 'startHour', key: 'startHour', render: (value: number) => formatClock(value) },
    { title: '计划停机', dataIndex: 'stopHour', key: 'stopHour', render: (value: number) => formatClock(value) },
    {
      title: '已运行',
      dataIndex: 'runHours',
      key: 'runHours',
      render: (value: number) => `${value.toFixed(1)} h`,
    },
    {
      title: '累计能耗',
      dataIndex: 'energy',
      key: 'energy',
      align: 'right',
      sorter: (a, b) => a.energy - b.energy,
      render: (value: number) => `${numberZh(value)} kWh`,
    },
    {
      title: '启停次数',
      dataIndex: 'starts',
      key: 'starts',
      align: 'center',
      render: (value: number, row) => <Tag color={row.status === 'warning' ? 'warning' : 'success'}>{value} 次</Tag>,
    },
  ];

  const handleEvent = (event: DayEvent) => {
    if (event.type === 'fdd' && event.target) navigate(`/fdd?diagnosis=${event.target}`);
    if (event.type === 'alarm' && event.target) navigate(`/assets?device=${event.target}`);
    if (event.type === 'optimize' && event.target) navigate(`/optimize?suggestion=${event.target}`);
  };

  const timelineItems = analytics.events.map((event) => ({
    color: EVENT_META[event.type].color,
    children: (
      <div className="energy-event-item">
        <div className="energy-event-head">
          <span className="energy-event-time">{event.time}</span>
          <Tag color={EVENT_META[event.type].color}>{EVENT_META[event.type].label}</Tag>
          <strong>{event.title}</strong>
        </div>
        <Typography.Paragraph type="secondary">{event.detail}</Typography.Paragraph>
        {event.target ? <Button type="link" onClick={() => handleEvent(event)}>查看关联对象</Button> : null}
      </div>
    ),
  }));

  return (
    <PageScaffold
      title="日度能耗分析"
      subtitle="面向当日运行与问题处置，分析小时负荷、累计电量、峰平谷、设备启停和业务事件。"
      eyebrow="实时运行与异常处置"
      extra={<Tag color={analytics.isToday ? 'processing' : 'default'}>{analytics.isToday ? `今日 · 已计量至 ${analytics.measuredHour}:00` : date}</Tag>}
    >
      <OperationsMetrics
        items={[
          {
            label: analytics.isToday ? '今日累计能耗' : '当日累计能耗',
            value: numberZh(analytics.totalEnergy),
            suffix: 'kWh',
            detail: `${currencyCny(analytics.cost)} · 已计量 ${analytics.measuredHour + 1} 小时`,
            icon: <ThunderboltOutlined />,
            tone: 'accent',
          },
          {
            label: '当前总功率',
            value: numberZh(latestMeasured?.totalPower ?? 0),
            suffix: 'kW',
            detail: `当日峰值 ${numberZh(analytics.peakPower)} kW · ${analytics.peakHour}`,
            icon: <DashboardOutlined />,
          },
          {
            label: '加权综合 COP',
            value: analytics.weightedCop.toFixed(2),
            detail: '按小时输入电量加权',
            icon: <LineChartOutlined />,
            tone: analytics.weightedCop >= 5 ? 'positive' : 'warning',
          },
          {
            label: '非营业时段能耗',
            value: analytics.offHoursRate.toFixed(1),
            suffix: '%',
            detail: `${numberZh(analytics.offHoursEnergy)} kWh · 峰时占比 ${peakTariffRate.toFixed(1)}%`,
            icon: <ClockCircleOutlined />,
            tone: analytics.offHoursRate > 15 ? 'warning' : 'positive',
          },
        ]}
      />

      <OperationsInsightBand
        title="日度运行结论"
        icon={<AlertOutlined />}
        items={[
          {
            key: 'peak',
            text: `当日峰值 ${numberZh(analytics.peakPower)} kW 出现在 ${analytics.peakHour}，峰谷差 ${numberZh(analytics.peakValleyGap)} kW。`,
            tone: analytics.peakValleyGap > 900 ? 'warning' : 'info',
          },
          {
            key: 'device',
            text: topDevice
              ? `${topDevice.name}累计能耗最高，为 ${numberZh(topDevice.energy)} kWh；点击设备名称可进入实时详情。`
              : '暂无设备运行数据。',
            tone: 'info',
          },
          {
            key: 'cycling',
            text: cyclingDevice
              ? `${cyclingDevice.name}当日启停 ${cyclingDevice.starts} 次，存在短周期运行风险，建议查看 FDD 诊断。`
              : '设备启停次数处于正常范围。',
            tone: cyclingDevice ? 'warning' : 'positive',
          },
          {
            key: 'tariff',
            text: `峰时电量 ${numberZh(peakTariffEnergy)} kWh，占当日 ${peakTariffRate.toFixed(1)}%，可结合优化建议评估移峰空间。`,
            tone: peakTariffRate > 35 ? 'warning' : 'positive',
          },
        ]}
      />

      <Row gutter={[16, 16]} className="ops-chart-row">
        <Col xs={24} xl={15}>
          <OperationsChartCard
            title="24 小时功率与累计能耗"
            description="功率使用左轴 kW，累计电量使用右轴 kWh；背景标识峰谷电价时段"
            meta={`${analytics.measuredHour + 1} 小时已计量`}
            extra={<span className="ops-chart-status">峰值 {numberZh(analytics.peakPower)} kW</span>}
            height={330}
            ariaLabel="日度24小时总功率与累计能耗双轴趋势"
            footer={<><span>左轴：瞬时功率</span><span>右轴：累计电量</span></>}
          >
            <ReactECharts option={loadOption} style={{ height: '100%' }} notMerge />
          </OperationsChartCard>
        </Col>
        <Col xs={24} xl={9}>
          <OperationsChartCard
            title="设备类别小时功率"
            description="冷水机组、冷冻泵和空调机组的功率堆叠，定位尖峰来源"
            meta="设备类别"
            height={330}
            ariaLabel="设备类别逐小时功率堆叠图"
            footer={<span>单位：kW</span>}
          >
            <ReactECharts option={systemOption} style={{ height: '100%' }} notMerge />
          </OperationsChartCard>
        </Col>
      </Row>

      <Row gutter={[16, 16]} className="ops-chart-row">
        <Col xs={24} lg={10}>
          <OperationsChartCard
            title="小时综合 COP"
            description="观察效率随负荷和峰值时段的变化，低于 4.6 标记为风险"
            meta={`加权 ${analytics.weightedCop.toFixed(2)}`}
            height={270}
            ariaLabel="日度逐小时综合COP趋势"
            footer={<span>周期 COP 按输入电量加权</span>}
          >
            <ReactECharts option={copOption} style={{ height: '100%' }} notMerge />
          </OperationsChartCard>
        </Col>
        <Col xs={24} lg={6}>
          <OperationsChartCard
            title="峰平谷电量"
            description="分时电量构成用于判断移峰空间"
            meta={`${currencyCny(analytics.cost)}`}
            height={270}
            ariaLabel="日度峰平谷电量构成图"
            footer={<span>综合电价暂按 0.78 元/kWh</span>}
          >
            <ReactECharts option={tariffOption} style={{ height: '100%' }} notMerge />
          </OperationsChartCard>
        </Col>
        <Col xs={24} lg={8}>
          <Card
            variant="borderless"
            className="energy-events-card"
            title={<OperationsPanelHeading title="当日业务事件" meta={`${analytics.events.length} 条`} />}
          >
            {timelineItems.length ? <OperationsTimeline items={timelineItems} /> : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="当前日期暂无事件" />}
          </Card>
        </Col>
      </Row>

      <Card
        variant="borderless"
        className="energy-table-card"
        title={<OperationsPanelHeading title="设备启停与当日能耗" meta={`${analytics.devices.length} 台设备`} />}
        extra={<Button onClick={() => navigateGranularity('week')}>查看本周重复模式</Button>}
      >
        <Table<DayDeviceTimeline>
          rowKey="id"
          size="middle"
          columns={deviceColumns}
          dataSource={analytics.devices}
          pagination={false}
          scroll={{ x: 760 }}
        />
        <div className="energy-table-note">
          <span>设备启停和累计能耗为模拟聚合；点击设备名称打开统一资产详情。</span>
          <span>异常事件可继续进入 FDD、资产或优化建议。</span>
        </div>
      </Card>

      <DeviceDrawer deviceId={selectedDeviceId} onClose={closeDevice} onAfterClose={detailFocus.restoreFocus} />
    </PageScaffold>
  );
}
