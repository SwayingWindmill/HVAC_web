import { useMemo, useState } from 'react';
import { Tree, Table, Tag, Input, Segmented, Card, Row, Col, Statistic, Typography, Button, Space } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { SearchOutlined } from '@ant-design/icons';
import PageScaffold from '@/components/PageScaffold';
import DeviceDrawer from './DeviceDrawer';
import { useTelemetryLive, MOCK_DEVICES } from '@/api';
import { BRAND } from '@/theme/tokens';
import {
  DEVICE_META,
  STATUS_MAP,
  STATUS_INFO,
  TYPE_LABEL,
  ASSET_TREE,
  ZONE_NAMES,
  type DevStatus,
} from './meta';

const TABLE_KEYS = ['power', 'cop', 'load'];

interface DeviceRow {
  id: string;
  name: string;
  type: keyof typeof TYPE_LABEL;
  status: DevStatus;
}

type ScopeKey = 'all' | 'running' | 'alarm' | 'maintenance';

function matchScope(id: string, sel: string | null): boolean {
  if (!sel || sel === 'b1') return true;
  if (DEVICE_META[id].zone === sel) return true;
  if (id === sel) return true;
  return false;
}

export default function Assets() {
  const [search, setSearch] = useState('');
  const [statusFilter, setStatusFilter] = useState<ScopeKey>('all');
  const [selected, setSelected] = useState<string | null>(null);
  const [drawerId, setDrawerId] = useState<string | null>(null);

  const { get } = useTelemetryLive(MOCK_DEVICES, TABLE_KEYS);

  const rows = useMemo<DeviceRow[]>(() => {
    return MOCK_DEVICES.filter((id) => {
      const m = DEVICE_META[id];
      if (search && !m.name.includes(search) && !id.includes(search)) return false;
      if (statusFilter !== 'all' && STATUS_MAP[id] !== statusFilter) return false;
      if (!matchScope(id, selected)) return false;
      return true;
    }).map((id) => ({ id, name: DEVICE_META[id].name, type: DEVICE_META[id].type, status: STATUS_MAP[id] }));
  }, [search, statusFilter, selected]);

  const totalPower = useMemo(
    () => Math.round(MOCK_DEVICES.reduce((s, id) => s + (get(id, 'power') ?? 0), 0)),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [get],
  );
  const runningCount = MOCK_DEVICES.filter((id) => STATUS_MAP[id] === 'running').length;
  const alarmCount = MOCK_DEVICES.filter((id) => STATUS_MAP[id] === 'alarm').length;

  const columns: ColumnsType<DeviceRow> = [
    {
      title: '设备',
      dataIndex: 'name',
      render: (name: string, r) => (
        <Space direction="vertical" size={0}>
          <Typography.Text strong>{name}</Typography.Text>
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            {TYPE_LABEL[r.type]} · {r.id}
          </Typography.Text>
        </Space>
      ),
    },
    {
      title: '类型',
      dataIndex: 'type',
      render: (t: keyof typeof TYPE_LABEL) => <Tag>{TYPE_LABEL[t]}</Tag>,
      responsive: ['md' as const],
    },
    {
      title: '状态',
      dataIndex: 'status',
      render: (s: DevStatus) => <Tag color={STATUS_INFO[s].color}>{STATUS_INFO[s].label}</Tag>,
    },
    {
      title: '实时功率',
      key: 'power',
      align: 'right',
      render: (_: unknown, r) => (
        <Typography.Text style={{ fontVariantNumeric: 'tabular-nums', color: BRAND.tealStrong }}>
          {Math.round(get(r.id, 'power') ?? 0)} kW
        </Typography.Text>
      ),
    },
    {
      title: 'COP',
      key: 'cop',
      align: 'right',
      responsive: ['md' as const],
      render: (_: unknown, r) => (
        <Typography.Text style={{ fontVariantNumeric: 'tabular-nums' }}>
          {(get(r.id, 'cop') ?? 0).toFixed(2)}
        </Typography.Text>
      ),
    },
    {
      title: '负荷率',
      key: 'load',
      align: 'right',
      responsive: ['lg' as const],
      render: (_: unknown, r) => (
        <Typography.Text style={{ fontVariantNumeric: 'tabular-nums' }}>
          {Math.round(get(r.id, 'load') ?? 0)}%
        </Typography.Text>
      ),
    },
    {
      title: '操作',
      key: 'action',
      align: 'right',
      render: (_: unknown, r) => (
        <Button type="link" onClick={() => setDrawerId(r.id)}>
          查看 ›
        </Button>
      ),
    },
  ];

  return (
    <PageScaffold
      title="设备与建筑"
      subtitle="建筑 → 分区 → 机组设备树，右侧列表与详情抽屉接入实时遥测层"
      extra={
        <Space wrap>
          <Input
            allowClear
            prefix={<SearchOutlined />}
            placeholder="搜索设备 / ID"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            style={{ width: 180 }}
          />
          <Segmented
            value={statusFilter}
            onChange={(v) => setStatusFilter(v as ScopeKey)}
            options={[
              { label: '全部', value: 'all' },
              { label: '运行', value: 'running' },
              { label: '告警', value: 'alarm' },
              { label: '维护', value: 'maintenance' },
            ]}
          />
        </Space>
      }
    >
      <Row gutter={[12, 12]} style={{ marginBottom: 16 }}>
        <Col xs={12} md={6}>
          <Card variant="borderless" styles={{ body: { padding: 16 } }}>
            <Statistic title="设备总数" value={MOCK_DEVICES.length} suffix="台" />
          </Card>
        </Col>
        <Col xs={12} md={6}>
          <Card variant="borderless" styles={{ body: { padding: 16 } }}>
            <Statistic title="运行中" value={runningCount} suffix={`/ ${MOCK_DEVICES.length}`} valueStyle={{ color: BRAND.teal }} />
          </Card>
        </Col>
        <Col xs={12} md={6}>
          <Card variant="borderless" styles={{ body: { padding: 16 } }}>
            <Statistic title="告警" value={alarmCount} valueStyle={{ color: alarmCount ? '#DC2626' : undefined }} />
          </Card>
        </Col>
        <Col xs={12} md={6}>
          <Card variant="borderless" styles={{ body: { padding: 16 } }}>
            <Statistic title="实时总功率" value={totalPower} suffix="kW" valueStyle={{ color: BRAND.tealStrong }} />
          </Card>
        </Col>
      </Row>

      <Row gutter={[16, 16]}>
        <Col xs={24} lg={7}>
          <Card
            variant="borderless"
            title="资产树"
            styles={{ body: { padding: '8px 8px' } }}
            style={{ height: '100%' }}
          >
            <Tree
              treeData={ASSET_TREE}
              defaultExpandedKeys={['b1', 'z1', 'z2', 'z3']}
              onSelect={(keys) => {
                const key = (keys[0] as string) ?? null;
                setSelected(key);
                if (key && DEVICE_META[key]) setDrawerId(key);
              }}
              blockNode
            />
          </Card>
        </Col>
        <Col xs={24} lg={17}>
          <Card variant="borderless" title={`设备列表${selected && ZONE_NAMES[selected] ? ` · ${ZONE_NAMES[selected]}` : ''}`}>
            <Table
              rowKey="id"
              size="middle"
              columns={columns}
              dataSource={rows}
              pagination={false}
              onRow={(r) => ({ onClick: () => setDrawerId(r.id), style: { cursor: 'pointer' } })}
            />
          </Card>
        </Col>
      </Row>

      <DeviceDrawer deviceId={drawerId} onClose={() => setDrawerId(null)} />
    </PageScaffold>
  );
}
