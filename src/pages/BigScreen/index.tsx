import { lazy, Suspense, useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Button, ConfigProvider, Switch, theme as antdTheme } from 'antd';
import {
  ApiOutlined,
  BulbOutlined,
  EnvironmentOutlined,
  FullscreenExitOutlined,
  SafetyCertificateOutlined,
  ThunderboltOutlined,
} from '@ant-design/icons';
import ReactECharts from 'echarts-for-react';
import { useTelemetryLive, MOCK_DEVICES } from '@/api';
import ErrorBoundary from '@/components/ErrorBoundary';
import { fddList, useOps } from '@/store/ops';
import { isWorkOrderActive } from '@/domain/opsMeta';
import { compareOpt, composeOpt, monthOpt } from './charts';
import {
  AlarmProgressSummary,
  AlarmRow,
  ChartFrame,
  DeviceStatusRail,
  HealthSummary,
  KpiCard,
  Panel,
  Spinner,
} from './components';
import {
  ALARM_PROGRESS,
  DEVICE_STATS,
  DIAGNOSTICS,
  HEALTH_SCORES,
  KPI_DATA,
  SCENES,
  SUGGESTIONS,
  type DeviceStat,
  type SceneKey,
} from './data';
import { ACCENT, ACCENT_DK, GREEN, RADIUS } from './theme';
import './BigScreen.css';

const System3D = lazy(() => import('./System3D'));

