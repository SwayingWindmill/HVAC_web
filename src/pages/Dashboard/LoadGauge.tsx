import { Card, Typography } from 'antd';
import ReactECharts from 'echarts-for-react';
import { useTelemetryLive, MOCK_DEVICES } from '@/api';
import { STATUS, LOAD_COMFORT } from '@/theme/tokens';

export default function LoadGauge() {
  // 综合负荷率：实时层（初始快照 + 推送）
  const { get } = useTelemetryLive(MOCK_DEVICES, ['load']);
  const vals = MOCK_DEVICES.map((d) => get(d, 'load')).filter((v): v is number => v != null);
  const load = vals.length ? Math.round(vals.reduce((a, b) => a + b, 0) / vals.length) : 63;
  const good = load >= LOAD_COMFORT[0] && load <= LOAD_COMFORT[1];

  const option = {
    series: [{
      type: 'gauge', min: 0, max: 100, radius: '92%', center: ['50%', '62%'],
      progress: { show: true, width: 12, itemStyle: { color: good ? STATUS.ok : STATUS.warn } },
      axisLine: { lineStyle: { width: 12, color: [[1, 'rgba(128,128,128,0.18)']] } },
      axisTick: { show: false }, splitLine: { length: 10, lineStyle: { color: '#999' } },
      axisLabel: { fontSize: 10, color: '#999', distance: 12 },
      pointer: { width: 4, itemStyle: { color: good ? STATUS.ok : STATUS.warn } },
      anchor: { show: true, size: 8, itemStyle: { color: good ? STATUS.ok : STATUS.warn } },
      detail: { valueAnimation: true, fontSize: 24, fontWeight: 'bolder', offsetCenter: [0, '38%'], formatter: '{value}%' },
      data: [{ value: load }],
    }],
  };
  return (
    <Card variant="borderless" title={<Typography.Text strong>综合负荷率</Typography.Text>}
      styles={{ body: { paddingTop: 4 } }}>
      <ReactECharts option={option} style={{ height: 180 }} notMerge />
      <div style={{ textAlign: 'center', fontSize: 12, opacity: 0.7 }}>
        舒适区间 {LOAD_COMFORT[0]}–{LOAD_COMFORT[1]}%
      </div>
    </Card>
  );
}
