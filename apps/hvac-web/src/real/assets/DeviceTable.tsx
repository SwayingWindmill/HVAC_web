import type { HTMLAttributes } from 'react';
import { ApiOutlined, EyeOutlined } from '@ant-design/icons';
import { Badge, Button, Empty, Space, Table, Tag, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { realAssetsDeviceTypeLabel, type RealAssetsDeviceRow } from './model';
import type { RealAssetsListMode } from './workspace-selectors';

const CONNECTION_LABELS = {
  ONLINE: '在线',
  OFFLINE: '离线',
  UNKNOWN: '连接未知',
  NOT_APPLICABLE: '连接不适用',
  UNAVAILABLE: '连接判定不可用',
} as const;

const READINESS_LABELS = {
  CURRENT: '数据完整',
  DEGRADED: '数据退化',
  INCOMPLETE: '数据不完整',
  NOT_APPLICABLE: '数据不适用',
  UNAVAILABLE: '数据判定不可用',
} as const;

const FRESHNESS_LABELS = {
  FRESH: '新鲜',
  STALE: '陈旧',
  MISSING: '缺失',
  NOT_APPLICABLE: '不适用',
  UNAVAILABLE: '不可用',
} as const;

const QUALITY_LABELS = {
  GOOD: '质量良好',
  DEGRADED: '质量退化',
  NO_DATA: '无可用数据',
  NOT_APPLICABLE: '质量不适用',
  UNAVAILABLE: '质量不可用',
} as const;

function assetBindings(binding: RealAssetsDeviceRow['binding']) {
  if (binding.state === 'bound') return [binding];
  if (binding.state === 'multi-bound') return binding.bindings;
  return [];
}

export function assetBindingLabel(binding: RealAssetsDeviceRow['binding']): string {
  const bindings = assetBindings(binding);
  if (bindings.length > 0) return bindings.map((item) => item.asset.displayName).join('、');
  return binding.state === 'ambiguous' ? '绑定关系冲突' : '未绑定 Asset';
}

function formatSampledAt(value: string, timeZone: string): string {
  const parsed = new Date(value);
  if (Number.isNaN(parsed.getTime())) return '采样时间不可用';
  return `采样 ${new Intl.DateTimeFormat('zh-CN', {
    timeZone,
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
  }).format(parsed)}`;
}

function pointEvidence(point: RealAssetsDeviceRow['operational']['points'][number], timeZone: string): string {
  if (point.state === 'UNAVAILABLE') return '当前状态服务无法确认该点位';
  if (point.state === 'MISSING') {
    if (point.missingReason === 'ONLY_REJECTED_CANDIDATES') return '候选观测均未被接受';
    if (point.missingReason === 'POLICY_NOT_CONFIGURED') return '遥测策略未配置';
    return '尚无已接受观测';
  }
  const freshness = point.freshness === 'STALE' ? '陈旧' : '新鲜';
  const quality = point.quality === 'GOOD' ? '良好' : point.quality ?? '未知';
  const sampledAt = point.sampledAt ? formatSampledAt(point.sampledAt, timeZone) : '采样时间不可用';
  return `${freshness} · ${quality} · ${sampledAt}`;
}

function BindingLabel({ row }: { row: RealAssetsDeviceRow }) {
  const spaceLabel = row.space.state === 'bound'
    ? row.space.space.displayName
    : row.space.state === 'ambiguous'
      ? 'Space 关系冲突'
      : '未绑定 Space';
  const assetLabel = assetBindingLabel(row.binding);
  const bindingMeta = row.binding.state === 'bound'
    ? row.binding.relationship.role
    : row.binding.state === 'multi-bound'
      ? `${row.binding.bindings.length} 个 Asset`
      : '';
  const warning = row.space.state !== 'bound' || row.binding.state === 'unbound' || row.binding.state === 'ambiguous';
  return (
    <span className={warning ? 'real-assets__binding-warning' : undefined}>
      <strong>{spaceLabel}</strong>
      <small>{assetLabel}{bindingMeta ? ` · ${bindingMeta}` : ''}</small>
    </span>
  );
}

export function DeviceTable({
  rows,
  allRows,
  compact,
  currentPending,
  currentUnavailable,
  listMode,
  selectedDeviceId,
  timeZone,
  onOpen,
  registerTrigger,
}: {
  readonly rows: readonly RealAssetsDeviceRow[];
  readonly allRows: readonly RealAssetsDeviceRow[];
  readonly compact: boolean;
  readonly currentPending: boolean;
  readonly currentUnavailable: boolean;
  readonly listMode: RealAssetsListMode;
  readonly selectedDeviceId: string | null;
  readonly timeZone: string;
  readonly onOpen: (deviceId: string) => void;
  readonly registerTrigger: (deviceId: string, node: HTMLElement | null) => void;
}) {
  const columns: ColumnsType<RealAssetsDeviceRow> = [
    {
      title: '通讯端点', key: 'device', fixed: 'left', width: 250,
      render: (_, row) => (
        <Button
          type="link"
          className="real-assets__device-link"
          data-testid="real-assets-open-device"
          aria-haspopup="dialog"
          aria-expanded={selectedDeviceId === row.device.id}
          ref={(node) => registerTrigger(row.device.id, node)}
          onClick={() => onOpen(row.device.id)}
        >
          <Space direction="vertical" size={0} align="start">
            <Space size={6} wrap><Typography.Text strong>{row.device.displayName}</Typography.Text><Tag>{realAssetsDeviceTypeLabel(row.device.deviceType)}</Tag></Space>
            <Typography.Text type="secondary" style={{ fontSize: 12 }}>{row.device.code}</Typography.Text>
          </Space>
        </Button>
      ),
    },
    { title: '区域 / 设备', key: 'asset', width: 210, render: (_, row) => <BindingLabel row={row} /> },
    {
      title: '数据状态', key: 'telemetry', width: 190,
      render: (_, row) => {
        if (currentPending) return <Badge status="processing" text="读取中" />;
        if (currentUnavailable) return <Badge status="error" text="状态服务不可用" />;
        const telemetry = row.operational.telemetry;
        const status = row.operational.needsAttention ? 'warning' : telemetry.readiness === 'CURRENT' ? 'success' : 'default';
        return (
          <Space direction="vertical" size={0}>
            <Badge status={status} text={READINESS_LABELS[telemetry.readiness]} />
            <Typography.Text type="secondary" style={{ fontSize: 12 }}>{FRESHNESS_LABELS[telemetry.freshness]} · {QUALITY_LABELS[telemetry.quality]}</Typography.Text>
            <Typography.Text type="secondary" style={{ fontSize: 12 }}>{telemetry.presentPointCount}/{telemetry.registeredPointCount} Point 有当前证据</Typography.Text>
          </Space>
        );
      },
    },
    {
      title: '连接', key: 'communication', width: 190,
      render: (_, row) => {
        const connection = row.operational.connection;
        return (
          <Space direction="vertical" size={0}>
            <Tag icon={<ApiOutlined />} color={connection.state === 'ONLINE' ? 'processing' : connection.state === 'OFFLINE' ? 'error' : undefined}>{CONNECTION_LABELS[connection.state]}</Tag>
            <Typography.Text type="secondary" style={{ fontSize: 12 }}>{connection.lastSeenAt ? formatSampledAt(connection.lastSeenAt, timeZone) : '最后通讯未提供'}</Typography.Text>
          </Space>
        );
      },
    },
    {
      title: '当前值', key: 'points', width: 230,
      render: (_, row) => (
        <Space direction="vertical" size={2}>
          {row.representativePoints.length > 0
            ? row.representativePoints.map((point) => (
              <div key={point.pointId}>
                <Typography.Text>{point.label} {point.displayValue}{point.unit ? ` ${point.unit}` : ''}</Typography.Text>
                <Typography.Text type="secondary" style={{ display: 'block', fontSize: 12 }}>{pointEvidence(point, timeZone)}</Typography.Text>
              </div>
            ))
            : <Typography.Text type="secondary">无可预览的 Registry Point</Typography.Text>}
        </Space>
      ),
    },
    {
      title: 'Registry', key: 'registry', width: 150,
      render: (_, row) => <Space direction="vertical" size={0}><Tag color={row.device.status === 'ACTIVE' ? 'green' : 'default'}>{row.device.status}</Tag><Typography.Text type="secondary" style={{ fontSize: 12 }}>Revision {row.device.revision}</Typography.Text></Space>,
    },
    {
      title: '操作', key: 'action', fixed: 'right', width: 110,
      render: (_, row) => <Button size="small" type="primary" ghost icon={<EyeOutlined />} data-testid="real-assets-open-device" aria-haspopup="dialog" aria-expanded={selectedDeviceId === row.device.id} onClick={() => onOpen(row.device.id)}>详情</Button>,
    },
  ];
  const visibleColumns = compact
    ? columns.filter((column) => ['device', 'asset', 'state', 'points', 'action'].includes(String(column.key)))
    : columns;
  return (
    <>
      <table className="real-assets__table real-shell-sr-only" aria-label="完整授权通讯端点运行投影">
        <tbody>
          {rows.map((row) => (
            <tr
              key={row.device.id}
              data-device-id={row.device.id}
              data-connection-state={currentUnavailable ? 'UNAVAILABLE' : currentPending ? 'LOADING' : row.operational.connection.state}
              data-telemetry-readiness={currentUnavailable ? 'UNAVAILABLE' : currentPending ? 'LOADING' : row.operational.telemetry.readiness}
              data-needs-attention={currentUnavailable || currentPending ? 'UNKNOWN' : String(row.operational.needsAttention)}
            >
              <td>{row.device.displayName} {row.device.code}</td>
              <td>{assetBindingLabel(row.binding)}</td>
              <td>{currentUnavailable ? '状态不可用' : currentPending ? '读取中' : CONNECTION_LABELS[row.operational.connection.state]}</td>
              <td>{currentUnavailable ? '状态不可用' : currentPending ? '读取中' : READINESS_LABELS[row.operational.telemetry.readiness]}</td>
              <td>{row.registeredPointCount} 个点位</td>
            </tr>
          ))}
        </tbody>
      </table>
      <Table<RealAssetsDeviceRow>
        rowKey={(row) => row.device.id}
        size="middle"
        columns={visibleColumns}
        dataSource={[...rows]}
        pagination={{ pageSize: 8, showSizeChanger: false }}
        scroll={{ x: compact ? 820 : 1240 }}
        locale={{
          emptyText: (
            <Empty
              image={Empty.PRESENTED_IMAGE_SIMPLE}
              description={listMode === 'attention' && !currentPending && !currentUnavailable && allRows.every((row) => !row.operational.needsAttention)
                ? '当前筛选范围内没有离线、陈旧、缺失、不完整或质量退化的通讯端点。切换到“全部资产”可查看完整列表。'
                : '当前筛选条件没有匹配的通讯端点。'}
            />
          ),
        }}
        onRow={(row) => ({
          'data-device-id': row.device.id,
          'data-connection-state': currentUnavailable ? 'UNAVAILABLE' : currentPending ? 'LOADING' : row.operational.connection.state,
          'data-telemetry-readiness': currentUnavailable ? 'UNAVAILABLE' : currentPending ? 'LOADING' : row.operational.telemetry.readiness,
        } as HTMLAttributes<HTMLTableRowElement>)}
      />
    </>
  );
}
