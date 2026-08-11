import { useMemo, type CSSProperties } from 'react';
import ReactECharts from 'echarts-for-react';
import { Alert, Button, Card, Space, Tag, Typography } from 'antd';
import { CalendarOutlined, DownloadOutlined, ThunderboltOutlined } from '@ant-design/icons';
import type { EnergySeriesPoint, EnergySeriesResponse } from '@/api/energy-analytics';
import type { EnergyWorkspacePeriod } from './energy-workspace';
import {
  buildCumulativeEnergy,
  buildEnergyCsv,
  buildMonthCalendar,
  buildWeekSlots,
  buildYearSlots,
  sortEnergyPoints,
  summarizeEnergyPoints,
} from './energy-presentation';

interface RealEnergyWorkspacePanelsProps {
  readonly period: EnergyWorkspacePeriod;
  readonly anchor: string;
  readonly timezone: string;
  readonly currentLabel: string;
  readonly previousLabel: string;
  readonly currentResponse: EnergySeriesResponse;
  readonly previousResponse?: EnergySeriesResponse;
  readonly previousLoading: boolean;
  readonly drillDownPeriod?: EnergyWorkspacePeriod | null;
  readonly onDrillDown: (periodStart: string) => void;
}

const PERIOD_LABELS: Record<EnergyWorkspacePeriod, string> = {
  day: '日',
  week: '周',
  month: '月',
  year: '年',
};

function formatEnergy(value: number | null | undefined, digits = 1): string {
  if (value === null || value === undefined) return '—';
  return `${new Intl.NumberFormat('zh-CN', { maximumFractionDigits: digits }).format(value)} kWh`;
}

function formatBucketLabel(point: EnergySeriesPoint, period: EnergyWorkspacePeriod, timezone: string): string {
  const date = new Date(point.periodStart);
  if (period === 'day') {
    return new Intl.DateTimeFormat('zh-CN', { timeZone: timezone, hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }).format(date);
  }
  if (period === 'year') {
    return new Intl.DateTimeFormat('zh-CN', { timeZone: timezone, month: 'short' }).format(date);
  }
  return new Intl.DateTimeFormat('zh-CN', { timeZone: timezone, month: '2-digit', day: '2-digit' }).format(date);
}

function formatPeriod(point: EnergySeriesPoint, timezone: string): string {
  const formatter = new Intl.DateTimeFormat('zh-CN', {
    timeZone: timezone,
    dateStyle: 'medium',
    timeStyle: 'short',
  });
  return `${formatter.format(new Date(point.periodStart))} — ${formatter.format(new Date(point.periodEnd))}`;
}

function changeText(current: number | undefined, previous: number | undefined): { label: string; tone?: 'positive' | 'negative' } {
  if (current === undefined || previous === undefined) return { label: '无可比基期' };
  if (previous === 0) return { label: current === 0 ? '持平' : `+${formatEnergy(current)}`, tone: current > 0 ? 'negative' : undefined };
  const percentage = ((current - previous) / previous) * 100;
  return {
    label: `${percentage >= 0 ? '+' : ''}${percentage.toFixed(1)}%`,
    tone: percentage > 0 ? 'negative' : percentage < 0 ? 'positive' : undefined,
  };
}

function downloadCsv(
  anchor: string,
  currentLabel: string,
  currentPoints: readonly EnergySeriesPoint[],
  previousLabel: string,
  previousPoints: readonly EnergySeriesPoint[],
) {
  const csv = buildEnergyCsv(currentLabel, currentPoints, previousLabel, previousPoints);
  const url = URL.createObjectURL(new Blob([`\uFEFF${csv}`], { type: 'text/csv;charset=utf-8' }));
  const link = document.createElement('a');
  link.href = url;
  link.download = `energy-${anchor}.csv`;
  link.click();
  URL.revokeObjectURL(url);
}

function PeriodValue({ point, previousPoint }: { point: EnergySeriesPoint | null; previousPoint: EnergySeriesPoint | null }) {
  const change = changeText(point?.energyKWh, previousPoint?.energyKWh);
  return (
    <>
      <strong>{formatEnergy(point?.energyKWh)}</strong>
      <span className={change.tone ? `real-energy__change real-energy__change--${change.tone}` : 'real-energy__change'}>
        {change.label}
      </span>
    </>
  );
}

