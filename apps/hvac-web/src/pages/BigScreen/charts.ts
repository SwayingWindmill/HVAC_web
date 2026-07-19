import * as echarts from 'echarts';
import { COMPOSITION } from './data';
import { ACCENT, AMBER, DIM, GREEN, PANEL_BD, TEXT, hexA } from './theme';

export const compareOpt = () => ({
  tooltip: { trigger: 'axis', backgroundColor: '#141c30', borderColor: PANEL_BD, textStyle: { color: TEXT, fontSize: 11 }, axisPointer: { lineStyle: { color: hexA(PANEL_BD, 0.8) } } },
  legend: { top: 0, right: 0, textStyle: { color: DIM, fontSize: 10 }, itemWidth: 14, itemHeight: 3, itemGap: 12, data: ['基准能耗', '实际能耗'] },
  grid: { left: 48, right: 12, top: 26, bottom: 28 },
  xAxis: { type: 'category', data: ['05-14','05-15','05-16','05-17','05-18','05-19','05-20'], axisLine: { lineStyle: { color: hexA(PANEL_BD, 0.6) } }, axisLabel: { color: DIM, fontSize: 10 } },
  yAxis: { type: 'value', max: 50000, splitLine: { lineStyle: { color: hexA(PANEL_BD, 0.3), type: 'dashed' } }, axisLabel: { color: DIM, fontSize: 10, formatter: '{value}' } },
  series: [
    { name: '基准能耗', type: 'line', smooth: true, symbol: 'none', lineStyle: { color: DIM, width: 1.5, type: 'dashed' }, data: [42000,43000,42800,43200,44100,43900,45000] },
    { name: '实际能耗', type: 'line', smooth: true, symbol: 'none', areaStyle: { color: new echarts.graphic.LinearGradient(0, 0, 0, 1, [{ offset: 0, color: hexA(ACCENT, 0.25) }, { offset: 1, color: hexA(ACCENT, 0.01) }]) }, lineStyle: { color: ACCENT, width: 2 }, data: [40000,41000,39500,38000,37000,36800,36000] },
  ],
});

export const monthOpt = () => ({
  tooltip: { trigger: 'axis', backgroundColor: '#141c30', borderColor: PANEL_BD, textStyle: { color: TEXT, fontSize: 11 } },
  legend: { top: 0, right: 0, textStyle: { color: DIM, fontSize: 10 }, data: ['节能量(MWh)', '节能率'] },
  grid: { left: 46, right: 46, top: 26, bottom: 26 },
  xAxis: { type: 'category', data: Array.from({ length: 12 }, (_, i) => `${i + 1}月`), axisLine: { lineStyle: { color: hexA(PANEL_BD, 0.6) } }, axisLabel: { color: DIM, fontSize: 10, interval: 1 } },
  yAxis: [
    { type: 'value', name: '(MWh)', max: 100000, splitLine: { lineStyle: { color: hexA(PANEL_BD, 0.3) } }, axisLabel: { color: DIM, fontSize: 10 } },
    { type: 'value', name: '(%)', max: 40, splitLine: { show: false }, axisLabel: { color: DIM, fontSize: 10, formatter: '{value}' } },
  ],
  series: [
    { name: '节能量(MWh)', type: 'bar', barWidth: 14, itemStyle: { color: new echarts.graphic.LinearGradient(0, 0, 0, 1, [{ offset: 0, color: ACCENT }, { offset: 1, color: hexA(ACCENT, 0.35) }]), borderRadius: [3, 3, 0, 0] }, data: [42000, 48000, 55000, 62000, 75000, 85000, 90000, 88000, 72000, 65000, 58000, 52000] },
    { name: '节能率', type: 'line', yAxisIndex: 1, smooth: true, symbol: 'circle', symbolSize: 4, lineStyle: { color: AMBER, width: 1.5 }, itemStyle: { color: AMBER }, data: [12, 14, 16, 19, 22, 25, 28, 27, 23, 21, 18, 15] },
  ],
});

export const composeOpt = () => ({
  tooltip: { trigger: 'item', formatter: '{b}: {c} kWh ({d}%)', backgroundColor: '#141c30', borderColor: PANEL_BD, textStyle: { color: TEXT, fontSize: 11 } },
  legend: {
    orient: 'vertical', right: 0, top: 'middle',
    textStyle: { color: DIM, fontSize: 11 },
    itemWidth: 10, itemHeight: 10, itemGap: 10,
    formatter: (name: string) => {
      const item = COMPOSITION.find((c) => c.name === name);
      return `${name}  ${item?.pct}%  ${(item?.kwh ?? 0).toLocaleString()}`;
    },
  },
  series: [{
    type: 'pie', radius: ['52%', '72%'], center: ['35%', '50%'],
    avoidLabelOverlap: false, label: { show: false },
    emphasis: { label: { show: true, fontSize: 13, fontWeight: 'bold', color: TEXT } },
    data: COMPOSITION.map((c, i) => ({ name: c.name, value: c.kwh, itemStyle: { color: [ACCENT, '#0e9c96', '#0b7f7a', '#086965', '#075652'][i % 5] } })),
  }],
});

export const healthGaugeOpt = () => ({
  series: [{
    type: 'gauge', startAngle: 200, endAngle: -20, min: 0, max: 100,
    radius: '100%', center: ['50%', '62%'],
    pointer: { show: false },
    progress: { show: true, overlap: false, roundCap: true, clip: false, itemStyle: { color: ACCENT } },
    axisLine: { roundCap: true, lineStyle: { width: 14, color: [[1, hexA(PANEL_BD, 0.5)]] } },
    axisTick: { show: false },
    splitLine: { length: 4, distance: -8, lineStyle: { width: 2, color: hexA(TEXT, 0.15) } },
    axisLabel: { show: false },
    detail: {
      valueAnimation: true,
      offsetCenter: [0, '20%'], fontSize: 32, fontWeight: 'bold', color: TEXT,
      formatter: '{value}', fontFamily: '"DIN Alternate", "Roboto Mono", monospace',
    },
    data: [{ value: 92 }],
  }],
  graphic: [{ type: 'text', left: 'center', top: '70%', style: { text: '健康状态: 优', fill: GREEN, fontSize: 13, textAlign: 'center' } }],
});

export const alarmGaugeOpt = () => ({
  series: [{
    type: 'gauge', startAngle: 200, endAngle: -20, min: 0, max: 100,
    radius: '95%', center: ['50%', '58%'],
    pointer: { show: false },
    progress: { show: true, roundCap: true, clip: false, itemStyle: { color: GREEN } },
    axisLine: { roundCap: true, lineStyle: { width: 12, color: [[1, hexA(PANEL_BD, 0.5)]] } },
    axisTick: { show: false },
    splitLine: { length: 3, distance: -6, lineStyle: { width: 2, color: hexA(TEXT, 0.12) } },
    axisLabel: { show: false },
    detail: { valueAnimation: true, offsetCenter: [0, '18%'], fontSize: 26, fontWeight: 'bold', color: TEXT, formatter: '{value}%' },
    data: [{ value: 85 }],
  }],
  graphic: [{ type: 'text', left: 'center', top: '66%', style: { text: '良好', fill: GREEN, fontSize: 12, textAlign: 'center' } }],
});
