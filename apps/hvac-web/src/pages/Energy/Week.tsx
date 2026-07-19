import { useMemo } from 'react';
import { Button, Card, Col, Progress, Row, Table, Tag, theme } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import ReactECharts from 'echarts-for-react';
import {
  AlertOutlined,
  CalendarOutlined,
  ClockCircleOutlined,
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
import { BRAND, STATUS } from '@/theme/tokens';
import { numberZh, percentText } from '@/utils/format';
import { createWeekAnalytics, formatDate, type WeekScheduleRow } from './analytics';
import { useEnergySystemContext } from './context';

const formatClock = (value: number) => {
  const hour = Math.floor(value);
  const minute = Math.round((value - hour) * 60);
  return `${String(hour).padStart(2, '0')}:${String(minute).padStart(2, '0')}`;
};

export default function EnergyWeek() {
  const { year, week, navigateGranularity } = useEnergySystemContext();
  const { token } = theme.useToken();
  const now = useMemo(() => new Date(), []);
  const analytics = useMemo(() => createWeekAnalytics(year, week, now), [now, week, year]);
  const measuredDays = analytics.days.filter((item) => item.status === 'actual');
  const riskDay = [...analytics.scheduleRows].sort((a, b) => b.offHoursEnergy - a.offHoursEnergy)[0];
  const lateDay = [...analytics.scheduleRows].sort((a, b) => b.stopHour - a.stopHour)[0];

  const labelColor = token.colorTextSecondary;
  const axisColor = token.colorBorder;
  const splitColor = token.colorSplit;

  const dailyOption = {
    aria: { enabled: true, description: `${year} 年第 ${week} 周每日能耗与上周同期对比。` },
    grid: { left: 58, right: 18, top: 38, bottom: 30 },
    tooltip: { trigger: 'axis', axisPointer: { type: 'shadow' } },
    legend: { top: 0, right: 8, textStyle: { color: labelColor, fontSize: 12 } },
    xAxis: {
      type: 'category',
      data: analytics.days.map((item) => `${item.weekday}\n${item.date.slice(5)}`),
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
        name: '本周',
        type: 'bar',
        barMaxWidth: 28,
        data: analytics.days.map((item) => item.status === 'future' ? null : ({
          value: item.energy,
          itemStyle: { color: item.index >= 5 ? '#f59e0b' : BRAND.teal },
        })),
      },
      {
        name: '上周同期',
        type: 'line',
        smooth: true,
        symbolSize: 6,
        data: analytics.days.map((item) => item.status === 'future' ? null : item.previous),
        lineStyle: { color: '#94a3b8', width: 1.6, type: 'dashed' },
        itemStyle: { color: '#94a3b8' },
      },
    ],
  };

  const heatmapOption = {
    aria: { enabled: true, description: `${year} 年第 ${week} 周星期与小时功率热力图。` },
    grid: { left: 58, right: 20, top: 22, bottom: 54 },
    tooltip: {
      position: 'top',
      formatter: (params: { data: [number, number, number | null] }) => {
        const [hour, dayIndex, value] = params.data;
        return `${analytics.days[dayIndex]?.weekday ?? ''} ${String(hour).padStart(2, '0')}:00<br/>${value === null ? '未发生' : `${value} kW`}`;
      },
    },
    xAxis: {
      type: 'category',
      data: Array.from({ length: 24 }, (_, hour) => String(hour).padStart(2, '0')),
      splitArea: { show: true },
      axisLine: { lineStyle: { color: axisColor } },
      axisLabel: { color: labelColor, fontSize: 10 },
    },
    yAxis: {
      type: 'category',
      data: analytics.days.map((item) => item.weekday),
      splitArea: { show: true },
      axisLine: { lineStyle: { color: axisColor } },
      axisLabel: { color: labelColor, fontSize: 11 },
    },
    visualMap: {
      min: 80,
      max: 1100,
      calculable: false,
      orient: 'horizontal',
      left: 'center',
      bottom: 4,
      textStyle: { color: labelColor, fontSize: 10 },
      inRange: { color: ['#e6fffb', '#5cdbd3', '#08979c', '#f59e0b'] },
    },
    series: [{
      name: '小时功率',
      type: 'heatmap',
      data: analytics.heatmap,
      label: { show: false },
      emphasis: { itemStyle: { shadowBlur: 8, shadowColor: 'rgba(0,0,0,0.25)' } },
    }],
  };

  const workdayCurve = Array.from({ length: 24 }, (_, hour) => {
    const values = analytics.heatmap
      .filter((item) => item[0] === hour && Number(item[1]) < 5 && item[2] !== null)
      .map((item) => Number(item[2]));
    return values.length ? Math.round(values.reduce((sum, value) => sum + value, 0) / values.length) : null;
  });
  const weekendCurve = Array.from({ length: 24 }, (_, hour) => {
    const values = analytics.heatmap
      .filter((item) => item[0] === hour && Number(item[1]) >= 5 && item[2] !== null)
      .map((item) => Number(item[2]));
    return values.length ? Math.round(values.reduce((sum, value) => sum + value, 0) / values.length) : null;
  });
  const profileOption = {
    aria: { enabled: true, description: '工作日与周末平均小时负荷曲线对比。' },
    grid: { left: 54, right: 18, top: 38, bottom: 28 },
    tooltip: { trigger: 'axis' },
    legend: { top: 0, right: 8, textStyle: { color: labelColor, fontSize: 12 } },
    xAxis: {
      type: 'category',
      data: Array.from({ length: 24 }, (_, hour) => `${String(hour).padStart(2, '0')}:00`),
      axisLine: { lineStyle: { color: axisColor } },
      axisLabel: { color: labelColor, fontSize: 10, interval: 2 },
    },
    yAxis: {
      type: 'value',
      name: 'kW',
      splitLine: { lineStyle: { color: splitColor } },
      axisLabel: { color: labelColor, fontSize: 11 },
    },
    series: [
      {
        name: '工作日平均',
        type: 'line',
        smooth: true,
        symbol: 'none',
        data: workdayCurve,
        lineStyle: { color: BRAND.teal, width: 2 },
        areaStyle: { color: 'rgba(15,181,174,0.10)' },
      },
      {
        name: '周末平均',
        type: 'line',
        smooth: true,
        symbol: 'none',
        data: weekendCurve,
        lineStyle: { color: '#f59e0b', width: 2 },
      },
    ],
  };

  const dayEvents = {
    click: (params: { dataIndex?: number }) => {
      if (typeof params.dataIndex !== 'number') return;
      const item = analytics.days[params.dataIndex];
      if (!item || item.status === 'future') return;
      const date = new Date(`${item.date}T00:00:00`);
      navigateGranularity('day', {
        year: date.getFullYear(),
        month: date.getMonth() + 1,
        day: date.getDate(),
        date: item.date,
        device: null,
      });
    },
  };

  const scheduleColumns: ColumnsType<WeekScheduleRow> = [
    {
      title: '日期',
      dataIndex: 'weekday',
      key: 'weekday',
      render: (value: string, row) => (
        <div className="energy-device-name">
          <Button
            type="link"
            className="energy-device-link"
            onClick={() => {
              const date = new Date(`${row.date}T00:00:00`);
              navigateGranularity('day', {
                year: date.getFullYear(),
                month: date.getMonth() + 1,
                day: date.getDate(),
                date: row.date,
              });
            }}
          >
            {value}
          </Button>
          <span className="energy-device-meta">{row.date}</span>
        </div>
      ),
    },
    { title: '启动', dataIndex: 'startHour', key: 'startHour', render: (value: number) => formatClock(value) },
    { title: '停机', dataIndex: 'stopHour', key: 'stopHour', render: (value: number) => formatClock(value) },
    {
      title: '非营业能耗',
      dataIndex: 'offHoursEnergy',
      key: 'offHoursEnergy',
      align: 'right',
      render: (value: number) => `${numberZh(value)} kWh`,
    },
    {
      title: '日程符合度',
      dataIndex: 'compliance',
      key: 'compliance',
      width: 150,
      render: (value: number) => <Progress percent={value} size="small" status={value < 85 ? 'exception' : 'active'} />,
    },
    {
      title: '判断',
      dataIndex: 'issue',
      key: 'issue',
      render: (value: string) => <Tag color={value === '日程匹配正常' ? 'success' : 'warning'}>{value}</Tag>,
    },
  ];

  return (
    <PageScaffold
      title="周度能耗分析"
      extra={<Tag color="processing">{formatDate(analytics.start)} 起 · {measuredDays.length}/7 天已计量</Tag>}
    >
      <OperationsMetrics
        items={[
          {
            label: 'WTD 累计能耗',
            value: numberZh(analytics.total),
            suffix: 'kWh',
            detail: `${measuredDays.length} 个已计量日 · 上周同期 ${numberZh(analytics.previousTotal)} kWh`,
            icon: <ThunderboltOutlined />,
            tone: 'accent',
          },
          {
            label: '上周同期变化',
            value: Math.abs(analytics.change).toFixed(1),
            suffix: '%',
            detail: analytics.change <= 0 ? '本周能耗下降' : '本周能耗反弹',
            icon: <LineChartOutlined />,
            tone: analytics.change <= 0 ? 'positive' : 'warning',
          },
          {
            label: '非营业时段占比',
            value: analytics.offHoursRate.toFixed(1),
            suffix: '%',
            detail: `${numberZh(analytics.offHoursTotal)} kWh · 目标低于 12%`,
            icon: <ClockCircleOutlined />,
            tone: analytics.offHoursRate > 15 ? 'warning' : 'positive',
          },
          {
            label: '周末能耗占比',
            value: analytics.weekendRate.toFixed(1),
            suffix: '%',
            detail: `平均启动 ${formatClock(analytics.averageStart)} · 停机 ${formatClock(analytics.averageStop)}`,
            icon: <CalendarOutlined />,
            tone: analytics.weekendRate > 22 ? 'warning' : 'default',
          },
        ]}
      />

      <Row gutter={[16, 16]} className="ops-chart-row">
        <Col xs={24} lg={11}>
          <OperationsChartCard
            title="本周每日能耗"
            description="工作日、周末与上周同期对比；点击某日进入日度分析"
            meta={`${measuredDays.length} 个已计量日`}
            height={300}
            ariaLabel="本周七日能耗与上周同期对比图"
            footer={<><span>青色：工作日</span><span>橙色：周末</span></>}
          >
            <ReactECharts option={dailyOption} onEvents={dayEvents} style={{ height: '100%' }} notMerge />
          </OperationsChartCard>
        </Col>
        <Col xs={24} lg={13}>
          <OperationsChartCard
            title="星期 × 小时负荷热力图"
            description="识别夜间基荷、提前启动、延迟停机和周末异常运行"
            meta="24 小时 × 7 天"
            extra={<span className={`ops-chart-status ${analytics.offHoursRate > 15 ? 'is-warning' : 'is-positive'}`}>非营业 {analytics.offHoursRate}%</span>}
            height={300}
            ariaLabel="星期与小时负荷热力图"
            footer={<span>未来日期保持为空，不生成实际值</span>}
          >
            <ReactECharts option={heatmapOption} style={{ height: '100%' }} notMerge />
          </OperationsChartCard>
        </Col>
      </Row>

      <OperationsInsightBand
        title="周度运行结论"
        icon={<AlertOutlined />}
        items={[
          {
            key: 'off-hours',
            text: riskDay
              ? `${riskDay.weekday}非营业时段能耗最高，为 ${numberZh(riskDay.offHoursEnergy)} kWh，建议核查夜间基荷和提前启动策略。`
              : '当前周暂无已计量日期。',
            tone: analytics.offHoursRate > 15 ? 'warning' : 'positive',
          },
          {
            key: 'schedule',
            text: lateDay
              ? `${lateDay.weekday}停机时间最晚（${formatClock(lateDay.stopHour)}），日程符合度 ${lateDay.compliance}%。`
              : '当前周暂无日程数据。',
            tone: lateDay && lateDay.compliance < 85 ? 'warning' : 'info',
          },
          {
            key: 'comparison',
            text: `本周累计较上周同期 ${percentText(analytics.change, { signed: true, digits: 1 })}，点击每日能耗柱可进入对应日度处置页。`,
            tone: analytics.change <= 0 ? 'positive' : 'warning',
          },
        ]}
      />

      <Row gutter={[16, 16]} className="ops-chart-row">
        <Col xs={24} lg={10}>
          <OperationsChartCard
            title="工作日与周末负荷轮廓"
            description="比较平均小时功率，判断周末是否仍保持过高基荷"
            meta="平均功率"
            height={280}
            ariaLabel="工作日与周末平均小时负荷曲线"
            footer={<span>单位：kW</span>}
          >
            <ReactECharts option={profileOption} style={{ height: '100%' }} notMerge />
          </OperationsChartCard>
        </Col>
        <Col xs={24} lg={14}>
          <Card
            variant="borderless"
            className="energy-table-card"
            title={<OperationsPanelHeading title="本周启停与日程符合度" meta={`${analytics.scheduleRows.length} 个已计量日`} />}
            extra={<Tag color={analytics.offHoursRate > 15 ? STATUS.warn : 'success'}>目标：非营业低于 12%</Tag>}
          >
            <Table<WeekScheduleRow>
              rowKey="date"
              size="middle"
              columns={scheduleColumns}
              dataSource={analytics.scheduleRows}
              pagination={false}
              scroll={{ x: 760 }}
            />
          </Card>
        </Col>
      </Row>
    </PageScaffold>
  );
}