function DayDistribution({
  points,
  previousPoints,
  timezone,
}: {
  points: readonly EnergySeriesPoint[];
  previousPoints: readonly EnergySeriesPoint[];
  timezone: string;
}) {
  const sorted = sortEnergyPoints(points);
  const previous = sortEnergyPoints(previousPoints);
  const byHour = new Map(sorted.map((point) => [
    Number(new Intl.DateTimeFormat('en', { timeZone: timezone, hour: '2-digit', hourCycle: 'h23' }).format(new Date(point.periodStart))),
    point,
  ]));
  const maximum = Math.max(1, ...sorted.map((point) => point.energyKWh));
  return (
    <Card
      className="energy-period-card"
      title={<Space><ThunderboltOutlined />24 小时电量分布</Space>}
      extra={<Typography.Text type="secondary">真实小时桶</Typography.Text>}
    >
      <div className="real-energy__hour-grid">
        {Array.from({ length: 24 }, (_, hour) => {
          const point = byHour.get(hour) ?? null;
          const intensity = point ? Math.max(0.08, point.energyKWh / maximum) : 0;
          return (
            <div
              key={hour}
              className={point ? 'real-energy__hour-cell real-energy__hour-cell--measured' : 'real-energy__hour-cell'}
              style={{ '--energy-intensity': intensity } as CSSProperties}
              title={point ? `${String(hour).padStart(2, '0')}:00 · ${formatEnergy(point.energyKWh)}` : `${String(hour).padStart(2, '0')}:00 · 未返回`}
            >
              <span>{String(hour).padStart(2, '0')}</span>
              <strong>{point ? new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 0 }).format(point.energyKWh) : '—'}</strong>
              <small>{changeText(point?.energyKWh, previous[hour]?.energyKWh).label}</small>
            </div>
          );
        })}
      </div>
    </Card>
  );
}

function WeekDistribution({
  anchor,
  points,
  previousPoints,
  timezone,
  onDrillDown,
}: {
  anchor: string;
  points: readonly EnergySeriesPoint[];
  previousPoints: readonly EnergySeriesPoint[];
  timezone: string;
  onDrillDown: (periodStart: string) => void;
}) {
  const slots = buildWeekSlots(anchor, points, previousPoints, timezone);
  return (
    <Card className="energy-period-card" title={<Space><CalendarOutlined />周度每日能耗</Space>} extra={<Tag>点击下钻到日</Tag>}>
      <div className="real-energy__week-grid">
        {slots.map((slot) => (
          <button
            type="button"
            key={slot.key}
            className="real-energy__period-tile"
            disabled={!slot.point}
            onClick={() => slot.point && onDrillDown(slot.point.periodStart)}
          >
            <span>{slot.label}</span>
            <small>{slot.key.slice(5).replace('-', '/')}</small>
            <PeriodValue point={slot.point} previousPoint={slot.previousPoint} />
          </button>
        ))}
      </div>
    </Card>
  );
}

function MonthDistribution({
  anchor,
  points,
  previousPoints,
  timezone,
  onDrillDown,
}: {
  anchor: string;
  points: readonly EnergySeriesPoint[];
  previousPoints: readonly EnergySeriesPoint[];
  timezone: string;
  onDrillDown: (periodStart: string) => void;
}) {
  const cells = buildMonthCalendar(anchor, points, previousPoints, timezone);
  return (
    <Card className="energy-period-card" title={<Space><CalendarOutlined />月度能耗日历</Space>} extra={<Tag>点击日期下钻</Tag>}>
      <div className="real-energy__calendar-weekdays" aria-hidden="true">
        {['一', '二', '三', '四', '五', '六', '日'].map((label) => <span key={label}>周{label}</span>)}
      </div>
      <div className="real-energy__calendar-grid">
        {cells.map((cell) => (
          <button
            type="button"
            key={cell.date}
            className={cell.inPeriod ? 'real-energy__calendar-cell' : 'real-energy__calendar-cell real-energy__calendar-cell--outside'}
            disabled={!cell.point}
            onClick={() => cell.point && onDrillDown(cell.point.periodStart)}
          >
            <span>{cell.day}</span>
            {cell.inPeriod ? <PeriodValue point={cell.point} previousPoint={cell.previousPoint} /> : null}
          </button>
        ))}
      </div>
    </Card>
  );
}

