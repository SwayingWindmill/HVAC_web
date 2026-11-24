import { useEffect, useState } from 'react';
import { Plus, Trash2 } from 'lucide-react';
import type { Site, SiteAssetModel } from '@/api/generated/platformGateway.gen';
import { presentRegistryError, registryAdminApi } from '@/api/registry';
import { ConfirmDiscardDialog } from '@/components/confirm-discard-dialog';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Spinner } from '@/components/ui/spinner';
import { Textarea } from '@/components/ui/textarea';
import { CLIMATE_ZONE_LABEL } from '@/lib/climate-zone';
import { isOneOf } from '@/lib/typed';
import { makeRegistryMutationMeta, newRegistryIdempotencyKey } from './model';
import { ResourcePointEditorFields } from './ResourcePointEditorFields';
import {
  CLIMATE_ZONES,
  ENTITY_STATUSES,
  RESOURCE_COPY,
  SPACE_TYPES,
  defaultEditDraft,
  isEditDraftValid,
  spaceTypeLabel,
  type EditDraft,
  type EditTarget,
} from './resource-workbench-model';
import { validateAndBuildTagMap } from './tags-model';

export interface ResourceEditorDialogProps {
  readonly site: Site;
  readonly model: SiteAssetModel;
  readonly editTarget: EditTarget | null;
  readonly csrfToken: string;
  readonly onClose: () => void;
  readonly onDirtyChange: (dirty: boolean) => void;
  readonly onError: (error: unknown) => void;
  readonly onSaved: (message: string, createdSiteId: string | null) => Promise<void>;
}

