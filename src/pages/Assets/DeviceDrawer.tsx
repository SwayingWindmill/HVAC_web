import { Button, Descriptions, Drawer, Empty, Progress, Spin, Tag, Typography } from 'antd';
import {
  ApiOutlined,
  DashboardOutlined,
  InfoCircleOutlined,
  LineChartOutlined,
  SafetyCertificateOutlined,
} from '@ant-design/icons';
import ReactECharts from 'echarts-for-react';
import { useTelemetryLive, useTimeseries } from '@/api';
import {
  OperationsActionFooter,
  OperationsDetailHeader,
  OperationsDetailSection,
  OperationsSummaryStrip,
} from '@/components/OperationsUI';
import { BRAND } from '@/theme/tokens';
import { DEVICE_META, STATUS_INFO, STATUS_MAP, TYPE_LABEL } from './meta';

const LIVE_KEYS = ['supplyTemp', 'returnTemp', 'power', 'cop', 'load', 'flow'];

interface DeviceDrawerProps {
  deviceId: string | null;
  onClose: () => void;
  onAfterClose?: () => void;
}

export default function DeviceDrawer({ deviceId, onClose, onAfterClose }: DeviceDrawerProps) {
  const { get } = useTelemetryLive(deviceId ? [deviceId] : [], LIVE_KEYS);
  const { data: tsPower, isLoading: tsLoading } = useTimeseries(deviceId ?? 'none', ['power'], '24h', !!deviceId);

  const meta = deviceId ? DEVICE_META[deviceId] : undefined;
  const status = deviceId ? STATUS_MAP[deviceId] : undefined;
  const statusInfo = status ? STATUS_INFO[status] : undefined;
  const pointRate = meta ? Math.round((meta.onlinePoints / meta.pointCount) * 100) : 0;
  const power = deviceId ? get(deviceId, 'power') ?? 0 : 0;
  const cop = deviceId ? get(deviceId, 'cop') ?? 0 : 0;
  const load = deviceId ? get(deviceId, 'load') ?? 0 : 0;
  const flow = deviceId ? get(deviceId, 'flow') ?? 0 : 0;
  const supply = deviceId ? get(deviceId, 'supplyTemp') ?? 0 : 0;
  const returnTemperature = deviceId ? get(deviceId, 'returnTemp') ?? 0 : 0;
  const deltaTemperature = Math.round((returnTemperature - supply) * 10) / 10;
  const powerPoints = tsPower?.power ?? [];
  const closeDrawer = () => {
    onClose();
    onAfterClose?.();
  };

  const sparkOption = {
    aria: {
      enabled: true,
      description: `${meta?.name ?? '设备'}近 24 小时功率趋势，单位为千瓦。`,
    },
    grid: { left: 48, right: 14, top: 18, bottom: 28 },
    tooltip: { trigger: 'axis' as const },
    xAxis: {
      type: 'time' as const,
      axisLine: { lineStyle: { color: '#94a3b8' } },
      axisLabel: { color: '#64748b', fontSize: 11 },
    },
    yAxis: {
      type: 'value' as const,
      name: 'kW',
      nameTextStyle: { color: '#64748b', fontSize: 11 },
      splitLine: { lineStyle: { color: 'rgba(100,116,139,0.16)' } },
      axisLabel: { color: '#64748b', fontSize: 11 },
    },
    series: [
      {
        type: 'line' as const,
        smooth: true,
        showSymbol: false,
        data: powerPoints.map((point) => [point.ts, point.value]),
        lineStyle: { color: BRAND.teal, width: 2 },
        areaStyle: { color: 'rgba(15,181,174,0.10)' },
      },
    ],
  };

  return (
    <Drawer
      rootClassName="ops-detail-drawer"
      width={720}
      open={Boolean(deviceId)}
      onClose={closeDrawer}
      afterOpenChange={(open) => {
        if (!open) onAfterClose?.();
      }}
      title={meta ? (
        <OperationsDetailHeader
          eyebrow={`设备资产 · ${TYPE_LABEL[meta.type]}`}
          title={meta.name}
          subtitle={`${meta.buildingName} / ${meta.floor} / ${meta.zoneName}`}
          status={statusInfo ? <Tag color={statusInfo.color}>{statusInfo.label}</Tag> : null}
          meta={<Typography.Text code>{deviceId}</Typography.Text>}
        />
      ) : '设备详情'}
      footer={meta ? (
        <OperationsActionFooter note="只读监测视图；设备控制和参数写入必须进入受权限保护的业务流程。">
          <Button onClick={closeDrawer}>关闭</Button>
        </OperationsActionFooter>
      ) : null}
    >
      {!meta || !deviceId ? (
        <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="未选择设备" />
      ) : (
        <div className="ops-detail-stack">
          <OperationsSummaryStrip
            ariaLabel="设备实时运行摘要"
            items={[
              { label: '实时功率', value: power, suffix: 'kW', tone: 'accent' },
              { label: '综合 COP', value: Number(cop).toFixed(2), tone: cop < 4.5 ? 'warning' : 'positive' },
              { label: '当前负荷', value: load, suffix: '%' },
              { label: '点位在线', value: pointRate, suffix: '%', tone: pointRate < 95 ? 'critical' : 'positive' },
            ]}
          />

          <OperationsDetailSection
            title="资产身份"
            icon={<InfoCircleOutlined />}
            description="设备身份、安装信息与责任归属。"
          >
            <Descriptions column={{ xs: 1, sm: 2 }} size="small" colon={false}>
              <Descriptions.Item label="设备 ID"><Typography.Text code>{deviceId}</Typography.Text></Descriptions.Item>
              <Descriptions.Item label="类型">{TYPE_LABEL[meta.type]}</Descriptions.Item>
              <Descriptions.Item label="厂家 / 型号">{meta.manufacturer} · {meta.model}</Descriptions.Item>
              <Descriptions.Item label="安装日期">{meta.installedAt}</Descriptions.Item>
              <Descriptions.Item label="维护负责人">{meta.maintainer}</Descriptions.Item>
              <Descriptions.Item label="位置">{meta.floor} / {meta.zoneName}</Descriptions.Item>
            </Descriptions>
          </OperationsDetailSection>

          <OperationsDetailSection
            title="通讯与点位"
            icon={<ApiOutlined />}
            description="网关、协议和最近通讯状态。"
            extra={`${meta.onlinePoints} / ${meta.pointCount} 点在线`}
          >
            <Descriptions column={{ xs: 1, sm: 2 }} size="small" colon={false}>
              <Descriptions.Item label="协议">{meta.protocol}</Descriptions.Item>
              <Descriptions.Item label="网关">{meta.gateway}</Descriptions.Item>
              <Descriptions.Item label="最后通讯">{meta.lastSeen}</Descriptions.Item>
            </Descriptions>
            <div style={{ marginTop: 12 }}>
              <Progress percent={pointRate} size="small" status={pointRate < 95 ? 'exception' : 'active'} />
            </div>
          </OperationsDetailSection>

          <OperationsDetailSection
            title="运行工况"
            icon={<DashboardOutlined />}
            description="实时水系统与设备额定参数。"
          >
            <div className="ops-detail-definition-grid">
              <div className="ops-detail-definition-row">
                <span className="ops-detail-definition-label">额定功率</span>
                <span className="ops-detail-definition-value">{meta.ratedPower} kW</span>
              </div>
              <div className="ops-detail-definition-row">
                <span className="ops-detail-definition-label">额定制冷量</span>
                <span className="ops-detail-definition-value">{meta.ratedCooling ? `${meta.ratedCooling} kW` : '—'}</span>
              </div>
              <div className="ops-detail-definition-row">
                <span className="ops-detail-definition-label">实时流量</span>
                <span className="ops-detail-definition-value">{flow} m³/h</span>
              </div>
              <div className="ops-detail-definition-row">
                <span className="ops-detail-definition-label">供回水温差</span>
                <span className="ops-detail-definition-value">{deltaTemperature} ℃</span>
              </div>
              <div className="ops-detail-definition-row">
                <span className="ops-detail-definition-label">供水温度</span>
                <span className="ops-detail-definition-value">{supply} ℃</span>
              </div>
              <div className="ops-detail-definition-row">
                <span className="ops-detail-definition-label">回水温度</span>
                <span className="ops-detail-definition-value">{returnTemperature} ℃</span>
              </div>
            </div>
          </OperationsDetailSection>

          <OperationsDetailSection
            title="近 24 小时功率"
            icon={<LineChartOutlined />}
            description="用于识别异常负荷、频繁启停和非营业时段运行。"
            extra={<span className="ops-chart-status is-positive">实时遥测</span>}
          >
            <div style={{ height: 240 }} aria-label={`${meta.name}近 24 小时功率趋势`}>
              {tsLoading ? (
                <div className="ops-chart-state"><Spin /></div>
              ) : powerPoints.length === 0 ? (
                <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无功率趋势" />
              ) : (
                <ReactECharts option={sparkOption} style={{ height: '100%' }} notMerge lazyUpdate />
              )}
            </div>
          </OperationsDetailSection>

          {pointRate < 95 ? (
            <OperationsDetailSection title="数据质量提示" icon={<SafetyCertificateOutlined />}>
              <div className="ops-detail-callout is-warning">
                当前点位在线率低于 95%。分析能效、故障或优化建议前，应先确认离线点位是否影响关键测量。
              </div>
            </OperationsDetailSection>
          ) : null}
        </div>
      )}
    </Drawer>
  );
}
