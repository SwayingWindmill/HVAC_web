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
  ApiOutlined,
  BlockOutlined,
  ClusterOutlined,
  DatabaseOutlined,
  EyeOutlined,
  NodeIndexOutlined,
  RightOutlined,
  SafetyCertificateOutlined,
  TabletOutlined,
} from '@ant-design/icons';
import { useSearchParams } from 'react-router';
import { useQueryClient } from '@tanstack/react-query';
import {
  flattenRegistryPages,
  useRegistryAssetModel,
  useRegistryDeviceDetail,
  useRegistryAssetDetail,
  useRegistrySite,
  useRegistrySites,
} from '@/api/registry';
import type { Device, Asset } from '@/api/generated/platformGateway.gen';
import { buildRealAssetsHierarchy, type RealAssetsHierarchyNode } from '@/real/assets/model';
import '@/real/assets/real-assets.css';
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

type LedgerTab = 'asset' | 'devices';

const isAssetKey = (key: Key): key is string => typeof key === 'string' && key.startsWith('asset:');
const isDeviceKey = (key: Key): key is string => typeof key === 'string' && key.startsWith('device:');

const hierarchyIcon = (kind: RealAssetsHierarchyNode['kind']) => {
  switch (kind) {
    case 'site': return <ApartmentOutlined style={{ color: STATUS.info }} />;
    case 'space': return <ClusterOutlined style={{ color: STATUS.info }} />;
    case 'asset': return <BlockOutlined style={{ color: STATUS.warn }} />;
    case 'device': return <TabletOutlined style={{ color: STATUS.info }} />;
  }
};

const hierarchyDataNode = (node: RealAssetsHierarchyNode): DataNode => ({
  key: node.key,
  title: (
    <span className="real-assets-tree-node" data-asset-kind={node.kind} title={`${node.label}｜${node.meta}`}>
      {node.label}
    </span>
  ),
  icon: hierarchyIcon(node.kind),
  children: node.children.map(hierarchyDataNode),
});

