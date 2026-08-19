import { useEffect, useMemo, useState } from 'react';
import { Alert, Button, Card, Col, Form, Input, List, Row, Select, Space, Tag, Typography } from 'antd';
import type { Capability, Site, SiteAssetModel, TemplateAssignment, TemplateKind, TemplateRevision } from '@/api/generated/platformGateway.gen';
import { presentRegistryError, registryAdminApi } from '@/api/registry';
import { makeRegistryMutationMeta, newRegistryIdempotencyKey } from './model';

interface Props {
  site: Site;
  model: SiteAssetModel;
  capabilities: ReadonlySet<Capability>;
  onDirtyChange: (dirty: boolean) => void;
}

function parseObject(source: string, label: string): Record<string, unknown> {
  const parsed: unknown = JSON.parse(source);
  if (!parsed || Array.isArray(parsed) || typeof parsed !== 'object') throw new Error(`${label} 必须是 JSON object。`);
  return parsed as Record<string, unknown>;
}

function parseStringMap(source: string): Record<string, string> {
  const value = parseObject(source, 'Release References');
  const entries = Object.entries(value);
  if (!entries.length || entries.some(([, item]) => typeof item !== 'string' || !item.trim())) throw new Error('Release References 必须包含至少一个非空字符串值。');
  return Object.fromEntries(entries) as Record<string, string>;
}

