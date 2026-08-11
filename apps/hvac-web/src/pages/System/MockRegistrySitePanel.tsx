import { useEffect, useMemo, useState } from 'react';
import { Button, Card, Col, Form, Modal, Row, Select, Tree, Typography, message } from 'antd';
import { ProDescriptions, ProForm, ProFormSelect, ProFormText } from '@ant-design/pro-components';
import type { DataNode } from 'antd/es/tree';
import {
  ApartmentOutlined,
  BlockOutlined,
  ClusterOutlined,
  DatabaseOutlined,
  PlusOutlined,
  SafetyCertificateOutlined,
} from '@ant-design/icons';
import {
  OperationsActionFooter,
  OperationsDetailHeader,
  OperationsInsightBand,
  OperationsPanelHeading,
  OperationsSectionIntro,
} from '@/components/OperationsUI';
import { mockAssetTree, mockSites, type AssetNode } from '@/mock/system';
import { BRAND, STATUS } from '@/theme/tokens';

const { Text } = Typography;
let nodeSequence = 100;

type AssetNodeFormValues = {
  parent: string;
  type: AssetNode['type'];
  name: string;
};

function insertNode(nodes: AssetNode[], parentKey: string | null, node: AssetNode): AssetNode[] {
  if (parentKey === null) return [...nodes, node];
  return nodes.map((item) => {
    if (item.key === parentKey) return { ...item, children: [...(item.children ?? []), node] };
    if (item.children) return { ...item, children: insertNode(item.children, parentKey, node) };
    return item;
  });
}

const flattenAssets = (nodes: AssetNode[]): AssetNode[] =>
  nodes.flatMap((node) => [node, ...(node.children ? flattenAssets(node.children) : [])]);

