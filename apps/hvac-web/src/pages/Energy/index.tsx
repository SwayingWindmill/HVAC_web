import { useMemo } from 'react';
import {
  Button,
  Card,
  Grid,
  Segmented,
  Select,
  Tag,
  Typography,
} from 'antd';
import {
  ApartmentOutlined,
  CalendarOutlined,
  DatabaseOutlined,
  DownloadOutlined,
  FundOutlined,
} from '@ant-design/icons';
import { Outlet, useLocation, useNavigate, useSearchParams } from 'react-router';
import { useUi } from '@/store/ui';
import { getAvailableDayCount } from './data';
import {
  COMPARE_MODE_LABEL,
  ENERGY_TYPE_META,
  type EnergyCompareMode,
  type EnergyGranularity,
  type EnergySystemContext,
  type EnergyType,
} from './context';
import './Energy.css';

const GRANULARITY_OPTIONS: Array<{ label: string; value: EnergyGranularity }> = [
  { label: '年度', value: 'year' },
  { label: '月度', value: 'month' },
  { label: '周度', value: 'week' },
  { label: '日度', value: 'day' },
];

const BUILDING_LABELS: Record<string, string> = {
  b1: '总部大楼',
  b2: '研发中心',
};

const isGranularity = (value: string | undefined): value is EnergyGranularity =>
  Boolean(value && GRANULARITY_OPTIONS.some((item) => item.value === value));

const isEnergyType = (value: string | null): value is EnergyType =>
  Boolean(value && value in ENERGY_TYPE_META);

const isCompareMode = (value: string | null): value is EnergyCompareMode =>
  Boolean(value && value in COMPARE_MODE_LABEL);

const formatDate = (date: Date) => [
  date.getFullYear(),
  String(date.getMonth() + 1).padStart(2, '0'),
  String(date.getDate()).padStart(2, '0'),
].join('-');

const getIsoWeek = (date: Date) => {
  const target = new Date(Date.UTC(date.getFullYear(), date.getMonth(), date.getDate()));
  const day = target.getUTCDay() || 7;
  target.setUTCDate(target.getUTCDate() + 4 - day);
  const yearStart = new Date(Date.UTC(target.getUTCFullYear(), 0, 1));
  return Math.ceil((((target.getTime() - yearStart.getTime()) / 86_400_000) + 1) / 7);
};

const getWeekStart = (year: number, week: number) => {
  const januaryFourth = new Date(year, 0, 4);
  const day = januaryFourth.getDay() || 7;
  const monday = new Date(year, 0, 4 - day + 1);
  monday.setDate(monday.getDate() + (week - 1) * 7);
  return monday;
};

const csvCell = (value: unknown) => `"${String(value ?? '').replace(/"/g, '""')}"`;

