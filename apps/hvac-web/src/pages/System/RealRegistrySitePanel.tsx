import { useEffect, useMemo, useState } from 'react';
import { Button, Card, Col, Descriptions, Row, Select, Space, Tag, Tooltip, Tree, Typography } from 'antd';
import type { DataNode } from 'antd/es/tree';
import {
  ApartmentOutlined,
  BlockOutlined,
  ClusterOutlined,
  DatabaseOutlined,
  PlusOutlined,
  TabletOutlined,
} from '@ant-design/icons';
import {
  flattenRegistryPages,
  useRegistryDevices,
  useRegistryEquipment,
  useRegistrySite,
  useRegistrySites,
} from '@/api/registry';
import { RegistryEmptyState, RegistryFailureState, RegistryLoadMore } from '@/components/RegistryState';
import {
  OperationsInsightBand,
  OperationsPanelHeading,
  OperationsSectionIntro,
} from '@/components/OperationsUI';
import { LoadingState } from '@/components/PageState';
import { useUi } from '@/store/ui';
import { BRAND, STATUS } from '@/theme/tokens';

const { Text } = Typography;

const lifecycleColor: Record<string, string> = {
  ACTIVE: 'green',
  INACTIVE: 'default',
  SUSPENDED: 'gold',
  RETIRED: 'default',
};

