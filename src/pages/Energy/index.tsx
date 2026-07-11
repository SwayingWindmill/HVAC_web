import { useEffect, useMemo } from 'react';
import {
  Button,
  Card,
  Col,
  Empty,
  Grid,
  Progress,
  Row,
  Segmented,
  Select,
  Table,
  Tag,
  Typography,
  theme,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import ReactECharts from 'echarts-for-react';
import { useSearchParams } from 'react-router-dom';
import {
  AlertOutlined,
  DashboardOutlined,
  DownloadOutlined,
  LineChartOutlined,
  ThunderboltOutlined,
} from '@ant-design/icons';
import { MOCK_DEVICES, useBuildingTimeseries, useTelemetryLive } from '@/api';
import { BRAND, STATUS } from '@/theme/tokens';
import { currencyCny, numberZh, percentText } from '@/utils/format';
import { useUi } from '@/store/ui';
import PageScaffold from '@/components/PageScaffold';
import {
  OperationsChartCard,
  OperationsInsightBand,
  OperationsMetrics,
  OperationsPanelHeading,
  useOperationsDetailFocus,
} from '@/components/OperationsUI';
import DeviceDrawer from '@/pages/Assets/DeviceDrawer';
import { DEVICE_META, TYPE_LABEL, type DeviceType } from '@/pages/Assets/meta';
import {
  ENERGY_TARIFF,
  createAnnualEnergy,
  createDailyEnergy,
  createDeviceEnergy,
  createEnergyCsv,
  getAvailableDayCount,
  getDaysInMonth,
  getMonthTotal,
  getPreviousMonth,
  type DeviceEnergyRow,
  type MonthlyEnergyPoint,
} from './data';
import './Energy.css';

const SYS_COLORS: Record<DeviceType, string> = {
  chiller: BRAND.teal,
  pump: '#3b82f6',
  ahu: '#f59e0b',
};

const SYS_ORDER: DeviceType[] = ['chiller', 'pump', 'ahu'];
const TYPE_BY_LABEL = SYS_ORDER.reduce<Record<string, DeviceType>>((result, type) => {
  result[TYPE_LABEL[type]] = type;
  return result;
}, {});
const BUILDING_LABELS: Record<string, string> = {
  b1: '总部大楼',
  b2: '研发中心',
};

const formatHour = (ts: number) => `${String(new Date(ts).getHours()).padStart(2, '0')}:00`;
const isDeviceType = (value: string | null): value is DeviceType => Boolean(value && SYS_ORDER.includes(value as DeviceType));

type ChartClickParams = {
  dataIndex?: number;
  name?: string;
  seriesName?: string;
};

export default function Energy() {
  const [searchParams, setSearchParams] = useSearchParams();
  const screens = Grid.useBreakpoint();
  const compactTable = !screens.xl;
  const { token } = theme.useToken();
  const buildingId = useUi((state) => state.buildingId);
  const detailFocus = useOperationsDetailFocus();
  const now = useMemo(() => new Date(), []);
  const currentYear = now.getFullYear();
  const currentMonth = now.getMonth() + 1;
  const requestedYear = Number(searchParams.get('year'));
  const year = Number.isInteger(requestedYear) && requestedYear >= currentYear - 3 && requestedYear <= currentYear
    ? requestedYear
    : currentYear;
  const maxSelectableMonth = year === currentYear ? currentMonth : 12;
  const requestedMonth = Number(searchParams.get('month'));
  const month = Number.isInteger(requestedMonth) && requestedMonth >= 1 && requestedMonth <= maxSelectableMonth
    ? requestedMonth
    : maxSelectableMonth;
  const availableDayCount = getAvailableDayCount(year, month, now);
  const requestedDay = Number(searchParams.get('day'));
  const selectedDayNumber = Number.isInteger(requestedDay) && requestedDay >= 1 && requestedDay <= availableDayCount
    ? requestedDay
    : Math.max(1, availableDayCount);
  const selectedType = isDeviceType(searchParams.get('type')) ? searchParams.get('type') as DeviceType : null;
  const deviceParam = searchParams.get('device');
  const selectedDeviceId = deviceParam && deviceParam in DEVICE_META ? deviceParam : null;

  useEffect(() => {
    const next = new URLSearchParams(searchParams);
    next.set('year', String(year));
    next.set('month', String(month));
    next.set('day', String(selectedDayNumber));
    if (selectedType) next.set('type', selectedType);
    else next.delete('type');
    if (selectedDeviceId) next.set('device', selectedDeviceId);
    else next.delete('device');
    if (next.toString() !== searchParams.toString()) setSearchParams(next, { replace: true });
  }, [month, searchParams, selectedDayNumber, selectedDeviceId, selectedType, setSearchParams, year]);

  const updateContext = (patch: Record<string, string | number | null>, replace = false) => {
    const next = new URLSearchParams(searchParams);
    Object.entries(patch).forEach(([key, value]) => {
      if (value === null) next.delete(key);
      else next.set(key, String(value));
    });
    setSearchParams(next, { replace });
  };

  const { data: dayPowerData, isLoading: powerLoading } = useBuildingTimeseries('day');
  const live = useTelemetryLive(MOCK_DEVICES, ['power']);

  const daily = useMemo(() => createDailyEnergy(year, month, availableDayCount), [availableDayCount, month, year]);
  const annual = useMemo(() => createAnnualEnergy(year, now), [now, year]);
  const monthTotal = daily.reduce((sum, item) => sum + item.total, 0);
  const monthCost = Math.round(monthTotal * ENERGY_TARIFF);
  const averageCop = daily.length ? daily.reduce((sum, item) => sum + item.cop, 0) / daily.length : 0;
  const selectedDay = daily.find((item) => item.day === selectedDayNumber) ?? daily[daily.length - 1];
  const annualCumulative = annual.slice(0, month).reduce((sum, item) => sum + item.energy, 0);
  const isMtd = year === currentYear && month === currentMonth;
  const comparisonDayCount = isMtd ? availableDayCount : getDaysInMonth(year, month);

  const previousMonth = getPreviousMonth(year, month);
  const previousMonthTotal = getMonthTotal(
    previousMonth.year,
    previousMonth.month,
    isMtd ? Math.min(comparisonDayCount, getDaysInMonth(previousMonth.year, previousMonth.month)) : undefined,
  );
  const previousYearTotal = getMonthTotal(
    year - 1,
    month,
    isMtd ? Math.min(comparisonDayCount, getDaysInMonth(year - 1, month)) : undefined,
  );
  const monthChange = previousMonthTotal ? ((monthTotal - previousMonthTotal) / previousMonthTotal) * 100 : 0;
  const yearSaving = previousYearTotal ? ((previousYearTotal - monthTotal) / previousYearTotal) * 100 : 0;

  const selectedScopeEnergy = selectedDay
    ? selectedType ? selectedDay[selectedType] : selectedDay.total
    : 0;
  const deviceRows = useMemo(
    () => createDeviceEnergy(selectedScopeEnergy, year, month, 1, selectedType, selectedDayNumber),
    [month, selectedDayNumber, selectedScopeEnergy, selectedType, year],
  );
  const totalPower = MOCK_DEVICES.reduce((sum, id) => sum + (live.get(id, 'power') ?? 0), 0);
  const loadSeries = dayPowerData.map((item, index) => ({
    ts: item.ts,
    value: index === dayPowerData.length - 1 && totalPower > 0 ? Math.round(totalPower) : Math.round(item.value),
  }));
  const peakLoad = loadSeries.reduce((max, item) => (item.value > max.value ? item : max), { ts: 0, value: 0 });
  const averageLoad = loadSeries.length
    ? Math.round(loadSeries.reduce((sum, item) => sum + item.value, 0) / loadSeries.length)
    : 0;

  const typeTotals = SYS_ORDER.reduce<Record<DeviceType, number>>((result, type) => {
    result[type] = daily.reduce((sum, item) => sum + item[type], 0);
    return result;
  }, { chiller: 0, pump: 0, ahu: 0 });
  const dominantType = SYS_ORDER.reduce((max, type) => (typeTotals[type] > typeTotals[max] ? type : max), SYS_ORDER[0]);
  const peakEnergyDay = daily.length ? daily.reduce((max, item) => (item.total > max.total ? item : max), daily[0]) : undefined;
  const topDevice = deviceRows[0];

  const selectYear = (nextYear: number) => {
    const nextMonth = Math.min(month, nextYear === currentYear ? currentMonth : 12);
    updateContext({
      year: nextYear,
      month: nextMonth,
      day: getAvailableDayCount(nextYear, nextMonth, now),
      device: null,
    });
  };

  const selectMonth = (nextMonth: number) => {
    updateContext({
      month: nextMonth,
      day: getAvailableDayCount(year, nextMonth, now),
      device: null,
    });
  };

  const selectDay = (nextDay: number, nextType: DeviceType | null = selectedType) => {
    updateContext({ day: nextDay, type: nextType, device: null });
  };

  const selectType = (nextType: DeviceType | null) => {
    updateContext({ type: nextType, device: null });
  };

  const openDevice = (id: string, trigger: HTMLElement) => {
    detailFocus.captureTrigger(trigger, id);
    updateContext({ device: id });
  };

  const closeDevice = () => {
    updateContext({ device: null }, true);
    detailFocus.restoreFocus();
  };

  const dailyChartEvents = {
    click: (params: ChartClickParams) => {
      if (typeof params.dataIndex !== 'number') return;
      const point = daily[params.dataIndex];
      if (!point) return;
      selectDay(point.day, params.seriesName ? TYPE_BY_LABEL[params.seriesName] ?? selectedType : selectedType);
    },
  };

  const compositionEvents = {
    click: (params: ChartClickParams) => {
      const type = params.name ? TYPE_BY_LABEL[params.name] : undefined;
      if (type) selectType(type);
    },
  };

  const annualChartEvents = {
    click: (params: ChartClickParams) => {
      if (typeof params.dataIndex !== 'number') return;
      const point = annual[params.dataIndex];
      if (point && point.status !== 'future') selectMonth(point.month);
    },
  };

  const axisColor = token.colorBorder;
  const labelColor = token.colorTextSecondary;
  const splitColor = token.colorSplit;

  const dailyStackOption = {
    aria: { enabled: true, description: `${year} 年 ${month} 月每日能耗按设备类别堆叠图。` },
    color: SYS_ORDER.map((type) => SYS_COLORS[type]),
    grid: { left: 56, right: 18, top: 38, bottom: 32 },
    tooltip: { trigger: 'axis', axisPointer: { type: 'shadow' }, valueFormatter: (value: number) => `${numberZh(value)} kWh` },
    legend: { top: 0, right: 8, textStyle: { color: labelColor, fontSize: 12 } },
    xAxis: {
      type: 'category',
      data: daily.map((item) => item.dateLabel),
      axisLine: { lineStyle: { color: axisColor } },
      axisLabel: { color: labelColor, fontSize: 11 },
    },
    yAxis: {
      type: 'value',
      name: 'kWh',
      splitLine: { lineStyle: { color: splitColor } },
      axisLabel: { color: labelColor, fontSize: 11 },
    },
    series: SYS_ORDER.map((type) => ({
      name: TYPE_LABEL[type],
      type: 'bar',
      stack: 'energy',
      barMaxWidth: 24,
      emphasis: { focus: 'series' },
      data: daily.map((item) => ({
        value: item[type],
        itemStyle: {
          opacity: item.day === selectedDayNumber ? 1 : 0.58,
          borderColor: item.day === selectedDayNumber ? token.colorText : 'transparent',
          borderWidth: item.day === selectedDayNumber ? 1 : 0,
        },
      })),
    })),
  };

  const compositionOption = {
    aria: { enabled: true, description: `${year} 年 ${month} 月各设备类别累计能耗构成。` },
    tooltip: { trigger: 'item', formatter: '{b}: {c} kWh ({d}%)' },
    legend: { bottom: 0, textStyle: { color: labelColor, fontSize: 12 } },
    series: [{
      type: 'pie',
      radius: ['48%', '72%'],
      center: ['50%', '43%'],
      itemStyle: { borderColor: token.colorBgContainer, borderWidth: 2 },
      label: { color: labelColor, formatter: '{b}\n{d}%', fontSize: 11 },
      data: SYS_ORDER.map((type) => ({
        name: TYPE_LABEL[type],
        value: typeTotals[type],
        selected: type === selectedType,
        itemStyle: { color: SYS_COLORS[type], opacity: selectedType && type !== selectedType ? 0.52 : 1 },
      })),
    }],
  };

  const annualOption = {
    aria: { enabled: true, description: `${year} 年月度能耗柱图与平均 COP 折线。` },
    grid: { left: 58, right: 54, top: 42, bottom: 30 },
    tooltip: { trigger: 'axis' },
    legend: { top: 0, right: 8, textStyle: { color: labelColor, fontSize: 12 } },
    xAxis: {
      type: 'category',
      data: annual.map((item) => item.label),
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
        name: '月度能耗',
        type: 'bar',
        barWidth: '46%',
        data: annual.map((item) => ({
          value: item.status === 'future' ? null : item.energy,
          itemStyle: {
            color: item.month === month ? BRAND.tealStrong : BRAND.teal,
            opacity: item.status === 'mtd' ? 0.72 : 1,
            borderColor: item.status === 'mtd' ? token.colorTextSecondary : 'transparent',
            borderWidth: item.status === 'mtd' ? 1 : 0,
          },
        })),
      },
      {
        name: '平均 COP',
        type: 'line',
        yAxisIndex: 1,
        smooth: true,
        symbolSize: 7,
        lineStyle: { color: '#f59e0b', width: 2 },
        itemStyle: { color: '#f59e0b' },
        data: annual.map((item) => item.status === 'future' ? null : item.cop),
      },
    ],
  };

  const loadOption = {
    aria: { enabled: true, description: '建筑近 24 小时实时功率负荷曲线，单位为 kW。' },
    grid: { left: 56, right: 18, top: 34, bottom: 30 },
    tooltip: { trigger: 'axis', valueFormatter: (value: number) => `${numberZh(value)} kW` },
    xAxis: {
      type: 'category',
      data: loadSeries.map((item) => formatHour(item.ts)),
      axisLine: { lineStyle: { color: axisColor } },
      axisLabel: { color: labelColor, fontSize: 11 },
    },
    yAxis: {
      type: 'value',
      name: 'kW',
      splitLine: { lineStyle: { color: splitColor } },
      axisLabel: { color: labelColor, fontSize: 11 },
    },
    series: [{
      name: '实时功率',
      type: 'line',
      smooth: true,
      symbol: 'none',
      data: loadSeries.map((item) => item.value),
      lineStyle: { color: BRAND.teal, width: 2 },
      areaStyle: { color: 'rgba(15, 181, 174, 0.14)' },
      markLine: averageLoad ? {
        symbol: 'none',
        label: { formatter: '日均 {c} kW', color: labelColor },
        lineStyle: { color: '#94a3b8', type: 'dashed' },
        data: [{ yAxis: averageLoad }],
      } : undefined,
      markPoint: peakLoad.value ? {
        data: [{ coord: [formatHour(peakLoad.ts), peakLoad.value], value: peakLoad.value, name: '峰值' }],
        itemStyle: { color: STATUS.warn },
      } : undefined,
    }],
  };

  const deviceColumns: ColumnsType<DeviceEnergyRow> = [
    {
      title: '排名',
      key: 'rank',
      width: 68,
      render: (_, __, index) => <Typography.Text type="secondary">#{index + 1}</Typography.Text>,
    },
    {
      title: '设备',
      dataIndex: 'name',
      key: 'name',
      width: 190,
      render: (name: string, row) => (
        <div className="energy-device-name">
          <Button
            type="link"
            className="energy-device-link"
            data-ops-detail-trigger={row.id}
            onClick={(event) => openDevice(row.id, event.currentTarget)}
          >
            {name}
          </Button>
          <span className="energy-device-meta">{row.id} · {row.zoneName}</span>
        </div>
      ),
    },
    {
      title: '类别',
      dataIndex: 'type',
      key: 'type',
      width: 110,
      render: (type: DeviceType) => <Tag color={SYS_COLORS[type]}>{TYPE_LABEL[type]}</Tag>,
    },
    {
      title: '累计运行',
      dataIndex: 'runHours',
      key: 'runHours',
      width: 110,
      sorter: (a, b) => a.runHours - b.runHours,
      render: (value: number) => `${numberZh(value)} h`,
    },
    {
      title: '累计能耗',
      dataIndex: 'energy',
      key: 'energy',
      width: 130,
      sorter: (a, b) => a.energy - b.energy,
      render: (value: number) => <Typography.Text strong>{numberZh(value)} kWh</Typography.Text>,
    },
    {
      title: '单位运行时能耗',
      dataIndex: 'unitEnergy',
      key: 'unitEnergy',
      width: 142,
      render: (value: number) => `${value} kWh/h`,
    },
    {
      title: '范围占比',
      dataIndex: 'share',
      key: 'share',
      width: 142,
      render: (value: number) => (
        <div className="energy-share-cell">
          <Progress percent={value} size="small" strokeColor={BRAND.teal} format={(percent) => `${percent}%`} />
        </div>
      ),
    },
    {
      title: '较前日',
      dataIndex: 'periodChange',
      key: 'periodChange',
      width: 88,
      render: (value: number) => (
        <span className={`energy-change ${value <= 0 ? 'is-lower' : 'is-higher'}`}>
          {percentText(value, { signed: true, digits: 1 })}
        </span>
      ),
    },
  ];

  const monthColumns: ColumnsType<MonthlyEnergyPoint> = [
    {
      title: '月份',
      dataIndex: 'label',
      key: 'label',
      width: 82,
      render: (value: string, row) => (
        <span>
          {value}
          {row.status === 'mtd' ? <Tag bordered={false} color="processing" style={{ marginInlineStart: 4 }}>MTD</Tag> : null}
        </span>
      ),
    },
    {
      title: '能耗',
      dataIndex: 'energy',
      key: 'energy',
      align: 'right',
      render: (value: number, row) => row.status === 'future' ? '—' : `${numberZh(value)} kWh`,
    },
    {
      title: '电费',
      dataIndex: 'cost',
      key: 'cost',
      align: 'right',
      render: (value: number, row) => row.status === 'future' ? '—' : currencyCny(value),
    },
    {
      title: 'COP',
      dataIndex: 'cop',
      key: 'cop',
      width: 70,
      align: 'right',
      render: (value: number, row) => row.status === 'future' ? '—' : value.toFixed(2),
    },
  ];

  const visibleDeviceColumns = compactTable
    ? deviceColumns.filter((column) => ['name', 'energy', 'share', 'periodChange'].includes(String(column.key)))
    : deviceColumns;

  const handleExport = () => {
    const csv = createEnergyCsv(year, month, daily, deviceRows, annual, selectedDayNumber, selectedType);
    const blob = new Blob([csv], { type: 'text/csv;charset=utf-8' });
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement('a');
    anchor.href = url;
    anchor.download = `energy-${year}-${String(month).padStart(2, '0')}-${String(selectedDayNumber).padStart(2, '0')}.csv`;
    document.body.appendChild(anchor);
    anchor.click();
    anchor.remove();
    URL.revokeObjectURL(url);
  };

  const yearOptions = Array.from({ length: 4 }, (_, index) => ({
    value: currentYear - index,
    label: `${currentYear - index} 年`,
  }));
  const monthOptions = Array.from({ length: 12 }, (_, index) => ({
    value: index + 1,
    label: `${index + 1} 月`,
    disabled: year === currentYear && index + 1 > currentMonth,
  }));
  const dayOptions = Array.from({ length: availableDayCount }, (_, index) => ({
    value: index + 1,
    label: `${index + 1} 日`,
  }));
  const drilldownLabel = `${year}-${String(month).padStart(2, '0')}-${String(selectedDayNumber).padStart(2, '0')}`;
  const comparisonLabel = isMtd ? `截至 ${month} 月 ${comparisonDayCount} 日` : '完整自然月';

  return (
    <PageScaffold
      title="能耗分析"
      subtitle="按统一计量周期分析设备类别、月度能耗、COP、实时负荷与设备累计贡献。"
      eyebrow="能源计量与效率"
    >
      <div className="energy-context-bar" aria-label="能耗分析上下文">
        <div className="energy-context-fields">
          <div className="energy-context-field is-wide">
            <span className="energy-context-label">分析范围</span>
            <span className="energy-context-value">{BUILDING_LABELS[buildingId] ?? buildingId} · 暖通系统</span>
          </div>
          <div className="energy-context-field">
            <span className="energy-context-label">计量周期</span>
            <span className="energy-context-value">{year} 年 {month} 月 · {comparisonLabel}</span>
          </div>
          <div className="energy-context-field">
            <span className="energy-context-label">钻取日期</span>
            <span className="energy-context-value">{drilldownLabel}</span>
          </div>
          <div className="energy-context-field">
            <span className="energy-context-label">综合电价</span>
            <span className="energy-context-value">{ENERGY_TARIFF.toFixed(2)} 元/kWh</span>
          </div>
          <div className="energy-context-field is-wide">
            <span className="energy-context-label">数据来源</span>
            <span className="energy-context-source">历史模拟聚合 · 实时设备遥测</span>
          </div>
        </div>
        <div className="energy-context-actions">
          <Select value={year} options={yearOptions} onChange={selectYear} aria-label="选择年份" />
          <Select value={month} options={monthOptions} onChange={selectMonth} aria-label="选择月份" />
          <Select value={selectedDayNumber} options={dayOptions} onChange={(value) => selectDay(value)} aria-label="选择日期" />
          <Button icon={<DownloadOutlined />} onClick={handleExport}>导出 CSV</Button>
        </div>
      </div>

      <OperationsMetrics
        items={[
          {
            label: '选中日能耗',
            value: numberZh(selectedDay?.total ?? 0),
            suffix: 'kWh',
            detail: selectedDay ? `${year}-${String(month).padStart(2, '0')}-${String(selectedDay.day).padStart(2, '0')} · ${currencyCny(selectedDay.cost)}` : '暂无数据',
            icon: <ThunderboltOutlined />,
            tone: 'accent',
          },
          {
            label: isMtd ? '本月累计（MTD）' : '本月累计',
            value: numberZh(monthTotal),
            suffix: 'kWh',
            detail: `${currencyCny(monthCost)} · 年累计 ${numberZh(annualCumulative)} kWh`,
            icon: <DashboardOutlined />,
          },
          {
            label: isMtd ? 'MTD 平均 COP' : '本月平均 COP',
            value: averageCop.toFixed(2),
            detail: averageCop >= 5.2 ? '高效运行区间' : '仍有能效优化空间',
            icon: <LineChartOutlined />,
            tone: averageCop >= 5.2 ? 'positive' : 'warning',
          },
          {
            label: isMtd ? 'MTD 同比节能' : '同比节能',
            value: Math.abs(yearSaving).toFixed(1),
            suffix: '%',
            detail: `${comparisonLabel} · ${yearSaving >= 0 ? '低于' : '高于'}去年同期 · 环比 ${percentText(monthChange, { signed: true, digits: 1 })}`,
            icon: <LineChartOutlined />,
            tone: yearSaving >= 0 ? 'positive' : 'warning',
          },
        ]}
      />

      <OperationsInsightBand
        title="能耗诊断"
        icon={<AlertOutlined />}
        items={[
          {
            key: 'peak-day',
            text: `本月峰值出现在 ${peakEnergyDay?.dateLabel ?? '-'}，当日能耗 ${numberZh(peakEnergyDay?.total ?? 0)} kWh。`,
            tone: 'warning',
          },
          {
            key: 'dominant-system',
            text: `${TYPE_LABEL[dominantType]}贡献本月最多能耗，占比 ${monthTotal ? Math.round((typeTotals[dominantType] / monthTotal) * 100) : 0}%。`,
            tone: 'info',
          },
          {
            key: 'top-device',
            text: topDevice
              ? `${drilldownLabel}${selectedType ? ` · ${TYPE_LABEL[selectedType]}` : ''}中，${topDevice.name}能耗最高，占当前范围 ${topDevice.share}%，较前日 ${percentText(topDevice.periodChange, { signed: true, digits: 1 })}。`
              : '当前钻取范围暂无设备能耗数据。',
            tone: topDevice && topDevice.periodChange > 3 ? 'warning' : 'positive',
          },
          {
            key: 'live-load',
            text: `当前实时总功率 ${numberZh(Math.round(totalPower))} kW，24 小时峰值 ${numberZh(peakLoad.value)} kW。`,
            tone: totalPower > averageLoad * 1.15 ? 'warning' : 'positive',
          },
        ]}
      />

      <Row gutter={[16, 16]} className="ops-chart-row">
        <Col xs={24} lg={15}>
          <OperationsChartCard
            title={`${year} 年 ${month} 月每日能耗`}
            description="按冷水机组、冷冻泵与空调机组拆分，识别异常日期和增量来源"
            meta={`${daily.length} 个计量日`}
            extra={<span className="ops-chart-status">历史模拟聚合</span>}
            height={320}
            empty={daily.length === 0}
            emptyDescription="当前月份暂无每日能耗数据"
            ariaLabel="月内每日设备类别能耗堆叠图"
            footer={<><span>总能耗 {numberZh(monthTotal)} kWh</span><span>计量口径：日累计电量</span></>}
          >
            <ReactECharts option={dailyStackOption} onEvents={dailyChartEvents} style={{ height: '100%' }} notMerge />
          </OperationsChartCard>
        </Col>
        <Col xs={24} lg={9}>
          <OperationsChartCard
            title="月度能耗构成"
            description="按设备类别汇总本月累计电量，而非实时功率占比"
            meta={`${deviceRows.length} 台设备`}
            height={320}
            empty={monthTotal <= 0}
            emptyDescription="当前月份暂无分类能耗数据"
            ariaLabel="月度设备类别累计能耗构成图"
            footer={<><span>主导类别：{TYPE_LABEL[dominantType]}</span><span>单位：kWh</span></>}
          >
            <ReactECharts option={compositionOption} onEvents={compositionEvents} style={{ height: '100%' }} notMerge />
          </OperationsChartCard>
        </Col>
      </Row>

      <Row gutter={[16, 16]} className="ops-chart-row">
        <Col xs={24} lg={12}>
          <OperationsChartCard
            title={`${year} 年月度能耗与 COP`}
            description="能耗柱图与平均 COP 折线联合判断负荷增长和效率变化"
            meta="12 个月"
            extra={<span className="ops-chart-status is-positive">当前选择 {month} 月</span>}
            height={280}
            empty={annual.length === 0}
            emptyDescription="暂无年度能耗与 COP 数据"
            ariaLabel="年度月度能耗柱图和平均COP折线图"
            footer={<><span>左轴：kWh</span><span>右轴：COP</span></>}
          >
            <ReactECharts option={annualOption} onEvents={annualChartEvents} style={{ height: '100%' }} notMerge />
          </OperationsChartCard>
        </Col>
        <Col xs={24} lg={12}>
          <OperationsChartCard
            title="24 小时实时负荷"
            description="独立展示功率曲线，避免将 kW 与累计能耗 kWh 混入同一序列"
            meta={`${MOCK_DEVICES.length} 台设备`}
            extra={<span className={`ops-chart-status ${totalPower > averageLoad * 1.15 ? 'is-warning' : 'is-positive'}`}>实时 {numberZh(Math.round(totalPower))} kW</span>}
            height={280}
            loading={powerLoading && loadSeries.length === 0}
            empty={!powerLoading && loadSeries.length === 0}
            emptyDescription="暂无实时负荷曲线数据"
            ariaLabel="近24小时建筑实时功率负荷曲线"
            footer={<><span>峰值 {numberZh(peakLoad.value)} kW</span><span>日均 {numberZh(averageLoad)} kW</span></>}
          >
            <ReactECharts option={loadOption} style={{ height: '100%' }} notMerge />
          </OperationsChartCard>
        </Col>
      </Row>

      <Row gutter={[16, 16]} align="stretch">
        <Col xs={24} xl={15}>
          <Card
            variant="borderless"
            className="energy-table-card"
            title={
              <OperationsPanelHeading
                title="选中日设备能耗钻取"
                meta={`${drilldownLabel} · ${selectedType ? TYPE_LABEL[selectedType] : '全部类别'} · ${deviceRows.length} 台设备`}
              />
            }
            extra={
              screens.md ? (
                <Segmented<DeviceType | 'all'>
                  size="small"
                  value={selectedType ?? 'all'}
                  onChange={(value) => selectType(value === 'all' ? null : value)}
                  options={[
                    { label: '全部', value: 'all' },
                    ...SYS_ORDER.map((type) => ({ label: TYPE_LABEL[type], value: type })),
                  ]}
                />
              ) : (
                <Select
                  size="small"
                  value={selectedType ?? 'all'}
                  onChange={(value) => selectType(value === 'all' ? null : value as DeviceType)}
                  options={[
                    { label: '全部类别', value: 'all' },
                    ...SYS_ORDER.map((type) => ({ label: TYPE_LABEL[type], value: type })),
                  ]}
                  aria-label="筛选设备类别"
                />
              )
            }
          >
            <Table<DeviceEnergyRow>
              rowKey="id"
              size="middle"
              columns={visibleDeviceColumns}
              dataSource={deviceRows}
              pagination={false}
              scroll={{ x: compactTable ? 620 : 1020 }}
              locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="当前日期和类别暂无设备能耗数据" /> }}
            />
            <div className="energy-table-note">
              <span>点击设备名称打开资产详情；日期和类别均保存在 URL 中。</span>
              <span>当前钻取电量 {numberZh(selectedScopeEnergy)} kWh，实时功率不参与排名。</span>
            </div>
          </Card>
        </Col>
        <Col xs={24} xl={9}>
          <Card
            variant="borderless"
            className="energy-table-card"
            title={<OperationsPanelHeading title={`${year} 年月度汇总`} meta="能耗 · 电费 · COP" />}
          >
            <Table<MonthlyEnergyPoint>
              rowKey="month"
              size="small"
              columns={monthColumns}
              dataSource={annual}
              pagination={false}
              scroll={{ x: 440, y: 430 }}
              onRow={(record) => ({
                onClick: record.status === 'future' ? undefined : () => selectMonth(record.month),
                'aria-disabled': record.status === 'future',
              })}
              rowClassName={(record) => [
                'energy-month-row',
                record.month === month ? 'is-selected' : '',
                record.status === 'future' ? 'is-future' : '',
              ].filter(Boolean).join(' ')}
              locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无月度汇总数据" /> }}
            />
          </Card>
        </Col>
      </Row>

      <DeviceDrawer
        deviceId={selectedDeviceId}
        onClose={closeDevice}
        onAfterClose={detailFocus.restoreFocus}
      />
    </PageScaffold>
  );
}
