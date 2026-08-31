import type { HTMLAttributes } from 'react';
import { EyeOutlined } from '@ant-design/icons';
import { Button, Empty, Space, Table, Tag, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import type { RealAssetsAssetRow } from './model';

export function AssetTable({
  rows,
  selectedAssetId,
  onOpen,
  registerTrigger,
}: {
  readonly rows: readonly RealAssetsAssetRow[];
  readonly selectedAssetId: string | null;
  readonly onOpen: (assetId: string) => void;
  readonly registerTrigger: (assetId: string, node: HTMLElement | null) => void;
}) {
  const columns: ColumnsType<RealAssetsAssetRow> = [
    {
      title: 'Asset',
      key: 'asset',
      fixed: 'left',
      width: 250,
      render: (_, row) => (
        <Button
          type="link"
          data-testid="real-assets-open-asset"
          aria-haspopup="dialog"
          aria-expanded={selectedAssetId === row.asset.id}
          ref={(node) => registerTrigger(row.asset.id, node)}
          onClick={() => onOpen(row.asset.id)}
        >
          <Space direction="vertical" size={0} align="start">
            <Typography.Text strong>{row.asset.displayName}</Typography.Text>
            <Typography.Text type="secondary" style={{ fontSize: 12 }}>{row.asset.code} · {row.asset.assetType}</Typography.Text>
          </Space>
        </Button>
      ),
    },
    { title: '区域', key: 'space', width: 160, render: (_, row) => row.space.state === 'bound' ? row.space.space.displayName : '未绑定 Space' },
    {
      title: 'Device 状态',
      key: 'state',
      width: 190,
      render: (_, row) => (
        <Space direction="vertical" size={0}>
          <Typography.Text>{row.offlineDeviceCount} 离线 · {row.dataIssueDeviceCount} 数据异常</Typography.Text>
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>{row.connectionUnknownDeviceCount} 连接未知</Typography.Text>
        </Space>
      ),
    },
    { title: '设备', key: 'devices', width: 140, render: (_, row) => <Typography.Text>{row.devices.length} 台</Typography.Text> },
    {
      title: '数据摘要',
      key: 'data',
      width: 190,
      render: (_, row) => (
        <Typography.Text type={row.dataIssueDeviceCount > 0 ? 'warning' : 'secondary'}>
          {row.dataIssueDeviceCount > 0 ? `${row.dataIssueDeviceCount} 台数据需关注` : '子设备数据无异常'}
        </Typography.Text>
      ),
    },
    { title: '控制能力', key: 'controls', width: 120, render: (_, row) => <Tag color={row.controlPoints.length > 0 ? 'blue' : undefined}>{row.controlPoints.length > 0 ? `${row.controlPoints.length} 项` : '无'}</Tag> },
    { title: '操作', key: 'action', fixed: 'right', width: 100, render: (_, row) => <Button size="small" type="primary" ghost icon={<EyeOutlined />} onClick={() => onOpen(row.asset.id)}>详情</Button> },
  ];

  return (
    <Table<RealAssetsAssetRow>
      rowKey={(row) => row.asset.id}
      size="middle"
      columns={columns}
      dataSource={[...rows]}
      pagination={{ pageSize: 10, showSizeChanger: false, showTotal: (total) => `共 ${total} 个 Asset` }}
      scroll={{ x: 1150 }}
      locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="当前层级或筛选条件没有匹配的 Asset。" /> }}
      onRow={(row) => ({ 'data-asset-id': row.asset.id } as HTMLAttributes<HTMLTableRowElement>)}
    />
  );
}
