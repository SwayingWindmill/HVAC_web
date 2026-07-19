import { ArrowDownOutlined, ArrowUpOutlined } from '@ant-design/icons';
import { useTelemetryLive, MOCK_DEVICES, MOCK_KPI } from '@/api';
import { COP_GOOD } from '@/theme/tokens';
import { currencyCny, numberZh } from '@/utils/format';

type MetricProps = {
  title: string;
  value: number | string;
  unit?: string;
  trend?: number;
  invert?: boolean;
  primary?: boolean;
  foot?: string;
  good?: boolean;
};

function Metric({ title, value, unit, trend, invert = false, primary = false, foot, good: explicitGood }: MetricProps) {
  const hasTrend = typeof trend === 'number';
  const good = explicitGood ?? (hasTrend ? (invert ? trend < 0 : trend > 0) : true);
  const Arrow = hasTrend && trend < 0 ? ArrowDownOutlined : ArrowUpOutlined;

  return (
    <div className={`dashboard-kpi-item${primary ? ' is-primary' : ''}`}>
      <div className="dashboard-kpi-head"><span>{title}</span></div>
      <div className="dashboard-kpi-value">
        {typeof value === 'number' ? numberZh(value) : value}
        {unit && <span className="dashboard-kpi-unit">{unit}</span>}
      </div>
      <div className="dashboard-kpi-foot">
        {hasTrend ? (
          <span className={`dashboard-kpi-trend ${good ? 'is-good' : 'is-bad'}`}>
            <Arrow /> {Math.abs(trend)}%
          </span>
        ) : null}
        <span className={hasTrend ? undefined : good ? 'is-good' : 'is-bad'}>{foot ?? '较上一周期'}</span>
      </div>
    </div>
  );
}

export default function KpiCards() {
  const { get } = useTelemetryLive(MOCK_DEVICES, ['power', 'cop']);
  const powerNow = Math.round(MOCK_DEVICES.reduce((sum, device) => sum + (get(device, 'power') ?? 0), 0));
  const cops = MOCK_DEVICES.map((device) => get(device, 'cop')).filter((value): value is number => value != null);
  const cop = cops.length ? Math.round((cops.reduce((sum, value) => sum + value, 0) / cops.length) * 10) / 10 : 0;
  const kpi = MOCK_KPI;
  const energyChange = Math.round(((kpi.energyToday - kpi.energyYesterday) / kpi.energyYesterday) * 1000) / 10;

  return (
    <div className="dashboard-kpi-grid">
      <Metric title="实时功率" value={powerNow} unit="kW" trend={kpi.trends.power} foot="较上一小时" primary />
      <Metric title="综合 COP" value={cop.toFixed(1)} good={cop >= COP_GOOD} foot={`${cop >= COP_GOOD ? '达到' : '低于'}目标 ≥ ${COP_GOOD}`} />
      <Metric title="今日能耗" value={kpi.energyToday} unit="kWh" trend={energyChange} invert foot={`昨日 ${numberZh(kpi.energyYesterday)} kWh`} />
      <Metric title="今日节能率" value={kpi.savingRate} unit="%" good={kpi.savingRate >= kpi.savingTarget} foot={`目标 ≥ ${kpi.savingTarget}%`} />
      <Metric title="电费节省" value={currencyCny(kpi.costSavingToday)} foot={`本月累计 ${currencyCny(kpi.costSavingMonth)}`} />
    </div>
  );
}
