import { useState } from 'react';
import { Card, Segmented, Typography } from 'antd';
import ReactECharts from 'echarts-for-react';
import { useBuildingTimeseries } from '@/api';
import { BRAND } from '@/theme/tokens';
import { useUi } from '@/store/ui';

type ChartRange = 'day' | 'week' | 'month';

export default function EnergyTrend() {
  const [range, setRange] = useState<ChartRange>('day');
  const mode = useUi((s) => s.themeMode);
  const dark = mode === 'dark';
  // 历史能耗趋势：React Query 拉取每台设备时序并聚合为建筑级曲线（#8 历史层）。
  const { data: points, isLoading } = useBuildingTimeseries(range);

  const fmt = (ts: number): string => {
    const d = new Date(ts);
    if (range === 'day') return `${String(d.getHours()).padStart(2, '0')}:00`;
    if (range === 'week') return ['日', '一', '二', '三', '四', '五', '六'][d.getDay()];
    return `${d.getDate()}`;
  };

  const option = {
    grid: { left: 48, right: 16, top: 24, bottom: 28 },
    tooltip: { trigger: 'axis' },
    xAxis: {
      type: 'category', data: points.map((p) => fmt(p.ts)),
      axisLine: { lineStyle: { color: dark ? '#444' : '#ccc' } },
      axisLabel: { color: dark ? '#aaa' : '#666', fontSize: 11 },
    },
    yAxis: {
      type: 'value', name: 'kWh',
      splitLine: { lineStyle: { color: dark ? '#222' : '#eee' } },
      axisLabel: { color: dark ? '#aaa' : '#666', fontSize: 11 },
    },
    series: [{
      type: 'line', smooth: true, data: points.map((p) => p.value),
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
    }],
  };

  return (
    <Card
      variant="borderless"
      title={<Typography.Text strong>能耗趋势</Typography.Text>}
      extra={<Segmented<ChartRange> value={range} onChange={setRange}
        options={[{ label: '日', value: 'day' }, { label: '周', value: 'week' }, { label: '月', value: 'month' }]} />}
      styles={{ body: { paddingTop: 8 } }}
    >
      {isLoading && points.length === 0 ? (
        <div style={{ height: 260, display: 'flex', alignItems: 'center', justifyContent: 'center', color: '#999', fontSize: 12 }}>
          加载中…
        </div>
      ) : (
        <ReactECharts option={option} style={{ height: 260 }} notMerge />
      )}
    </Card>
  );
}