export default function MockRegistrySitePanel() {
  const [tree, setTree] = useState<AssetNode[]>(mockAssetTree);
  const [siteId, setSiteId] = useState(mockSites[0].id);
  const [assetModal, setAssetModal] = useState(false);
  const [assetForm] = Form.useForm<AssetNodeFormValues>();
  const flatAssetNodes = useMemo(() => flattenAssets(tree), [tree]);

  useEffect(() => {
    if (!assetModal) return undefined;
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setAssetModal(false);
    };
    window.addEventListener('keydown', closeOnEscape, true);
    return () => window.removeEventListener('keydown', closeOnEscape, true);
  }, [assetModal]);

  const treeData = useMemo(() => {
    const icon = (type: AssetNode['type']) =>
      type === 'building' ? <ApartmentOutlined style={{ color: BRAND.teal }} />
        : type === 'zone' ? <ClusterOutlined style={{ color: STATUS.info }} />
          : <BlockOutlined style={{ color: STATUS.warn }} />;
    const walk = (nodes: AssetNode[]): DataNode[] => nodes.map((node) => ({
      key: node.key,
      title: node.title,
      icon: icon(node.type),
      children: node.children ? walk(node.children) : undefined,
    }));
    return walk(tree);
  }, [tree]);

  const flatNodes = useMemo(() => {
    const output: { key: string; title: string }[] = [];
    const walk = (nodes: AssetNode[], depth: number) => nodes.forEach((node) => {
      output.push({ key: node.key, title: `${'　'.repeat(depth)}${node.title}` });
      if (node.children) walk(node.children, depth + 1);
    });
    walk(tree, 0);
    return output;
  }, [tree]);

  const openAddAsset = () => {
    assetForm.resetFields();
    assetForm.setFieldsValue({ parent: 'root', type: 'building' });
    setAssetModal(true);
  };

  const submitAsset = () => {
    assetForm.validateFields().then((values) => {
      Modal.confirm({
        title: '二次确认（人在回路）',
        icon: <SafetyCertificateOutlined style={{ color: BRAND.teal }} />,
        content: `确认在「${values.parent === 'root' ? '根' : values.parent}」下新增${values.type === 'building' ? '建筑' : values.type === 'zone' ? '分区' : '机组'}「${values.name}」？`,
        okText: '确认提交',
        cancelText: '取消',
        onOk: () => {
          const parentKey = values.parent === 'root' ? null : values.parent;
          const node: AssetNode = { key: `n${++nodeSequence}`, title: values.name, type: values.type };
          setTree((current) => insertNode(current, parentKey, node));
          message.success('节点已新增（mock）');
          setAssetModal(false);
        },
      });
    });
  };

  return (
    <div className="system-tab-stack" data-testid="mock-registry-system-panel">
      <OperationsSectionIntro
        title="站点与资产拓扑"
        icon={<ApartmentOutlined />}
        meta={`${flatAssetNodes.length} 个节点`}
        actions={(
          <>
            <Select
              value={siteId}
              aria-label="选择站点"
              onChange={setSiteId}
              options={mockSites.map((site) => ({ value: site.id, label: site.name }))}
            />
            <Button type="primary" icon={<PlusOutlined />} onClick={openAddAsset}>新增节点</Button>
          </>
        )}
      />

      <OperationsInsightBand
        title="结构变更"
        icon={<ClusterOutlined />}
        items={[
          { text: '新增节点当前仅写入本地 mock 树。', tone: 'warning' },
          { text: '生产写入必须校验父子类型、唯一名称与资源权限。', tone: 'info' },
          { text: '成功变更后应触发资产同步并生成审计记录。', tone: 'positive' },
        ]}
      />

      <Row gutter={[16, 16]} className="system-equal-row">
        <Col xs={24} lg={15}>
          <Card
            variant="borderless"
            title={<OperationsPanelHeading title="资产结构" meta={mockSites.find((site) => site.id === siteId)?.name} />}
          >
            <div className="system-tree-shell">
              <Tree showIcon defaultExpandAll treeData={treeData} />
            </div>
          </Card>
        </Col>
        <Col xs={24} lg={9}>
          <Card
            variant="borderless"
            title={<OperationsPanelHeading title="站点配置摘要" icon={<DatabaseOutlined />} />}
          >
            <ProDescriptions
              column={1}
              size="small"
              className="system-descriptions"
              columns={[
                { title: '当前站点', key: 'site', renderText: () => mockSites.find((site) => site.id === siteId)?.name ?? '—' },
                { title: '站点 ID', key: 'siteId', render: () => <Text code>{siteId}</Text> },
                { title: '资产节点', key: 'assetCount', renderText: () => flatAssetNodes.length },
                { title: '同步模式', key: 'syncMode', renderText: () => 'Mock Tree' },
                { title: '目标接口', key: 'endpoint', render: () => <Text code>GET /assets/tree</Text> },
                { title: '写入策略', key: 'writePolicy', renderText: () => '二次确认 + 审计日志' },
              ]}
            />
          </Card>
        </Col>
      </Row>

      <Modal
        open={assetModal}
        title={(
          <OperationsDetailHeader
            eyebrow="站点与资产拓扑"
            title="新增资产节点"
            subtitle="选择父节点、资源类型和名称，提交后进入结构变更确认。"
          />
        )}
        width={620}
        className="ops-detail-modal system-governance-modal"
        onCancel={() => setAssetModal(false)}
        footer={(
          <OperationsActionFooter note="结构变更当前仅写入 mock 资产树，生产环境必须同步审计日志。">
            <Button onClick={() => setAssetModal(false)}>取消</Button>
            <Button type="primary" onClick={submitAsset}>审阅并确认</Button>
          </OperationsActionFooter>
        )}
        destroyOnHidden
        forceRender
      >
        <div className="system-modal-note">
          <ClusterOutlined />
          <span>资产节点会影响遥测归属、诊断对象和权限范围。提交后还会进行二次确认。</span>
        </div>
        <ProForm<AssetNodeFormValues> form={assetForm} layout="vertical" submitter={false}>
          <ProFormSelect
            name="parent"
            label="父节点"
            rules={[{ required: true }]}
            options={[{ value: 'root', label: '根（顶级建筑）' }, ...flatNodes.map((node) => ({ value: node.key, label: node.title }))]}
          />
          <ProFormSelect
            name="type"
            label="节点类型"
            rules={[{ required: true }]}
            options={[{ value: 'building', label: '建筑' }, { value: 'zone', label: '分区' }, { value: 'unit', label: '机组/设备' }]}
          />
          <ProFormText name="name" label="名称" rules={[{ required: true, message: '请输入名称' }]} placeholder="如 冷水机组 #3" />
        </ProForm>
      </Modal>
    </div>
  );
}
