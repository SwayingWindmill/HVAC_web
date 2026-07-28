import { Alert, Card, Col, Descriptions, Row, Space, Table, Tag, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import type {
  DeviceObservationSnapshot,
  DeviceDisplayState,
  TelemetryKeyState,
} from '@/api/generated/s2Telemetry.gen';
import type { DeviceLiveResult, PresenceBatchItem } from '@/api/telemetry-current';
import { presentTelemetryError } from '@/api/telemetry-current';
import {
  buildDeviceTelemetryHighlights,
  formatTelemetryDisplayValue,
  formatTelemetryUnit,
  getDeviceTelemetryProfile,
  telemetryPointDefinition,
} from '@/domain/centralPlantTelemetry';
import { LoadingState } from './PageState';

const displayLabels: Record<Exclude<DeviceDisplayState, null>, string> = {
  ONLINE: 'ONLINE',
  OFFLINE: 'OFFLINE',
  STALE: 'STALE',
  UNKNOWN: 'UNKNOWN',
  UNAVAILABLE: 'UNAVAILABLE',
};

const displayColors: Record<Exclude<DeviceDisplayState, null>, string> = {
  ONLINE: 'green',
  OFFLINE: 'default',
  STALE: 'orange',
  UNKNOWN: 'gold',
  UNAVAILABLE: 'red',
};

function snapshotDisplay(snapshot: DeviceObservationSnapshot): Exclude<DeviceDisplayState, null> {
  if (snapshot.evaluationAvailability === 'UNAVAILABLE') return 'UNAVAILABLE';
  return snapshot.displayState ?? 'UNKNOWN';
}

function formatInstant(value: string | null | undefined): string {
  if (!value) return '—';
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? value : parsed.toLocaleString('zh-CN', { hour12: false });
}

export function DevicePresenceCell({ item, pending }: { item?: PresenceBatchItem; pending: boolean }) {
  if (pending && !item) return <Typography.Text type="secondary">读取 Presence…</Typography.Text>;
  if (!item) return <Tag>UNKNOWN</Tag>;
  if (item.status === 'error') {
    return (
      <Space direction="vertical" size={0} data-device-display-state="UNAVAILABLE">
        <Tag color="red">UNAVAILABLE</Tag>
        <Typography.Text type="secondary">{item.problem.code}</Typography.Text>
      </Space>
    );
  }
  const display = snapshotDisplay(item.snapshot);
  const lastSeen = item.snapshot.presence.lastSeenAt ?? item.snapshot.presence.lastKnown?.lastSeenAt;
  return (
    <Space direction="vertical" size={0} data-device-display-state={display}>
      <Space size={4}>
        <Tag color={displayColors[display]}>{displayLabels[display]}</Tag>
        {item.snapshot.telemetryReadiness !== 'CURRENT' ? <Tag color="orange">{item.snapshot.telemetryReadiness}</Tag> : null}
      </Space>
      <Typography.Text type="secondary">Last seen {formatInstant(lastSeen)}</Typography.Text>
    </Space>
  );
}

interface TelemetryRow {
  key: string;
  label: string;
  value: string;
  unit: string;
  freshness: string;
  quality: string;
  sampledAt: string;
  reasons: string;
  missing: boolean;
}

function telemetryRows(snapshot: DeviceObservationSnapshot): TelemetryRow[] {
  return snapshot.values.map((state: TelemetryKeyState) => {
    const definition = telemetryPointDefinition(state.key);
    if (state.state === 'MISSING') {
      return {
        key: state.key,
        label: definition.label,
        value: 'MISSING（不补零）',
        unit: '—',
        freshness: 'MISSING',
        quality: '—',
        sampledAt: '—',
        reasons: state.missingReason,
        missing: true,
      };
    }
    return {
      key: state.key,
      label: definition.label,
      value: formatTelemetryDisplayValue(state.value, definition.precision),
      unit: formatTelemetryUnit(state.unit ?? definition.defaultUnit) ?? '—',
      freshness: state.freshness,
      quality: state.quality,
      sampledAt: formatInstant(state.sampledAt),
      reasons: state.qualityReasons.join(', ') || '—',
      missing: false,
    };
  });
}

const telemetryColumns: ColumnsType<TelemetryRow> = [
  {
    title: '点位', dataIndex: 'label', key: 'label', width: 190,
    render: (value: string, row) => (
      <Space direction="vertical" size={0}>
        <Typography.Text strong>{value}</Typography.Text>
        <Typography.Text code type="secondary">{row.key}</Typography.Text>
      </Space>
    ),
  },
  {
    title: 'Last Known / Current value', dataIndex: 'value', key: 'value', width: 200,
    render: (value: string, row) => row.missing
      ? <Typography.Text type="secondary">{value}</Typography.Text>
      : <Typography.Text strong>{value} {row.unit === '—' ? '' : row.unit}</Typography.Text>,
  },
  {
    title: 'Freshness', dataIndex: 'freshness', key: 'freshness', width: 110,
    render: (value: string) => <Tag color={value === 'FRESH' ? 'green' : value === 'STALE' ? 'orange' : 'default'}>{value}</Tag>,
  },
  {
    title: 'Quality', dataIndex: 'quality', key: 'quality', width: 110,
    render: (value: string) => value === '—' ? '—' : <Tag color={value === 'GOOD' ? 'green' : 'red'}>{value}</Tag>,
  },
  { title: '原 sampledAt', dataIndex: 'sampledAt', key: 'sampledAt', width: 190 },
  { title: 'Reason', dataIndex: 'reasons', key: 'reasons', width: 220 },
];

function DeviceTelemetryHighlights({ deviceType, snapshot }: { deviceType?: string | null; snapshot: DeviceObservationSnapshot }) {
  const profile = getDeviceTelemetryProfile(deviceType);
  if (profile.kind === 'GENERIC') return null;
  const highlights = buildDeviceTelemetryHighlights(deviceType, snapshot);
  return (
    <Card
      size="small"
      title={`${profile.title}实时运行摘要`}
      data-central-plant-profile={profile.kind}
      styles={{ body: { padding: 12 } }}
    >
      <Row gutter={[10, 10]}>
        {highlights.map((item) => (
          <Col xs={12} md={6} key={item.key}>
            <Space direction="vertical" size={2} style={{ width: '100%' }}>
              <Typography.Text type="secondary">{item.label}</Typography.Text>
              {item.state === 'MISSING' ? (
                <Typography.Text type="secondary">MISSING（不补零）</Typography.Text>
              ) : (
                <Typography.Text strong style={{ fontSize: 18 }}>
                  {item.displayValue}{item.unit ? ` ${item.unit}` : ''}
                </Typography.Text>
              )}
              <Space size={4} wrap>
                <Tag color={item.freshness === 'FRESH' ? 'green' : item.freshness === 'STALE' ? 'orange' : 'default'}>
                  {item.freshness}
                </Tag>
                {item.quality ? <Tag color={item.quality === 'GOOD' ? 'green' : 'red'}>{item.quality}</Tag> : null}
              </Space>
            </Space>
          </Col>
        ))}
      </Row>
    </Card>
  );
}

function TransportState({ result }: { result: DeviceLiveResult }) {
  const state = result.state;
  if (!state) return null;
  if (state.status === 'snapshot') {
    return (
      <Alert
        type="warning"
        showIcon
        message={state.reason === 'reconnecting' ? '实时 transport 已降级，正在恢复' : '正在安装或恢复权威 Snapshot'}
        description="当前仅展示同一权威 Snapshot，不会混合尚未连续应用的 publication。"
        data-transport-state="degraded"
      />
    );
  }
  if (state.status === 'unavailable') {
    return (
      <Alert
        type="warning"
        showIcon
        message={state.reason === 'transport-unavailable' ? '实时 transport 暂不可用' : '实时状态需要重新同步'}
        description={state.retryable ? '保留最后仍获授权的 Snapshot，并重新读取权威状态。' : '当前状态不可重试。'}
        data-transport-state="unavailable"
      />
    );
  }
  return null;
}

export function DeviceTelemetryPanel({ result, deviceType }: { result: DeviceLiveResult; deviceType?: string | null }) {
  if (result.pending && !result.state) return <LoadingState tip="正在读取 exact-key Snapshot 并建立实时订阅" minHeight={180} />;
  if (result.error) {
    const error = presentTelemetryError(result.error);
    return <Alert type="error" showIcon message={error.title} description={error.description} data-device-live-state="error" />;
  }
  const state = result.state;
  if (!state) return <Typography.Text type="secondary">尚未选择 Device。</Typography.Text>;
  if (state.status === 'revoked') {
    return (
      <Alert
        type="error"
        showIcon
        message="Device 已撤权或不再可见"
        description="页面状态和浏览器 Last Known 已清除，不会继续展示旧值。"
        data-device-live-state="revoked"
      />
    );
  }
  if (state.status === 'initializing' || !state.snapshot) {
    return <LoadingState tip="正在建立设备状态" minHeight={180} />;
  }

  const snapshot = state.snapshot;
  const display = snapshotDisplay(snapshot);
  const rows = telemetryRows(snapshot);
  const lastKnownPresence = snapshot.presence.lastKnown;
  return (
    <Space direction="vertical" size={12} style={{ width: '100%' }} data-device-live-state={state.status} data-device-display-state={display}>
      <TransportState result={result} />
      {snapshot.evaluationAvailability === 'UNAVAILABLE' ? (
        <Alert
          type="error"
          showIcon
          message="Platform current-state UNAVAILABLE"
          description={snapshot.availabilityReasons.join(', ') || '权威 evaluator 当前无法给出可用状态。'}
          data-platform-availability="UNAVAILABLE"
        />
      ) : null}
      <Descriptions column={{ xs: 1, sm: 2 }} size="small" colon={false}>
        <Descriptions.Item label="Display State"><Tag color={displayColors[display]}>{display}</Tag></Descriptions.Item>
        <Descriptions.Item label="Presence">{snapshot.presence.currentState ?? 'NOT_APPLICABLE'}</Descriptions.Item>
        <Descriptions.Item label="Telemetry Readiness"><Tag>{snapshot.telemetryReadiness}</Tag></Descriptions.Item>
        <Descriptions.Item label="Business Revision">{snapshot.businessRevision}</Descriptions.Item>
        <Descriptions.Item label="Evaluated At">{formatInstant(snapshot.evaluatedAt)}</Descriptions.Item>
        <Descriptions.Item label="Last Seen">{formatInstant(snapshot.presence.lastSeenAt)}</Descriptions.Item>
        <Descriptions.Item label="Last Known Presence">{lastKnownPresence?.state ?? '—'}</Descriptions.Item>
        <Descriptions.Item label="Last Known At">{formatInstant(lastKnownPresence?.lastSeenAt ?? lastKnownPresence?.evaluatedAt)}</Descriptions.Item>
      </Descriptions>
      <DeviceTelemetryHighlights deviceType={deviceType} snapshot={snapshot} />
      <Table<TelemetryRow>
        rowKey="key"
        size="small"
        pagination={false}
        columns={telemetryColumns}
        dataSource={rows}
        scroll={{ x: 970 }}
        locale={{ emptyText: '当前 exact-key Snapshot 没有遥测值' }}
        aria-label="Device latest telemetry exact keys"
      />
      <Typography.Text type="secondary">
        Last Known 值保留原 sampledAt；STALE、SUSPECT、MISSING 和 UNAVAILABLE 不会被改写成当前值或 0。
      </Typography.Text>
    </Space>
  );
}
