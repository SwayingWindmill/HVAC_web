import { useCallback, useEffect, useMemo, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { RefreshCw, X } from 'lucide-react';
import type {
  Capability,
  Device,
  Site,
  SiteAssetModel,
} from '@/api/generated/platformGateway.gen';
import {
  flattenRegistryPages,
  presentRegistryError,
  useRegistryDevicePoints,
  useRegistryFilteredAssets,
  useRegistryFilteredDevices,
  useRegistryTemplates,
} from '@/api/registry';
import { DataTableBlock } from '@/blocks/data-table';
import { DataTable, DataTablePagination } from '@/components/data-table';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { useDataTable } from '@/hooks/use-data-table';
import { ReplaceHardwareDialog } from './ReplaceHardwareDialog';
import { ResourceRebindDialog, ResourceRetireDialog } from './ResourceActionDialogs';
import { ResourceEditorDialog } from './ResourceEditorDialog';
import { ResourceFilterBar, type ResourceTypeOption } from './ResourceFilterBar';
import { ResourceSpaceTreeCard } from './ResourceSpaceTreeCard';
import { createDevicePointColumns, createResourceColumns } from './resource-table-columns';
import type { RegistryAdminResourceType } from './model';
import {
  RESOURCE_COPY,
  WRITE_CAPABILITY,
  assetRowsToResourceRows,
  deviceRowsToResourceRows,
  entityRows,
  newEditTarget,
  type EditTarget,
  type ResourceRow,
  type ResourceStatusFilter,
} from './resource-workbench-model';
import { parseTagFilterInput } from './tags-model';

interface Props {
  site: Site;
  sites: readonly Site[];
  model: SiteAssetModel;
  capabilities: ReadonlySet<Capability>;
  csrfToken: string;
  onDirtyChange: (dirty: boolean) => void;
  onRefresh: () => Promise<void>;
  onSiteCreated: (siteId: string) => void;
}

export function RegistryResourceWorkbench({
  site,
  sites,
  model,
  capabilities,
  csrfToken,
  onDirtyChange,
  onRefresh,
  onSiteCreated,
}: Props) {
  const queryClient = useQueryClient();
  const [resourceType, setResourceType] = useState<RegistryAdminResourceType>('SPACE');
  const [allSites, setAllSites] = useState(false);
  const [statusFilter, setStatusFilter] = useState<ResourceStatusFilter>('ALL');
  const [selectedSpaceId, setSelectedSpaceId] = useState<string | null>(null);
  const [templateKeyFilter, setTemplateKeyFilter] = useState<string>('ALL');
  const [tagInput, setTagInput] = useState('');
  const [appliedTagFilter, setAppliedTagFilter] = useState<string | undefined>(undefined);
  const [tagFilterError, setTagFilterError] = useState<string | null>(null);
  const [editTarget, setEditTarget] = useState<EditTarget | null>(null);
  const [retireTarget, setRetireTarget] = useState<ResourceRow | null>(null);
  const [replaceTarget, setReplaceTarget] = useState<Device | null>(null);
  const [rebindOpen, setRebindOpen] = useState(false);
  const [editDirty, setEditDirty] = useState(false);
  const [retireDirty, setRetireDirty] = useState(false);
  const [replaceDirty, setReplaceDirty] = useState(false);
  const [rebindDirty, setRebindDirty] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [resultMessage, setResultMessage] = useState<string | null>(null);
  const [selectedDeviceId, setSelectedDeviceId] = useState<string | null>(null);
  const pointsQuery = useRegistryDevicePoints(selectedDeviceId);
  const devicePoints = flattenRegistryPages(pointsQuery.data);
  const dirty = editDirty || retireDirty || replaceDirty || rebindDirty;

  useEffect(() => onDirtyChange(dirty), [dirty, onDirtyChange]);
  useEffect(() => {
    setSelectedSpaceId(null);
  }, [site.id]);

  const filterableType = resourceType === 'ASSET' || resourceType === 'DEVICE';
  const isServerFiltered =
    filterableType &&
    (allSites ||
      statusFilter !== 'ALL' ||
      Boolean(selectedSpaceId) ||
      templateKeyFilter !== 'ALL' ||
      Boolean(appliedTagFilter));

  const serverFilterParams = useMemo(() => ({
    ...(statusFilter !== 'ALL' ? { status: statusFilter } : {}),
    ...(selectedSpaceId && !allSites ? { spaceId: selectedSpaceId } : {}),
    ...(templateKeyFilter !== 'ALL' ? { templateKey: templateKeyFilter } : {}),
    ...(appliedTagFilter ? { tag: [appliedTagFilter] } : {}),
  }), [allSites, appliedTagFilter, selectedSpaceId, statusFilter, templateKeyFilter]);

  const canReadTemplatesForFilter =
    filterableType &&
    (capabilities.has('template.manage') || capabilities.has('asset.read') || capabilities.has('device.list'));
  const templatesFilterQuery = useRegistryTemplates(undefined, canReadTemplatesForFilter);
  const templateKeys = useMemo(() => {
    const list = flattenRegistryPages(templatesFilterQuery.data);
    return Array.from(new Set(list.map((item) => item.templateKey))).sort((a, b) => a.localeCompare(b));
  }, [templatesFilterQuery.data]);

  const filteredAssetsQuery = useRegistryFilteredAssets(
    site.id,
    allSites,
    serverFilterParams,
    resourceType === 'ASSET' && isServerFiltered && capabilities.has('asset.read'),
  );
  const filteredDevicesQuery = useRegistryFilteredDevices(
    site.id,
    allSites,
    serverFilterParams,
    resourceType === 'DEVICE' && isServerFiltered && capabilities.has('device.list'),
  );
  const activeFilteredQuery =
    resourceType === 'ASSET' && isServerFiltered
      ? filteredAssetsQuery
      : resourceType === 'DEVICE' && isServerFiltered
        ? filteredDevicesQuery
        : null;

  const siteNameById = useMemo(
    () => new Map(sites.map((item) => [item.id, item.displayName])),
    [sites],
  );
  const spaceNameById = useMemo(
    () => new Map(model.spaces.map((item) => [item.id, item.displayName])),
    [model.spaces],
  );

  const rows = useMemo(() => {
    if (resourceType === 'ASSET' && isServerFiltered) {
      return assetRowsToResourceRows(flattenRegistryPages(filteredAssetsQuery.data));
    }
    if (resourceType === 'DEVICE' && isServerFiltered) {
      return deviceRowsToResourceRows(flattenRegistryPages(filteredDevicesQuery.data));
    }
    return entityRows(site, model, resourceType);
  }, [filteredAssetsQuery.data, filteredDevicesQuery.data, isServerFiltered, model, resourceType, site]);

  const resourceTypeOptions = useMemo<ResourceTypeOption[]>(() => [
    { key: 'SPACE', label: RESOURCE_COPY.SPACE.tabLabel, count: model.spaces.length },
    { key: 'ASSET', label: RESOURCE_COPY.ASSET.tabLabel, count: model.assets.length },
    { key: 'DEVICE', label: RESOURCE_COPY.DEVICE.tabLabel, count: model.devices.length },
    { key: 'POINT', label: RESOURCE_COPY.POINT.tabLabel, count: model.telemetryPoints.length },
    { key: 'SITE', label: RESOURCE_COPY.SITE.tabLabel, count: 1 },
  ], [model]);

  const writeAllowed = capabilities.has(WRITE_CAPABILITY[resourceType]);
  const retireAllowed = capabilities.has('registry.retire');
  const bindingAllowed = capabilities.has('binding.write');
  const selectedDevice = useMemo(() => model.devices.find((device) => device.id === selectedDeviceId), [model.devices, selectedDeviceId]);

  const gatewayDevicesForReplacement = useMemo(() => {
    if (!replaceTarget) return [];
    const byId = new Map<string, Device>();
    for (const device of model.devices) byId.set(device.id, device);
    for (const row of rows) {
      if (row.type === 'DEVICE') byId.set(row.entity.id, row.entity);
    }
    return Array.from(byId.values()).filter(
      (candidate) =>
        candidate.siteId === replaceTarget.siteId
        && candidate.deviceType === 'GATEWAY'
        && candidate.status === 'ACTIVE'
        && candidate.id !== replaceTarget.id,
    );
  }, [model.devices, replaceTarget, rows]);

  const openEditor = useCallback((target: EditTarget) => {
    setError(null);
    setEditTarget(target);
  }, []);

  const invalidateFilteredQueries = useCallback(async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ['registry', 'filtered-assets'] }),
      queryClient.invalidateQueries({ queryKey: ['registry', 'filtered-devices'] }),
    ]);
  }, [queryClient]);

  const applyTagFilter = () => {
    const parsed = parseTagFilterInput(tagInput);
    if (!parsed.ok) {
      setTagFilterError(parsed.error);
      return;
    }
    setTagFilterError(null);
    setAppliedTagFilter(parsed.tag);
  };

  const resetResourceFilters = () => {
    setAllSites(false);
    setStatusFilter('ALL');
    setSelectedSpaceId(null);
    setTemplateKeyFilter('ALL');
    setTagInput('');
    setAppliedTagFilter(undefined);
    setTagFilterError(null);
  };

  const resourceColumns = useMemo(
    () =>
      createResourceColumns({
        filterableType,
        allSites,
        siteNameById,
        capabilities,
        retireAllowed,
        onOpenEditor: openEditor,
        onReplaceHardware: (device) => {
          setError(null);
          setReplaceTarget(device);
        },
        onRetireRow: (row) => setRetireTarget(row),
        onSelectDevice: (deviceId) => setSelectedDeviceId(deviceId),
      }),
    [allSites, capabilities, filterableType, openEditor, retireAllowed, siteNameById],
  );

  const resourceTable = useDataTable({
    key: `registry-resource-${resourceType}`,
    data: [...rows],
    columns: resourceColumns,
    pageSize: 10,
    getRowId: (row) => row.key,
  });

  const pointColumns = useMemo(() => createDevicePointColumns(), []);
  const pointTable = useDataTable({
    key: `registry-device-points-${selectedDeviceId ?? 'none'}`,
    data: [...devicePoints],
    columns: pointColumns,
    pageSize: 10,
    getRowId: (row) => row.id,
  });

  const selectedDeviceRow = rows.find((item) => item.type === 'DEVICE' && item.id === selectedDeviceId);
  const selectedDeviceLabel = selectedDeviceRow?.displayName ?? selectedDevice?.displayName ?? '设备';

  const presentedError = (() => {
    const activeErr = error ?? activeFilteredQuery?.error;
    if (!activeErr) return null;
    if (activeErr instanceof Error && !('problem' in activeErr)) {
      return {
        kind: 'invalid-request' as const,
        title: '输入校验未通过',
        description: activeErr.message,
        retryable: false,
      };
    }
    return presentRegistryError(activeErr);
  })();
  const pointListError = pointsQuery.error ? presentRegistryError(pointsQuery.error) : null;

  return (
    <div className="space-y-4">
      {presentedError ? <Alert variant="destructive"><AlertTitle>{presentedError.title}</AlertTitle><AlertDescription>{presentedError.description}</AlertDescription></Alert> : null}
      {resultMessage ? <Alert><AlertTitle>登记已更新</AlertTitle><AlertDescription>{resultMessage}</AlertDescription></Alert> : null}

      <div className="grid gap-4 xl:grid-cols-[320px_minmax(0,1fr)]">
        <ResourceSpaceTreeCard
          spaces={model.spaces}
          selectedSpaceId={selectedSpaceId}
          allSites={allSites}
          spaceNameById={spaceNameById}
          onSelectSpace={(nextSpaceId) => {
            setSelectedSpaceId(nextSpaceId);
            if (nextSpaceId && resourceType !== 'ASSET' && resourceType !== 'DEVICE') {
              setResourceType('DEVICE');
            }
            resourceTable.setPageIndex(0);
          }}
        />

        <DataTableBlock
          className="min-w-0"
          title={RESOURCE_COPY[resourceType].tableTitle}
          description="停用或退役的记录会保留历史关联"
          controls={(
            <ResourceFilterBar
              resourceType={resourceType}
              resourceTypeOptions={resourceTypeOptions}
              writeAllowed={writeAllowed}
              bindingAllowed={bindingAllowed}
              filterableType={filterableType}
              allSites={allSites}
              statusFilter={statusFilter}
              templateKeyFilter={templateKeyFilter}
              templateKeys={templateKeys}
              tagInput={tagInput}
              isServerFiltered={isServerFiltered}
              tagFilterError={tagFilterError}
              onResourceTypeChange={(val) => {
                setResourceType(val);
                resourceTable.setPageIndex(0);
              }}
              onCreateClick={() => openEditor(newEditTarget(resourceType))}
              onRebindClick={() => setRebindOpen(true)}
              onAllSitesChange={(nextAll) => {
                setAllSites(nextAll);
                if (nextAll) setSelectedSpaceId(null);
                resourceTable.setPageIndex(0);
              }}
              onStatusFilterChange={(value) => {
                setStatusFilter(value);
                resourceTable.setPageIndex(0);
              }}
              onTemplateKeyFilterChange={(value) => {
                setTemplateKeyFilter(value);
                resourceTable.setPageIndex(0);
              }}
              onTagInputChange={(value) => {
                setTagInput(value);
                if (tagFilterError) setTagFilterError(null);
              }}
              onApplyTagFilter={() => {
                applyTagFilter();
                resourceTable.setPageIndex(0);
              }}
              onResetFilters={() => {
                resetResourceFilters();
                resourceTable.setPageIndex(0);
              }}
            />
          )}
        >
          {activeFilteredQuery?.isLoading ? (
            <div className="grid min-h-24 place-items-center rounded-md border text-sm text-muted-foreground">正在按条件查询…</div>
          ) : (
            <DataTable
              table={resourceTable}
              tableAriaLabel={RESOURCE_COPY[resourceType].tableTitle}
              empty={RESOURCE_COPY[resourceType].emptyLabel}
              getHeaderCellProps={(header) => ({
                className: header.id === 'actions' ? 'w-80' : undefined,
              })}
              footer={<DataTablePagination table={resourceTable} totalRows={rows.length} />}
            />
          )}
          {activeFilteredQuery?.hasNextPage ? (
            <div className="mt-3 flex justify-center">
              <Button
                variant="outline"
                size="sm"
                onClick={() => void activeFilteredQuery.fetchNextPage()}
                disabled={activeFilteredQuery.isFetchingNextPage}
              >
                <RefreshCw className={activeFilteredQuery.isFetchingNextPage ? 'motion-safe:animate-spin' : undefined} />
                加载更多结果
              </Button>
            </div>
          ) : null}
        </DataTableBlock>
      </div>

      {selectedDeviceId ? (
        <DataTableBlock
          title={`${selectedDeviceLabel} · 点位`}
          actions={<Button variant="ghost" size="sm" onClick={() => setSelectedDeviceId(null)}><X />关闭</Button>}
        >
          {pointListError ? <Alert variant="destructive"><AlertTitle>{pointListError.title}</AlertTitle><AlertDescription>{pointListError.description}</AlertDescription></Alert> : null}
          {pointsQuery.isLoading ? <div className="grid min-h-24 place-items-center rounded-md border text-sm text-muted-foreground">正在读取点位…</div> : (
            <DataTable
              table={pointTable}
              tableAriaLabel={`${selectedDevice?.displayName ?? '设备'}点位登记`}
              empty="当前设备没有点位登记"
              footer={<DataTablePagination table={pointTable} totalRows={devicePoints.length} />}
            />
          )}
          {pointsQuery.hasNextPage ? (
            <div className="flex justify-center">
              <Button variant="outline" onClick={() => void pointsQuery.fetchNextPage()} disabled={pointsQuery.isFetchingNextPage}>
                <RefreshCw className={pointsQuery.isFetchingNextPage ? 'motion-safe:animate-spin' : undefined} />
                加载更多
              </Button>
            </div>
          ) : null}
        </DataTableBlock>
      ) : null}

      <ResourceEditorDialog
        site={site}
        model={model}
        editTarget={editTarget}
        csrfToken={csrfToken}
        onClose={() => setEditTarget(null)}
        onDirtyChange={setEditDirty}
        onError={setError}
        onSaved={async (message, createdSiteId) => {
          setResultMessage(message);
          setEditTarget(null);
          await Promise.all([onRefresh(), invalidateFilteredQueries()]);
          if (createdSiteId) onSiteCreated(createdSiteId);
        }}
      />

      <ResourceRetireDialog
        site={site}
        retireTarget={retireTarget}
        csrfToken={csrfToken}
        onClose={() => setRetireTarget(null)}
        onDirtyChange={setRetireDirty}
        onError={setError}
        onRetired={async (message) => {
          setResultMessage(message);
          setRetireTarget(null);
          await Promise.all([onRefresh(), invalidateFilteredQueries()]);
        }}
      />

      <ResourceRebindDialog
        site={site}
        model={model}
        open={rebindOpen}
        csrfToken={csrfToken}
        onClose={() => setRebindOpen(false)}
        onDirtyChange={setRebindDirty}
        onError={setError}
        onRebound={async (message) => {
          setResultMessage(message);
          setRebindOpen(false);
          await Promise.all([onRefresh(), invalidateFilteredQueries()]);
        }}
      />

      <ReplaceHardwareDialog
        open={Boolean(replaceTarget)}
        device={replaceTarget}
        gatewayDevices={gatewayDevicesForReplacement}
        csrfToken={csrfToken}
        onOpenChange={(open) => {
          if (!open) setReplaceTarget(null);
        }}
        onDirtyChange={setReplaceDirty}
        onReplaced={async (result, target) => {
          setError(null);
          setResultMessage(
            `已完成「${target.displayName}」的硬件更换（新硬件标识：${result.newExternalId}），原有设备、点位与历史数据保持连续。`,
          );
          setReplaceTarget(null);
          setReplaceDirty(false);
          await Promise.all([onRefresh(), invalidateFilteredQueries()]);
        }}
      />
    </div>
  );
}