export default function BigScreen() {
  const navigate = useNavigate();
  const suggestions = useOps((state) => state.suggestions);
  const workOrders = useOps((state) => state.workOrders);

  const [scene, setScene] = useState<SceneKey>('overview');
  const [compactScreen, setCompactScreen] = useState(() => window.innerWidth < 1280);
  const [cursorHidden, setCursorHidden] = useState(false);
  const [clock, setClock] = useState(() => new Date());
  const [realData, setRealData] = useState(false);

  useEffect(() => {
    const id = window.setInterval(() => setClock(new Date()), 1000);
    return () => window.clearInterval(id);
  }, []);

  useEffect(() => {
    const onResize = () => setCompactScreen(window.innerWidth < 1280);
    window.addEventListener('resize', onResize);
    return () => window.removeEventListener('resize', onResize);
  }, []);

  useEffect(() => {
    const id = window.setInterval(() => {
      setScene((current) => {
        const index = SCENES.findIndex((item) => item.key === current);
        return SCENES[(index + 1) % SCENES.length].key;
      });
    }, 45000);
    return () => window.clearInterval(id);
  }, []);

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') navigate('/dashboard');
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [navigate]);

  useEffect(() => {
    let timer = 0;
    const reset = () => {
      setCursorHidden(false);
      window.clearTimeout(timer);
      timer = window.setTimeout(() => setCursorHidden(true), 3000);
    };
    document.addEventListener('mousemove', reset);
    document.addEventListener('keydown', reset);
    timer = window.setTimeout(() => setCursorHidden(true), 3000);
    return () => {
      document.removeEventListener('mousemove', reset);
      document.removeEventListener('keydown', reset);
      window.clearTimeout(timer);
    };
  }, []);

  const clockStr = clock.toLocaleTimeString('zh-CN', { hour12: false });
  const dateStr = clock.toLocaleDateString('zh-CN', { month: '2-digit', day: '2-digit', weekday: 'short' });

  const { get } = useTelemetryLive(realData ? MOCK_DEVICES : [], ['power', 'cop', 'load']);
  const live = (index: number) => ({
    power: get(MOCK_DEVICES[index], 'power') ?? 0,
    cop: get(MOCK_DEVICES[index], 'cop') ?? 0,
    load: get(MOCK_DEVICES[index], 'load') ?? 0,
  });

  const deviceStats: DeviceStat[] = realData
    ? [
        { name: '冷机', run: 2, total: 3, power: Math.round(live(0).power + live(1).power), cop: Math.round(((live(0).cop + live(1).cop) / 2) * 100) / 100, load: Math.round((live(0).load + live(1).load) / 2), unit: 'kW' },
        { name: '冷却塔', run: 2, total: 3, power: Math.round(live(2).power), load: Math.round(live(2).load), unit: 'kW' },
        { name: '冷冻泵', run: 2, total: 3, power: Math.round(live(3).power), load: Math.round(live(3).load), freq: 105, unit: 'kW' },
        { name: '冷却泵', run: 2, total: 3, power: Math.round(live(4).power), load: Math.round(live(4).load), unit: 'kW' },
        { name: '末端', run: 48, total: 52, power: Math.round(live(5).power), load: Math.round(live(5).load), unit: 'kW' },
      ]
    : DEVICE_STATS;

  const liveCop = realData ? Math.round(((live(0).cop + live(1).cop) / 2) * 100) / 100 : 6.28;
  const liveLoad = realData ? Math.round((live(0).load + live(1).load) / 2) : 45.4;
  const activeScene = SCENES.find((item) => item.key === scene) ?? SCENES[0];
  const approvedSaving = suggestions
    .filter((item) => item.status === 'approved' || item.status === 'dispatched')
    .reduce((sum, item) => sum + item.saving.cny, 0);
  const activeTickets = workOrders.filter(isWorkOrderActive).length;
  const highRiskFdd = fddList.filter((item) => item.severity === 'critical' || item.severity === 'major').length;
  const runningDevices = deviceStats.reduce((sum, item) => sum + item.run, 0);
  const totalDevices = deviceStats.reduce((sum, item) => sum + item.total, 0);
  const kpiData = KPI_DATA.map((item) => {
    if (item.label === '累计节省电费') return { ...item, value: item.value + approvedSaving };
    if (item.label === '冷站综合COP') return { ...item, value: liveCop };
    if (item.label === '系统在线率') return { ...item, value: highRiskFdd > 0 ? 98.9 : item.value };
    return item;
  });

  return (
    <ConfigProvider theme={{ algorithm: antdTheme.darkAlgorithm, token: { colorPrimary: ACCENT, borderRadius: RADIUS } }}>
      <div className={`bigscreen-shell${cursorHidden ? ' is-cursor-hidden' : ''}`}>
        <main className="bigscreen-stage">
          <header className="bigscreen-header">
            <div className="bigscreen-brand">
              <span className="bigscreen-eyebrow">HVAC OPERATIONS COMMAND</span>
              <h1>商业建筑智慧能源驾驶舱</h1>
              <p>{activeScene.subtitle}</p>
            </div>

            <nav className="bigscreen-scenes" aria-label="驾驶舱场景">
              {SCENES.map((item) => (
                <button
                  key={item.key}
                  type="button"
                  className={`bigscreen-scene-button${item.key === scene ? ' is-active' : ''}`}
                  aria-pressed={item.key === scene}
                  onClick={() => setScene(item.key)}
                >
                  {item.label}
                </button>
              ))}
            </nav>

            <div className="bigscreen-header-meta">
              <span className="bigscreen-live-status"><i className="bigscreen-live-dot" />系统在线</span>
              <span className="bigscreen-meta-item is-optional"><ApiOutlined />实时数据 <Switch size="small" checked={realData} onChange={setRealData} /></span>
              <span className="bigscreen-meta-item is-optional"><EnvironmentOutlined />28℃</span>
              <span className="bigscreen-meta-item is-optional">{dateStr}</span>
              <strong className="bigscreen-clock">{clockStr}</strong>
            </div>
          </header>

          <section className="bigscreen-kpi-band" aria-label="核心运行指标">
            {(compactScreen ? kpiData.slice(0, 4) : kpiData).map((item) => <KpiCard key={item.label} item={item} />)}
          </section>

          <section className="bigscreen-body">
            <aside className="bigscreen-column bigscreen-column-left">
              <Panel title="能耗与基线" eyebrow="ENERGY BASELINE" extra="近 7 日">
                <ChartFrame><ReactECharts option={compareOpt()} style={{ width: '100%', height: '100%' }} opts={{ renderer: 'canvas' }} notMerge /></ChartFrame>
              </Panel>

              {!compactScreen && (
                <Panel title="月度节能趋势" eyebrow="MONTHLY SAVING" extra="MWh / %">
                  <ChartFrame><ReactECharts option={monthOpt()} style={{ width: '100%', height: '100%' }} opts={{ renderer: 'canvas' }} notMerge /></ChartFrame>
                </Panel>
              )}

              {!compactScreen && (
                <Panel title="设备能耗构成" eyebrow="ENERGY MIX" extra="本月">
                  <ChartFrame><ReactECharts option={composeOpt()} style={{ width: '100%', height: '100%' }} opts={{ renderer: 'canvas' }} notMerge /></ChartFrame>
                </Panel>
              )}
            </aside>

            <section className="bigscreen-main">
              <Panel
                title={`${activeScene.label} · 冷站运行主视图`}
                eyebrow="PLANT SYSTEM VIEW"
                extra={<span>刷新 {clockStr}</span>}
                accent
                className="bigscreen-system-panel"
              >
                <div className="bigscreen-system-meta">
                  <span><BulbOutlined /> 运行策略 <strong className="is-accent">节能优先</strong></span>
                  <span>室外温度 <strong>28℃</strong></span>
                  <span>供 / 回水 <strong className="is-accent">7℃ / 12℃</strong></span>
                  <span>当前负载 <strong>{liveLoad}%</strong></span>
                </div>

                <div className="bigscreen-system-canvas" data-testid="bigscreen-system-canvas">
                  <Suspense fallback={<Spinner />}>
                    <ErrorBoundary fallback={<div style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', flex: 1, color: '#718199', fontSize: 11, textAlign: 'center', padding: 20 }}>当前环境不支持 3D 渲染<br />驾驶舱其余数据正常显示</div>}>
                      <System3D
                        cop={liveCop}
                        chillerRun={2}
                        chillerTotal={2}
                        towerRun={2}
                        towerTotal={2}
                        pumpRun={3}
                        pumpTotal={3}
                        load={liveLoad}
                        style={{ position: 'absolute', inset: 0 }}
                      />
                    </ErrorBoundary>
                  </Suspense>
                  <span className="bigscreen-canvas-mode"><SafetyCertificateOutlined />固定等轴视图 · 可拖拽检查</span>
                </div>

                <DeviceStatusRail items={deviceStats} />

                <div className="bigscreen-system-legend">
                  <span className="bigscreen-legend-item"><i className="bigscreen-legend-line" style={{ background: ACCENT }} />冷冻水供水</span>
                  <span className="bigscreen-legend-item"><i className="bigscreen-legend-line" style={{ background: ACCENT_DK }} />冷冻水回水</span>
                  <span className="bigscreen-legend-item"><i className="bigscreen-legend-line" style={{ background: GREEN }} />冷却水回路</span>
                  <span className="bigscreen-legend-note">只读展示 · 策略执行需审批</span>
                </div>
              </Panel>
            </section>

            <aside className="bigscreen-column bigscreen-column-right">
              <Panel title="资产健康" eyebrow="ASSET HEALTH" extra="综合评分">
                <HealthSummary items={HEALTH_SCORES} />
              </Panel>

              <Panel title="异常诊断" eyebrow="ACTIVE DIAGNOSTICS" extra={`${highRiskFdd} 条高风险`}>
                <div className="bigscreen-alarm-list">
                  {DIAGNOSTICS.map((item) => <AlarmRow key={item.title} item={item} />)}
                </div>
              </Panel>

              {!compactScreen && (
                <Panel title="告警闭环" eyebrow="ALARM LOOP" extra="今日">
                  <AlarmProgressSummary
                    total={ALARM_PROGRESS.total}
                    resolved={ALARM_PROGRESS.resolved}
                    active={activeTickets}
                    overdue={ALARM_PROGRESS.overdue}
                  />
                </Panel>
              )}

              {!compactScreen && (
                <Panel title="建议优化动作" eyebrow="RECOMMENDED ACTIONS" extra="待评审">
                  <div className="bigscreen-suggestion-list">
                    {SUGGESTIONS.map((text) => (
                      <div key={text} className="bigscreen-suggestion-item">
                        <span>{text}</span>
                        <strong>待评审</strong>
                      </div>
                    ))}
                  </div>
                </Panel>
              )}
            </aside>
          </section>

          <footer className="bigscreen-footer">
            <span className="bigscreen-footer-item"><i className="bigscreen-live-dot" />数据更新时间 <strong>{clockStr}</strong></span>
            <span className="bigscreen-footer-item"><ThunderboltOutlined />设备在线 <strong>{runningDevices}/{totalDevices}</strong></span>
            <span className="bigscreen-footer-item"><SafetyCertificateOutlined />高风险诊断 <strong>{highRiskFdd}</strong></span>
            <span className="bigscreen-footer-item"><ApiOutlined />遥测延迟 <strong>{realData ? '1.2s' : '演示数据'}</strong></span>
            <span className="bigscreen-footer-item"><BulbOutlined />当前场景 <strong>{activeScene.label}</strong></span>
            <Button size="small" icon={<FullscreenExitOutlined />} className="bigscreen-exit-button" onClick={() => navigate('/dashboard')}>退出大屏</Button>
          </footer>
        </main>
      </div>
    </ConfigProvider>
  );
}
