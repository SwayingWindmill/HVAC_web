import { useEffect, useMemo, useState, type Key } from 'react';
import {
  Alert,
  Button,
  Card,
  Col,
  Descriptions,
  Drawer,
  Grid,
  Input,
  Row,
  Select,
  Space,
  Table,
  Tabs,
  Tag,
  Tree,
  Typography,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import type { DataNode } from 'antd/es/tree';
import {
  ApartmentOutlined,
  BlockOutlined,
  DatabaseOutlined,
  EyeOutlined,
  NodeIndexOutlined,
  SafetyCertificateOutlined,
  TabletOutlined,
} from '@ant-design/icons';
import { useSearchParams } from 'react-router-dom';
import { useQueryClient } from '@tanstack/react-query';
import {
  flattenRegistryPages,
  useRegistryDeviceDetail,
  useRegistryDevices,
  useRegistryEquipment,
  useRegistryEquipmentDetail,
  useRegistryOrganizations,
  useRegistrySite,
  useRegistrySites,
} from '@/api/registry';
import type { Device, Equipment } from '@/api/generated/platformGateway.gen';
import {
  purgeTelemetryCurrentState,
  useDeviceTelemetryLive,
  useVisibleDevicePresence,
  type TelemetryCurrentRuntime,
} from '@/api/telemetry-current';
import { DevicePresenceCell, DeviceTelemetryPanel } from '@/components/DeviceTelemetryState';
import { RegistryEmptyState, RegistryFailureState, RegistryLoadMore } from '@/components/RegistryState';
import { LoadingState } from '@/components/PageState';
import PageScaffold from '@/components/PageScaffold';
import {
  OperationsActionFooter,
  OperationsDetailHeader,
  OperationsDetailSection,
  OperationsMetrics,
  OperationsPanelHeading,
  useOperationsDetailFocus,
} from '@/components/OperationsUI';
import { useUi } from '@/store/ui';
import { STATUS } from '@/theme/tokens';

const lifecycleColor: Record<string, string> = {
  ACTIVE: 'green',
  INACTIVE: 'default',
  RETIRED: 'default',
};

const lifecycleLabel: Record<string, string> = {
  ACTIVE: '启用',
  INACTIVE: '停用',
  RETIRED: '已退役',
};

type LedgerTab = 'equipment' | 'devices';

const isEquipmentKey = (key: Key): key is string => typeof key === 'string' && key.startsWith('equipment:');
const isDeviceKey = (key: Key): key is string => typeof key === 'string' && key.startsWith('device:');

export interface RealAssetsProps {
  telemetryRuntime?: TelemetryCurrentRuntime;
}

export default function RealAssets({ telemetryRuntime }: RealAssetsProps = {}) {
  const [searchParams, setSearchParams] = useSearchParams();
  const screens = Grid.useBreakpoint();
  const compactTable = !screens.xl;
  const queryClient = useQueryClient();
  const buildingId = useUi((state) => state.buildingId);
  const setBuilding = useUi((state) => state.setBuilding);
  const detailFocus = useOperationsDetailFocus();

  const organizationsQuery = useRegistryOrganizations();
  const organizations = flattenRegistryPages(organizationsQuery.data);
  const [organizationId, setOrganizationId] = useState<string | null>(null);
  const sitesQuery = useRegistrySites(organizationId);
  const sites = flattenRegistryPages(sitesQuery.data);
  const [siteId, setSiteId] = useState<string | null>(null);
  const siteQuery = useRegistrySite(siteId);
  const equipmentQuery = useRegistryEquipment(siteId);
  const devicesQuery = useRegistryDevices(siteId);
  const equipment = flattenRegistryPages(equipmentQuery.data);
  const devices = flattenRegistryPages(devicesQuery.data);

  const [activeTab, setActiveTab] = useState<LedgerTab>('equipment');
  const [keyword, setKeyword] = useState('');
  const [lifecycle, setLifecycle] = useState<string>('all');

  const equipmentParam = searchParams.get('equipment');
  const deviceParam = searchParams.get('device');
  const equipmentDetail = useRegistryEquipmentDetail(equipmentParam);
  const deviceDetail = useRegistryDeviceDetail(deviceParam);

  useEffect(() => {
    if (organizationId && organizations.some((organization) => organization.id === organizationId)) return;
    const nextOrganizationId = organizations[0]?.id ?? null;
    if (organizationId !== nextOrganizationId) setOrganizationId(nextOrganizationId);
  }, [organizationId, organizations]);

  useEffect(() => {
    if (siteId && sites.some((site) => site.id === siteId)) return;
    const nextSiteId = sites[0]?.id ?? null;
    if (siteId !== nextSiteId) setSiteId(nextSiteId);
    if (nextSiteId && buildingId !== nextSiteId) setBuilding(nextSiteId);
  }, [buildingId, setBuilding, siteId, sites]);

  const clearDetailParams = () => {
    const next = new URLSearchParams(searchParams);
    next.delete('equipment');
    next.delete('device');
    setSearchParams(next, { replace: true });
  };

  const selectOrganization = (value: string) => {
    purgeTelemetryCurrentState(queryClient, telemetryRuntime);
    clearDetailParams();
    setOrganizationId(value);
    setSiteId(null);
    setKeyword('');
  };

  const selectSite = (value: string) => {
    purgeTelemetryCurrentState(queryClient, telemetryRuntime);
    clearDetailParams();
    setSiteId(value);
    setBuilding(value);
    setKeyword('');
  };

  const openEquipment = (id: string, trigger?: HTMLElement) => {
    if (trigger) detailFocus.captureTrigger(trigger, id);
    const next = new URLSearchParams(searchParams);
    next.set('equipment', id);
    next.delete('device');
    setSearchParams(next, { replace: true });
  };

  const openDevice = (id: string, trigger?: HTMLElement) => {
    if (trigger) detailFocus.captureTrigger(trigger, id);
    const next = new URLSearchParams(searchParams);
    next.set('device', id);
    next.delete('equipment');
    setSearchParams(next, { replace: true });
  };

  const closeDetail = () => {
    const next = new URLSearchParams(searchParams);
    next.delete('equipment');
    next.delete('device');
    setSearchParams(next, { replace: true });
    detailFocus.restoreFocus();
  };

  const equipmentRows = useMemo(() => {
    const query = keyword.trim().toLowerCase();
    return equipment.filter((item) => {
      if (lifecycle !== 'all' && item.status !== lifecycle) return false;
      if (!query) return true;
      return [item.id, item.code, item.displayName, item.equipmentType].some((value) => value.toLowerCase().includes(query));
    });
  }, [equipment, keyword, lifecycle]);

  const deviceRows = useMemo(() => {
    const query = keyword.trim().toLowerCase();
    return devices.filter((item) => {
      if (lifecycle !== 'all' && item.status !== lifecycle) return false;
      if (!query) return true;
      return [item.id, item.code, item.displayName, item.deviceType].some((value) => value.toLowerCase().includes(query));
    });
  }, [devices, keyword, lifecycle]);

  const presenceQuery = useVisibleDevicePresence(deviceRows, organizationId, siteId, telemetryRuntime);
  const selectedDevice = deviceParam
    ? deviceDetail.data ?? devices.find((device) => device.id === deviceParam) ?? null
    : null;
  const deviceLive = useDeviceTelemetryLive(selectedDevice, telemetryRuntime);

  useEffect(() => {
    if (!deviceParam || devicesQuery.isPending || devicesQuery.isFetching || devicesQuery.hasNextPage) return;
    if (devices.some((device) => device.id === deviceParam)) return;
    purgeTelemetryCurrentState(queryClient, telemetryRuntime);
    const next = new URLSearchParams(searchParams);
    next.delete('device');
    setSearchParams(next, { replace: true });
  }, [deviceParam, devices, devicesQuery.hasNextPage, devicesQuery.isFetching, devicesQuery.isPending, queryClient, searchParams, setSearchParams, telemetryRuntime]);

  const treeData = useMemo<DataNode[]>(() => {
    const site = siteQuery.data;
    if (!site) return [];
    return [{
      key: `site:${site.id}`,
      title: `${site.displayName} · ${site.timezone}`,
      icon: <ApartmentOutlined style={{ color: STATUS.info }} />,
      children: [
        {
          key: `equipment-group:${site.id}`,
          title: `Equipment · 已加载 ${equipment.length}`,
          icon: <BlockOutlined style={{ color: STATUS.warn }} />,
          children: equipment.map((item) => ({
            key: `equipment:${item.id}`,
            title: item.displayName,
            icon: <BlockOutlined style={{ color: STATUS.warn }} />,
            isLeaf: true,
          })),
        },
        {
          key: `device-group:${site.id}`,
          title: `Device · 已加载 ${devices.length}`,
          icon: <TabletOutlined style={{ color: STATUS.info }} />,
          children: devices.map((item) => ({
            key: `device:${item.id}`,
            title: item.displayName,
            icon: <TabletOutlined style={{ color: STATUS.info }} />,
            isLeaf: true,
          })),
        },
      ],
    }];
  }, [devices, equipment, siteQuery.data]);

  const selectTreeNode = (keys: Key[]) => {
    const key = keys[0];
    if (!key) return;
    if (isEquipmentKey(key)) {
      setActiveTab('equipment');
      openEquipment(key.slice('equipment:'.length));
    }
    if (isDeviceKey(key)) {
      setActiveTab('devices');
      openDevice(key.slice('device:'.length));
    }
  };

  const equipmentColumns: ColumnsType<Equipment> = [
    {
      title: 'Equipment', dataIndex: 'displayName', key: 'name', width: 240,
      render: (value: string, row) => (
        <Space direction="vertical" size={0}>
          <Space size={6}><Typography.Text strong>{value}</Typography.Text><Tag>{row.equipmentType}</Tag></Space>
          <Typography.Text type="secondary" code copyable={{ text: row.id }}>{row.code} · {row.id}</Typography.Text>
        </Space>
      ),
    },
    { title: '生命周期', dataIndex: 'status', key: 'status', width: 110, render: (value: string) => <Tag color={lifecycleColor[value]}>{lifecycleLabel[value] ?? value}</Tag> },
    { title: 'Revision', dataIndex: 'revision', key: 'revision', width: 90 },
    { title: 'Site ID', dataIndex: 'siteId', key: 'site', width: 270, render: (value: string) => <Typography.Text code copyable>{value}</Typography.Text> },
    {
      title: '操作', key: 'action', width: 100, fixed: 'right',
      render: (_, row) => (
        <Button
          size="small"
          type="primary"
          ghost
          icon={<EyeOutlined />}
          data-ops-detail-trigger={row.id}
          onClick={(event) => openEquipment(row.id, event.currentTarget)}
        >详情</Button>
      ),
    },
  ];

  const deviceColumns: ColumnsType<Device> = [
    {
      title: 'Device', dataIndex: 'displayName', key: 'name', width: 240,
      render: (value: string, row) => (
        <Space direction="vertical" size={0}>
          <Space size={6}><Typography.Text strong>{value}</Typography.Text><Tag>{row.deviceType}</Tag></Space>
          <Typography.Text type="secondary" code copyable={{ text: row.id }}>{row.code} · {row.id}</Typography.Text>
        </Space>
      ),
    },
    { title: 'Registry 生命周期', dataIndex: 'status', key: 'status', width: 150, render: (value: string) => <Tag color={lifecycleColor[value]}>{lifecycleLabel[value] ?? value}</Tag> },
    {
      title: 'Presence / latest', key: 'online', width: 210,
      render: (_, row) => (
        <DevicePresenceCell
          item={presenceQuery.data?.byDeviceId.get(row.id)}
          pending={presenceQuery.isPending || presenceQuery.isFetching}
        />
      ),
    },
    { title: 'Revision', dataIndex: 'revision', key: 'revision', width: 90 },
    {
      title: '操作', key: 'action', width: 100, fixed: 'right',
      render: (_, row) => (
        <Button
          size="small"
          type="primary"
          ghost
          icon={<EyeOutlined />}
          data-ops-detail-trigger={row.id}
          onClick={(event) => openDevice(row.id, event.currentTarget)}
        >详情</Button>
      ),
    },
  ];

  if (organizationsQuery.isPending) return <PageScaffold title="设备与建筑"><LoadingState tip="正在读取授权 Organization" /></PageScaffold>;
  if (organizationsQuery.error) {
    return <PageScaffold title="设备与建筑"><RegistryFailureState error={organizationsQuery.error} onRetry={() => void organizationsQuery.refetch()} /></PageScaffold>;
  }
  if (organizations.length === 0) {
    return <PageScaffold title="设备与建筑"><RegistryEmptyState description="当前账号没有可见的 Organization。" /></PageScaffold>;
  }

  const selectedSite = siteQuery.data ?? sites.find((site) => site.id === siteId);
  const selectedOrganization = organizations.find((organization) => organization.id === organizationId);
  const attentionCount = [...equipment, ...devices].filter((item) => item.status !== 'ACTIVE').length;
  const visiblePresence = presenceQuery.data?.items ?? [];
  const onlineCount = visiblePresence.filter((item) => item.status === 'ok' && item.snapshot.displayState === 'ONLINE').length;
  const telemetryAttentionCount = visiblePresence.filter((item) => item.status === 'error'
    || (item.status === 'ok' && ['OFFLINE', 'STALE', 'UNKNOWN', 'UNAVAILABLE'].includes(item.snapshot.displayState ?? 'UNKNOWN'))).length;
  const detailOpen = Boolean(equipmentParam || deviceParam);
  const detailLoading = equipmentParam ? equipmentDetail.isPending : deviceDetail.isPending;
  const detailError = equipmentParam ? equipmentDetail.error : deviceDetail.error;
  const detailResource = equipmentParam ? equipmentDetail.data : deviceDetail.data;
  const detailKind = equipmentParam ? 'Equipment' : 'Device';

  return (
    <PageScaffold
      title="设备与建筑"
      extra={<Tag icon={<SafetyCertificateOutlined />}>Registry 只读 · real</Tag>}
    >
      <div data-testid="real-registry-assets-page">
        <OperationsMetrics
          items={[
            { label: '已加载 Equipment', value: equipment.length, icon: <BlockOutlined /> },
            { label: '可见 Device', value: devices.length, icon: <TabletOutlined /> },
            { label: '当前 ONLINE', value: onlineCount, detail: '来自 Presence-only Snapshot', tone: 'positive' },
            { label: '状态需关注', value: telemetryAttentionCount, detail: 'OFFLINE / STALE / UNKNOWN / UNAVAILABLE', tone: telemetryAttentionCount ? 'warning' : 'positive' },
            { label: '非 ACTIVE', value: attentionCount, detail: 'Registry 生命周期，非在线状态', tone: attentionCount ? 'warning' : 'accent' },
          ]}
        />
        {presenceQuery.error ? (
          <Alert
            type="error"
            showIcon
            message="可见 Device 的 Presence batch 暂不可用"
            description="真实模式保持 Registry 列表可见，但不会回退到 Legacy、ThingsBoard、Socket.IO 或 Mock 状态。"
            action={<Button size="small" onClick={() => void presenceQuery.refetch()}>重试</Button>}
            data-presence-batch-state="error"
          />
        ) : presenceQuery.data?.partial ? (
          <Alert
            type="warning"
            showIcon
            message="Presence batch 返回部分结果"
            description="每个失败 Device 独立显示 UNAVAILABLE；成功 Device 仍使用同一批次的权威 Snapshot。"
            data-presence-batch-state="partial"
          />
        ) : (
          <Alert
            type="info"
            showIcon
            message="S2 Presence 与 latest telemetry 已接入"
            description="列表使用 bounded Presence-only batch；详情仅请求 UI 展示的 exact keys，并通过同一 Snapshot/Business Revision 状态模型应用实时 delta。"
            data-presence-batch-state="ready"
          />
        )}

        <Row gutter={[16, 16]}>
          <Col xs={24} lg={7} xl={6}>
            <Card
              variant="borderless"
              title={<OperationsPanelHeading icon={<ApartmentOutlined />} title="授权 Registry 导航" />}
              styles={{ body: { padding: 12 } }}
            >
              <Space direction="vertical" size={10} style={{ width: '100%' }}>
                <Select
                  aria-label="选择 Organization"
                  value={organizationId ?? undefined}
                  onChange={selectOrganization}
                  options={organizations.map((organization) => ({ value: organization.id, label: organization.displayName }))}
                  style={{ width: '100%' }}
                />
                <Select
                  aria-label="选择 Site"
                  value={siteId ?? undefined}
                  onChange={selectSite}
                  loading={sitesQuery.isPending}
                  disabled={sitesQuery.isError || sites.length === 0}
                  placeholder={sites.length === 0 ? '暂无授权 Site' : '选择 Site'}
                  options={sites.map((site) => ({ value: site.id, label: site.displayName }))}
                  style={{ width: '100%' }}
                />
                {sitesQuery.error ? (
                  <RegistryFailureState error={sitesQuery.error} compact onRetry={() => void sitesQuery.refetch()} />
                ) : treeData.length === 0 ? (
                  <RegistryEmptyState description="当前 Site 暂无 Registry 资源。" />
                ) : (
                  <Tree showIcon defaultExpandAll treeData={treeData} onSelect={selectTreeNode} />
                )}
                <RegistryLoadMore
                  hasMore={Boolean(organizationsQuery.hasNextPage)}
                  loading={organizationsQuery.isFetchingNextPage}
                  onLoadMore={() => void organizationsQuery.fetchNextPage()}
                  label="更多 Organization"
                />
                <RegistryLoadMore
                  hasMore={Boolean(sitesQuery.hasNextPage)}
                  loading={sitesQuery.isFetchingNextPage}
                  onLoadMore={() => void sitesQuery.fetchNextPage()}
                  label="更多 Site"
                />
              </Space>
            </Card>
          </Col>

          <Col xs={24} lg={17} xl={18}>
            <Card variant="borderless" styles={{ body: { padding: 16 } }}>
              <Space direction="vertical" size={12} style={{ width: '100%' }}>
                <div className="ops-toolbar">
                  <OperationsPanelHeading
                    icon={<NodeIndexOutlined />}
                    title="Registry 台账"
                    meta={selectedSite ? `${selectedSite.displayName} · ${selectedSite.timezone}` : '请选择 Site'}
                  />
                  <Space wrap>
                    <Input
                      allowClear
                      aria-label="搜索 Registry 资源"
                      placeholder="搜索平台 ID、code、名称或类型"
                      value={keyword}
                      onChange={(event) => setKeyword(event.target.value)}
                      style={{ width: 280 }}
                    />
                    <Select
                      aria-label="筛选 Registry 生命周期"
                      value={lifecycle}
                      onChange={setLifecycle}
                      options={[
                        { value: 'all', label: '全部生命周期' },
                        { value: 'ACTIVE', label: 'ACTIVE' },
                        { value: 'INACTIVE', label: 'INACTIVE' },
                        { value: 'RETIRED', label: 'RETIRED' },
                      ]}
                      style={{ width: 150 }}
                    />
                  </Space>
                </div>

                {!siteId ? (
                  <RegistryEmptyState description="请选择一个授权 Site。" />
                ) : siteQuery.error ? (
                  <RegistryFailureState error={siteQuery.error} onRetry={() => void siteQuery.refetch()} />
                ) : siteQuery.isPending ? (
                  <LoadingState tip="正在读取 Site 权威信息" minHeight={220} />
                ) : (
                  <Tabs
                    activeKey={activeTab}
                    onChange={(key) => setActiveTab(key as LedgerTab)}
                    items={[
                      {
                        key: 'equipment',
                        label: `Equipment (${equipmentRows.length})`,
                        children: equipmentQuery.error ? (
                          <RegistryFailureState error={equipmentQuery.error} onRetry={() => void equipmentQuery.refetch()} />
                        ) : equipmentQuery.isPending ? (
                          <LoadingState tip="正在读取 Equipment" minHeight={220} />
                        ) : (
                          <>
                            <Table<Equipment>
                              rowKey="id"
                              size="middle"
                              columns={compactTable ? equipmentColumns.filter((column) => ['name', 'status', 'action'].includes(String(column.key))) : equipmentColumns}
                              dataSource={equipmentRows}
                              pagination={false}
                              scroll={{ x: compactTable ? 620 : 980 }}
                              locale={{ emptyText: <RegistryEmptyState description="没有符合条件的 Equipment。" /> }}
                            />
                            <RegistryLoadMore
                              hasMore={Boolean(equipmentQuery.hasNextPage)}
                              loading={equipmentQuery.isFetchingNextPage}
                              onLoadMore={() => void equipmentQuery.fetchNextPage()}
                              label="加载更多 Equipment"
                            />
                          </>
                        ),
                      },
                      {
                        key: 'devices',
                        label: `Device (${deviceRows.length})`,
                        children: devicesQuery.error ? (
                          <RegistryFailureState error={devicesQuery.error} onRetry={() => void devicesQuery.refetch()} />
                        ) : devicesQuery.isPending ? (
                          <LoadingState tip="正在读取 Device" minHeight={220} />
                        ) : (
                          <>
                            <Table<Device>
                              rowKey="id"
                              size="middle"
                              columns={compactTable ? deviceColumns.filter((column) => ['name', 'status', 'online', 'action'].includes(String(column.key))) : deviceColumns}
                              dataSource={deviceRows}
                              pagination={false}
                              scroll={{ x: compactTable ? 680 : 900 }}
                              locale={{ emptyText: <RegistryEmptyState description="没有符合条件的 Device。" /> }}
                            />
                            <RegistryLoadMore
                              hasMore={Boolean(devicesQuery.hasNextPage)}
                              loading={devicesQuery.isFetchingNextPage}
                              onLoadMore={() => void devicesQuery.fetchNextPage()}
                              label="加载更多 Device"
                            />
                          </>
                        ),
                      },
                    ]}
                  />
                )}
              </Space>
            </Card>
          </Col>
        </Row>
      </div>

      <Drawer
        rootClassName="ops-detail-drawer"
        width={680}
        open={detailOpen}
        onClose={closeDetail}
        afterOpenChange={(open) => { if (!open) detailFocus.restoreFocus(); }}
        title={detailResource ? (
          <OperationsDetailHeader
            eyebrow={`Registry ${detailKind}`}
            title={detailResource.displayName}
            subtitle={`${selectedOrganization?.displayName ?? 'Organization'} · ${selectedSite?.displayName ?? 'Site'}`}
            status={<Tag color={lifecycleColor[detailResource.status]}>{lifecycleLabel[detailResource.status] ?? detailResource.status}</Tag>}
            meta={<Typography.Text code>{detailResource.id}</Typography.Text>}
          />
        ) : `${detailKind} 详情`}
        footer={(
          <OperationsActionFooter note="Registry 生命周期与 S2 current-state 分离；详情只读取 exact keys，不提供历史、控制或 Mock fallback。">
            <Button onClick={closeDetail}>关闭</Button>
          </OperationsActionFooter>
        )}
      >
        {detailLoading ? (
          <LoadingState tip={`正在读取 ${detailKind}`} />
        ) : detailError ? (
          <RegistryFailureState
            error={detailError}
            onRetry={() => void (equipmentParam ? equipmentDetail.refetch() : deviceDetail.refetch())}
          />
        ) : detailResource ? (
          <div className="ops-detail-stack">
            <OperationsDetailSection
              title="权威 Registry 身份"
              icon={equipmentParam ? <BlockOutlined /> : <TabletOutlined />}
              description="以下字段由 Platform Gateway 的生成契约校验。"
            >
              <Descriptions column={{ xs: 1, sm: 2 }} size="small" colon={false}>
                <Descriptions.Item label="Platform ID"><Typography.Text code copyable>{detailResource.id}</Typography.Text></Descriptions.Item>
                <Descriptions.Item label="Code">{detailResource.code}</Descriptions.Item>
                <Descriptions.Item label="Owning Organization ID"><Typography.Text code copyable>{detailResource.owningOrganizationId}</Typography.Text></Descriptions.Item>
                <Descriptions.Item label="Site ID"><Typography.Text code copyable>{detailResource.siteId}</Typography.Text></Descriptions.Item>
                <Descriptions.Item label="类型">{'equipmentType' in detailResource ? detailResource.equipmentType : detailResource.deviceType}</Descriptions.Item>
                <Descriptions.Item label="Lifecycle">{detailResource.status}</Descriptions.Item>
                <Descriptions.Item label="Revision">{detailResource.revision}</Descriptions.Item>
                <Descriptions.Item label="Updated At">{detailResource.updatedAt}</Descriptions.Item>
              </Descriptions>
            </OperationsDetailSection>
            {!equipmentParam ? (
              <OperationsDetailSection
                title="S2 Presence 与 latest telemetry"
                icon={<DatabaseOutlined />}
                description="ONLINE/OFFLINE/STALE/UNKNOWN/UNAVAILABLE 与点位 MISSING/SUSPECT 均来自同一权威 Snapshot 状态模型。"
              >
                <DeviceTelemetryPanel result={deviceLive} />
              </OperationsDetailSection>
            ) : null}
          </div>
        ) : (
          <RegistryEmptyState description="未选择 Registry 资源。" />
        )}
      </Drawer>
    </PageScaffold>
  );
}
