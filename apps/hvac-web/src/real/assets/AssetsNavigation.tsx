import type { Key } from 'react';
import { Tree } from 'antd';
import type { DataNode } from 'antd/es/tree';
import { ApartmentOutlined, BlockOutlined, ClusterOutlined, RightOutlined, TabletOutlined } from '@ant-design/icons';
import type { RealAssetsHierarchyNode } from './model';

const HIERARCHY_ICONS = {
  site: <ApartmentOutlined />,
  space: <ClusterOutlined />,
  asset: <BlockOutlined />,
  device: <TabletOutlined />,
} as const;

function hierarchyDataNode(node: RealAssetsHierarchyNode): DataNode {
  return {
    key: node.key,
    icon: HIERARCHY_ICONS[node.kind],
    title: (
      <span
        className="real-assets-tree-node"
        data-testid={node.kind === 'site'
          ? 'real-assets-hierarchy-site'
          : node.kind === 'asset' && node.key.startsWith('asset:unbound:')
            ? 'real-assets-hierarchy-unbound'
            : `real-assets-hierarchy-${node.kind}`}
        data-asset-kind={node.kind}
        data-asset-id={node.kind === 'asset' && !node.key.includes('unbound:') ? node.key.slice('asset:'.length) : undefined}
        data-device-id={node.kind === 'device' ? node.deviceIds[0] : undefined}
        title={`${node.label}｜${node.meta}`}
      >
        {node.label}
      </span>
    ),
    children: node.children.map(hierarchyDataNode),
  };
}

export function AssetsNavigation({
  siteId,
  root,
  selectedKey,
  onSelect,
}: {
  readonly siteId: string;
  readonly root: RealAssetsHierarchyNode;
  readonly selectedKey: string;
  readonly onSelect: (key: string) => void;
}) {
  const expandedKeys = [
    root.key,
    ...root.children.filter((node) => node.kind === 'space').map((node) => node.key),
  ];
  return (
    <div className="assets-hierarchy-card__scroll" role="navigation" aria-label="资产层级导航">
      <Tree
        key={siteId}
        className="real-assets-navigation-tree"
        showIcon
        blockNode
        defaultExpandedKeys={expandedKeys}
        switcherIcon={({ isLeaf }) => isLeaf ? null : <RightOutlined className="real-assets-tree-switcher" />}
        treeData={[hierarchyDataNode(root)]}
        selectedKeys={[selectedKey]}
        onSelect={(keys: Key[]) => onSelect(String(keys[0] ?? `site:${siteId}`))}
      />
    </div>
  );
}