export function ResourceEditorDialog({
  site,
  model,
  editTarget,
  csrfToken,
  onClose,
  onDirtyChange,
  onError,
  onSaved,
}: ResourceEditorDialogProps) {
  const [editDraft, setEditDraft] = useState<EditDraft>(() => defaultEditDraft({ type: 'SPACE' }));
  const [editDirty, setEditDirty] = useState(false);
  const [confirmDiscardOpen, setConfirmDiscardOpen] = useState(false);
  const [editError, setEditError] = useState<unknown>(null);
  const [working, setWorking] = useState(false);

  useEffect(() => {
    setEditError(null);
    setConfirmDiscardOpen(false);
    if (!editTarget) {
      setEditDirty(false);
      onDirtyChange(false);
      return;
    }
    onError(null);
    setEditDraft(defaultEditDraft(editTarget));
    setEditDirty(false);
    onDirtyChange(false);
  }, [editTarget, onDirtyChange, onError]);

  const discardAndCloseEdit = () => {
    setEditDirty(false);
    setConfirmDiscardOpen(false);
    setEditError(null);
    onDirtyChange(false);
    onClose();
  };

  const closeEdit = () => {
    if (working) return;
    if (editDirty) {
      setConfirmDiscardOpen(true);
      return;
    }
    discardAndCloseEdit();
  };

  const updateEdit = <K extends keyof EditDraft>(key: K, value: EditDraft[K]) => {
    setEditDraft((current) => {
      const next = { ...current, [key]: value };
      if (key === 'pointType') {
        if (value === 'COUNTER') {
          next.valueType = 'NUMBER';
          next.writable = false;
          next.counterDecreaseMode = next.counterDecreaseMode || 'RESET_TO_ZERO';
        } else if (value === 'COMMAND') {
          next.writable = true;
          next.counterDecreaseMode = '';
          next.counterRolloverModulus = null;
        } else {
          next.writable = false;
          next.counterDecreaseMode = '';
          next.counterRolloverModulus = null;
        }
      }
      if (key === 'counterDecreaseMode' && value !== 'ROLLOVER') next.counterRolloverModulus = null;
      return next;
    });
    setEditDirty(true);
    onDirtyChange(true);
  };

  const editValid = isEditDraftValid(editTarget, editDraft);

  const runEdit = async () => {
    if (!editTarget || !editValid) return;
    const targetSiteId =
      editTarget.entity && 'siteId' in editTarget.entity
        ? editTarget.entity.siteId
        : site.id;
    let validatedTags: Record<string, string> | undefined;
    if (editTarget.type === 'ASSET' || editTarget.type === 'DEVICE') {
      const tagRes = validateAndBuildTagMap(editDraft.tagEntries);
      if (!tagRes.ok) {
        setEditError(new Error(tagRes.error));
        return;
      }
      validatedTags = tagRes.tags;
    }
    const expectedRevision = editTarget.entity?.revision ?? 0;
    const meta = makeRegistryMutationMeta(
      expectedRevision,
      editDraft.reason.trim(),
      newRegistryIdempotencyKey(`${editTarget.type.toLowerCase()}-${editTarget.entity ? 'update' : 'create'}`),
    );
    setWorking(true);
    setEditError(null);
    onError(null);
    try {
      let createdSiteId: string | null = null;
      if (editTarget.type === 'SITE') {
        const area = editDraft.floorAreaM2.trim();
        const body = {
          code: editDraft.code.trim(),
          displayName: editDraft.displayName.trim(),
          timezone: editDraft.timezone.trim(),
          status: editDraft.status,
          floorAreaM2: area === '' ? undefined : Number(area),
          climateZone: editDraft.climateZone || undefined,
          meta,
        };
        const saved = editTarget.entity
          ? await registryAdminApi.updateSite(editTarget.entity.id, body, csrfToken)
          : await registryAdminApi.createSite(body, csrfToken);
        createdSiteId = editTarget.entity ? null : saved.id;
      } else if (editTarget.type === 'SPACE') {
        const body = {
          parentSpaceId: editDraft.parentSpaceId || undefined,
          code: editDraft.code.trim(),
          displayName: editDraft.displayName.trim(),
          spaceType: editDraft.spaceType,
          status: editDraft.status,
          meta,
        };
        if (editTarget.entity) await registryAdminApi.updateSpace(site.id, editTarget.entity.id, body, csrfToken);
        else await registryAdminApi.createSpace(site.id, body, csrfToken);
      } else if (editTarget.type === 'ASSET') {
        const body = {
          code: editDraft.code.trim(),
          displayName: editDraft.displayName.trim(),
          assetType: editDraft.assetType.trim(),
          status: editDraft.status,
          tags: validatedTags ?? {},
          meta,
        };
        if (editTarget.entity) await registryAdminApi.updateAsset(targetSiteId, editTarget.entity.id, body, csrfToken);
        else await registryAdminApi.createAsset(site.id, body, csrfToken);
      } else if (editTarget.type === 'DEVICE') {
        const body = {
          code: editDraft.code.trim(),
          displayName: editDraft.displayName.trim(),
          deviceType: editDraft.deviceType.trim(),
          status: editDraft.status,
          tags: validatedTags ?? {},
          meta,
        };
        if (editTarget.entity) await registryAdminApi.updateDevice(targetSiteId, editTarget.entity.id, body, csrfToken);
        else await registryAdminApi.createDevice(site.id, body, csrfToken);
      } else {
        const body = {
          reportingDeviceId: editDraft.reportingDeviceId,
          sensorId: editDraft.sensorId || undefined,
          pointCode: editDraft.pointCode.trim(),
          sourceKey: editDraft.sourceKey.trim(),
          displayName: editDraft.displayName.trim(),
          pointType: editDraft.pointType,
          valueType: editDraft.valueType,
          unit: editDraft.unit.trim() || undefined,
          writable: editDraft.writable,
          sampleIntervalMs: editDraft.sampleIntervalMs,
          publishIntervalMs: editDraft.publishIntervalMs,
          staleAfterMs: editDraft.staleAfterMs,
          counterDecreaseMode: editDraft.pointType === 'COUNTER' ? editDraft.counterDecreaseMode || undefined : undefined,
          counterRolloverModulus: editDraft.pointType === 'COUNTER' && editDraft.counterDecreaseMode === 'ROLLOVER' ? editDraft.counterRolloverModulus ?? undefined : undefined,
          sourceMetadata: editTarget.entity ? editTarget.entity.sourceMetadata : {},
          status: editDraft.status,
          meta,
        };
        if (editTarget.entity) await registryAdminApi.updatePoint(site.id, editTarget.entity.id, body, csrfToken);
        else await registryAdminApi.createPoint(site.id, body, csrfToken);
      }
      const message = editTarget.entity ? RESOURCE_COPY[editTarget.type].updatedMessage : RESOURCE_COPY[editTarget.type].createdMessage;
      setEditDirty(false);
      onDirtyChange(false);
      await onSaved(message, createdSiteId);
    } catch (reason) {
      setEditError(reason);
    } finally {
      setWorking(false);
    }
  };

  const tagCheck = validateAndBuildTagMap(editDraft.tagEntries);
  const presentedEditError = (() => {
    if (!editError) return null;
    if (editError instanceof Error && !('problem' in editError)) {
      return {
        title: '输入校验未通过',
        description: editError.message,
      };
    }
    return presentRegistryError(editError);
  })();

  return (
    <>
      <Dialog open={Boolean(editTarget)} onOpenChange={(open) => { if (!open) closeEdit(); }}>
        <DialogContent
          className="max-h-[88vh] overflow-y-auto sm:max-w-2xl"
          showCloseButton={!working}
          onEscapeKeyDown={(event) => { if (working) event.preventDefault(); }}
          onPointerDownOutside={(event) => { if (working) event.preventDefault(); }}
          onInteractOutside={(event) => { if (working) event.preventDefault(); }}
        >
          <DialogHeader>
            <DialogTitle>{editTarget ? (editTarget.entity ? RESOURCE_COPY[editTarget.type].editTitle : RESOURCE_COPY[editTarget.type].createLabel) : ''}</DialogTitle>
            <DialogDescription>保存时服务端会再次检查权限、对象状态和并发版本。</DialogDescription>
          </DialogHeader>
          {presentedEditError ? (
            <Alert variant="destructive">
              <AlertTitle>{presentedEditError.title}</AlertTitle>
              <AlertDescription>{presentedEditError.description}</AlertDescription>
            </Alert>
          ) : null}
        {editTarget ? (
          <div className="grid gap-4">
            {editTarget.type !== 'POINT' ? (
              <>
                <label className="grid gap-1.5 text-sm">
                  <span>编码</span>
                  <Input
                    value={editDraft.code}
                    autoCorrect="off"
                    autoCapitalize="none"
                    spellCheck={false}
                    onChange={(event) => updateEdit('code', event.target.value)}
                  />
                </label>
                <label className="grid gap-1.5 text-sm">
                  <span>名称</span>
                  <Input value={editDraft.displayName} onChange={(event) => updateEdit('displayName', event.target.value)} />
                </label>
              </>
            ) : null}

            {editTarget.type === 'SITE' ? (
              <>
                <label className="grid gap-1.5 text-sm">
                  <span>时区</span>
                  <Input
                    value={editDraft.timezone}
                    autoCorrect="off"
                    autoCapitalize="none"
                    spellCheck={false}
                    onChange={(event) => updateEdit('timezone', event.target.value)}
                  />
                </label>
                <div className="grid grid-cols-2 gap-4">
                  <label className="grid gap-1.5 text-sm">
                    <span>建筑面积（m²，可选）</span>
                    <Input inputMode="decimal" className="tabular-nums" value={editDraft.floorAreaM2} onChange={(event) => updateEdit('floorAreaM2', event.target.value)} />
                    <span className="text-xs text-muted-foreground">站点对标按它计算单位面积用电</span>
                  </label>
                  <label className="grid gap-1.5 text-sm">
                    <span>气候区（可选）</span>
                    <Select
                      value={editDraft.climateZone || '__none__'}
                      onValueChange={(value) => {
                        if (value === '__none__') updateEdit('climateZone', '');
                        else if (isOneOf(CLIMATE_ZONES, value)) updateEdit('climateZone', value);
                      }}
                    >
                      <SelectTrigger><SelectValue /></SelectTrigger>
                      <SelectContent>
                        <SelectGroup>
                          <SelectItem value="__none__">未登记</SelectItem>
                          {CLIMATE_ZONES.map((zone) => <SelectItem key={zone} value={zone}>{CLIMATE_ZONE_LABEL[zone]}</SelectItem>)}
                        </SelectGroup>
                      </SelectContent>
                    </Select>
                    <span className="text-xs text-muted-foreground">按 GB 50176 热工分区</span>
                  </label>
                </div>
              </>
            ) : null}

            {editTarget.type === 'SPACE' ? (
              <>
                <label className="grid gap-1.5 text-sm">
                  <span>空间类型</span>
                  <Select value={editDraft.spaceType} onValueChange={(value) => { if (isOneOf(SPACE_TYPES, value)) updateEdit('spaceType', value); }}>
                    <SelectTrigger><SelectValue /></SelectTrigger>
                    <SelectContent>
                      <SelectGroup>{SPACE_TYPES.map((value) => <SelectItem key={value} value={value}>{spaceTypeLabel(value)}</SelectItem>)}</SelectGroup>
                    </SelectContent>
                  </Select>
                </label>
                <label className="grid gap-1.5 text-sm">
                  <span>上级空间</span>
                  <Select value={editDraft.parentSpaceId || '__none__'} onValueChange={(value) => updateEdit('parentSpaceId', value === '__none__' ? '' : value)}>
                    <SelectTrigger><SelectValue /></SelectTrigger>
                    <SelectContent>
                      <SelectGroup>
                        <SelectItem value="__none__">无上级空间</SelectItem>
                        {model.spaces.filter((space) => space.id !== editTarget.entity?.id).map((space) => (
                          <SelectItem key={space.id} value={space.id}>{space.displayName} · {space.code}</SelectItem>
                        ))}
                      </SelectGroup>
                    </SelectContent>
                  </Select>
                </label>
              </>
            ) : null}

            {editTarget.type === 'ASSET' ? (
              <label className="grid gap-1.5 text-sm">
                <span>资产类型</span>
                <Input
                  value={editDraft.assetType}
                  autoCorrect="off"
                  autoCapitalize="none"
                  spellCheck={false}
                  onChange={(event) => updateEdit('assetType', event.target.value)}
                />
              </label>
            ) : null}

            {editTarget.type === 'DEVICE' ? (
              <label className="grid gap-1.5 text-sm">
                <span>设备类型</span>
                <Input
                  value={editDraft.deviceType}
                  autoCorrect="off"
                  autoCapitalize="none"
                  spellCheck={false}
                  onChange={(event) => updateEdit('deviceType', event.target.value)}
                />
              </label>
            ) : null}

            {(editTarget.type === 'ASSET' || editTarget.type === 'DEVICE') ? (
              <div className="space-y-2 rounded-lg border bg-muted/30 p-3">
                <div className="flex items-center justify-between gap-2">
                  <span className="text-xs font-semibold text-foreground">标签（Key-Value，可选）</span>
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onClick={() => updateEdit('tagEntries', [...editDraft.tagEntries, { key: '', value: '' }])}
                  >
                    <Plus data-icon="inline-start" />
                    添加标签
                  </Button>
                </div>
                {editDraft.tagEntries.length === 0 ? (
                  <p className="text-xs text-muted-foreground">暂无标签；可用于运维与登记筛选（例如 vendor:carrier）。</p>
                ) : (
                  <div className="space-y-1.5">
                    {editDraft.tagEntries.map((entry, index) => (
                      <div key={index} className="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto] gap-2">
                        <Input
                          aria-label={`标签键 ${index + 1}`}
                          placeholder="vendor"
                          value={entry.key}
                          autoCorrect="off"
                          autoCapitalize="none"
                          spellCheck={false}
                          className="h-8 font-mono text-xs"
                          onChange={(event) => {
                            const nextVal = event.currentTarget.value;
                            updateEdit('tagEntries', editDraft.tagEntries.map((item, i) => (i === index ? { ...item, key: nextVal } : item)));
                          }}
                        />
                        <Input
                          aria-label={`标签值 ${index + 1}`}
                          placeholder="carrier"
                          value={entry.value}
                          autoCorrect="off"
                          autoCapitalize="none"
                          spellCheck={false}
                          className="h-8 font-mono text-xs"
                          onChange={(event) => {
                            const nextVal = event.currentTarget.value;
                            updateEdit('tagEntries', editDraft.tagEntries.map((item, i) => (i === index ? { ...item, value: nextVal } : item)));
                          }}
                        />
                        <Button
                          type="button"
                          variant="ghost"
                          size="icon-sm"
                          aria-label={`删除标签 ${index + 1}`}
                          onClick={() => updateEdit('tagEntries', editDraft.tagEntries.filter((_, i) => i !== index))}
                        >
                          <Trash2 className="size-3.5" />
                        </Button>
                      </div>
                    ))}
                  </div>
                )}
                {!tagCheck.ok ? <p className="text-xs text-destructive-text">{tagCheck.error}</p> : null}
              </div>
            ) : null}

            {editTarget.type === 'POINT' ? (
              <ResourcePointEditorFields model={model} editDraft={editDraft} onUpdateEdit={updateEdit} />
            ) : null}

            <label className="grid gap-1.5 text-sm">
              <span>状态</span>
              <Select value={editDraft.status} onValueChange={(value) => { if (isOneOf(ENTITY_STATUSES, value)) updateEdit('status', value); }}>
                <SelectTrigger><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectGroup>
                    <SelectItem value="ACTIVE">启用</SelectItem>
                    <SelectItem value="INACTIVE">停用</SelectItem>
                  </SelectGroup>
                </SelectContent>
              </Select>
            </label>
            <label className="grid gap-1.5 text-sm">
              <span>变更原因</span>
              <Textarea rows={2} value={editDraft.reason} onChange={(event) => updateEdit('reason', event.target.value)} />
            </label>
            {editTarget.entity ? (
              <Alert>
                <AlertTitle>并发保护已启用</AlertTitle>
                <AlertDescription>若此对象已被其他人更新，本次保存会被服务端拒绝，不会覆盖更新后的数据。</AlertDescription>
              </Alert>
            ) : null}
          </div>
        ) : null}
        <DialogFooter>
          <Button variant="outline" disabled={working} onClick={closeEdit}>取消</Button>
          <Button disabled={!editValid || working} onClick={() => void runEdit()}>
            {working ? <Spinner data-icon="inline-start" /> : null}
            {working ? '正在保存…' : '保存'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>

    <ConfirmDiscardDialog
      open={confirmDiscardOpen}
      onOpenChange={setConfirmDiscardOpen}
      onConfirmDiscard={discardAndCloseEdit}
    />
    </>
  );
}