export default function RealRegistrySitePanel() {
  const buildingId = useUi((state) => state.buildingId);
  const setBuilding = useUi((state) => state.setBuilding);
  const sitesQuery = useRegistrySites();
  const sites = flattenRegistryPages(sitesQuery.data);
  const [siteId, setSiteId] = useState<string | null>(null);
  const siteQuery = useRegistrySite(siteId);
  const equipmentQuery = useRegistryEquipment(siteId);
  const devicesQuery = useRegistryDevices(siteId);
  const equipment = flattenRegistryPages(equipmentQuery.data);
  const devices = flattenRegistryPages(devicesQuery.data);

  useEffect(() => {
    if (siteId && sites.some((site) => site.id === siteId)) return;
    const nextSiteId = sites[0]?.id ?? null;
    if (siteId !== nextSiteId) setSiteId(nextSiteId);
    if (nextSiteId && buildingId !== nextSiteId) setBuilding(nextSiteId);
  }, [buildingId, setBuilding, siteId, sites]);

  const selectSite = (value: string) => {
    setSiteId(value);
    setBuilding(value);
  };

  const treeData = useMemo<DataNode[]>(() => {
    const site = siteQuery.data;
    if (!site) return [];
    const equipmentNodes: DataNode[] = equipment.map((item) => ({
      key: `equipment:${item.id}`,
      title: (
        <Space size={6}>
          <span>{item.displayName}</span>
          <Tag>{item.equipmentType}</Tag>
          <Tag color={lifecycleColor[item.status]}>{item.status}</Tag>
        </Space>
      ),
      icon: <BlockOutlined style={{ color: STATUS.warn }} />,
      isLeaf: true,
    }));
    const deviceNodes: DataNode[] = devices.map((item) => ({
      key: `device:${item.id}`,
      title: (
        <Space size={6}>
          <span>{item.displayName}</span>
          <Tag>{item.deviceType}</Tag>
          <Tag color={lifecycleColor[item.status]}>{item.status}</Tag>
        </Space>
      ),
      icon: <TabletOutlined style={{ color: STATUS.info }} />,
      isLeaf: true,
    }));
    return [{
      key: `site:${site.id}`,
      title: `${site.displayName} · ${site.code}`,
      icon: <ApartmentOutlined style={{ color: BRAND.teal }} />,
      children: [
        {
          key: `equipment-group:${site.id}`,
          title: `Equipment · ${equipment.length}`,
          icon: <ClusterOutlined style={{ color: STATUS.warn }} />,
          children: equipmentNodes,
        },
        {
          key: `device-group:${site.id}`,
          title: `Device · ${devices.length}`,
          icon: <ClusterOutlined style={{ color: STATUS.info }} />,
          children: deviceNodes,
        },
      ],
    }];
  }, [devices, equipment, siteQuery.data]);

  if (sitesQuery.isPending) return <LoadingState tip="正在读取授权 Site" />;
  if (sitesQuery.error) {
    return <RegistryFailureState error={sitesQuery.error} onRetry={() => void sitesQuery.refetch()} />;
  }
  if (sites.length === 0) {
    return <RegistryEmptyState description="当前账号没有可见的 Site。" />;
  }

  const selectedSite = siteQuery.data ?? sites.find((site) => site.id === siteId);
  const childLoading = Boolean(siteId) && (siteQuery.isPending || equipmentQuery.isPending || devicesQuery.isPending);

  return (
    <div className="system-tab-stack" data-testid="real-registry-system-panel">
      <OperationsSectionIntro
        title="站点与 Registry 结构"
        icon={<ApartmentOutlined />}
        meta={`${equipment.length} Equipment · ${devices.length} Device`}
        actions={(
          <Space wrap>
            <Select
              value={siteId ?? undefined}
              aria-label="选择 Site"
              onChange={selectSite}
              loading={sitesQuery.isPending}
              disabled={sitesQuery.isError || sites.length === 0}
              placeholder={sites.length === 0 ? '暂无授权 Site' : '选择 Site'}
              options={sites.map((site) => ({ value: site.id, label: site.displayName }))}
              style={{ minWidth: 190 }}
            />
            <Tooltip title="S1 仅提供 Registry 读取；写入控制已明确延期。">
              <Button icon={<PlusOutlined />} disabled>新增节点</Button>
            </Tooltip>
          </Space>
        )}
      />

      <OperationsInsightBand
        title="权威数据边界"
        icon={<ClusterOutlined />}
        items={[
          { text: 'Tenant 与 Site 身份来自当前授权的 Gateway Registry API；Site 直接携带 tenantId。', tone: 'positive' },
          { text: 'Equipment 与 Device 保持独立身份；当前状态仅表示 Registry 生命周期。', tone: 'info' },
          { text: '真实模式请求失败时会显式降级，不会替换成本地 Mock 资产。', tone: 'warning' },
        ]}
      />

      {childLoading ? (
        <LoadingState tip="正在读取 Site Registry 结构" minHeight={240} />
      ) : siteQuery.error ? (
        <RegistryFailureState error={siteQuery.error} onRetry={() => void siteQuery.refetch()} />
      ) : (
        <Row gutter={[16, 16]} className="system-equal-row">
          <Col xs={24} lg={15}>
            <Card
              variant="borderless"
              title={<OperationsPanelHeading title="Registry 结构" meta={selectedSite?.displayName} />}
            >
              {equipmentQuery.error ? (
                <RegistryFailureState error={equipmentQuery.error} compact onRetry={() => void equipmentQuery.refetch()} />
              ) : devicesQuery.error ? (
                <RegistryFailureState error={devicesQuery.error} compact onRetry={() => void devicesQuery.refetch()} />
              ) : treeData.length === 0 ? (
                <RegistryEmptyState description="该 Site 暂无 Equipment 或 Device。" />
              ) : (
                <div className="system-tree-shell">
                  <Tree showIcon defaultExpandAll treeData={treeData} />
                </div>
              )}
              <RegistryLoadMore
                hasMore={Boolean(equipmentQuery.hasNextPage)}
                loading={equipmentQuery.isFetchingNextPage}
                onLoadMore={() => void equipmentQuery.fetchNextPage()}
                label="加载更多 Equipment"
              />
              <RegistryLoadMore
                hasMore={Boolean(devicesQuery.hasNextPage)}
                loading={devicesQuery.isFetchingNextPage}
                onLoadMore={() => void devicesQuery.fetchNextPage()}
                label="加载更多 Device"
              />
            </Card>
          </Col>
          <Col xs={24} lg={9}>
            <Card
              variant="borderless"
              title={<OperationsPanelHeading title="Site 权威摘要" icon={<DatabaseOutlined />} />}
            >
              <Descriptions column={1} size="small" className="system-descriptions">
                <Descriptions.Item label="Tenant ID"><Text code copyable>{selectedSite?.tenantId ?? '—'}</Text></Descriptions.Item>
                <Descriptions.Item label="Site">{selectedSite?.displayName ?? '—'}</Descriptions.Item>
                <Descriptions.Item label="Site ID"><Text code copyable>{selectedSite?.id ?? '—'}</Text></Descriptions.Item>
                <Descriptions.Item label="IANA 时区"><Tag color="blue">{selectedSite?.timezone ?? '—'}</Tag></Descriptions.Item>
                <Descriptions.Item label="生命周期"><Tag color={lifecycleColor[selectedSite?.status ?? '']}>{selectedSite?.status ?? '—'}</Tag></Descriptions.Item>
                <Descriptions.Item label="Revision">{selectedSite?.revision ?? '—'}</Descriptions.Item>
                <Descriptions.Item label="同步模式">Gateway Registry · read-only</Descriptions.Item>
              </Descriptions>
            </Card>
          </Col>
        </Row>
      )}

      <RegistryLoadMore
        hasMore={Boolean(sitesQuery.hasNextPage)}
        loading={sitesQuery.isFetchingNextPage}
        onLoadMore={() => void sitesQuery.fetchNextPage()}
        label="加载更多 Site"
      />
    </div>
  );
}
