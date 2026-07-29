import { useEffect, useMemo, useState, type Key } from 'react';
import {
  Badge,
  Button,
  Card,
  Col,
  Empty,
  Grid,
  Input,
  Row,
  Select,
  Space,
  Table,
  Tag,
  Tree,
  Typography,
  message,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import {
  ApartmentOutlined,
  ApiOutlined,
  ClusterOutlined,
  DatabaseOutlined,
  EyeOutlined,
  NodeIndexOutlined,
  ToolOutlined,
  WifiOutlined,
} from '@ant-design/icons';
import { useSearchParams } from 'react-router';
import PageScaffold from '@/components/PageScaffold';
import { OperationsMetrics, OperationsPanelHeading, useOperationsDetailFocus } from '@/components/OperationsUI';
import { can } from '@/auth/permissions';
import { useUi } from '@/store/ui';
import {
  ASSET_TREE,
  DEVICE_META,
  STATUS_INFO,
  STATUS_MAP,
  TYPE_LABEL,
  type DevStatus,
  type DeviceAsset,
  type DeviceType,
} from './meta';
import DeviceDrawer from './DeviceDrawer';

type DeviceRow = DeviceAsset & { id: string; status: DevStatus; pointOnlineRate: number };
type TypeFilter = DeviceType | 'all';
type StatusFilter = DevStatus | 'all';

const TYPE_OPTIONS: { label: string; value: TypeFilter }[] = [
  { label: '全部类型', value: 'all' },
  { label: TYPE_LABEL.chiller, value: 'chiller' },
  { label: TYPE_LABEL.pump, value: 'pump' },
  { label: TYPE_LABEL.ahu, value: 'ahu' },
];

const STATUS_OPTIONS: { label: string; value: StatusFilter }[] = [
  { label: '全部状态', value: 'all' },
  { label: STATUS_INFO.running.label, value: 'running' },
  { label: STATUS_INFO.alarm.label, value: 'alarm' },
  { label: STATUS_INFO.maintenance.label, value: 'maintenance' },
];

const buildRows = (): DeviceRow[] =>
  Object.entries(DEVICE_META).map(([id, meta]) => {
    const status = STATUS_MAP[id];
    return {
      id,
      status,
      pointOnlineRate: Math.round((meta.onlinePoints / meta.pointCount) * 100),
      ...meta,
    };
  });

const toKeyString = (key: Key | null) => (typeof key === 'string' ? key : null);
const isDeviceKey = (key: Key | null) => {
  const k = toKeyString(key);
  return Boolean(k && k in DEVICE_META);
};
const isZoneKey = (key: Key | null) => {
  const k = toKeyString(key);
  return Boolean(k && k.startsWith('z'));
};

export default function Assets() {
  const [searchParams, setSearchParams] = useSearchParams();
  const screens = Grid.useBreakpoint();
  const compactTable = !screens.xl;
  const { role } = useUi();
  const canManageAssets = can(role, 'manage', 'asset');
  const [selectedTreeKey, setSelectedTreeKey] = useState<Key>('b1');
  const [selectedDeviceId, setSelectedDeviceId] = useState<string | null>(null);
  const detailFocus = useOperationsDetailFocus();
  const [typeFilter, setTypeFilter] = useState<TypeFilter>('all');
  const [statusFilter, setStatusFilter] = useState<StatusFilter>('all');
  const [keyword, setKeyword] = useState('');
  const deviceParam = searchParams.get('device');

  useEffect(() => {
    if (!deviceParam) return;
    if (!(deviceParam in DEVICE_META)) {
      const next = new URLSearchParams(searchParams);
      next.delete('device');
      setSearchParams(next, { replace: true });
      message.warning(`未找到设备 ${deviceParam}`);
      return;
    }
    if (selectedDeviceId !== deviceParam) {
      setSelectedTreeKey(deviceParam);
      setTypeFilter('all');
      setStatusFilter('all');
      setKeyword('');
      setSelectedDeviceId(deviceParam);
    }
  }, [deviceParam, searchParams, selectedDeviceId, setSearchParams]);

  const openDevice = (id: string, trigger?: HTMLElement) => {
    if (trigger) detailFocus.captureTrigger(trigger, id);
    const next = new URLSearchParams(searchParams);
    next.set('device', id);
    setSearchParams(next, { replace: true });
    setSelectedDeviceId(id);
  };

  const closeDevice = () => {
    const next = new URLSearchParams(searchParams);
    next.delete('device');
    setSearchParams(next, { replace: true });
    setSelectedDeviceId(null);
    detailFocus.restoreFocus();
  };

  const allRows = useMemo(buildRows, []);

  const summary = useMemo(() => {
    const running = allRows.filter((d) => d.status === 'running').length;
    const alarm = allRows.filter((d) => d.status === 'alarm').length;
    const maintenance = allRows.filter((d) => d.status === 'maintenance').length;
    const totalPoints = allRows.reduce((sum, d) => sum + d.pointCount, 0);
    const onlinePoints = allRows.reduce((sum, d) => sum + d.onlinePoints, 0);
    return { total: allRows.length, running, alarm, maintenance, totalPoints, onlinePoints };
  }, [allRows]);

  const rows = useMemo(() => {
    const q = keyword.trim().toLowerCase();
    return allRows.filter((d) => {
      if (isDeviceKey(selectedTreeKey) && d.id !== selectedTreeKey) return false;
      if (isZoneKey(selectedTreeKey) && d.zone !== selectedTreeKey) return false;
      if (typeFilter !== 'all' && d.type !== typeFilter) return false;
      if (statusFilter !== 'all' && d.status !== statusFilter) return false;
      if (!q) return true;
      return [d.id, d.name, d.zoneName, d.floor, d.manufacturer, d.model, d.gateway, d.maintainer]
        .some((value) => value.toLowerCase().includes(q));
    });
  }, [allRows, keyword, selectedTreeKey, statusFilter, typeFilter]);

  const columns: ColumnsType<DeviceRow> = [
    {
      title: '设备',
      dataIndex: 'name',
      key: 'name',
      fixed: 'left',
      width: 220,
      render: (name: string, row) => (
        <Space direction="vertical" size={0}>
          <Space size={6}>
            <Typography.Text strong>{name}</Typography.Text>
            <Tag>{TYPE_LABEL[row.type]}</Tag>
          </Space>
          <Typography.Text type="secondary" style={{ fontSize: 12 }} copyable={{ text: row.id }}>{row.id}</Typography.Text>
        </Space>
      ),
    },
    {
      title: '位置',
      key: 'location',
      width: 180,
      render: (_, row) => (
        <Space direction="vertical" size={0}>
          <Typography.Text>{row.buildingName} / {row.floor}</Typography.Text>
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>{row.zoneName}</Typography.Text>
        </Space>
      ),
    },
    {
      title: '状态',
      dataIndex: 'status',
      key: 'status',
      width: 100,
      render: (status: DevStatus) => {
        const info = STATUS_INFO[status];
        return <Badge color={info.color} text={info.label} />;
      },
    },
    {
      title: '通讯',
      key: 'comm',
      width: 180,
      render: (_, row) => (
        <Space direction="vertical" size={0}>
          <Tag icon={<ApiOutlined />} color="processing">{row.protocol}</Tag>
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>{row.gateway}</Typography.Text>
        </Space>
      ),
    },
    {
      title: '点位在线',
      key: 'points',
      width: 130,
      sorter: (a, b) => a.pointOnlineRate - b.pointOnlineRate,
      render: (_, row) => (
        <Space direction="vertical" size={0}>
          <Typography.Text>{row.onlinePoints} / {row.pointCount}</Typography.Text>
          <Typography.Text type={row.pointOnlineRate < 95 ? 'warning' : 'secondary'} style={{ fontSize: 12 }}>
            {row.pointOnlineRate}% 在线
          </Typography.Text>
        </Space>
      ),
    },
    {
      title: '铭牌',
      key: 'rating',
      width: 160,
      render: (_, row) => (
        <Space direction="vertical" size={0}>
          <Typography.Text>{row.ratedPower} kW</Typography.Text>
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>{row.ratedCooling ? `${row.ratedCooling} kW 制冷量` : row.model}</Typography.Text>
        </Space>
      ),
    },
    {
      title: '维护',
      key: 'maintainer',
      width: 150,
      render: (_, row) => (
        <Space direction="vertical" size={0}>
          <Typography.Text>{row.maintainer}</Typography.Text>
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>最后通讯：{row.lastSeen}</Typography.Text>
        </Space>
      ),
    },
    {
      title: '操作',
      key: 'action',
      width: 110,
      fixed: 'right',
      render: (_, row) => (
        <Button
          size="small"
          type="primary"
          ghost
          icon={<EyeOutlined />}
          data-ops-detail-trigger={row.id}
          onClick={(event) => openDevice(row.id, event.currentTarget)}
        >
          详情
        </Button>
      ),
    },
  ];

  const tableColumns = compactTable
    ? columns.filter((column) => ['name', 'location', 'status', 'points', 'action'].includes(String(column.key)))
    : columns;

  return (
    <PageScaffold
      title="设备与建筑"
      extra={<Tag>{canManageAssets ? '可维护' : '只读'}</Tag>}
    >
      <OperationsMetrics
        items={[
          { label: '设备总数', value: summary.total, icon: <ApartmentOutlined /> },
          { label: '运行中', value: summary.running, detail: `${summary.total - summary.running} 需关注`, icon: <WifiOutlined />, tone: 'positive' },
          { label: '告警 / 维护', value: summary.alarm + summary.maintenance, detail: `${summary.alarm} 告警 · ${summary.maintenance} 维护`, icon: <ToolOutlined />, tone: summary.alarm ? 'critical' : 'warning' },
          { label: '在线点位', value: summary.onlinePoints, suffix: `/ ${summary.totalPoints}`, detail: `${Math.round((summary.onlinePoints / Math.max(summary.totalPoints, 1)) * 100)}%`, icon: <DatabaseOutlined />, tone: 'accent' },
        ]}
      />

      <Row gutter={[16, 16]}>
        <Col xs={24} lg={7} xl={6}>
          <Card
            variant="borderless"
            title={<OperationsPanelHeading icon={<ClusterOutlined />} title="建筑设备树" />}
            styles={{ body: { padding: 12 } }}
          >
            <Tree
              defaultExpandAll
              treeData={ASSET_TREE}
              selectedKeys={[selectedTreeKey]}
              onSelect={(keys) => setSelectedTreeKey(keys[0] ?? 'b1')}
            />
          </Card>
        </Col>

        <Col xs={24} lg={17} xl={18}>
          <Card variant="borderless" styles={{ body: { padding: 16 } }}>
            <Space direction="vertical" size={12} style={{ width: '100%' }}>
              <div className="ops-toolbar">
                <OperationsPanelHeading icon={<NodeIndexOutlined />} title="设备台账" meta={`${rows.length} 台`} />
                <Space wrap>
                  <Input.Search
                    allowClear
                    placeholder="搜索设备、网关或负责人"
                    value={keyword}
                    onChange={(e) => setKeyword(e.target.value)}
                    style={{ width: 260 }}
                  />
                  <Select value={typeFilter} onChange={setTypeFilter} options={TYPE_OPTIONS} style={{ width: 130 }} />
                  <Select value={statusFilter} onChange={setStatusFilter} options={STATUS_OPTIONS} style={{ width: 130 }} />
                </Space>
              </div>

              <Table<DeviceRow>
                rowKey="id"
                size="middle"
                columns={tableColumns}
                dataSource={rows}
                pagination={{ pageSize: 8, showSizeChanger: false }}
                scroll={{ x: compactTable ? 740 : 1180 }}
                locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="没有符合条件的设备" /> }}
              />
            </Space>
          </Card>
        </Col>
      </Row>

      <DeviceDrawer
        deviceId={selectedDeviceId}
        onClose={closeDevice}
        onAfterClose={detailFocus.restoreFocus}
      />
    </PageScaffold>
  );
}