export function RegistryTemplateWorkbench({ site, model, capabilities, onDirtyChange }: Props) {
  const [releaseForm] = Form.useForm();
  const [assignmentForm] = Form.useForm();
  const [releaseDirty, setReleaseDirty] = useState(false);
  const [assignmentDirty, setAssignmentDirty] = useState(false);
  const [working, setWorking] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [releases, setReleases] = useState<TemplateRevision[]>([]);
  const [assignments, setAssignments] = useState<TemplateAssignment[]>([]);
  const allowed = capabilities.has('template.manage');
  const targetType = Form.useWatch('targetType', assignmentForm) as TemplateKind | undefined;

  useEffect(() => onDirtyChange(releaseDirty || assignmentDirty), [releaseDirty, assignmentDirty, onDirtyChange]);

  const targets = useMemo(() => {
    if (targetType === 'ASSET') return model.assets.map((value) => ({ value: value.id, label: `${value.displayName} · ${value.code}` }));
    if (targetType === 'DEVICE') return model.devices.map((value) => ({ value: value.id, label: `${value.displayName} · ${value.code}` }));
    if (targetType === 'POINT') return model.telemetryPoints.map((value) => ({ value: value.id, label: `${value.displayName} · ${value.pointCode}` }));
    return [];
  }, [model, targetType]);

  const releaseTemplate = async () => {
    const values = await releaseForm.validateFields();
    setWorking(true);
    setError(null);
    try {
      const released = await registryAdminApi.releaseTemplate({
        templateKey: values.templateKey,
        templateKind: values.templateKind,
        payload: parseObject(values.payload, 'Payload'),
        releaseReferences: parseStringMap(values.releaseReferences),
        meta: makeRegistryMutationMeta(0, values.reason, newRegistryIdempotencyKey('template-release')),
      });
      setReleases((current) => [released, ...current.filter((item) => item.id !== released.id)]);
      assignmentForm.setFieldsValue({ targetType: released.templateKind, templateRevisionId: released.id, reason: `Assign ${released.templateKey} revision ${released.revisionNumber}` });
      releaseForm.resetFields();
      setReleaseDirty(false);
    } catch (reason) {
      setError(reason);
    } finally {
      setWorking(false);
    }
  };

  const assignTemplate = async () => {
    const values = await assignmentForm.validateFields();
    setWorking(true);
    setError(null);
    try {
      const assignment = await registryAdminApi.assignTemplate({
        siteId: site.id,
        targetType: values.targetType,
        targetId: values.targetId,
        templateRevisionId: values.templateRevisionId,
        effectiveAt: new Date().toISOString(),
        meta: makeRegistryMutationMeta(0, values.reason, newRegistryIdempotencyKey('template-assignment')),
      });
      setAssignments((current) => [assignment, ...current]);
      assignmentForm.resetFields(['targetId', 'reason']);
      setAssignmentDirty(false);
    } catch (reason) {
      setError(reason);
    } finally {
      setWorking(false);
    }
  };

  const presentedError = error ? presentRegistryError(error) : null;

  return <Space direction="vertical" size={16} style={{ width: '100%' }}>
    {!allowed ? <Alert type="info" showIcon message="当前 Principal 没有 template.manage" description="模板区域保持只读；服务器仍会对每次模板操作重新授权。" /> : null}
    {presentedError ? <Alert type="error" showIcon message={presentedError.title} description={presentedError.description} closable onClose={() => setError(null)} /> : null}
    <Alert type="info" showIcon message="TemplateRevision 发布后不可修改" description="这里的 Draft 仅是浏览器编辑态。Release 后服务器生成 immutable TemplateRevision；所谓 rollback 是再次 Assign 一个之前发布的 Revision，不会改写历史。" />
    <Row gutter={[16, 16]}>
      <Col xs={24} xl={12}>
        <Card title="Template Draft → Release">
          <Form
            form={releaseForm}
            layout="vertical"
            disabled={!allowed}
            initialValues={{ templateKind: 'DEVICE', payload: '{}', releaseReferences: '{\n  "registrySchema": "v1"\n}', reason: '发布 Registry TemplateRevision' }}
            onValuesChange={() => setReleaseDirty(true)}
          >
            <Form.Item name="templateKey" label="Template Key" rules={[{ required: true }]}><Input placeholder="ahu-standard" /></Form.Item>
            <Form.Item name="templateKind" label="Template Kind" rules={[{ required: true }]}><Select options={['ASSET', 'DEVICE', 'POINT'].map((value) => ({ value }))} /></Form.Item>
            <Form.Item name="payload" label="Payload JSON" rules={[{ required: true }]}><Input.TextArea rows={8} className="font-mono" /></Form.Item>
            <Form.Item name="releaseReferences" label="Release References JSON" rules={[{ required: true }]}><Input.TextArea rows={4} className="font-mono" /></Form.Item>
            <Form.Item name="reason" label="Release reason" rules={[{ required: true }]}><Input.TextArea rows={2} /></Form.Item>
            <Button type="primary" onClick={releaseTemplate} loading={working}>Release immutable revision</Button>
          </Form>
        </Card>
      </Col>
      <Col xs={24} xl={12}>
        <Card title="Assign / Rollback">
          <Form
            form={assignmentForm}
            layout="vertical"
            disabled={!allowed}
            initialValues={{ targetType: 'DEVICE' }}
            onValuesChange={() => setAssignmentDirty(true)}
          >
            <Form.Item name="targetType" label="Target Type" rules={[{ required: true }]}><Select options={['ASSET', 'DEVICE', 'POINT'].map((value) => ({ value }))} /></Form.Item>
            <Form.Item name="targetId" label="Target" rules={[{ required: true }]}><Select showSearch optionFilterProp="label" options={targets} /></Form.Item>
            <Form.Item name="templateRevisionId" label="Exact TemplateRevision ID" rules={[{ required: true }]}><Input /></Form.Item>
            <Form.Item name="reason" label="Assignment reason" rules={[{ required: true }]}><Input.TextArea rows={2} /></Form.Item>
            <Button type="primary" onClick={assignTemplate} loading={working}>Create new assignment interval</Button>
          </Form>
          <Typography.Paragraph type="secondary" style={{ marginTop: 16 }}>
            公共契约没有“浏览器侧模板目录权威”。下面只展示本次会话由服务器返回的 Revision，便于立即 Assign 或回滚到已知 Revision。
          </Typography.Paragraph>
          <List
            size="small"
            dataSource={releases}
            locale={{ emptyText: '本次会话尚未 Release TemplateRevision' }}
            renderItem={(item) => <List.Item actions={[<Button key="use" type="link" onClick={() => assignmentForm.setFieldsValue({ targetType: item.templateKind, templateRevisionId: item.id })}>使用</Button>]}>
              <List.Item.Meta title={<Space>{item.templateKey}<Tag>r{item.revisionNumber}</Tag><Tag>{item.templateKind}</Tag></Space>} description={<Typography.Text code copyable>{item.id}</Typography.Text>} />
            </List.Item>}
          />
        </Card>
      </Col>
    </Row>
    <Card title="Assignment evidence from this session">
      <List
        size="small"
        dataSource={assignments}
        locale={{ emptyText: '尚无新 Assignment' }}
        renderItem={(item) => <List.Item actions={[<Button key="rollback" type="link" onClick={() => assignmentForm.setFieldsValue({ targetType: item.targetType, targetId: item.targetId, templateRevisionId: item.templateRevisionId, reason: `Rollback by new assignment to ${item.templateRevisionId}` })}>再次 Assign</Button>]}>
          <List.Item.Meta title={<Space><Tag>{item.targetType}</Tag><Typography.Text code>{item.targetId}</Typography.Text></Space>} description={<Space direction="vertical" size={0}><Typography.Text>Revision: <Typography.Text code>{item.templateRevisionId}</Typography.Text></Typography.Text><Typography.Text type="secondary">validFrom {item.validFrom}</Typography.Text></Space>} />
        </List.Item>}
      />
    </Card>
  </Space>;
}