function YearDistribution({
  anchor,
  points,
  previousPoints,
  timezone,
  onDrillDown,
}: {
  anchor: string;
  points: readonly EnergySeriesPoint[];
  previousPoints: readonly EnergySeriesPoint[];
  timezone: string;
  onDrillDown: (periodStart: string) => void;
}) {
  const slots = buildYearSlots(anchor, points, previousPoints, timezone);
  return (
    <Card className="energy-period-card" title={<Space><CalendarOutlined />年度月份总览</Space>} extra={<Tag>点击月份下钻</Tag>}>
      <div className="real-energy__year-grid">
        {slots.map((slot) => (
          <button
            type="button"
            key={slot.key}
            className="real-energy__period-tile real-energy__period-tile--month"
            disabled={!slot.point}
            onClick={() => slot.point && onDrillDown(slot.point.periodStart)}
          >
            <span>{slot.label}</span>
            <PeriodValue point={slot.point} previousPoint={slot.previousPoint} />
          </button>
        ))}
      </div>
    </Card>
  );
}

export function RealEnergyWorkspacePanels({
  period,
  anchor,
  timezone,
  currentLabel,
  previousLabel,
  currentResponse,
  previousResponse,
  previousLoading,
  drillDownPeriod,
  onDrillDown,
}: RealEnergyWorkspacePanelsProps) {
  const currentPoints = useMemo(() => sortEnergyPoints(currentResponse.points), [currentResponse.points]);
  const previousPoints = useMemo(() => sortEnergyPoints(previousResponse?.points ?? []), [previousResponse?.points]);
  const currentSummary = useMemo(() => summarizeEnergyPoints(currentPoints), [currentPoints]);
  const maximumLength = Math.max(currentPoints.length, previousPoints.length);
  const categories = Array.from({ length: maximumLength }, (_, index) => currentPoints[index]
    ? formatBucketLabel(currentPoints[index], period, timezone)
    : `第 ${index + 1} 期`);
  const cumulative = period === 'day' ? buildCumulativeEnergy(currentPoints).map(([, value]) => value) : [];
  const chartOption = {
    animation: false,
    color: ['#0f9f98', '#8b9aa9', '#ef9b2d'],
    grid: { left: 56, right: period === 'day' ? 64 : 24, top: 42, bottom: 52 },
    legend: { top: 0 },
    tooltip: {
      trigger: 'axis',
      valueFormatter: (value: number | null) => value === null ? '未返回' : `${value} kWh`,
    },
    xAxis: { type: 'category', data: categories, axisLabel: { hideOverlap: true } },
    yAxis: period === 'day'
      ? [{ type: 'value', name: '小时电量 kWh', min: 0 }, { type: 'value', name: '累计 kWh', min: 0 }]
      : { type: 'value', name: 'kWh', min: 0 },
    series: [
      {
        name: currentLabel,
        type: 'bar',
        data: Array.from({ length: maximumLength }, (_, index) => currentPoints[index]?.energyKWh ?? null),
        barMaxWidth: 32,
        itemStyle: { borderRadius: [5, 5, 0, 0] },
      },
      {
        name: previousLoading ? '比较基期读取中' : previousLabel,
        type: 'line',
        data: Array.from({ length: maximumLength }, (_, index) => previousPoints[index]?.energyKWh ?? null),
        connectNulls: false,
        symbol: 'circle',
        symbolSize: 5,
      },
      ...(period === 'day' ? [{
        name: '当日累计',
        type: 'line',
        yAxisIndex: 1,
        data: cumulative,
        smooth: true,
        showSymbol: false,
      }] : []),
    ],
  };
  const chartEvents = {
    click: (parameters: { dataIndex?: number }) => {
      if (!drillDownPeriod || parameters.dataIndex === undefined) return;
      const point = currentPoints[parameters.dataIndex];
      if (point) onDrillDown(point.periodStart);
    },
  };
  const tableLength = Math.max(currentPoints.length, previousPoints.length);

  return (
    <>
      <Card
        className="energy-chart-card energy-chart-card--converged"
        title={(
          <div>
            <Typography.Text strong id="energy-trend-title">{PERIOD_LABELS[period]}度电能趋势与基期对比</Typography.Text>
            <Typography.Paragraph type="secondary">
              所有值均来自 Site 级权威 Energy Read Model；比较线按周期桶序号对齐，缺失桶保持为空。
            </Typography.Paragraph>
          </div>
        )}
        extra={(
          <Space wrap>
            <Tag>{currentSummary.measuredCount} 个当前桶</Tag>
            <Button
              icon={<DownloadOutlined />}
              onClick={() => downloadCsv(anchor, currentLabel, currentPoints, previousLabel, previousPoints)}
              disabled={currentPoints.length === 0 && previousPoints.length === 0}
            >
              导出真实数据
            </Button>
          </Space>
        )}
        aria-labelledby="energy-trend-title"
      >
        {currentPoints.length === 0 ? (
          <div className="real-energy__empty" role="status">当前授权范围内没有返回能源时段，此状态不代表 0 kWh。</div>
        ) : (
          <ReactECharts option={chartOption} onEvents={chartEvents} style={{ height: 390 }} notMerge lazyUpdate />
        )}
      </Card>

      {period === 'day' ? <DayDistribution points={currentPoints} previousPoints={previousPoints} timezone={timezone} /> : null}
      {period === 'week' ? <WeekDistribution anchor={anchor} points={currentPoints} previousPoints={previousPoints} timezone={timezone} onDrillDown={onDrillDown} /> : null}
      {period === 'month' ? <MonthDistribution anchor={anchor} points={currentPoints} previousPoints={previousPoints} timezone={timezone} onDrillDown={onDrillDown} /> : null}
      {period === 'year' ? <YearDistribution anchor={anchor} points={currentPoints} previousPoints={previousPoints} timezone={timezone} onDrillDown={onDrillDown} /> : null}

      <Card
        className="energy-table-card"
        title={(
          <div>
            <Typography.Text strong id="energy-period-table-title">当前周期与比较基期明细</Typography.Text>
            <Typography.Paragraph type="secondary">表格不补零；当前桶与比较桶仅按顺序并列，不伪造时间对齐关系。</Typography.Paragraph>
          </div>
        )}
        extra={<Tag>{tableLength} 行</Tag>}
        aria-labelledby="energy-period-table-title"
      >
        {tableLength === 0 ? (
          <div className="real-energy__empty real-energy__empty--compact" role="status">没有可列出的返回时段。</div>
        ) : (
          <div className="real-energy__table-scroll">
            <table>
              <thead>
                <tr>
                  <th scope="col">当前时段</th>
                  <th scope="col">当前电能</th>
                  <th scope="col">比较基期</th>
                  <th scope="col">基期电能</th>
                  <th scope="col">变化</th>
                  {drillDownPeriod ? <th scope="col">操作</th> : null}
                </tr>
              </thead>
              <tbody>
                {Array.from({ length: tableLength }, (_, index) => {
                  const point = currentPoints[index];
                  const previousPoint = previousPoints[index];
                  const change = changeText(point?.energyKWh, previousPoint?.energyKWh);
                  return (
                    <tr key={`${point?.periodStart ?? 'missing'}:${previousPoint?.periodStart ?? 'missing'}:${index}`}>
                      <td>{point ? formatPeriod(point, timezone) : '未返回'}</td>
                      <td>{formatEnergy(point?.energyKWh, 2)}</td>
                      <td>{previousPoint ? formatPeriod(previousPoint, timezone) : '未返回'}</td>
                      <td>{formatEnergy(previousPoint?.energyKWh, 2)}</td>
                      <td className={change.tone ? `real-energy__change real-energy__change--${change.tone}` : 'real-energy__change'}>{change.label}</td>
                      {drillDownPeriod ? (
                        <td>
                          <Button size="small" disabled={!point} onClick={() => point && onDrillDown(point.periodStart)}>
                            查看{PERIOD_LABELS[drillDownPeriod]}
                          </Button>
                        </td>
                      ) : null}
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
      </Card>

      <Alert
        className="real-energy__deferred"
        type="info"
        showIcon
        message="尚未接入权威模型的 Energy 能力"
        description={(
          <Space wrap size={[8, 8]}>
            {['峰谷平电价与费用', '碳排放', '运行基线与目标', '设备级能耗分摊', '楼宇与区域排名', '非营业时段识别', 'COP 联合分析'].map((item) => <Tag key={item}>{item}</Tag>)}
            <Typography.Text type="secondary">这些区域不会使用 Demo 公式或前端推断值替代。</Typography.Text>
          </Space>
        )}
      />
    </>
  );
}
