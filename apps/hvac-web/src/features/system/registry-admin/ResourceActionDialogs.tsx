import { useEffect, useState } from 'react';
import type { Site, SiteAssetModel } from '@/api/generated/platformGateway.gen';
import { registryAdminApi } from '@/api/registry';
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
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Textarea } from '@/components/ui/textarea';
import { isOneOf } from '@/lib/typed';
import { confirmDiscardRegistryDraft } from './confirmDiscard';
import { makeRegistryMutationMeta, newRegistryIdempotencyKey } from './model';
import {
  POINT_SUBJECT_TYPES,
  REBIND_KINDS,
  RESOURCE_COPY,
  rebindRoleLabel,
  rebindRolesForKind,
  selectOptionsForBinding,
  type RebindDraft,
  type ResourceRow,
} from './resource-workbench-model';

export interface ResourceRetireDialogProps {
  readonly site: Site;
  readonly retireTarget: ResourceRow | null;
  readonly csrfToken: string;
  readonly onClose: () => void;
  readonly onDirtyChange: (dirty: boolean) => void;
  readonly onError: (error: unknown) => void;
  readonly onRetired: (message: string) => Promise<void>;
}

export function ResourceRetireDialog({
  site,
  retireTarget,
  csrfToken,
  onClose,
  onDirtyChange,
  onError,
  onRetired,
}: ResourceRetireDialogProps) {
  const [retireReason, setRetireReason] = useState('');
  const [retireDirty, setRetireDirty] = useState(false);
  const [working, setWorking] = useState(false);

  useEffect(() => {
    if (!retireTarget) {
      setRetireReason('');
      setRetireDirty(false);
      onDirtyChange(false);
      return;
    }
    setRetireReason(`${RESOURCE_COPY[retireTarget.type].retireTitle} ${retireTarget.displayName}`);
    setRetireDirty(false);
    onDirtyChange(false);
  }, [retireTarget, onDirtyChange]);

  const closeRetire = () => {
    if (!confirmDiscardRegistryDraft(retireDirty)) return;
    setRetireReason('');
    setRetireDirty(false);
    onDirtyChange(false);
    onClose();
  };

  const runRetire = async () => {
    if (!retireTarget || !retireReason.trim()) return;
    const targetSiteId = retireTarget.siteId ?? site.id;
    setWorking(true);
    onError(null);
    try {
      const result = await registryAdminApi.retire(targetSiteId, {
        resourceType: retireTarget.type,
        resourceId: retireTarget.id,
        meta: makeRegistryMutationMeta(retireTarget.revision, retireReason.trim(), newRegistryIdempotencyKey('registry-retire')),
      }, csrfToken);
      const message = result.status === 'COMPLETED'
        ? RESOURCE_COPY[retireTarget.type].retiredMessage
        : `被 ${result.dependencyCount} 个活动关联项占用，暂时无法${RESOURCE_COPY[retireTarget.type].retireAction}，原有登记保持不变。`;
      setRetireReason('');
      setRetireDirty(false);
      onDirtyChange(false);
      await onRetired(message);
    } catch (reason) {
      onError(reason);
    } finally {
      setWorking(false);
    }
  };

  return (
    <Dialog open={Boolean(retireTarget)} onOpenChange={(open) => { if (!open) closeRetire(); }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{retireTarget ? RESOURCE_COPY[retireTarget.type].retireTitle : ''}</DialogTitle>
          <DialogDescription>执行前会先检查活动关联；存在活动关联时不会变更原有登记。</DialogDescription>
        </DialogHeader>
        <Alert>
          <AlertTitle>保留历史记录</AlertTitle>
          <AlertDescription>服务端不会物理删除历史记录，仅在无活动关联时变更对象状态。</AlertDescription>
        </Alert>
        <label className="grid gap-1.5 text-sm">
          <span>变更原因</span>
          <Textarea
            rows={3}
            value={retireReason}
            onChange={(event) => {
              setRetireReason(event.target.value);
              setRetireDirty(true);
              onDirtyChange(true);
            }}
          />
        </label>
        <DialogFooter>
          <Button variant="outline" onClick={closeRetire}>取消</Button>
          <Button variant="destructive" disabled={!retireReason.trim() || working} onClick={() => void runRetire()}>
            {working ? '正在处理…' : (retireTarget ? RESOURCE_COPY[retireTarget.type].retireConfirm : '确认')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

const DEFAULT_REBIND_DRAFT: RebindDraft = {
  kind: 'DEVICE_ASSET',
  sourceId: '',
  targetId: '',
  targetType: 'ASSET',
  role: 'CONTROLLER',
  reason: '调整登记关系',
};

export interface ResourceRebindDialogProps {
  readonly site: Site;
  readonly model: SiteAssetModel;
  readonly open: boolean;
  readonly csrfToken: string;
  readonly onClose: () => void;
  readonly onDirtyChange: (dirty: boolean) => void;
  readonly onError: (error: unknown) => void;
  readonly onRebound: (message: string) => Promise<void>;
}

export function ResourceRebindDialog({
  site,
  model,
  open,
  csrfToken,
  onClose,
  onDirtyChange,
  onError,
  onRebound,
}: ResourceRebindDialogProps) {
  const [rebindDraft, setRebindDraft] = useState<RebindDraft>(DEFAULT_REBIND_DRAFT);
  const [rebindDirty, setRebindDirty] = useState(false);
  const [working, setWorking] = useState(false);

  useEffect(() => {
    if (!open) {
      setRebindDirty(false);
      onDirtyChange(false);
      return;
    }
    setRebindDraft(DEFAULT_REBIND_DRAFT);
    setRebindDirty(false);
    onDirtyChange(false);
  }, [open, onDirtyChange]);

  const closeRebind = () => {
    if (!confirmDiscardRegistryDraft(rebindDirty)) return;
    setRebindDirty(false);
    onDirtyChange(false);
    onClose();
  };

  const updateRebind = <K extends keyof RebindDraft>(key: K, value: RebindDraft[K]) => {
    setRebindDraft((current) => {
      const next = { ...current, [key]: value };
      if (key === 'kind') {
        next.sourceId = '';
        next.targetId = '';
        next.targetType = value === 'POINT_SUBJECT' ? 'ASSET' : next.targetType;
        next.role = value === 'DEVICE_ASSET' ? 'CONTROLLER' : value === 'POINT_SUBJECT' ? 'DESCRIBES' : 'INSTALLED_IN';
      }
      if (key === 'targetType') next.targetId = '';
      return next;
    });
    setRebindDirty(true);
    onDirtyChange(true);
  };

  const runRebind = async () => {
    if (!rebindDraft.sourceId || !rebindDraft.targetId || !rebindDraft.role || !rebindDraft.reason.trim()) return;
    setWorking(true);
    onError(null);
    try {
      await registryAdminApi.rebind(site.id, {
        kind: rebindDraft.kind,
        sourceId: rebindDraft.sourceId,
        targetId: rebindDraft.targetId,
        targetType: rebindDraft.kind === 'POINT_SUBJECT' ? rebindDraft.targetType : undefined,
        role: rebindDraft.role,
        effectiveAt: new Date().toISOString(),
        meta: makeRegistryMutationMeta(0, rebindDraft.reason.trim(), newRegistryIdempotencyKey('registry-rebind')),
      }, csrfToken);
      setRebindDirty(false);
      onDirtyChange(false);
      setRebindDraft(DEFAULT_REBIND_DRAFT);
      await onRebound('归属已调整：旧的归属到此结束，新的归属从现在生效。');
    } catch (reason) {
      onError(reason);
    } finally {
      setWorking(false);
    }
  };

  const rebindRoles = rebindRolesForKind(rebindDraft.kind);
  const bindingOptions = selectOptionsForBinding(rebindDraft.kind, model, site, rebindDraft.targetType);

  return (
    <Dialog open={open} onOpenChange={(nextOpen) => { if (!nextOpen) closeRebind(); }}>
      <DialogContent className="sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>调整登记关系</DialogTitle>
          <DialogDescription>选择已有登记对象建立新的有效关系，不需要手工填写内部标识。</DialogDescription>
        </DialogHeader>
        <div className="grid gap-4">
          <label className="grid gap-1.5 text-sm">
            <span>关系类型</span>
            <Select value={rebindDraft.kind} onValueChange={(value) => { if (isOneOf(REBIND_KINDS, value)) updateRebind('kind', value); }}>
              <SelectTrigger><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectGroup>
                  <SelectItem value="DEVICE_ASSET">设备 → 资产</SelectItem>
                  <SelectItem value="ASSET_SPACE">资产 → 空间</SelectItem>
                  <SelectItem value="DEVICE_SPACE">设备 → 空间</SelectItem>
                  <SelectItem value="POINT_SUBJECT">点位 → 测量对象</SelectItem>
                </SelectGroup>
              </SelectContent>
            </Select>
          </label>
          {rebindDraft.kind === 'POINT_SUBJECT' ? (
            <label className="grid gap-1.5 text-sm">
              <span>目标对象类型</span>
              <Select value={rebindDraft.targetType} onValueChange={(value) => { if (isOneOf(POINT_SUBJECT_TYPES, value)) updateRebind('targetType', value); }}>
                <SelectTrigger><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectGroup>
                    <SelectItem value="SITE">站点</SelectItem>
                    <SelectItem value="SPACE">空间</SelectItem>
                    <SelectItem value="ASSET">资产</SelectItem>
                  </SelectGroup>
                </SelectContent>
              </Select>
            </label>
          ) : null}
          <label className="grid gap-1.5 text-sm">
            <span>来源对象</span>
            <Select value={rebindDraft.sourceId || undefined} onValueChange={(value) => updateRebind('sourceId', value)}>
              <SelectTrigger><SelectValue placeholder="选择来源对象" /></SelectTrigger>
              <SelectContent>
                <SelectGroup>{bindingOptions.sources.map((option) => <SelectItem key={option.id} value={option.id}>{option.label}</SelectItem>)}</SelectGroup>
              </SelectContent>
            </Select>
          </label>
          <label className="grid gap-1.5 text-sm">
            <span>目标对象</span>
            <Select value={rebindDraft.targetId || undefined} onValueChange={(value) => updateRebind('targetId', value)}>
              <SelectTrigger><SelectValue placeholder="选择目标对象" /></SelectTrigger>
              <SelectContent>
                <SelectGroup>{bindingOptions.targets.map((option) => <SelectItem key={option.id} value={option.id}>{option.label}</SelectItem>)}</SelectGroup>
              </SelectContent>
            </Select>
          </label>
          <label className="grid gap-1.5 text-sm">
            <span>关系角色</span>
            <Select value={rebindDraft.role} onValueChange={(value) => updateRebind('role', value)}>
              <SelectTrigger><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectGroup>{rebindRoles.map((role) => <SelectItem key={role} value={role}>{rebindRoleLabel(role)}</SelectItem>)}</SelectGroup>
              </SelectContent>
            </Select>
          </label>
          <label className="grid gap-1.5 text-sm">
            <span>变更原因</span>
            <Textarea rows={2} value={rebindDraft.reason} onChange={(event) => updateRebind('reason', event.target.value)} />
          </label>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={closeRebind}>取消</Button>
          <Button disabled={working || !rebindDraft.sourceId || !rebindDraft.targetId || !rebindDraft.role || !rebindDraft.reason.trim()} onClick={() => void runRebind()}>
            {working ? '正在保存…' : '保存关系'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
