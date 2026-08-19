import { useEffect, useState } from 'react';
import { Alert, Button, Card, Col, Input, Row, Space, Table, Tag, Typography } from 'antd';
import type { Capability, ImportPlan, ImportPlanRequest, Site, SiteAssetModel } from '@/api/generated/platformGateway.gen';
import { importPlanRequestSchema } from '@/api/generated/platformGateway.gen';
import { presentRegistryError, registryAdminApi } from '@/api/registry';
import { buildRegistryExport, canCommitImportPlan, makeRegistryMutationMeta, newRegistryIdempotencyKey, registryExportFileName } from './model';

interface Props {
  site: Site;
  model: SiteAssetModel;
  capabilities: ReadonlySet<Capability>;
  onDirtyChange: (dirty: boolean) => void;
  onRefresh: () => Promise<void>;
}

const EXAMPLE_IMPORT = JSON.stringify({
  namespace: 'site-import-v1',
  rows: [
    {
      rowNumber: 1,
      resourceType: 'ASSET',
      externalId: 'ahu-01',
      expectedRevision: 0,
      payload: { code: 'ahu-01', displayName: 'AHU 01', assetType: 'AHU', status: 'ACTIVE' },
    },
  ],
}, null, 2);

export function RegistryImportExportWorkbench({ site, model, capabilities, onDirtyChange, onRefresh }: Props) {
  const [source, setSource] = useState(EXAMPLE_IMPORT);
  const [dirty, setDirty] = useState(false);
  const [plan, setPlan] = useState<ImportPlan | null>(null);
  const [working, setWorking] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [commitMessage, setCommitMessage] = useState<string | null>(null);
  const allowed = capabilities.has('registry.import');

  useEffect(() => onDirtyChange(dirty), [dirty, onDirtyChange]);

  const dryRun = async () => {
    setWorking(true);
    setError(null);
    setCommitMessage(null);
    try {
      const parsed = importPlanRequestSchema.parse(JSON.parse(source)) as ImportPlanRequest;
      const nextPlan = await registryAdminApi.planImport(site.id, parsed);
      setPlan(nextPlan);
      setDirty(false);
    } catch (reason) {
      setError(reason);
    } finally {
      setWorking(false);
    }
  };

  const commit = async () => {
    if (!plan || !canCommitImportPlan(plan)) return;
    setWorking(true);
    setError(null);
    try {
      const result = await registryAdminApi.commitImport(site.id, {
        plan,
        meta: makeRegistryMutationMeta(0, `Commit reviewed Registry import ${plan.planId}`, newRegistryIdempotencyKey('registry-import-commit')),
      });
      setCommitMessage(`Import commit 完成：${result.results.filter((row) => row.status === 'COMMITTED').length} rows，digest ${result.planDigest.slice(0, 12)}…`);
      setPlan(null);
      await onRefresh();
    } catch (reason) {
      setError(reason);
    } finally {
      setWorking(false);
    }
  };

  const readFile = async (file: File | undefined) => {
    if (!file) return;
    try {
      const text = await file.text();
      setSource(text);
      setDirty(true);
      setPlan(null);
    } catch (reason) {
      setError(reason);
    }
  };

  const exportRegistry = () => {
    const payload = buildRegistryExport(site, model, new Date().toISOString());
    const blob = new Blob([JSON.stringify(payload, null, 2)], { type: 'application/json' });
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement('a');
    anchor.href = url;
    anchor.download = registryExportFileName(site);
    anchor.click();
    URL.revokeObjectURL(url);
  };

  const presentedError = error ? presentRegistryError(error) : null;

  return <Space direction="vertical" size={16} style={{ width: '100%' }}>
    {presentedError ? <Alert type="error" showIcon message={presentedError.title} description={presentedError.description} closable onClose={() => setError(null)} /> : null}
    {commitMessage ? <Alert type="success" showIcon message={commitMessage} closable onClose={() => setCommitMessage(null)} /> : null}
    <Row gutter={[16, 16]}>
      <Col xs={24} xl={15}>
        <Card title="Import: dry-run → reviewed plan → commit" extra={<Tag color={allowed ? 'green' : 'default'}>{allowed ? 'registry.import' : 'read only'}</Tag>}>
          <Alert type="info" showIcon message="浏览器不直接写拓扑" description="JSON 首先送到 Registry owner 做 dry-run。只有服务器返回的 plan 每一行都是 READY 时，Commit 按钮才可用；plan 过期或 Revision 变化会返回冲突并要求重新 dry-run。" />
          <div style={{ marginTop: 16 }}>
            <input type="file" accept="application/json,.json" disabled={!allowed} onChange={(event) => readFile(event.currentTarget.files?.[0])} />
          </div>
          <Input.TextArea
            value={source}
            onChange={(event) => { setSource(event.target.value); setDirty(true); setPlan(null); }}
            rows={16}
            className="font-mono"
            disabled={!allowed}
            style={{ marginTop: 12 }}
          />
          <Space style={{ marginTop: 12 }}>
            <Button type="primary" disabled={!allowed} loading={working} onClick={dryRun}>Dry-run</Button>
            <Button type="primary" danger disabled={!allowed || !canCommitImportPlan(plan)} loading={working} onClick={commit}>Commit reviewed plan</Button>
          </Space>
        </Card>
      </Col>
      <Col xs={24} xl={9}>
        <Card title="Controlled export">
          <Typography.Paragraph>
            Export 来源是服务器返回的当前 SiteAssetModel。为避免把自由 metadata 当作配置机密带出，导出文件只保留 canonical identity、typed topology、Revision 与 Point 采集语义；不会包含 CredentialRef、secret、Sensor metadata 或 Point sourceMetadata。
          </Typography.Paragraph>
          <Space direction="vertical" style={{ width: '100%' }}>
            <Typography.Text>Site: <Typography.Text strong>{site.displayName}</Typography.Text></Typography.Text>
            <Typography.Text>Revision: {site.revision}</Typography.Text>
            <Typography.Text>Spaces / Assets / Devices / Points: {model.counts.spaces} / {model.counts.assets} / {model.counts.deviceEndpoints} / {model.counts.points}</Typography.Text>
            <Button onClick={exportRegistry}>导出无秘密 Registry snapshot</Button>
          </Space>
        </Card>
      </Col>
    </Row>
    {plan ? <Card title={<Space>Server Import Plan <Typography.Text code>{plan.planId}</Typography.Text><Tag>{plan.digest.slice(0, 12)}…</Tag></Space>}>
      <Table
        rowKey="rowNumber"
        dataSource={plan.results}
        pagination={false}
        columns={[
          { title: 'Row', dataIndex: 'rowNumber', width: 80 },
          { title: 'Type', dataIndex: 'resourceType', width: 110 },
          { title: 'External ID', dataIndex: 'externalId' },
          { title: 'Expected Rev', dataIndex: 'expectedRevision', width: 120 },
          { title: 'Status', dataIndex: 'status', width: 110, render: (value) => <Tag color={value === 'READY' ? 'green' : value === 'COMMITTED' ? 'blue' : 'red'}>{value}</Tag> },
          { title: 'Evidence', render: (_, row) => row.errorCode ? `${row.errorCode}: ${row.message ?? ''}` : row.targetId ?? 'new resource' },
        ]}
      />
    </Card> : null}
  </Space>;
}
