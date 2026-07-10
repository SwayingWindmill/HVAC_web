import { lazy, Suspense, useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Button, ConfigProvider, Switch, Typography, theme as antdTheme } from 'antd';
import {
  ApiOutlined,
  BulbOutlined,
  DesktopOutlined,
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
import { alarmGaugeOpt, compareOpt, composeOpt, healthGaugeOpt, monthOpt } from './charts';
import { AlarmRow, DevCard, KpiCard, Panel, Spinner } from './components';
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
import { ACCENT, ACCENT_DK, AMBER, BG, DIM, GREEN, PANEL_BD, RADIUS, RED, TEXT, hexA } from './theme';

const System3D = lazy(() => import('./System3D'));

export default function BigScreen() {
  const navigate = useNavigate();
  const suggestions = useOps((state) => state.suggestions);
  const workOrders = useOps((state) => state.workOrders);

  const [scene, setScene] = useState<SceneKey>('overview');
  const [compactScreen, setCompactScreen] = useState(() => window.innerWidth < 1200);
  const [cursorHidden, setCursorHidden] = useState(false);
  const [clock, setClock] = useState(() => new Date());
  const [realData, setRealData] = useState(false);

  useEffect(() => {
    const id = setInterval(() => setClock(new Date()), 1000);
    return () => clearInterval(id);
  }, []);

  useEffect(() => {
    const onResize = () => setCompactScreen(window.innerWidth < 1200);
    window.addEventListener('resize', onResize);
    return () => window.removeEventListener('resize', onResize);
  }, []);

  useEffect(() => {
    const id = window.setInterval(() => {
      setScene((current) => {
        const index = SCENES.findIndex((item) => item.key === current);
        return SCENES[(index + 1) % SCENES.length].key;
      });
    }, 15000);
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
    let timer: number;
    const reset = () => {
      setCursorHidden(false);
      clearTimeout(timer);
      timer = window.setTimeout(() => setCursorHidden(true), 3000);
    };
    document.addEventListener('mousemove', reset);
    document.addEventListener('keydown', reset);
    timer = window.setTimeout(() => setCursorHidden(true), 3000);
    return () => {
      document.removeEventListener('mousemove', reset);
      document.removeEventListener('keydown', reset);
      clearTimeout(timer);
    };
  }, []);

  const clockStr = clock.toLocaleTimeString('zh-CN', { hour12: false });
  const dateStr = clock.toLocaleDateString('zh-CN', { year: 'numeric', month: 'long', day: 'numeric', weekday: 'long' });

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
  const kpiData = KPI_DATA.map((item) => {
    if (item.label === '累计节省电费') return { ...item, value: item.value + approvedSaving };
    if (item.label === '冷站综合COP') return { ...item, value: liveCop };
    if (item.label === '系统在线率') return { ...item, value: highRiskFdd > 0 ? 98.9 : item.value };
    return item;
  });

  return (
    <ConfigProvider theme={{ algorithm: antdTheme.darkAlgorithm, token: { colorPrimary: ACCENT, borderRadius: RADIUS } }}>
      <div
        style={{
          position: 'fixed', inset: 0, background: BG,
          cursor: cursorHidden ? 'none' : 'default', display: 'flex', alignItems: 'center', justifyContent: 'center',
          overflow: 'hidden',
        }}
      >
        <div
          style={{
            width: 'min(100vw, calc(100vh * 16 / 9))',
            height: 'min(100vh, calc(100vw * 9 / 16))',
            color: TEXT,
            fontFamily: 'system-ui, -apple-system, "PingFang SC", "Microsoft YaHei", sans-serif',
            display: 'flex',
            flexDirection: 'column',
            overflow: 'hidden',
            background: BG,
            boxShadow: `0 0 60px ${hexA('#000000', 0.55)}`,
          }}
        >
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', padding: '10px 20px', flexShrink: 0 }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
            <DesktopOutlined style={{ fontSize: 20, color: ACCENT }} />
            <div>
              <Typography.Text style={{ color: TEXT, fontSize: 18, fontWeight: 700, letterSpacing: 0.5 }}>
                商业建筑智慧能源驾驶舱
              </Typography.Text>
              <div style={{ color: DIM, fontSize: 11, marginTop: 2 }}>{activeScene.subtitle}</div>
            </div>
          </div>
          <div style={{ display: 'flex', alignItems: 'center', gap: 16, color: DIM, fontSize: 13 }}>
            <span style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 12 }}>
              {SCENES.map((item) => (
                <button
                  key={item.key}
                  type="button"
                  onClick={() => setScene(item.key)}
                  style={{
                    cursor: 'pointer',
                    color: item.key === scene ? TEXT : DIM,
                    background: item.key === scene ? hexA(ACCENT, 0.18) : 'transparent',
                    border: `1px solid ${item.key === scene ? hexA(ACCENT, 0.5) : hexA(PANEL_BD, 0.8)}`,
                    borderRadius: 999,
                    padding: '2px 9px',
                    fontSize: 11,
                  }}
                >
                  {item.label}
                </button>
              ))}
            </span>
            <span style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 12 }}>
              <ApiOutlined style={{ color: realData ? ACCENT : undefined }} />
              实时数据
              <Switch size="small" checked={realData} onChange={setRealData} style={{ background: realData ? ACCENT : undefined }} />
            </span>
            {!compactScreen && <span style={{ display: 'flex', alignItems: 'center', gap: 4 }}><EnvironmentOutlined /> 28℃</span>}
            {!compactScreen && <span style={{ fontVariantNumeric: 'tabular-nums' }}>{dateStr}</span>}
            <span style={{ color: ACCENT, fontVariantNumeric: 'tabular-nums', fontWeight: 600 }}>{clockStr}</span>
          </div>
        </div>

        <div style={{ display: 'flex', gap: 10, padding: '0 20px 10px', flexShrink: 0 }}>
          {(compactScreen ? kpiData.slice(0, 4) : kpiData).map((item) => <KpiCard key={item.label} item={item} />)}
        </div>

        <div style={{ flex: 1, minHeight: 0, display: 'flex', gap: 10, padding: '0 20px 8px' }}>
          <div style={{ flex: compactScreen ? '0 0 20%' : '0 0 22%', minWidth: compactScreen ? 180 : 200, maxWidth: 300, display: 'flex', flexDirection: 'column', gap: 8 }}>
            <Panel title="能耗对比" style={{ flex: 0, minHeight: 0 }}>
              <ReactECharts option={compareOpt()} style={{ flex: 1, minHeight: 140 }} opts={{ renderer: 'canvas' }} notMerge />
              <div style={{ display: 'flex', gap: 16, marginTop: 6, paddingTop: 8, borderTop: `1px solid ${hexA(PANEL_BD, 0.4)}` }}>
                <div>
                  <span style={{ color: DIM, fontSize: 10 }}>今日节电量</span><br />
                  <span style={{ color: ACCENT, fontSize: 16, fontWeight: 700, fontVariantNumeric: 'tabular-nums' }}>12,860</span>
                  <span style={{ color: DIM, fontSize: 10 }}> kWh</span>
                </div>
                <div>
                  <span style={{ color: DIM, fontSize: 10 }}>节能率</span><br />
                  <span style={{ color: ACCENT, fontSize: 16, fontWeight: 700, fontVariantNumeric: 'tabular-nums' }}>18.6%</span>
                </div>
              </div>
            </Panel>

            {!compactScreen && (
              <Panel title="月度节能趋势" style={{ flex: 1, minHeight: 0 }}>
                <ReactECharts option={monthOpt()} style={{ flex: 1, minHeight: 180 }} opts={{ renderer: 'canvas' }} notMerge />
              </Panel>
            )}

            {!compactScreen && (
              <Panel title="设备能耗构成（本月）" style={{ flex: 1, minHeight: 0 }}>
                <ReactECharts option={composeOpt()} style={{ flex: 1, minHeight: 170 }} opts={{ renderer: 'canvas' }} notMerge />
              </Panel>
            )}
          </div>

          <div style={{ flex: 1, minWidth: 0, display: 'flex', flexDirection: 'column' }}>
            <Panel title={`${activeScene.label} · 冷站系统总览`} accent style={{ flex: 1, minHeight: 0, position: 'relative', overflow: 'hidden', padding: 0 }}>
              <div style={{
                display: 'flex', alignItems: 'center', gap: 16, padding: '10px 15px',
                borderBottom: `1px solid ${hexA(PANEL_BD, 0.5)}`,
                background: 'linear-gradient(to right, rgba(15,181,174,0.04), transparent)',
                zIndex: 2, position: 'relative',
              }}>
                <span style={{ color: DIM, fontSize: 11, display: 'flex', alignItems: 'center', gap: 4 }}>
                  <BulbOutlined /> 运行模式 · <span style={{ color: ACCENT }}>节能优先</span>
                </span>
                <span style={{ color: DIM, fontSize: 11, display: 'flex', alignItems: 'center', gap: 4 }}>
                  <EnvironmentOutlined /> 室外温度：<span style={{ color: TEXT }}>28℃</span>
                </span>
                <span style={{ color: DIM, fontSize: 11 }}>
                  供回水温度：<span style={{ color: ACCENT }}>7℃</span> / <span style={{ color: AMBER }}>12℃</span>
                </span>
              </div>

              <div style={{ flex: 1, minHeight: 0, position: 'relative' }}>
                <Suspense fallback={<Spinner />}>
                  <ErrorBoundary fallback={<div style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', flex: 1, color: DIM, fontSize: 13, textAlign: 'center', padding: 20 }}>当前环境不支持 3D 渲染<br />（驾驶舱其余数据正常显示）</div>}>
                    <System3D
                      cop={liveCop} chillerRun={2} chillerTotal={2}
                      towerRun={2} towerTotal={2}
                      pumpRun={3} pumpTotal={3}
                      load={liveLoad}
                      style={{ position: 'absolute', inset: 0 }}
                    />
                  </ErrorBoundary>
                </Suspense>

                <DevCard d={deviceStats[0]} style={{ left: '1%', top: '18%' }} />
                <DevCard d={deviceStats[1]} style={{ right: '1%', top: '16%' }} />
                {!compactScreen && <DevCard d={deviceStats[2]} style={{ left: '1%', bottom: '20%' }} />}
                {!compactScreen && <DevCard d={deviceStats[3]} style={{ right: '8%', bottom: '18%' }} />}
                {!compactScreen && <DevCard d={deviceStats[4]} style={{ right: '1%', bottom: '16%' }} />}
              </div>

              <div style={{ display: 'flex', gap: 16, padding: '7px 15px', borderTop: `1px solid ${hexA(PANEL_BD, 0.4)}`, fontSize: 10, color: DIM, zIndex: 2, background: hexA(BG, 0.7) }}>
                <span style={{ display: 'flex', alignItems: 'center', gap: 4 }}><span style={{ width: 18, height: 2, background: ACCENT, display: 'inline-block' }} /> 冷冻水供水</span>
                <span style={{ display: 'flex', alignItems: 'center', gap: 4 }}><span style={{ width: 18, height: 2, background: ACCENT_DK, display: 'inline-block' }} /> 冷冻水回水</span>
                <span style={{ display: 'flex', alignItems: 'center', gap: 4 }}><span style={{ width: 18, height: 2, background: GREEN, display: 'inline-block' }} /> 冷却水供水</span>
                <span style={{ display: 'flex', alignItems: 'center', gap: 4 }}><span style={{ width: 18, height: 2, background: hexA(GREEN, 0.55), display: 'inline-block' }} /> 冷却水回水</span>
                <span style={{ display: 'flex', alignItems: 'center', gap: 4 }}><span style={{ width: 18, height: 2, background: hexA(DIM, 0.4), borderBottom: `1px dashed ${DIM}` }} /> 冷却塔进风</span>
                <span style={{ marginLeft: 'auto', color: ACCENT, fontSize: 11, display: 'flex', alignItems: 'center', gap: 3 }}>只读展示 · 策略需审批</span>
              </div>
            </Panel>
          </div>

          <div style={{ flex: compactScreen ? '0 0 20%' : '0 0 22%', minWidth: compactScreen ? 180 : 200, maxWidth: 300, display: 'flex', flexDirection: 'column', gap: 8 }}>
            <Panel title="设备健康评分" style={{ flex: 0, minHeight: 0 }}>
              <div style={{ display: 'flex', gap: 14 }}>
                <div style={{ width: 110 }}>
                  <ReactECharts option={healthGaugeOpt()} style={{ width: 110, height: 130 }} opts={{ renderer: 'canvas' }} notMerge />
                </div>
                <div style={{ flex: 1, display: 'flex', flexDirection: 'column', justifyContent: 'center', gap: 5 }}>
                  {HEALTH_SCORES.map((h) => (
                    <div key={h.label} style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', fontSize: 12 }}>
                      <span style={{ color: DIM }}>{h.label}</span>
                      <span style={{ color: h.value >= 92 ? ACCENT : h.value >= 89 ? AMBER : RED, fontWeight: 600, fontVariantNumeric: 'tabular-nums' }}>{h.value}</span>
                    </div>
                  ))}
                </div>
              </div>
            </Panel>

            <Panel title="异常诊断" style={{ flex: '0 0 auto' }}>
              {DIAGNOSTICS.map((item) => <AlarmRow key={item.title} a={item} />)}
            </Panel>

            {!compactScreen && (
              <Panel title="告警闭环进度" style={{ flex: 0, minHeight: 0 }}>
                <div style={{ display: 'flex', gap: 12 }}>
                  <div style={{ width: 100 }}>
                    <ReactECharts option={alarmGaugeOpt()} style={{ width: 100, height: 105 }} opts={{ renderer: 'canvas' }} notMerge />
                  </div>
                  <div style={{ flex: 1, display: 'flex', flexDirection: 'column', justifyContent: 'center', gap: 4 }}>
                    {[
                      { label: '告警总数', value: ALARM_PROGRESS.total },
                      { label: '待处理工单', value: activeTickets, color: activeTickets > 0 ? AMBER : GREEN },
                      { label: '已处理', value: ALARM_PROGRESS.resolved, color: GREEN },
                      { label: '超时未处理', value: ALARM_PROGRESS.overdue, color: RED },
                    ].map((s) => (
                      <div key={s.label} style={{ display: 'flex', justifyContent: 'space-between', fontSize: 11 }}>
                        <span style={{ color: DIM }}>{s.label}</span>
                        <span style={{ color: s.color ?? TEXT, fontWeight: 600, fontVariantNumeric: 'tabular-nums' }}>{s.value}</span>
                      </div>
                    ))}
                  </div>
                </div>
              </Panel>
            )}

            {!compactScreen && (
              <Panel title="建议优化动作" style={{ flex: 1, minHeight: 0 }}>
                <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
                  {SUGGESTIONS.map((text) => (
                    <div key={text} style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', padding: '6px 9px', borderRadius: 8, background: hexA(ACCENT, 0.07), border: `1px solid ${hexA(ACCENT, 0.15)}` }}>
                      <span style={{ color: TEXT, fontSize: 11, flex: 1 }}>{text}</span>
                      <Button size="small" type="primary" ghost style={{ height: 22, fontSize: 10, paddingInline: 10, marginLeft: 8 }}>待评审</Button>
                    </div>
                  ))}
                </div>
              </Panel>
            )}
          </div>
        </div>

        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 24, padding: '6px 20px', flexShrink: 0, color: DIM, fontSize: 11, borderTop: `1px solid ${hexA(PANEL_BD, 0.3)}` }}>
          <span style={{ display: 'flex', alignItems: 'center', gap: 4 }}><ThunderboltOutlined /> 智慧能源</span>
          <span style={{ display: 'flex', alignItems: 'center', gap: 4 }}><ApiOutlined /> 精益运行</span>
          <span style={{ display: 'flex', alignItems: 'center', gap: 4 }}><SafetyCertificateOutlined /> 绿色低碳</span>
          <span style={{ display: 'flex', alignItems: 'center', gap: 4 }}><BulbOutlined /> 价值创造</span>
          <Button size="small" ghost icon={<FullscreenExitOutlined />} onClick={() => navigate('/dashboard')} style={{ marginLeft: 'auto', color: DIM, borderColor: hexA(PANEL_BD, 0.5), height: 24 }}>
            退出大屏
          </Button>
        </div>
        </div>
      </div>
    </ConfigProvider>
  );
}
