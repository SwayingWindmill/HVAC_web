import { Button, Card, Empty, Grid, Segmented, Skeleton, Space, Tag, Typography } from 'antd';
import ReactECharts from 'echarts-for-react';
import { useMemo, useState } from 'react';
import { useBuildingTimeseries } from '@/api';
import { useUi } from '@/store/ui';
import { BRAND } from '@/theme/tokens';

const FORECAST_HOURS = 12;

type ViewMode = 'power' | 'normalized';

type ChartPoint = {
  label: string;
  actual: number | null;
  forecast: number | null;
  lower: number | null;
  band: number | null;
};

function hourLabel(ts: number) {
  return `${String(new Date(ts).getHours()).padStart(2, '0')}:00`;
}

export default function PerformanceTrend() {
  const screens = Grid.useBreakpoint();
  const compact = screens.md === false;
  const [mode, setMode] = useState<ViewMode>('power');
  const dark = useUi((state) => state.themeMode === 'dark');
  const chartHeight = compact ? 260 : 320;
  const { data: points, isLoading, isError, refetch } = useBuildingTimeseries('day');

  const chartData = useMemo<ChartPoint[]>(() => {
    if (!points.length) return [];

    const actual = points.slice(-18);
    const recentValues = actual.slice(-4).map((point) => point.value);
    const recentAverage = recentValues.reduce((sum, value) => sum + value, 0) / Math.max(recentValues.length, 1);
    const lastTs = actual.at(-1)?.ts ?? Date.now();
    const actualRows: ChartPoint[] = actual.map((point) => ({
      label: hourLabel(point.ts),
      actual: point.value,
      forecast: null,
      lower: null,
      band: null,
    }));

    const forecastRows = Array.from({ length: FORECAST_HOURS }, (_, index): ChartPoint => {
      const hour = new Date(lastTs + (index + 1) * 3_600_000).getHours();
      const daytimeLift = hour >= 9 && hour <= 17 ? Math.sin(((hour - 9) / 8) * Math.PI) * 0.18 : -0.04;
      const forecast = Math.round(recentAverage * (1 + daytimeLift - index * 0.003));
      const spread = Math.round(forecast * (0.065 + index * 0.003));
      return {
        label: hourLabel(lastTs + (index + 1) * 3_600_000),
        actual: null,
        forecast,
        lower: forecast - spread,
        band: spread * 2,
      };
    });

    const lastActual = actualRows.at(-1);
    if (lastActual) {
      lastActual.forecast = lastActual.actual;
      lastActual.lower = lastActual.actual;
      lastActual.band = 0;
    }

    const rows = [...actualRows, ...forecastRows];
    if (mode === 'power') return rows;

    const max = Math.max(...rows.flatMap((row) => [row.actual ?? 0, row.forecast ?? 0]));
    return rows.map((row) => ({
      ...row,
      actual: row.actual == null ? null : Math.round((row.actual / max) * 100),
      forecast: row.forecast == null ? null : Math.round((row.forecast / max) * 100),
      lower: row.lower == null ? null : Math.round((row.lower / max) * 100),
      band: row.band == null ? null : Math.round((row.band / max) * 100),
    }));
  }, [mode, points]);

  const actualValues = chartData.map((row) => row.actual).filter((value): value is number => value != null);
  const forecastValues = chartData.map((row) => row.forecast).filter((value): value is number => value != null);
  const current = actualValues.at(-1) ?? 0;
  const forecastPeak = Math.max(...forecastValues, 0);
  const peakIndex = chartData.findIndex((row) => row.forecast === forecastPeak);
  const capacity = mode === 'power' ? 1250 : 92;
  const lowerValues = chartData.map((row) => row.lower).filter((value): value is number => value != null);
  const chartMin = mode === 'power'
    ? Math.max(0, Math.floor(Math.min(...actualValues, ...lowerValues, current) * 0.86 / 50) * 50)
    : 0;
  const chartMax = mode === 'power'
    ? Math.ceil(Math.max(current, forecastPeak) * 1.08 / 50) * 50
    : 110;
  const unit = mode === 'power' ? 'kW' : '%';
  const axisColor = dark ? '#a8b3c1' : '#64748b';
  const splitColor = dark ? 'rgba(255,255,255,0.065)' : 'rgba(15,23,42,0.07)';
  const peakLabel = chartData[peakIndex]?.label;

  const option = {
    animationDuration: 420,
    aria: {
      enabled: true,
      decal: { show: false },
      description: `建筑负荷趋势图。当前负荷 ${current}${unit}，示例预测峰值 ${forecastPeak}${unit}，峰值时刻 ${peakLabel ?? '未知'}。`,
    },
    grid: { left: compact ? 44 : 52, right: compact ? 12 : 18, top: compact ? 18 : 28, bottom: 34 },
    tooltip: {
      trigger: 'axis',
      backgroundColor: dark ? 'rgba(15,23,42,0.96)' : 'rgba(255,255,255,0.98)',
      borderColor: dark ? 'rgba(255,255,255,0.12)' : 'rgba(15,23,42,0.08)',
      textStyle: { color: dark ? '#e5edf5' : '#1e293b' },
      formatter: (params: Array<{ axisValue: string; seriesName: string; value: number | null }>) => {
        const visible = params.filter((item) => item.value != null && (item.seriesName === '实际负荷' || item.seriesName === 'P50 预测'));
        return [`<strong>${params[0]?.axisValue ?? ''}</strong>`, ...visible.map((item) => `${item.seriesName}：${Number(item.value).toLocaleString('zh-CN')} ${unit}`)].join('<br/>');
      },
    },
    legend: {
      show: !compact,
      top: 0,
      right: 8,
      icon: 'roundRect',
      itemWidth: 16,
      itemHeight: 3,
      textStyle: { color: axisColor, fontSize: 12 },
      data: ['实际负荷', 'P50 预测'],
    },
    xAxis: {
      type: 'category',
      boundaryGap: false,
      data: chartData.map((row) => row.label),
      axisLine: { lineStyle: { color: splitColor } },
      axisTick: { show: false },
      axisLabel: { color: axisColor, fontSize: 12, interval: compact ? 5 : 3 },
    },
    yAxis: {
      type: 'value',
      name: unit,
      min: chartMin,
      max: chartMax,
      nameTextStyle: { color: axisColor, padding: [0, 0, 0, -20] },
      axisLabel: { color: axisColor, fontSize: 12 },
      splitLine: { lineStyle: { color: splitColor } },
    },
    series: [
      {
        name: 'P10 下界',
        type: 'line',
        stack: 'forecast-band',
        data: chartData.map((row) => row.lower),
        symbol: 'none',
        lineStyle: { opacity: 0 },
        areaStyle: { opacity: 0 },
        silent: true,
      },
      {
        name: 'P10-P90 区间',
        type: 'line',
        stack: 'forecast-band',
        data: chartData.map((row) => row.band),
        symbol: 'none',
        lineStyle: { opacity: 0 },
        areaStyle: { color: 'rgba(14,165,233,0.15)' },
        silent: true,
      },
      {
        name: '实际负荷',
        type: 'line',
        smooth: 0.24,
        data: chartData.map((row) => row.actual),
        symbol: 'none',
        itemStyle: { color: BRAND.teal },
        lineStyle: { color: BRAND.teal, width: 3 },
        areaStyle: {
          color: {
            type: 'linear', x: 0, y: 0, x2: 0, y2: 1,
            colorStops: [
              { offset: 0, color: 'rgba(15,181,174,0.16)' },
              { offset: 1, color: 'rgba(15,181,174,0.01)' },
            ],
          },
        },
      },
      {
        name: 'P50 预测',
        type: 'line',
        smooth: 0.24,
        data: chartData.map((row) => row.forecast),
        symbol: 'none',
        itemStyle: { color: '#0ea5e9' },
        lineStyle: { color: '#0ea5e9', width: 2.6, type: 'dashed' },
      },
    ],
  };

  return (
    <Card
      variant="borderless"
      className="dashboard-section-card dashboard-performance-card"
      title={
        <Space size={9}>
          <Typography.Text strong>负荷趋势与预测</Typography.Text>
          <Tag color="blue" title="预测数据来自演示模型，不用于生产控制">示例预测</Tag>
        </Space>
      }
      extra={<Segmented<ViewMode> size="small" value={mode} onChange={setMode} options={[{ label: '功率', value: 'power' }, { label: '负荷率', value: 'normalized' }]} />}
    >
      <div className="dashboard-performance-summary">
        <div><span>当前负荷</span><strong>{current.toLocaleString('zh-CN')} <small>{unit}</small></strong></div>
        <div><span>预测峰值</span><strong>{forecastPeak.toLocaleString('zh-CN')} <small>{unit}</small></strong></div>
        {!compact && <div><span>峰值时刻</span><strong>{peakLabel ?? '--:--'}</strong></div>}
        {!compact && <div><span>剩余容量</span><strong className={capacity - current <= 0 ? 'is-warning' : ''}>{Math.max(0, capacity - current).toLocaleString('zh-CN')} <small>{unit}</small></strong></div>}
      </div>

      {isLoading && !chartData.length ? (
        <div className="dashboard-chart-skeleton" style={{ height: chartHeight }}>
          <Skeleton active title={false} paragraph={{ rows: compact ? 4 : 5 }} />
        </div>
      ) : isError ? (
        <div className="dashboard-chart-state" style={{ height: chartHeight }}>
          <Typography.Text strong>负荷数据暂时不可用</Typography.Text>
          <Typography.Text type="secondary">请检查遥测服务连接，或稍后重试。</Typography.Text>
          <Button size="small" onClick={() => void refetch()}>重新加载</Button>
        </div>
      ) : !chartData.length ? (
        <div className="dashboard-chart-state" style={{ height: chartHeight }}>
          <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="当前建筑暂无可用功率数据" />
        </div>
      ) : (
        <ReactECharts option={option} style={{ height: chartHeight }} notMerge />
      )}

    </Card>
  );
}
