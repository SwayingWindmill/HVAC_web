import { useEffect, useMemo, useRef, useState, type FormEvent } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { Plus, Rocket, Trash2 } from 'lucide-react';
import { toast } from 'sonner';
import type {
  Device,
  OTACampaignCreateRequest,
  OTACampaignDetail,
  OTAPackage,
  Site,
} from '@/api/generated/platformGateway.gen';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import {
  Field,
  FieldDescription,
  FieldError,
  FieldLabel,
  FieldLegend,
  FieldSet,
} from '@/components/ui/field';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Spinner } from '@/components/ui/spinner';
import { Textarea } from '@/components/ui/textarea';
import type { TagEntryDraft } from '@/features/system/registry-admin/tags-model';
import {
  OTA_COMPONENT_LABELS,
  describeOtaError,
  extractSuggestedTagsFromDevices,
  normalizeTargetTagDrafts,
} from '../ota-model';
import { otaApi, otaKeys } from '../ota-query';

interface CampaignFormErrors {
  packageId?: string;
  name?: string;
  tags?: string;
  canaryPercent?: string;
  failureRateThresholdPct?: string;
  healthCheckTimeoutSec?: string;
  reason?: string;
}

function parseIntField(raw: string, min: number, max: number, label: string): {
  readonly ok: true;
  readonly value: number;
} | {
  readonly ok: false;
  readonly error: string;
} {
  const trimmed = raw.trim();
  if (!/^\d+$/.test(trimmed)) {
    return { ok: false, error: `${label}须为 ${min} 至 ${max} 之间的整数` };
  }
  const num = Number(trimmed);
  if (!Number.isInteger(num) || num < min || num > max) {
    return { ok: false, error: `${label}须在 ${min} 至 ${max} 之间` };
  }
  return { ok: true, value: num };
}

