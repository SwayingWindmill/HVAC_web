import { useCallback, useEffect, useMemo, useState } from 'react';
import { Alert, Button, Card, Empty, Form, Input, Select, Space, Spin, Tabs, Tag, Typography } from 'antd';
import type { Capability } from '@/api/generated/platformGateway.gen';
import {
  flattenRegistryPages,
  presentRegistryError,
  registryAdminApi,
  useRegistryAssetModel,
  useRegistrySite,
  useRegistrySites,
} from '@/api/registry';
import type { ProtectedScopeDraft } from '../protected-scope';
import { RegistryImportExportWorkbench } from './RegistryImportExportWorkbench';
import { RegistryResourceWorkbench } from './RegistryResourceWorkbench';
import { RegistryTemplateWorkbench } from './RegistryTemplateWorkbench';
import { makeRegistryMutationMeta, newRegistryIdempotencyKey } from './model';
import { confirmDiscardRegistryDraft, useRegistryDirtyGuard } from './useDirtyGuard';

interface Props {
  capabilities: readonly Capability[];
  registerUnsavedDraft: (draft: ProtectedScopeDraft) => () => void;
}

export function RegistryAdministration({ capabilities: capabilityList, registerUnsavedDraft }: Props) {
  const capabilities = useMemo(() => new Set(capabilityList), [capabilityList]);
  const sitesQuery = useRegistrySites(capabilities.has('site.list'));
  const sites = flattenRegistryPages(sitesQuery.data);
  const [selectedSiteId, setSelectedSiteId] = useState<string | null>(null);
  const [activeTab, setActiveTab] = useState('resources');
  const [dirty, setDirty] = useState(false);
  const [creatingSite, setCreatingSite] = useState(false);
  const [mutationError, setMutationError] = useState<unknown>(null);
  const [createSiteForm] = Form.useForm();
  const siteQuery = useRegistrySite(selectedSiteId);
  const modelQuery = useRegistryAssetModel(selectedSiteId);

  useRegistryDirtyGuard(dirty);

  useEffect(() => {
    if (!selectedSiteId && sites.length) setSelectedSiteId(sites[0].id);
    if (selectedSiteId && sites.length && !sites.some((site) => site.id === selectedSiteId)) setSelectedSiteId(sites[0].id);
  }, [selectedSiteId, sites]);

  useEffect(() => registerUnsavedDraft({
    id: 'registry-administration-draft',
    label: selectedSiteId ? `Registry 管理 · ${selectedSiteId}` : 'Registry 管理',
    isDirty: () => dirty,
  }), [dirty, registerUnsavedDraft, selectedSiteId]);

  const refresh = useCallback(async () => {
    await Promise.all([
      sitesQuery.refetch(),
      selectedSiteId ? siteQuery.refetch() : Promise.resolve(),
      selectedSiteId ? modelQuery.refetch() : Promise.resolve(),
    ]);
  }, [modelQuery, selectedSiteId, siteQuery, sitesQuery]);

  const changeSite = (siteId: string) => {
    if (!confirmDiscardRegistryDraft(dirty)) return;
    setDirty(false);
    setSelectedSiteId(siteId);
  };

  const changeTab = (key: string) => {
    if (!confirmDiscardRegistryDraft(dirty)) return;
    setDirty(false);
    setActiveTab(key);
  };

  const createFirstSite = async () => {
    const values = await createSiteForm.validateFields();
    setCreatingSite(true);
    setMutationError(null);
    try {
      const created = await registryAdminApi.createSite({
        code: values.code,
        displayName: values.displayName,
        timezone: values.timezone,
        status: 'ACTIVE',
        meta: makeRegistryMutationMeta(0, values.reason, newRegistryIdempotencyKey('site-create')),
      });
      await sitesQuery.refetch();
      setDirty(false);
      createSiteForm.resetFields();
      setSelectedSiteId(created.id);
    } catch (reason) {
      setMutationError(reason);
    } finally {
      setCreatingSite(false);
    }
  };

  const site = siteQuery.data;
  const model = modelQuery.data;
  const loading = sitesQuery.isLoading || (Boolean(selectedSiteId) && (siteQuery.isLoading || modelQuery.isLoading));
  const error = mutationError ?? sitesQuery.error ?? siteQuery.error ?? modelQuery.error;
  const presentedError = error ? presentRegistryError(error) : null;

  if (loading && !site && !model) {
    return <Card><Space><Spin /><Typography.Text>正在加载 Registry owner 数据…</Typography.Text></Space></Card>;
  }

  return <Space direction="vertical" size={16} style={{ width: '100%' }}>
    <Card>
      <Space wrap size={16}>
        <div>
          <Typography.Text type="secondary">Authoritative Site</Typography.Text>
          <div>
            <Select
              value={selectedSiteId ?? undefined}
              placeholder="选择 Site"
              style={{ minWidth: 280 }}
              showSearch
              optionFilterProp="label"
              onChange={changeSite}
              options={sites.map((value) => ({ value: value.id, label: `${value.displayName} · ${value.code}` }))}
            />
          </div>
        </div>
        <Tag color="blue">Core Registry owner</Tag>
        <Tag>generated OpenAPI client</Tag>
        {dirty ? <Tag color="orange">unsaved draft protected</Tag> : null}
      </Space>
    </Card>

    {presentedError ? <Alert type="error" showIcon message={presentedError.title} description={presentedError.description} /> : null}
    {!selectedSiteId || !site || !model ? (
      sites.length === 0 && capabilities.has('site.list') && capabilities.has('site.write') ? (
        <Card title="创建首个 Site">
          <Alert type="info" showIcon message="当前 Tenant 还没有 Site" description="Site 创建仍由 Core Registry owner 执行，浏览器只提交生成契约定义的 mutation。" style={{ marginBottom: 16 }} />
          <Form
            form={createSiteForm}
            layout="vertical"
            initialValues={{ timezone: 'Asia/Shanghai', reason: '创建首个 Registry Site' }}
            onValuesChange={() => setDirty(true)}
          >
            <Form.Item name="code" label="Code" rules={[{ required: true }]}><Input placeholder="central-plant" /></Form.Item>
            <Form.Item name="displayName" label="Display Name" rules={[{ required: true }]}><Input /></Form.Item>
            <Form.Item name="timezone" label="Timezone" rules={[{ required: true }]}><Input /></Form.Item>
            <Form.Item name="reason" label="创建原因" rules={[{ required: true }]}><Input.TextArea rows={2} /></Form.Item>
            <Button type="primary" loading={creatingSite} onClick={createFirstSite}>创建 Site</Button>
          </Form>
        </Card>
      ) : <Empty description="当前 Principal 没有可管理的 Site，或 Site Registry 当前不可用。" />
    ) : (
      <Tabs
        key={selectedSiteId}
        activeKey={activeTab}
        destroyOnHidden
        onChange={changeTab}
        items={[
          {
            key: 'resources',
            label: '资源与绑定',
            children: <RegistryResourceWorkbench
              site={site}
              model={model}
              capabilities={capabilities}
              onDirtyChange={setDirty}
              onRefresh={refresh}
              onSiteCreated={(siteId) => setSelectedSiteId(siteId)}
            />,
          },
          {
            key: 'templates',
            label: 'Template Revision',
            children: <RegistryTemplateWorkbench site={site} model={model} capabilities={capabilities} onDirtyChange={setDirty} />,
          },
          {
            key: 'import-export',
            label: 'Import / Export',
            children: <RegistryImportExportWorkbench site={site} model={model} capabilities={capabilities} onDirtyChange={setDirty} onRefresh={refresh} />,
          },
        ]}
      />
    )}
  </Space>;
}
