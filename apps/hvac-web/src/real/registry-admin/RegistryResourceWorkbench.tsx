import { useCallback, useEffect, useMemo, useState } from 'react';
import {
  Alert,
  Button,
  Card,
  Col,
  Empty,
  Form,
  Input,
  InputNumber,
  Modal,
  Row,
  Select,
  Space,
  Table,
  Tag,
  Tree,
  Typography,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import type {
  Asset,
  Capability,
  Device,
  Site,
  SiteAssetModel,
  Space as RegistrySpace,
  TelemetryPoint,
} from '@/api/generated/platformGateway.gen';
import {
  flattenRegistryPages,
  getRegistrySpaceChildren,
  presentRegistryError,
  registryAdminApi,
  useRegistryDevicePoints,
} from '@/api/registry';
import {
  confirmDiscardRegistryDraft,
} from './useDirtyGuard';
import {
  makeRegistryMutationMeta,
  newRegistryIdempotencyKey,
  type RegistryAdminResourceType,
} from './model';

interface Props {
  site: Site;
  model: SiteAssetModel;
  capabilities: ReadonlySet<Capability>;
  onDirtyChange: (dirty: boolean) => void;
  onRefresh: () => Promise<void>;
  onSiteCreated: (siteId: string) => void;
}

type ResourceEntity = Site | RegistrySpace | Asset | Device | TelemetryPoint;

type ResourceRow = {
  key: string;
  id: string;
  type: RegistryAdminResourceType;
  code: string;
  displayName: string;
  subtype: string;
  status: string;
  revision: number;
  entity: ResourceEntity;
};

type SpaceTreeNode = {
  key: string;
  title: string;
  isLeaf?: boolean;
  children?: SpaceTreeNode[];
};

const WRITE_CAPABILITY: Record<RegistryAdminResourceType, Capability> = {
  SITE: 'site.write',
  SPACE: 'space.write',
  ASSET: 'asset.write',
  DEVICE: 'device.write',
  POINT: 'point.write',
};

const RESOURCE_LABEL: Record<RegistryAdminResourceType, string> = {
  SITE: 'Site',
  SPACE: 'Space',
  ASSET: 'Asset',
  DEVICE: 'Device',
  POINT: 'Point',
};

const SPACE_TYPES = ['CAMPUS', 'BUILDING', 'FLOOR', 'ZONE', 'ROOM', 'PLANT_ROOM', 'ROOFTOP', 'OUTDOOR', 'TENANT_SPACE', 'OTHER'];
const POINT_TYPES = ['TELEMETRY', 'COUNTER', 'STATE', 'SETTING', 'COMMAND'];
const VALUE_TYPES = ['BOOLEAN', 'NUMBER', 'STRING', 'JSON'];

function toTreeNodes(spaces: RegistrySpace[]): SpaceTreeNode[] {
  return spaces.map((space) => ({ key: space.id, title: `${space.displayName} · ${space.spaceType}`, isLeaf: false }));
}

function replaceTreeChildren(nodes: SpaceTreeNode[], key: string, children: SpaceTreeNode[]): SpaceTreeNode[] {
  return nodes.map((node) => node.key === key
    ? { ...node, children, isLeaf: children.length === 0 }
    : { ...node, children: node.children ? replaceTreeChildren(node.children, key, children) : node.children });
}

function entityRows(site: Site, model: SiteAssetModel, type: RegistryAdminResourceType): ResourceRow[] {
  switch (type) {
    case 'SITE':
      return [{ key: site.id, id: site.id, type, code: site.code, displayName: site.displayName, subtype: site.timezone, status: site.status, revision: site.revision, entity: site }];
    case 'SPACE':
      return model.spaces.map((value) => ({ key: value.id, id: value.id, type, code: value.code, displayName: value.displayName, subtype: value.spaceType, status: value.status, revision: value.revision, entity: value }));
    case 'ASSET':
      return model.assets.map((value) => ({ key: value.id, id: value.id, type, code: value.code, displayName: value.displayName, subtype: value.assetType, status: value.status, revision: value.revision, entity: value }));
    case 'DEVICE':
      return model.devices.map((value) => ({ key: value.id, id: value.id, type, code: value.code, displayName: value.displayName, subtype: value.deviceType, status: value.status, revision: value.revision, entity: value }));
    case 'POINT':
      return model.telemetryPoints.map((value) => ({ key: value.id, id: value.id, type, code: value.pointCode, displayName: value.displayName, subtype: `${value.pointType} / ${value.valueType}`, status: value.status, revision: value.revision, entity: value }));
  }
}

function initialEditValues(type: RegistryAdminResourceType, entity?: ResourceEntity) {
  if (!entity) {
    return type === 'POINT'
      ? { status: 'ACTIVE', writable: false, pointType: 'TELEMETRY', valueType: 'NUMBER', sampleIntervalMs: 1000, publishIntervalMs: 5000, staleAfterMs: 15000 }
      : { status: 'ACTIVE' };
  }
  if (type === 'SITE') return { code: (entity as Site).code, displayName: entity.displayName, timezone: (entity as Site).timezone, status: entity.status };
  if (type === 'SPACE') {
    const value = entity as RegistrySpace;
    return { code: value.code, displayName: value.displayName, parentSpaceId: value.parentSpaceId ?? undefined, spaceType: value.spaceType, status: value.status };
  }
  if (type === 'ASSET') return { code: (entity as Asset).code, displayName: entity.displayName, assetType: (entity as Asset).assetType, status: entity.status };
  if (type === 'DEVICE') return { code: (entity as Device).code, displayName: entity.displayName, deviceType: (entity as Device).deviceType, status: entity.status };
  const value = entity as TelemetryPoint;
  return {
    reportingDeviceId: value.reportingDeviceId,
    sensorId: value.sensorId ?? undefined,
    pointCode: value.pointCode,
    sourceKey: value.sourceKey,
    displayName: value.displayName,
    pointType: value.pointType,
    valueType: value.valueType,
    unit: value.unit ?? undefined,
    writable: value.writable,
    sampleIntervalMs: value.sampleIntervalMs,
    publishIntervalMs: value.publishIntervalMs,
    staleAfterMs: value.staleAfterMs,
    counterDecreaseMode: value.counterDecreaseMode ?? undefined,
    counterRolloverModulus: value.counterRolloverModulus ?? undefined,
    status: value.status,
  };
}

export function RegistryResourceWorkbench({ site, model, capabilities, onDirtyChange, onRefresh, onSiteCreated }: Props) {
  const [resourceType, setResourceType] = useState<RegistryAdminResourceType>('SPACE');
  const [editTarget, setEditTarget] = useState<{ type: RegistryAdminResourceType; entity?: ResourceEntity } | null>(null);
  const [retireTarget, setRetireTarget] = useState<ResourceRow | null>(null);
  const [rebindOpen, setRebindOpen] = useState(false);
  const [editDirty, setEditDirty] = useState(false);
  const [retireDirty, setRetireDirty] = useState(false);
  const [rebindDirty, setRebindDirty] = useState(false);
  const [working, setWorking] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [resultMessage, setResultMessage] = useState<string | null>(null);
  const [treeData, setTreeData] = useState<SpaceTreeNode[]>([]);
  const [treeLoading, setTreeLoading] = useState(false);
  const [selectedDeviceId, setSelectedDeviceId] = useState<string | null>(null);
  const [editForm] = Form.useForm();
  const [retireForm] = Form.useForm();
  const [rebindForm] = Form.useForm();
  const pointsQuery = useRegistryDevicePoints(selectedDeviceId);
  const devicePoints = flattenRegistryPages(pointsQuery.data);
  const dirty = editDirty || retireDirty || rebindDirty;

  useEffect(() => onDirtyChange(dirty), [dirty, onDirtyChange]);

  const reloadRootSpaces = useCallback(async () => {
    const controller = new AbortController();
    setTreeLoading(true);
    try {
      const spaces = await getRegistrySpaceChildren(site.id, null, controller.signal);
      setTreeData(toTreeNodes(spaces));
    } catch (reason) {
      if (!controller.signal.aborted) setError(reason);
    } finally {
      if (!controller.signal.aborted) setTreeLoading(false);
    }
  }, [site.id]);

  useEffect(() => {
    void reloadRootSpaces();
  }, [reloadRootSpaces]);

  const rows = useMemo(() => entityRows(site, model, resourceType), [site, model, resourceType]);
  const writeAllowed = capabilities.has(WRITE_CAPABILITY[resourceType]);
  const retireAllowed = capabilities.has('registry.retire');
  const bindingAllowed = capabilities.has('binding.write');

  const closeEdit = () => {
    if (!confirmDiscardRegistryDraft(editDirty)) return;
    setEditTarget(null);
    setEditDirty(false);
    editForm.resetFields();
  };
  const closeRetire = () => {
    if (!confirmDiscardRegistryDraft(retireDirty)) return;
    setRetireTarget(null);
    setRetireDirty(false);
    retireForm.resetFields();
  };
  const closeRebind = () => {
    if (!confirmDiscardRegistryDraft(rebindDirty)) return;
    setRebindOpen(false);
    setRebindDirty(false);
    rebindForm.resetFields();
  };

  const openEditor = (type: RegistryAdminResourceType, entity?: ResourceEntity) => {
    setError(null);
    setEditTarget({ type, entity });
    setEditDirty(false);
    editForm.setFieldsValue({ ...initialEditValues(type, entity), reason: entity ? `更新 ${RESOURCE_LABEL[type]}` : `创建 ${RESOURCE_LABEL[type]}` });
  };

  const runEdit = async () => {
    if (!editTarget) return;
    const values = await editForm.validateFields();
    const { type, entity } = editTarget;
    const expectedRevision = entity?.revision ?? 0;
    const meta = makeRegistryMutationMeta(expectedRevision, values.reason, newRegistryIdempotencyKey(`${type.toLowerCase()}-${entity ? 'update' : 'create'}`));
    setWorking(true);
    setError(null);
    try {
      let createdSiteId: string | null = null;
      if (type === 'SITE') {
        const body = { code: values.code, displayName: values.displayName, timezone: values.timezone, status: values.status, meta };
        const saved = entity ? await registryAdminApi.updateSite(entity.id, body) : await registryAdminApi.createSite(body);
        createdSiteId = entity ? null : saved.id;
      } else if (type === 'SPACE') {
        const body = { parentSpaceId: values.parentSpaceId || undefined, code: values.code, displayName: values.displayName, spaceType: values.spaceType, status: values.status, meta };
        if (entity) await registryAdminApi.updateSpace(site.id, entity.id, body);
        else await registryAdminApi.createSpace(site.id, body);
      } else if (type === 'ASSET') {
        const body = { code: values.code, displayName: values.displayName, assetType: values.assetType, status: values.status, meta };
        if (entity) await registryAdminApi.updateAsset(site.id, entity.id, body);
        else await registryAdminApi.createAsset(site.id, body);
      } else if (type === 'DEVICE') {
        const body = { code: values.code, displayName: values.displayName, deviceType: values.deviceType, status: values.status, meta };
        if (entity) await registryAdminApi.updateDevice(site.id, entity.id, body);
        else await registryAdminApi.createDevice(site.id, body);
      } else {
        const body = {
          reportingDeviceId: values.reportingDeviceId,
          sensorId: values.sensorId || undefined,
          pointCode: values.pointCode,
          sourceKey: values.sourceKey,
          displayName: values.displayName,
          pointType: values.pointType,
          valueType: values.valueType,
          unit: values.unit || undefined,
          writable: Boolean(values.writable),
          sampleIntervalMs: values.sampleIntervalMs,
          publishIntervalMs: values.publishIntervalMs,
          staleAfterMs: values.staleAfterMs,
          counterDecreaseMode: values.pointType === 'COUNTER' ? values.counterDecreaseMode : undefined,
          counterRolloverModulus: values.pointType === 'COUNTER' && values.counterDecreaseMode === 'ROLLOVER' ? values.counterRolloverModulus : undefined,
          sourceMetadata: entity ? (entity as TelemetryPoint).sourceMetadata : {},
          status: values.status,
          meta,
        };
        if (entity) await registryAdminApi.updatePoint(site.id, entity.id, body);
        else await registryAdminApi.createPoint(site.id, body);
      }
      setResultMessage(`${RESOURCE_LABEL[type]} ${entity ? '更新' : '创建'}成功。`);
      setEditTarget(null);
      setEditDirty(false);
      editForm.resetFields();
      await Promise.all([onRefresh(), reloadRootSpaces()]);
      if (createdSiteId) onSiteCreated(createdSiteId);
    } catch (reason) {
      setError(reason);
    } finally {
      setWorking(false);
    }
  };

  const runRetire = async () => {
    if (!retireTarget) return;
    const values = await retireForm.validateFields();
    setWorking(true);
    setError(null);
    try {
      const result = await registryAdminApi.retire(site.id, {
        resourceType: retireTarget.type,
        resourceId: retireTarget.id,
        meta: makeRegistryMutationMeta(retireTarget.revision, values.reason, newRegistryIdempotencyKey('registry-retire')),
      });
      setResultMessage(result.status === 'COMPLETED'
        ? `${RESOURCE_LABEL[retireTarget.type]} 已完成退役。`
        : `退役被 ${result.dependencyCount} 个活动依赖阻塞；未执行硬删除。`);
      setRetireTarget(null);
      setRetireDirty(false);
      retireForm.resetFields();
      await Promise.all([onRefresh(), reloadRootSpaces()]);
    } catch (reason) {
      setError(reason);
    } finally {
      setWorking(false);
    }
  };

  const runRebind = async () => {
    const values = await rebindForm.validateFields();
    setWorking(true);
    setError(null);
    try {
      await registryAdminApi.rebind(site.id, {
        kind: values.kind,
        sourceId: values.sourceId,
        targetId: values.targetId,
        targetType: values.targetType || undefined,
        role: values.role,
        effectiveAt: new Date().toISOString(),
        meta: makeRegistryMutationMeta(0, values.reason, newRegistryIdempotencyKey('registry-rebind')),
      });
      setResultMessage('旧绑定区间已关闭，并创建了新的 typed binding 区间。');
      setRebindOpen(false);
      setRebindDirty(false);
      rebindForm.resetFields();
      await Promise.all([onRefresh(), reloadRootSpaces()]);
    } catch (reason) {
      setError(reason);
    } finally {
      setWorking(false);
    }
  };

  const loadTreeNode = async (node: SpaceTreeNode) => {
    if (node.children) return;
    const controller = new AbortController();
    try {
      const children = await getRegistrySpaceChildren(site.id, node.key, controller.signal);
      setTreeData((current) => replaceTreeChildren(current, node.key, toTreeNodes(children)));
    } catch (reason) {
      setError(reason);
    }
  };

  const columns: ColumnsType<ResourceRow> = [
    { title: '名称', dataIndex: 'displayName', render: (value, row) => <Space direction="vertical" size={0}><Typography.Text strong>{value}</Typography.Text><Typography.Text type="secondary" code>{row.code}</Typography.Text></Space> },
    { title: '类型', dataIndex: 'subtype', width: 190 },
    { title: '状态', dataIndex: 'status', width: 110, render: (value) => <Tag color={value === 'ACTIVE' ? 'green' : 'default'}>{value}</Tag> },
    { title: 'Revision', dataIndex: 'revision', width: 90 },
    {
      title: '操作', width: 190, render: (_, row) => <Space>
        <Button type="link" disabled={!capabilities.has(WRITE_CAPABILITY[row.type]) || row.status === 'RETIRED'} onClick={() => openEditor(row.type, row.entity)}>编辑</Button>
        <Button type="link" danger disabled={!retireAllowed || row.status === 'RETIRED'} onClick={() => { setRetireTarget(row); setRetireDirty(false); retireForm.setFieldsValue({ reason: `退役 ${RESOURCE_LABEL[row.type]} ${row.displayName}` }); }}>退役</Button>
        {row.type === 'DEVICE' ? <Button type="link" onClick={() => setSelectedDeviceId(row.id)}>Points</Button> : null}
      </Space>,
    },
  ];

  const presentedError = error ? presentRegistryError(error) : null;
  const pointListError = pointsQuery.error ? presentRegistryError(pointsQuery.error) : null;
  const editPointType = Form.useWatch('pointType', editForm);
  const editCounterDecreaseMode = Form.useWatch('counterDecreaseMode', editForm);
  const rebindKind = Form.useWatch('kind', rebindForm);
  const rebindRoles = rebindKind === 'DEVICE_ASSET' ? ['CONTROLLER', 'METER', 'SENSOR', 'GATEWAY', 'SUPERVISORY_CONTROLLER']
    : rebindKind === 'ASSET_SPACE' ? ['INSTALLED_IN', 'SERVES']
      : rebindKind === 'DEVICE_SPACE' ? ['INSTALLED_IN', 'SERVES', 'GATEWAY_FOR', 'SUPERVISES']
        : ['DESCRIBES', 'CONTROLS'];

  return <Space direction="vertical" size={16} style={{ width: '100%' }}>
    {presentedError ? <Alert type="error" showIcon message={presentedError.title} description={`${presentedError.description}${presentedError.traceId ? ` Trace ${presentedError.traceId}` : ''}`} closable onClose={() => setError(null)} /> : null}
    {resultMessage ? <Alert type="success" showIcon message={resultMessage} closable onClose={() => setResultMessage(null)} /> : null}
    <Row gutter={[16, 16]}>
      <Col xs={24} xl={8}>
        <Card title="Space hierarchy" extra={<Tag>cursor / lazy</Tag>} loading={treeLoading}>
          {treeData.length ? <Tree treeData={treeData} loadData={(node) => loadTreeNode(node as SpaceTreeNode)} /> : <Empty description="当前 Site 没有 Space" />}
        </Card>
      </Col>
      <Col xs={24} xl={16}>
        <Card
          title="Registry resources"
          extra={<Space>
            <Select value={resourceType} style={{ width: 120 }} onChange={(value) => setResourceType(value)} options={Object.keys(RESOURCE_LABEL).map((value) => ({ value, label: RESOURCE_LABEL[value as RegistryAdminResourceType] }))} />
            <Button type="primary" disabled={!writeAllowed} onClick={() => openEditor(resourceType)}>创建 {RESOURCE_LABEL[resourceType]}</Button>
            <Button disabled={!bindingAllowed} onClick={() => { setRebindOpen(true); setRebindDirty(false); rebindForm.setFieldsValue({ kind: 'DEVICE_ASSET', role: 'CONTROLLER', reason: '调整 Registry typed binding' }); }}>重绑</Button>
          </Space>}
        >
          <Table rowKey="key" columns={columns} dataSource={rows} pagination={{ pageSize: 8 }} scroll={{ x: 760 }} />
        </Card>
      </Col>
    </Row>

    {selectedDeviceId ? <Card title="Device Points" extra={<Button type="link" onClick={() => setSelectedDeviceId(null)}>关闭</Button>}>
      {pointListError ? <Alert type="error" showIcon message={pointListError.title} description={pointListError.description} style={{ marginBottom: 12 }} /> : null}
      <Table<TelemetryPoint>
        rowKey="id"
        loading={pointsQuery.isLoading}
        dataSource={devicePoints}
        pagination={false}
        columns={[
          { title: 'Point', dataIndex: 'displayName', render: (value, row) => <Space direction="vertical" size={0}><Typography.Text strong>{value}</Typography.Text><Typography.Text code>{row.pointCode}</Typography.Text></Space> },
          { title: 'Source Key', dataIndex: 'sourceKey' },
          { title: 'Type', render: (_, row) => `${row.pointType} / ${row.valueType}` },
          { title: 'Writable', dataIndex: 'writable', width: 90, render: (value) => value ? '是' : '否' },
          { title: 'Revision', dataIndex: 'revision', width: 90 },
        ]}
      />
      {pointsQuery.hasNextPage ? <Button onClick={() => pointsQuery.fetchNextPage()} loading={pointsQuery.isFetchingNextPage}>加载下一页</Button> : null}
    </Card> : null}

    <Modal title={`${editTarget?.entity ? '编辑' : '创建'} ${editTarget ? RESOURCE_LABEL[editTarget.type] : ''}`} open={Boolean(editTarget)} onCancel={closeEdit} onOk={runEdit} confirmLoading={working} maskClosable={false} keyboard={false} destroyOnHidden>
      <Form
        form={editForm}
        layout="vertical"
        onValuesChange={(changedValues) => {
          setEditDirty(true);
          if ('pointType' in changedValues) {
            if (changedValues.pointType === 'COUNTER') {
              editForm.setFieldsValue({ valueType: 'NUMBER', writable: false, counterDecreaseMode: editForm.getFieldValue('counterDecreaseMode') ?? 'RESET_TO_ZERO' });
            } else if (changedValues.pointType === 'COMMAND') {
              editForm.setFieldsValue({ writable: true, counterDecreaseMode: undefined, counterRolloverModulus: undefined });
            } else {
              editForm.setFieldsValue({ writable: false, counterDecreaseMode: undefined, counterRolloverModulus: undefined });
            }
          }
          if (changedValues.counterDecreaseMode && changedValues.counterDecreaseMode !== 'ROLLOVER') {
            editForm.setFieldValue('counterRolloverModulus', undefined);
          }
        }}
      >
        {editTarget?.type === 'SITE' ? <>
          <Form.Item name="code" label="Code" rules={[{ required: true }]}><Input /></Form.Item>
          <Form.Item name="displayName" label="Display Name" rules={[{ required: true }]}><Input /></Form.Item>
          <Form.Item name="timezone" label="Timezone" rules={[{ required: true }]}><Input placeholder="Asia/Shanghai" /></Form.Item>
        </> : null}
        {editTarget?.type === 'SPACE' ? <>
          <Form.Item name="code" label="Code" rules={[{ required: true }]}><Input /></Form.Item>
          <Form.Item name="displayName" label="Display Name" rules={[{ required: true }]}><Input /></Form.Item>
          <Form.Item name="spaceType" label="Space Type" rules={[{ required: true }]}><Select options={SPACE_TYPES.map((value) => ({ value }))} /></Form.Item>
          <Form.Item name="parentSpaceId" label="Parent Space"><Select allowClear showSearch optionFilterProp="label" options={model.spaces.filter((space) => space.id !== editTarget.entity?.id).map((space) => ({ value: space.id, label: `${space.displayName} · ${space.code}` }))} /></Form.Item>
        </> : null}
        {editTarget?.type === 'ASSET' ? <>
          <Form.Item name="code" label="Code" rules={[{ required: true }]}><Input /></Form.Item>
          <Form.Item name="displayName" label="Display Name" rules={[{ required: true }]}><Input /></Form.Item>
          <Form.Item name="assetType" label="Asset Type" rules={[{ required: true }]}><Input /></Form.Item>
        </> : null}
        {editTarget?.type === 'DEVICE' ? <>
          <Form.Item name="code" label="Code" rules={[{ required: true }]}><Input /></Form.Item>
          <Form.Item name="displayName" label="Display Name" rules={[{ required: true }]}><Input /></Form.Item>
          <Form.Item name="deviceType" label="Device Type" rules={[{ required: true }]}><Input /></Form.Item>
        </> : null}
        {editTarget?.type === 'POINT' ? <>
          <Form.Item name="reportingDeviceId" label="Reporting Device" rules={[{ required: true }]}><Select showSearch optionFilterProp="label" options={model.devices.map((device) => ({ value: device.id, label: `${device.displayName} · ${device.code}` }))} /></Form.Item>
          <Form.Item name="sensorId" label="Physical Sensor (optional)"><Select allowClear showSearch optionFilterProp="label" options={model.sensors.map((sensor) => ({ value: sensor.id, label: `${sensor.displayName} · ${sensor.code}` }))} /></Form.Item>
          <Form.Item name="pointCode" label="Point Code" rules={[{ required: true }]}><Input /></Form.Item>
          <Form.Item name="sourceKey" label="Source Key" rules={[{ required: true }]}><Input /></Form.Item>
          <Form.Item name="displayName" label="Display Name" rules={[{ required: true }]}><Input /></Form.Item>
          <Row gutter={12}><Col span={12}><Form.Item name="pointType" label="Point Type" rules={[{ required: true }]}><Select options={POINT_TYPES.map((value) => ({ value }))} /></Form.Item></Col><Col span={12}><Form.Item name="valueType" label="Value Type" rules={[{ required: true }]}><Select disabled={editPointType === 'COUNTER'} options={VALUE_TYPES.map((value) => ({ value }))} /></Form.Item></Col></Row>
          {editPointType === 'COUNTER' ? <Row gutter={12}>
            <Col span={12}><Form.Item name="counterDecreaseMode" label="Counter decrease" rules={[{ required: true }]}><Select options={['RESET_TO_ZERO', 'ROLLOVER', 'INVALID'].map((value) => ({ value }))} /></Form.Item></Col>
            <Col span={12}><Form.Item name="counterRolloverModulus" label="Rollover modulus" rules={editCounterDecreaseMode === 'ROLLOVER' ? [{ required: true }, { type: 'number', min: Number.MIN_VALUE }] : []}><InputNumber disabled={editCounterDecreaseMode !== 'ROLLOVER'} min={Number.MIN_VALUE} style={{ width: '100%' }} /></Form.Item></Col>
          </Row> : null}
          <Form.Item name="unit" label="Unit"><Input /></Form.Item>
          <Row gutter={12}>
            <Col span={8}><Form.Item name="sampleIntervalMs" label="Sample ms" rules={[{ required: true }]}><InputNumber min={100} style={{ width: '100%' }} /></Form.Item></Col>
            <Col span={8}><Form.Item name="publishIntervalMs" label="Publish ms" dependencies={['sampleIntervalMs']} rules={[{ required: true }, ({ getFieldValue }) => ({ validator: (_, value) => value >= getFieldValue('sampleIntervalMs') ? Promise.resolve() : Promise.reject(new Error('Publish 必须不小于 Sample')) })]}><InputNumber min={100} style={{ width: '100%' }} /></Form.Item></Col>
            <Col span={8}><Form.Item name="staleAfterMs" label="Stale ms" dependencies={['publishIntervalMs']} rules={[{ required: true }, ({ getFieldValue }) => ({ validator: (_, value) => value >= getFieldValue('publishIntervalMs') ? Promise.resolve() : Promise.reject(new Error('Stale 必须不小于 Publish')) })]}><InputNumber min={100} style={{ width: '100%' }} /></Form.Item></Col>
          </Row>
          <Form.Item name="writable" label="Writable"><Select disabled={editPointType !== 'SETTING'} options={[{ value: false, label: 'No' }, { value: true, label: 'Yes' }]} /></Form.Item>
        </> : null}
        <Form.Item name="status" label="Status" rules={[{ required: true }]}><Select options={['ACTIVE', 'INACTIVE'].map((value) => ({ value }))} /></Form.Item>
        <Form.Item name="reason" label="变更原因" rules={[{ required: true }]}><Input.TextArea rows={2} /></Form.Item>
        {editTarget?.entity ? <Alert type="info" showIcon message={`Expected Revision = ${editTarget.entity.revision}`} description="如果服务器 Revision 已变化，本次写入会以 409 冲突失败，不会覆盖新数据。" /> : null}
      </Form>
    </Modal>

    <Modal title={`退役 ${retireTarget ? RESOURCE_LABEL[retireTarget.type] : ''}`} open={Boolean(retireTarget)} onCancel={closeRetire} onOk={runRetire} confirmLoading={working} okButtonProps={{ danger: true }} maskClosable={false} keyboard={false} destroyOnHidden>
      <Alert type="warning" showIcon message="退役通过 dependency-aware saga 执行" description="不会暴露硬删除。存在活动依赖时，服务器返回 BLOCKED 并保留权威数据。" />
      <Form form={retireForm} layout="vertical" onValuesChange={() => setRetireDirty(true)} style={{ marginTop: 16 }}>
        <Form.Item name="reason" label="退役原因" rules={[{ required: true }]}><Input.TextArea rows={3} /></Form.Item>
      </Form>
    </Modal>

    <Modal title="Typed Binding Rebind" open={rebindOpen} onCancel={closeRebind} onOk={runRebind} confirmLoading={working} maskClosable={false} keyboard={false} destroyOnHidden>
      <Form form={rebindForm} layout="vertical" onValuesChange={() => setRebindDirty(true)}>
        <Form.Item name="kind" label="Binding Kind" rules={[{ required: true }]}><Select options={['DEVICE_ASSET', 'ASSET_SPACE', 'DEVICE_SPACE', 'POINT_SUBJECT'].map((value) => ({ value }))} /></Form.Item>
        {rebindKind === 'POINT_SUBJECT' ? <Form.Item name="targetType" label="Target Type" rules={[{ required: true }]}><Select options={['SITE', 'SPACE', 'ASSET'].map((value) => ({ value }))} /></Form.Item> : null}
        <Form.Item name="sourceId" label="Source ID" rules={[{ required: true }]}><Input /></Form.Item>
        <Form.Item name="targetId" label="Target ID" rules={[{ required: true }]}><Input /></Form.Item>
        <Form.Item name="role" label="Role" rules={[{ required: true }]}><Select options={rebindRoles.map((value) => ({ value }))} /></Form.Item>
        <Form.Item name="reason" label="变更原因" rules={[{ required: true }]}><Input.TextArea rows={2} /></Form.Item>
      </Form>
    </Modal>
  </Space>;
}