export default function EnergySystem() {
  const [searchParams, setSearchParams] = useSearchParams();
  const location = useLocation();
  const navigate = useNavigate();
  const screens = Grid.useBreakpoint();
  const buildingId = useUi((state) => state.buildingId);
  const now = useMemo(() => new Date(), []);
  const currentYear = now.getFullYear();
  const currentMonth = now.getMonth() + 1;
  const pathSegment = location.pathname.split('/')[2];
  const granularity: EnergyGranularity = isGranularity(pathSegment) ? pathSegment : 'month';

  const requestedYear = Number(searchParams.get('year'));
  const year = Number.isInteger(requestedYear) && requestedYear >= currentYear - 3 && requestedYear <= currentYear
    ? requestedYear
    : currentYear;
  const maxMonth = year === currentYear ? currentMonth : 12;
  const requestedMonth = Number(searchParams.get('month'));
  const month = Number.isInteger(requestedMonth) && requestedMonth >= 1 && requestedMonth <= maxMonth
    ? requestedMonth
    : maxMonth;
  const availableDays = getAvailableDayCount(year, month, now);
  const requestedDay = Number(searchParams.get('day'));
  const day = Number.isInteger(requestedDay) && requestedDay >= 1 && requestedDay <= availableDays
    ? requestedDay
    : Math.max(1, availableDays);
  const selectedDate = new Date(year, month - 1, day);
  const currentWeek = getIsoWeek(now);
  const maxWeek = year === currentYear ? currentWeek : 53;
  const requestedWeek = Number(searchParams.get('week'));
  const week = Number.isInteger(requestedWeek) && requestedWeek >= 1 && requestedWeek <= maxWeek
    ? requestedWeek
    : Math.min(getIsoWeek(selectedDate), maxWeek);
  const date = formatDate(selectedDate);
  const requestedType = searchParams.get('energyType');
  const energyType: EnergyType = isEnergyType(requestedType) && ENERGY_TYPE_META[requestedType].enabled
    ? requestedType
    : 'electricity';
  const requestedCompare = searchParams.get('compare');
  const compareMode: EnergyCompareMode = isCompareMode(requestedCompare) ? requestedCompare : 'year-over-year';
  const energyMeta = ENERGY_TYPE_META[energyType];

  const updateParams = (patch: Record<string, string | number | null>, replace = false) => {
    const next = new URLSearchParams(searchParams);
    Object.entries(patch).forEach(([key, value]) => {
      if (value === null) next.delete(key);
      else next.set(key, String(value));
    });
    setSearchParams(next, { replace });
  };

  const navigateGranularity = (
    nextGranularity: EnergyGranularity,
    patch: Record<string, string | number | null> = {},
  ) => {
    const next = new URLSearchParams(searchParams);
    Object.entries(patch).forEach(([key, value]) => {
      if (value === null) next.delete(key);
      else next.set(key, String(value));
    });
    navigate(`/energy/${nextGranularity}?${next.toString()}`);
  };

  const selectYear = (nextYear: number) => {
    const nextMonth = Math.min(month, nextYear === currentYear ? currentMonth : 12);
    const nextDay = getAvailableDayCount(nextYear, nextMonth, now);
    const nextDate = new Date(nextYear, nextMonth - 1, Math.max(1, nextDay));
    updateParams({
      year: nextYear,
      month: nextMonth,
      day: Math.max(1, nextDay),
      date: formatDate(nextDate),
      week: Math.min(getIsoWeek(nextDate), nextYear === currentYear ? currentWeek : 53),
      device: null,
    });
  };

  const selectMonth = (nextMonth: number) => {
    const nextDay = getAvailableDayCount(year, nextMonth, now);
    const nextDate = new Date(year, nextMonth - 1, Math.max(1, nextDay));
    updateParams({
      month: nextMonth,
      day: Math.max(1, nextDay),
      date: formatDate(nextDate),
      week: Math.min(getIsoWeek(nextDate), maxWeek),
      device: null,
    });
  };

  const selectDay = (nextDay: number) => {
    const nextDate = new Date(year, month - 1, nextDay);
    updateParams({ day: nextDay, date: formatDate(nextDate), week: getIsoWeek(nextDate), device: null });
  };

  const selectWeek = (nextWeek: number) => {
    const weekStart = getWeekStart(year, nextWeek);
    const boundedDate = weekStart > now ? now : weekStart;
    updateParams({
      week: nextWeek,
      month: boundedDate.getMonth() + 1,
      day: boundedDate.getDate(),
      date: formatDate(boundedDate),
      device: null,
    });
  };

  const periodLabel = granularity === 'year'
    ? `${year} 年`
    : granularity === 'month'
      ? `${year} 年 ${month} 月`
      : granularity === 'week'
        ? `${year} 年第 ${week} 周 · ${formatDate(getWeekStart(year, week))} 起`
        : date;

  const exportCurrentView = () => {
    const visibleMetrics = Array.from(document.querySelectorAll<HTMLElement>('.ops-metric'))
      .filter((element) => element.offsetParent !== null)
      .map((element) => [
        element.querySelector('.ops-metric-label')?.textContent?.trim() ?? '',
        element.querySelector('.ops-metric-value')?.textContent?.trim() ?? '',
        element.querySelector('.ops-metric-detail')?.textContent?.trim() ?? '',
      ]);
    const visibleCharts = Array.from(document.querySelectorAll<HTMLElement>('.ops-chart-card'))
      .filter((element) => element.offsetParent !== null)
      .map((element) => element.querySelector('.ops-panel-title')?.textContent?.trim() ?? '')
      .filter(Boolean);
    const rows = [
      ['综合能耗分析导出'],
      ['页面', GRANULARITY_OPTIONS.find((item) => item.value === granularity)?.label],
      ['建筑', BUILDING_LABELS[buildingId] ?? buildingId],
      ['能源类型', energyMeta.label],
      ['周期', periodLabel],
      ['对比口径', COMPARE_MODE_LABEL[compareMode]],
      ['页面链接', location.pathname + location.search],
      [],
      ['核心指标'],
      ['指标', '数值', '说明'],
      ...visibleMetrics,
      [],
      ['分析图表'],
      ...visibleCharts.map((title) => [title]),
    ];
    const csv = `\uFEFF${rows.map((row) => row.map(csvCell).join(',')).join('\r\n')}`;
    const blob = new Blob([csv], { type: 'text/csv;charset=utf-8' });
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement('a');
    anchor.href = url;
    anchor.download = `energy-${granularity}-${date}.csv`;
    document.body.appendChild(anchor);
    anchor.click();
    anchor.remove();
    URL.revokeObjectURL(url);
  };

  const context: EnergySystemContext = {
    granularity,
    year,
    month,
    day,
    week,
    date,
    energyType,
    compareMode,
    unit: energyMeta.unit,
    energyLabel: energyMeta.label,
    updateParams,
    navigateGranularity,
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
  const dayOptions = Array.from({ length: availableDays }, (_, index) => ({
    value: index + 1,
    label: `${index + 1} 日`,
  }));
  const weekOptions = Array.from({ length: maxWeek }, (_, index) => ({
    value: index + 1,
    label: `第 ${index + 1} 周`,
  }));

  return (
    <div className="energy-system-root">
      <section className="energy-system-header" aria-label="综合能耗分析系统导航">
        <div className="energy-system-heading">
          <div className="energy-system-eyebrow"><FundOutlined /> 综合能源管理</div>
          <div className="energy-system-title-row">
            <div>
              <Typography.Title level={2}>综合能耗分析</Typography.Title>
              <Typography.Paragraph>
                以年度经营、月度复盘、周度策略和日度处置四种时间尺度，连续钻取至系统、设备和运维业务闭环。
              </Typography.Paragraph>
            </div>
            <Segmented<EnergyGranularity>
              className="energy-granularity-nav"
              size={screens.md ? 'large' : 'middle'}
              value={granularity}
              options={GRANULARITY_OPTIONS}
              onChange={(value) => navigateGranularity(value)}
            />
          </div>
        </div>
      </section>

      <Card variant="borderless" className="energy-system-toolbar">
        <div className="energy-system-context">
          <div className="energy-system-context-item">
            <span className="energy-system-context-icon"><ApartmentOutlined /></span>
            <div>
              <span className="energy-system-context-label">分析范围</span>
              <strong>{BUILDING_LABELS[buildingId] ?? buildingId} · 暖通系统</strong>
            </div>
          </div>
          <div className="energy-system-context-item">
            <span className="energy-system-context-icon"><CalendarOutlined /></span>
            <div>
              <span className="energy-system-context-label">当前周期</span>
              <strong>{periodLabel}</strong>
            </div>
          </div>
          <div className="energy-system-context-item">
            <span className="energy-system-context-icon"><DatabaseOutlined /></span>
            <div>
              <span className="energy-system-context-label">数据状态</span>
              <strong>历史模拟聚合 · 实时遥测</strong>
            </div>
          </div>
        </div>

        <div className="energy-system-controls">
          <Select value={year} options={yearOptions} onChange={selectYear} aria-label="选择分析年份" />
          {granularity === 'month' || granularity === 'day' ? (
            <Select value={month} options={monthOptions} onChange={selectMonth} aria-label="选择分析月份" />
          ) : null}
          {granularity === 'day' ? (
            <Select value={day} options={dayOptions} onChange={selectDay} aria-label="选择分析日期" />
          ) : null}
          {granularity === 'week' ? (
            <Select value={week} options={weekOptions} onChange={selectWeek} aria-label="选择分析周" />
          ) : null}
          <Select<EnergyType>
            value={energyType}
            aria-label="选择能源类型"
            options={Object.entries(ENERGY_TYPE_META).map(([value, meta]) => ({
              value: value as EnergyType,
              label: meta.enabled ? meta.label : `${meta.label} · 待接入`,
              disabled: !meta.enabled,
            }))}
            onChange={(value) => updateParams({ energyType: value })}
          />
          <Select<EnergyCompareMode>
            value={compareMode}
            aria-label="选择对比口径"
            options={Object.entries(COMPARE_MODE_LABEL).map(([value, label]) => ({
              value: value as EnergyCompareMode,
              label,
            }))}
            onChange={(value) => updateParams({ compare: value })}
          />
          <Button icon={<DownloadOutlined />} onClick={exportCurrentView}>导出当前视图</Button>
          <Tag color="processing">一期：电能</Tag>
        </div>
      </Card>

      <Outlet context={context} />
    </div>
  );
}