const defaultExpandedTreeKeys = (nodes: readonly DataNode[]): Key[] => {
  const root = nodes[0];
  if (!root) return [];
  return [
    root.key,
    ...(root.children ?? []).filter((node) => String(node.key).startsWith('space:')).map((node) => node.key),
  ];
};

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

  const sitesQuery = useRegistrySites();
  const sites = flattenRegistryPages(sitesQuery.data);
  const [siteId, setSiteId] = useState<string | null>(null);
  const siteQuery = useRegistrySite(siteId);
  const assetModelQuery = useRegistryAssetModel(siteId);
  const assetModel = assetModelQuery.data;
  const assets = assetModel?.assets ?? [];
  const devices = assetModel?.devices ?? [];

  const [activeTab, setActiveTab] = useState<LedgerTab>('asset');
  const [keyword, setKeyword] = useState('');
  const [lifecycle, setLifecycle] = useState<string>('all');

  const assetParam = searchParams.get('asset');
  const deviceParam = searchParams.get('device');
  const assetDetail = useRegistryAssetDetail(assetParam);
  const deviceDetail = useRegistryDeviceDetail(deviceParam);

  useEffect(() => {
    if (siteId && sites.some((site) => site.id === siteId)) return;
    const nextSiteId = sites[0]?.id ?? null;
    if (siteId !== nextSiteId) setSiteId(nextSiteId);
    if (nextSiteId && buildingId !== nextSiteId) setBuilding(nextSiteId);
  }, [buildingId, setBuilding, siteId, sites]);

  const clearDetailParams = () => {
    const next = new URLSearchParams(searchParams);
    next.delete('asset');
    next.delete('device');
    setSearchParams(next, { replace: true });
  };

  const selectSite = (value: string) => {
    purgeTelemetryCurrentState(queryClient, telemetryRuntime);
    clearDetailParams();
    setSiteId(value);
    setBuilding(value);
    setKeyword('');
  };

  const openAsset = (id: string, trigger?: HTMLElement) => {
    if (trigger) detailFocus.captureTrigger(trigger, id);
    const next = new URLSearchParams(searchParams);
    next.set('asset', id);
    next.delete('device');
    setSearchParams(next, { replace: true });
  };

  const openDevice = (id: string, trigger?: HTMLElement) => {
    if (trigger) detailFocus.captureTrigger(trigger, id);
    const next = new URLSearchParams(searchParams);
    next.set('device', id);
    next.delete('asset');
    setSearchParams(next, { replace: true });
  };

  const closeDetail = () => {
    const next = new URLSearchParams(searchParams);
    next.delete('asset');
    next.delete('device');
    setSearchParams(next, { replace: true });
    detailFocus.restoreFocus();
  };

  const assetRows = useMemo(() => {
    const query = keyword.trim().toLowerCase();
    return assets.filter((item) => {
      if (lifecycle !== 'all' && item.status !== lifecycle) return false;
      if (!query) return true;
      return [item.id, item.code, item.displayName, item.assetType].some((value) => value.toLowerCase().includes(query));
    });
  }, [assets, keyword, lifecycle]);

  const deviceRows = useMemo(() => {
    const query = keyword.trim().toLowerCase();
    return devices.filter((item) => {
      if (lifecycle !== 'all' && item.status !== lifecycle) return false;
      if (!query) return true;
      return [item.id, item.code, item.displayName, item.deviceType].some((value) => value.toLowerCase().includes(query));
    });
  }, [devices, keyword, lifecycle]);

  const selectedSite = siteQuery.data ?? sites.find((site) => site.id === siteId);
  const presenceQuery = useVisibleDevicePresence(deviceRows, selectedSite?.tenantId ?? null, siteId, telemetryRuntime);
  const selectedDevice = deviceParam
    ? deviceDetail.data ?? devices.find((device) => device.id === deviceParam) ?? null
    : null;
  const deviceLive = useDeviceTelemetryLive(selectedDevice, telemetryRuntime);

  useEffect(() => {
    if (assetModelQuery.isPending || assetModelQuery.isFetching) return;
    const assetVisible = !assetParam || assets.some((item) => item.id === assetParam);
    const deviceVisible = !deviceParam || devices.some((item) => item.id === deviceParam);
    if (assetVisible && deviceVisible) return;
    purgeTelemetryCurrentState(queryClient, telemetryRuntime);
    const next = new URLSearchParams(searchParams);
    if (!assetVisible) next.delete('asset');
    if (!deviceVisible) next.delete('device');
    setSearchParams(next, { replace: true });
  }, [
    assetModelQuery.isFetching,
    assetModelQuery.isPending,
    deviceParam,
    devices,
    assets,
    assetParam,
    queryClient,
    searchParams,
    setSearchParams,
    telemetryRuntime,
  ]);

  const treeData = useMemo<DataNode[]>(() => {
    const site = siteQuery.data;
    if (!site || !assetModel) return [];
    return [hierarchyDataNode(buildRealAssetsHierarchy(assetModel, `${site.displayName} · ${site.timezone}`))];
  }, [assetModel, siteQuery.data]);

  const selectTreeNode = (keys: Key[]) => {
    const key = keys[0];
    if (!key) return;
    if (isAssetKey(key)) {
      const assetId = key.slice('asset:'.length);
      if (assets.some((item) => item.id === assetId)) {
        setActiveTab('asset');
        openAsset(assetId);
      }
    }
    if (isDeviceKey(key)) {
      const deviceId = key.slice('device:'.length);
      if (devices.some((item) => item.id === deviceId)) {
        setActiveTab('devices');
        openDevice(deviceId);
      }
    }
  };

  const assetColumns: ColumnsType<Asset> = [
    {
      title: 'Asset', dataIndex: 'displayName', key: 'name', width: 240,
      render: (value: string, row) => (
        <Space direction="vertical" size={0}>
          <Space size={6}><Typography.Text strong>{value}</Typography.Text><Tag>{row.assetType}</Tag></Space>
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
          onClick={(event) => openAsset(row.id, event.currentTarget)}
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

  if (sitesQuery.isPending) return <PageScaffold title="设备与建筑"><LoadingState tip="正在读取授权 Site" /></PageScaffold>;
  if (sitesQuery.error) {
    return <PageScaffold title="设备与建筑"><RegistryFailureState error={sitesQuery.error} onRetry={() => void sitesQuery.refetch()} /></PageScaffold>;
  }
  if (sites.length === 0) {
    return <PageScaffold title="设备与建筑"><RegistryEmptyState description="当前账号没有可见的 Site。" /></PageScaffold>;
  }

  const attentionCount = [...assets, ...devices].filter((item) => item.status !== 'ACTIVE').length;
  const visiblePresence = presenceQuery.data?.items ?? [];
  const onlineCount = visiblePresence.filter((item) => item.status === 'ok' && item.snapshot.displayState === 'ONLINE').length;
  const telemetryAttentionCount = visiblePresence.filter((item) => item.status === 'error'
    || (item.status === 'ok' && ['OFFLINE', 'STALE', 'UNKNOWN', 'UNAVAILABLE'].includes(item.snapshot.displayState ?? 'UNKNOWN'))).length;
  const detailOpen = Boolean(assetParam || deviceParam);
  const detailLoading = assetParam ? assetDetail.isPending : deviceDetail.isPending;
  const detailError = assetParam ? assetDetail.error : deviceDetail.error;
  const detailResource = assetParam ? assetDetail.data : deviceDetail.data;
  const detailKind = assetParam ? 'Asset' : 'Device';

  return (
    <PageScaffold
      title="设备与建筑"
      extra={<Tag icon={<SafetyCertificateOutlined />}>Registry 只读 · real</Tag>}
    >
      <div data-testid="real-registry-assets-page">
        <OperationsMetrics
          ariaLabel="Site 原子资产模型摘要"
          items={[
            { label: 'Space', value: assetModel?.counts.spaces ?? 0, icon: <ClusterOutlined />, detail: '空间层级' },
            { label: 'Asset', value: assetModel?.counts.assets ?? 0, icon: <BlockOutlined />, detail: '物理设备' },
            { label: 'Device Endpoint', value: assetModel?.counts.deviceEndpoints ?? 0, icon: <TabletOutlined />, detail: '通信端点' },
            { label: 'Sensor', value: assetModel?.counts.physicalSensors ?? 0, icon: <ApiOutlined />, detail: '测量单元' },
            { label: 'Point', value: assetModel?.counts.points ?? 0, icon: <DatabaseOutlined />, detail: '标准点位目录' },
          ]}
        />
        <OperationsMetrics
          ariaLabel="Device Endpoint 运行摘要"
          items={[
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
            description="真实模式保持 Registry 列表可见，但不会回退到 Legacy、Provider 直读、Socket.IO 或 Mock 状态。"
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
                  aria-label="选择 Site"
                  value={siteId ?? undefined}
                  onChange={selectSite}
                  loading={sitesQuery.isPending}
                  disabled={sites.length === 0}
                  placeholder={sites.length === 0 ? '暂无授权 Site' : '选择 Site'}
                  options={sites.map((site) => ({ value: site.id, label: site.displayName }))}
                  style={{ width: '100%' }}
                />
                {!siteId ? (
                  <RegistryEmptyState description="请选择一个授权 Site。" />
                ) : assetModelQuery.error ? (
                  <RegistryFailureState error={assetModelQuery.error} compact onRetry={() => void assetModelQuery.refetch()} />
                ) : assetModelQuery.isPending ? (
                  <LoadingState tip="正在读取原子 Asset Model" minHeight={160} />
                ) : treeData.length === 0 ? (
                  <RegistryEmptyState description="当前 Site 暂无 Registry 资源。" />
                ) : (
                  <Tree
                    className="real-assets-navigation-tree"
                    showIcon
                    blockNode
                    defaultExpandedKeys={defaultExpandedTreeKeys(treeData)}
                    switcherIcon={({ isLeaf }) => isLeaf ? null : <RightOutlined className="real-assets-tree-switcher" />}
                    treeData={treeData}
                    onSelect={selectTreeNode}
                  />
                )}
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
                        key: 'asset',
                        label: `Asset (${assetRows.length})`,
                        children: assetModelQuery.error ? (
                          <RegistryFailureState error={assetModelQuery.error} onRetry={() => void assetModelQuery.refetch()} />
                        ) : assetModelQuery.isPending ? (
                          <LoadingState tip="正在读取 Asset" minHeight={220} />
                        ) : (
                          <>
                            <Table<Asset>
                              rowKey="id"
                              size="middle"
                              columns={compactTable ? assetColumns.filter((column) => ['name', 'status', 'action'].includes(String(column.key))) : assetColumns}
                              dataSource={assetRows}
                              pagination={false}
                              scroll={{ x: compactTable ? 620 : 980 }}
                              locale={{ emptyText: <RegistryEmptyState description="没有符合条件的 Asset。" /> }}
                            />

                          </>
                        ),
                      },
                      {
                        key: 'devices',
                        label: `Device (${deviceRows.length})`,
                        children: assetModelQuery.error ? (
                          <RegistryFailureState error={assetModelQuery.error} onRetry={() => void assetModelQuery.refetch()} />
                        ) : assetModelQuery.isPending ? (
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
            subtitle={selectedSite ? `${selectedSite.displayName} · Tenant ${selectedSite.tenantId}` : 'Site'}
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
            onRetry={() => void (assetParam ? assetDetail.refetch() : deviceDetail.refetch())}
          />
        ) : detailResource ? (
          <div className="ops-detail-stack">
            <OperationsDetailSection
              title="权威 Registry 身份"
              icon={assetParam ? <BlockOutlined /> : <TabletOutlined />}
              description="以下字段由 Platform Gateway 的生成契约校验。"
            >
              <Descriptions column={{ xs: 1, sm: 2 }} size="small" colon={false}>
                <Descriptions.Item label="Platform ID"><Typography.Text code copyable>{detailResource.id}</Typography.Text></Descriptions.Item>
                <Descriptions.Item label="Code">{detailResource.code}</Descriptions.Item>
                <Descriptions.Item label="Tenant ID"><Typography.Text code copyable>{detailResource.tenantId}</Typography.Text></Descriptions.Item>
                <Descriptions.Item label="Site ID"><Typography.Text code copyable>{detailResource.siteId}</Typography.Text></Descriptions.Item>
                <Descriptions.Item label="类型">{'assetType' in detailResource ? detailResource.assetType : detailResource.deviceType}</Descriptions.Item>
                <Descriptions.Item label="Lifecycle">{detailResource.status}</Descriptions.Item>
                <Descriptions.Item label="Revision">{detailResource.revision}</Descriptions.Item>
                <Descriptions.Item label="Updated At">{detailResource.updatedAt}</Descriptions.Item>
              </Descriptions>
            </OperationsDetailSection>
            {!assetParam ? (
              <OperationsDetailSection
                title="S2 Presence 与 latest telemetry"
                icon={<DatabaseOutlined />}
                description="ONLINE/OFFLINE/STALE/UNKNOWN/UNAVAILABLE 与点位 MISSING/SUSPECT 均来自同一权威 Snapshot 状态模型。"
              >
                <DeviceTelemetryPanel result={deviceLive} deviceType={selectedDevice?.deviceType} />
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