export function OtaCampaignCreateDialog({
  open,
  packages,
  sites,
  activeSiteId,
  allDevices,
  csrfToken,
  onOpenChange,
  onCreated,
}: {
  readonly open: boolean;
  readonly packages: readonly OTAPackage[];
  readonly sites: readonly Site[];
  readonly activeSiteId: string | null;
  readonly allDevices: readonly Device[];
  readonly csrfToken: string;
  readonly onOpenChange: (open: boolean) => void;
  readonly onCreated: (detail: OTACampaignDetail) => void;
}) {
  const queryClient = useQueryClient();
  const activePackages = useMemo(
    () => packages.filter((pkg) => pkg.status === 'ACTIVE'),
    [packages],
  );
  const suggestedTags = useMemo(
    () => extractSuggestedTagsFromDevices(allDevices),
    [allDevices],
  );

  const [packageId, setPackageId] = useState('');
  const [name, setName] = useState('');
  const [selectedSiteIds, setSelectedSiteIds] = useState<string[]>(() =>
    activeSiteId ? [activeSiteId] : [],
  );
  const [tagEntries, setTagEntries] = useState<TagEntryDraft[]>([]);
  const [canaryPercent, setCanaryPercent] = useState('20');
  const [failureRateThresholdPct, setFailureRateThresholdPct] = useState('20');
  const [healthCheckTimeoutSec, setHealthCheckTimeoutSec] = useState('180');
  const [autoPromoteRollout, setAutoPromoteRollout] = useState(false);
  const [reason, setReason] = useState('');
  const [errors, setErrors] = useState<CampaignFormErrors>({});
  const prevOpenRef = useRef(open);

  useEffect(() => {
    const justOpened = open && !prevOpenRef.current;
    prevOpenRef.current = open;
    if (!open) return;
    if (!packageId || !activePackages.some((pkg) => pkg.id === packageId)) {
      setPackageId(activePackages[0]?.id ?? '');
    }
    if (justOpened && activeSiteId) {
      setSelectedSiteIds([activeSiteId]);
    }
  }, [open, packageId, activePackages, activeSiteId]);

  const matchedGatewayCount = useMemo(() => {
    const normalized = normalizeTargetTagDrafts(tagEntries);
    const requiredTags = normalized.ok ? Object.entries(normalized.tags) : [];
    const siteSet = selectedSiteIds.length > 0 ? new Set(selectedSiteIds) : null;
    return allDevices.filter((device) => {
      if (device.deviceType !== 'GATEWAY' || device.status !== 'ACTIVE') return false;
      if (siteSet && !siteSet.has(device.siteId)) return false;
      return requiredTags.every(([k, v]) => device.tags?.[k] === v);
    }).length;
  }, [allDevices, selectedSiteIds, tagEntries]);

  const resetForm = () => {
    setPackageId(activePackages[0]?.id ?? '');
    setName('');
    setSelectedSiteIds(activeSiteId ? [activeSiteId] : []);
    setTagEntries([]);
    setCanaryPercent('20');
    setFailureRateThresholdPct('20');
    setHealthCheckTimeoutSec('180');
    setAutoPromoteRollout(false);
    setReason('');
    setErrors({});
  };

  const createMutation = useMutation({
    mutationFn: async (body: OTACampaignCreateRequest) => otaApi.createCampaign(body, csrfToken),
    onSuccess: (detail) => {
      void queryClient.invalidateQueries({ queryKey: otaKeys.campaigns() });
      queryClient.setQueryData(otaKeys.campaignDetail(detail.id), detail);
      toast.success(`灰度发布任务「${detail.name}」已发起`);
      resetForm();
      onOpenChange(false);
      onCreated(detail);
    },
  });

  const handleToggleSite = (siteId: string, checked: boolean) => {
    setSelectedSiteIds((prev) => {
      if (checked) {
        return prev.includes(siteId) ? prev : [...prev, siteId];
      }
      return prev.filter((id) => id !== siteId);
    });
  };

  const handleApplySuggestedTag = (key: string, value: string) => {
    setTagEntries((prev) => {
      const idx = prev.findIndex((entry) => entry.key.trim() === key);
      if (idx >= 0) {
        return prev.map((entry, i) => (i === idx ? { key, value } : entry));
      }
      return [...prev, { key, value }];
    });
    if (errors.tags) {
      setErrors((prev) => ({ ...prev, tags: undefined }));
    }
  };

  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const nextErrors: CampaignFormErrors = {};

    if (!packageId) {
      nextErrors.packageId = '请选择要发布的升级包';
    }
    const trimmedName = name.trim();
    if (!trimmedName) {
      nextErrors.name = '请填写发布任务名称';
    } else if (trimmedName.length > 160) {
      nextErrors.name = '任务名称不能超过 160 个字符';
    }

    const parsedCanary = parseIntField(canaryPercent, 1, 100, '灰度比例');
    if (!parsedCanary.ok) nextErrors.canaryPercent = parsedCanary.error;

    const parsedThreshold = parseIntField(failureRateThresholdPct, 1, 100, '熔断阈值');
    if (!parsedThreshold.ok) nextErrors.failureRateThresholdPct = parsedThreshold.error;

    const parsedTimeout = parseIntField(healthCheckTimeoutSec, 10, 1800, '健康检查超时');
    if (!parsedTimeout.ok) nextErrors.healthCheckTimeoutSec = parsedTimeout.error;

    const tagResult = normalizeTargetTagDrafts(tagEntries);
    if (!tagResult.ok) nextErrors.tags = tagResult.error;

    const trimmedReason = reason.trim();
    if (!trimmedReason) {
      nextErrors.reason = '请填写本次发布的变更说明';
    } else if (trimmedReason.length > 1000) {
      nextErrors.reason = '变更说明不能超过 1000 个字符';
    }

    if (
      Object.keys(nextErrors).length > 0 ||
      !parsedCanary.ok ||
      !parsedThreshold.ok ||
      !parsedTimeout.ok ||
      !tagResult.ok
    ) {
      setErrors(nextErrors);
      return;
    }

    const requestBody: OTACampaignCreateRequest = {
      name: trimmedName,
      packageId,
      canaryPercent: parsedCanary.value,
      failureRateThresholdPct: parsedThreshold.value,
      healthCheckTimeoutSec: parsedTimeout.value,
      autoPromoteRollout,
      reason: trimmedReason,
      ...(selectedSiteIds.length > 0 ? { targetSiteIds: selectedSiteIds } : {}),
      ...(Object.keys(tagResult.tags).length > 0 ? { targetTags: tagResult.tags } : {}),
    };

    createMutation.mutate(requestBody);
  };

  return (
    <Dialog
      open={open}
      onOpenChange={(nextOpen) => {
        if (createMutation.isPending) return;
        if (!nextOpen) {
          resetForm();
          createMutation.reset();
        }
        onOpenChange(nextOpen);
      }}
    >
      <DialogContent
        className="max-h-[90vh] overflow-y-auto sm:max-w-2xl"
        showCloseButton={!createMutation.isPending}
        onEscapeKeyDown={(event) => {
          if (createMutation.isPending) event.preventDefault();
        }}
        onPointerDownOutside={(event) => {
          if (createMutation.isPending) event.preventDefault();
        }}
        onInteractOutside={(event) => {
          if (createMutation.isPending) event.preventDefault();
        }}
      >
        <DialogHeader>
          <DialogTitle>发起灰度发布任务？</DialogTitle>
          <DialogDescription>
            先向灰度批网关推送目标版本，健康检查通过后放量到其余网关；失败率达到熔断阈值时自动暂停并等待处置。
          </DialogDescription>
        </DialogHeader>

        <form noValidate className="space-y-4" onSubmit={handleSubmit}>
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
            <Field data-invalid={Boolean(errors.packageId)}>
              <FieldLabel htmlFor="ota-campaign-package">目标升级包</FieldLabel>
              <Select
                value={packageId}
                disabled={createMutation.isPending || activePackages.length === 0}
                onValueChange={(nextPkg) => {
                  setPackageId(nextPkg);
                  if (errors.packageId) setErrors((prev) => ({ ...prev, packageId: undefined }));
                }}
              >
                <SelectTrigger id="ota-campaign-package" className="w-full">
                  <SelectValue placeholder="选择可用升级包" />
                </SelectTrigger>
                <SelectContent>
                  {activePackages.map((pkg) => (
                    <SelectItem key={pkg.id} value={pkg.id}>
                      {OTA_COMPONENT_LABELS[pkg.component]} v{pkg.version} ({pkg.targetArch})
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <FieldError>{errors.packageId}</FieldError>
            </Field>

            <Field data-invalid={Boolean(errors.name)}>
              <FieldLabel htmlFor="ota-campaign-name">发布任务名称</FieldLabel>
              <Input
                id="ota-campaign-name"
                value={name}
                maxLength={160}
                placeholder="例如：松雅轩 1.5.0 守护进程灰度"
                disabled={createMutation.isPending}
                aria-invalid={Boolean(errors.name)}
                aria-describedby={errors.name ? 'ota-campaign-name-error' : undefined}
                onChange={(event) => {
                  setName(event.target.value);
                  if (errors.name) setErrors((prev) => ({ ...prev, name: undefined }));
                }}
              />
              <FieldError id="ota-campaign-name-error">{errors.name}</FieldError>
            </Field>
          </div>

          <FieldSet className="gap-3 rounded-lg border border-divider p-3.5">
            <FieldLegend variant="label" className="px-1">
              目标网关筛选
            </FieldLegend>

            <div className="space-y-2">
              <span className="text-xs font-medium text-foreground">目标站点</span>
              <div className="flex flex-wrap gap-x-4 gap-y-2">
                {sites.map((site) => {
                  const checkboxId = `ota-campaign-site-${site.id}`;
                  const checked = selectedSiteIds.includes(site.id);
                  return (
                    <div key={site.id} className="flex items-center gap-2">
                      <Checkbox
                        id={checkboxId}
                        checked={checked}
                        disabled={createMutation.isPending}
                        onCheckedChange={(state) => handleToggleSite(site.id, state === true)}
                      />
                      <Label htmlFor={checkboxId} className="cursor-pointer text-xs font-normal">
                        {site.displayName}
                      </Label>
                    </div>
                  );
                })}
              </div>
              <p className="text-[11px] leading-5 text-muted-foreground">
                不勾选任何站点时，将面向当前账号全部授权站点筛选目标网关。
              </p>
            </div>

            <div className={selectedSiteIds.length === 0 ? 'rounded-md border border-warning/40 bg-warning-soft/50 px-3 py-2 text-xs text-warning-text' : 'rounded-md border bg-muted/40 px-3 py-2 text-xs text-muted-foreground'}>
              {selectedSiteIds.length === 0
                ? `未勾选站点：将面向全部授权站点筛选，预计匹配 ${matchedGatewayCount} 台激活网关。`
                : `已选 ${selectedSiteIds.length} 个站点，预计匹配 ${matchedGatewayCount} 台激活网关。`}
            </div>

            <div className="space-y-2 border-t border-divider pt-3">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <span className="text-xs font-medium text-foreground">标签过滤条件（可选）</span>
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  disabled={createMutation.isPending}
                  onClick={() => setTagEntries((prev) => [...prev, { key: '', value: '' }])}
                >
                  <Plus data-icon="inline-start" />
                  添加标签条件
                </Button>
              </div>

              {suggestedTags.length > 0 ? (
                <div className="flex flex-wrap items-center gap-1.5">
                  <span className="text-[11px] text-muted-foreground">当前网关常用标签：</span>
                  {suggestedTags.map((tag) => (
                    <Button
                      key={`${tag.key}=${tag.value}`}
                      type="button"
                      variant="outline"
                      size="sm"
                      className="h-6 px-2 font-mono text-[11px]"
                      disabled={createMutation.isPending}
                      onClick={() => handleApplySuggestedTag(tag.key, tag.value)}
                    >
                      {tag.key}={tag.value}
                    </Button>
                  ))}
                </div>
              ) : null}

              {tagEntries.length > 0 ? (
                <div className="space-y-2">
                  {tagEntries.map((entry, idx) => (
                    <div key={idx} className="flex items-center gap-2">
                      <Input
                        aria-label={`标签第 ${idx + 1} 行键名`}
                        placeholder="键名，如 env"
                        value={entry.key}
                        disabled={createMutation.isPending}
                        autoCorrect="off"
                        autoCapitalize="none"
                        spellCheck={false}
                        className="font-mono text-xs"
                        onChange={(event) => {
                          const nextVal = event.target.value;
                          setTagEntries((prev) =>
                            prev.map((row, rowIdx) => (rowIdx === idx ? { ...row, key: nextVal } : row)),
                          );
                          if (errors.tags) setErrors((prev) => ({ ...prev, tags: undefined }));
                        }}
                      />
                      <Input
                        aria-label={`标签第 ${idx + 1} 行值`}
                        placeholder="值，如 pilot"
                        value={entry.value}
                        disabled={createMutation.isPending}
                        autoCorrect="off"
                        autoCapitalize="none"
                        spellCheck={false}
                        className="font-mono text-xs"
                        onChange={(event) => {
                          const nextVal = event.target.value;
                          setTagEntries((prev) =>
                            prev.map((row, rowIdx) => (rowIdx === idx ? { ...row, value: nextVal } : row)),
                          );
                          if (errors.tags) setErrors((prev) => ({ ...prev, tags: undefined }));
                        }}
                      />
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon-sm"
                        aria-label={`移除第 ${idx + 1} 行标签`}
                        disabled={createMutation.isPending}
                        onClick={() => {
                          setTagEntries((prev) => prev.filter((_, rowIdx) => rowIdx !== idx));
                          if (errors.tags) setErrors((prev) => ({ ...prev, tags: undefined }));
                        }}
                      >
                        <Trash2 />
                      </Button>
                    </div>
                  ))}
                </div>
              ) : null}

              <FieldDescription>
                填写后仅升级全部标签均匹配的网关；网关也会继承所绑定设备对象的标签。
              </FieldDescription>
              <FieldError>{errors.tags}</FieldError>
            </div>
          </FieldSet>

          <FieldSet className="gap-3 rounded-lg border border-divider p-3.5">
            <FieldLegend variant="label" className="px-1">
              灰度放量与自动熔断策略
            </FieldLegend>
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
              <Field data-invalid={Boolean(errors.canaryPercent)}>
                <FieldLabel htmlFor="ota-campaign-canary-pct">灰度批比例 (%)</FieldLabel>
                <Input
                  id="ota-campaign-canary-pct"
                  type="number"
                  min={1}
                  max={100}
                  inputMode="numeric"
                  value={canaryPercent}
                  disabled={createMutation.isPending}
                  aria-invalid={Boolean(errors.canaryPercent)}
                  onChange={(event) => {
                    setCanaryPercent(event.target.value);
                    if (errors.canaryPercent) setErrors((prev) => ({ ...prev, canaryPercent: undefined }));
                  }}
                />
                <FieldError>{errors.canaryPercent}</FieldError>
              </Field>

              <Field data-invalid={Boolean(errors.failureRateThresholdPct)}>
                <FieldLabel htmlFor="ota-campaign-threshold-pct">失败熔断阈值 (%)</FieldLabel>
                <Input
                  id="ota-campaign-threshold-pct"
                  type="number"
                  min={1}
                  max={100}
                  inputMode="numeric"
                  value={failureRateThresholdPct}
                  disabled={createMutation.isPending}
                  aria-invalid={Boolean(errors.failureRateThresholdPct)}
                  onChange={(event) => {
                    setFailureRateThresholdPct(event.target.value);
                    if (errors.failureRateThresholdPct) {
                      setErrors((prev) => ({ ...prev, failureRateThresholdPct: undefined }));
                    }
                  }}
                />
                <FieldError>{errors.failureRateThresholdPct}</FieldError>
              </Field>

              <Field data-invalid={Boolean(errors.healthCheckTimeoutSec)}>
                <FieldLabel htmlFor="ota-campaign-timeout-sec">健康检查超时 (秒)</FieldLabel>
                <Input
                  id="ota-campaign-timeout-sec"
                  type="number"
                  min={10}
                  max={1800}
                  inputMode="numeric"
                  value={healthCheckTimeoutSec}
                  disabled={createMutation.isPending}
                  aria-invalid={Boolean(errors.healthCheckTimeoutSec)}
                  onChange={(event) => {
                    setHealthCheckTimeoutSec(event.target.value);
                    if (errors.healthCheckTimeoutSec) {
                      setErrors((prev) => ({ ...prev, healthCheckTimeoutSec: undefined }));
                    }
                  }}
                />
                <FieldError>{errors.healthCheckTimeoutSec}</FieldError>
              </Field>
            </div>

            <div className="flex items-center gap-2 pt-1">
              <Checkbox
                id="ota-campaign-auto-promote"
                checked={autoPromoteRollout}
                disabled={createMutation.isPending}
                onCheckedChange={(checked) => setAutoPromoteRollout(checked === true)}
              />
              <Label htmlFor="ota-campaign-auto-promote" className="cursor-pointer text-xs font-normal">
                灰度批全部升级成功后自动放量到全量批（关闭后需人工确认再放量）
              </Label>
            </div>
          </FieldSet>

          <Field data-invalid={Boolean(errors.reason)}>
            <FieldLabel htmlFor="ota-campaign-reason">发布变更原因</FieldLabel>
            <Textarea
              id="ota-campaign-reason"
              value={reason}
              maxLength={1000}
              rows={2}
              placeholder="例如：修复边缘守护进程串口重连超时并先行灰度验证"
              disabled={createMutation.isPending}
              aria-invalid={Boolean(errors.reason)}
              aria-describedby={errors.reason ? 'ota-campaign-reason-error' : undefined}
              onChange={(event) => {
                setReason(event.target.value);
                if (errors.reason) setErrors((prev) => ({ ...prev, reason: undefined }));
              }}
            />
            <FieldError id="ota-campaign-reason-error">{errors.reason}</FieldError>
          </Field>

          {createMutation.isError ? (
            <Alert variant="destructive">
              <AlertDescription>
                {describeOtaError(createMutation.error, '发布任务未能发起，请检查目标范围和参数。')}
              </AlertDescription>
            </Alert>
          ) : null}

          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              disabled={createMutation.isPending}
              onClick={() => {
                resetForm();
                createMutation.reset();
                onOpenChange(false);
              }}
            >
              取消
            </Button>
            <Button type="submit" disabled={createMutation.isPending || activePackages.length === 0}>
              {createMutation.isPending ? <Spinner data-icon="inline-start" /> : <Rocket data-icon="inline-start" />}
              {createMutation.isPending ? '正在发起…' : '发起灰度发布'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
