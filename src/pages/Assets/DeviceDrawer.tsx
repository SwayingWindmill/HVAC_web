import { Drawer, Descriptions, Statistic, Tag, Typography, Empty } from 'antd';
import ReactECharts from 'echarts-for-react';
import { useTelemetryLive, useTimeseries } from '@/api';
import { BRAND } from '@/theme/tokens';
import { DEVICE_META, STATUS_INFO, STATUS_MAP, TYPE_LABEL } from './meta';

const LIVE_KEYS = ['supplyTemp', 'returnTemp', 'power', 'cop', 'load', 'flow'];

interface DeviceDrawerProps {
  deviceId: string | null;
  onClose: () => void;
}

export default function DeviceDrawer({ deviceId, onClose }: DeviceDrawerProps) {
  const { get } = useTelemetryLive(deviceId ? [deviceId] : [], LIVE_KEYS);
  const { data: tsPower, isLoading: tsLoading } = useTimeseries(deviceId ?? 'none', ['power'], '24h', !!deviceId);

  const meta = deviceId ? DEVICE_META[deviceId] : undefined;
  const status = deviceId ? STATUS_MAP[deviceId] : undefined;
  const statusInfo = status ? STATUS_INFO[status] : undefined;

  const sparkOption = {
    grid: { left: 44, right: 12, top: 16, bottom: 24 },
    tooltip: { trigger: 'axis' as const },
    xAxis: {
      type: 'time' as const,
      axisLine: { lineStyle: { color: '#d9d9d9' } },
      axisLabel: { color: '#8c8c8c', fontSize: 11 },
    },
    yAxis: {
      type: 'value' as const,
      name: 'kW',
      nameTextStyle: { color: '#8c8c8c', fontSize: 11 },
      splitLine: { lineStyle: { color: '#f0f0f0' } },
      axisLabel: { color: '#8c8c8c', fontSize: 11 },
    },
    series: [
      {
        type: 'line' as const,
        smooth: true,
        showSymbol: false,
        data: (tsPower?.power ?? []).map((p) => [p.ts, p.value]),
        lineStyle: { color: BRAND.teal, width: 2 },
        areaStyle: { color: 'rgba(15,181,174,0.12)' },
      },
    ],
  };

  return (
    <Drawer
      width={420}
      open={!!deviceId}
      onClose={onClose}
      title={
        meta ? (
          <span style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
            {meta.name}
            {statusInfo && <Tag color={statusInfo.color}>{statusInfo.label}</Tag>}
          </span>
        ) : (
          '设备详情'
        )
      }
    >
      {!meta ? (
        <Empty description="未选择设备" />
      ) : (
        <>
          <Descriptions column={1} size="small" style={{ marginBottom: 16 }}>
            <Descriptions.Item label="类型">{TYPE_LABEL[meta.type]}</Descriptions.Item>
            <Descriptions.Item label="所属分区">
              {meta.zoneName}（{meta.zone}）
            </Descriptions.Item>
            <Descriptions.Item label="设备 ID">{deviceId}</Descriptions.Item>
          </Descriptions>

          <div
            style={{
              display: 'grid',
              gridTemplateColumns: 'repeat(2, 1fr)',
              gap: 12,
              marginBottom: 16,
            }}
          >
            <Statistic title="实时功率" value={get(deviceId!, 'power') ?? 0} suffix="kW" valueStyle={{ color: BRAND.tealStrong }} />
            <Statistic title="COP" value={get(deviceId!, 'cop') ?? 0} precision={2} />
            <Statistic title="负荷率" value={get(deviceId!, 'load') ?? 0} suffix="%" />
            <Statistic title="流量" value={get(deviceId!, 'flow') ?? 0} suffix="m³/h" />
            <Statistic title="供水温度" value={get(deviceId!, 'supplyTemp') ?? 0} suffix="℃" />
            <Statistic title="回水温度" value={get(deviceId!, 'returnTemp') ?? 0} suffix="℃" />
          </div>

          <Typography.Text type="secondary" style={{ fontSize: 13 }}>
            功率趋势（近 24h）
          </Typography.Text>
          <div style={{ marginTop: 8, height: 200 }}>
            {tsLoading ? (
              <Empty description="加载中" />
            ) : (
              <ReactECharts option={sparkOption} style={{ height: 200 }} notMerge lazyUpdate />
            )}
          </div>
        </>
      )}
    </Drawer>
  );
}
