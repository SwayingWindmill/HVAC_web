import { useId, useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { RefreshCw, Settings2 } from 'lucide-react';
import { toast } from 'sonner';
import type { Device, GatewayDesiredConfig } from '@/api/generated/platformGateway.gen';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Label } from '@/components/ui/label';
import { Skeleton } from '@/components/ui/skeleton';
import { Textarea } from '@/components/ui/textarea';
import { cn } from '@/lib/utils';
import {
  CONFIG_SHADOW_SYNC_PRESENTATION,
  describeGatewayConfigShadowError,
} from './gateway-config-shadow-labels';
import {
  buildValidatedShadowUpdateRequestFromDesired,
  createShadowEditorDraft,
  summarizeConcentratorShadowUnits,
  type GatewayConfigShadowEditorDraft,
} from './gateway-config-shadow-model';
import { AutonomyPolicyEditorSection } from './GatewayAutonomyEditorSection';
import { DriverConfigsEditorSection } from './GatewayDriverEditorSection';
import {
  PointMappingsEditorSection,
  SamplingPoliciesEditorSection,
} from './GatewayShadowMappingSections';
import { GatewayShadowSummaryCard } from './GatewayShadowSummaryCard';
import { gatewayConfigShadowApi, gatewayConfigShadowKey } from './gateway-credentials';

const SHADOW_TONE_CLASSES = {
  neutral: { chip: 'bg-secondary text-secondary-foreground', dot: 'bg-subtle-foreground' },
  warning: { chip: 'bg-warning-soft text-warning-text', dot: 'bg-warning' },
  success: { chip: 'bg-success-soft text-success-text', dot: 'bg-success' },
  danger: { chip: 'bg-destructive-soft text-destructive-text', dot: 'bg-destructive' },
} as const;

interface ShadowSavePayload {
  readonly expectedRevision: number;
  readonly currentDesired: GatewayDesiredConfig | undefined;
  readonly draft: GatewayConfigShadowEditorDraft;
}

export function GatewayConfigShadowSection({
  gateway,
  csrfToken,
  canWrite,
  stopped,
}: {
  readonly gateway: Device;
  readonly csrfToken: string;
  readonly canWrite: boolean;
  readonly stopped: boolean;
}) {
  const queryClient = useQueryClient();
  const baseId = useId();
  const reasonId = `${baseId}-reason`;
  const [editorOpen, setEditorOpen] = useState(false);
  const [draft, setDraft] = useState<GatewayConfigShadowEditorDraft>(() =>
    createShadowEditorDraft(undefined),
  );
  const [validationError, setValidationError] = useState<string | null>(null);

  const shadowQuery = useQuery({
    queryKey: gatewayConfigShadowKey(gateway.id),
    queryFn: ({ signal }) => gatewayConfigShadowApi.get(gateway.id, signal),
    retry: false,
  });

  const saveMutation = useMutation({
    mutationFn: async ({ expectedRevision, currentDesired, draft: capturedDraft }: ShadowSavePayload) => {
      const built = buildValidatedShadowUpdateRequestFromDesired(
        capturedDraft,
        expectedRevision,
        currentDesired,
      );
      if (!built.ok) {
        throw new Error(built.error);
      }
      return gatewayConfigShadowApi.update(gateway.id, built.request, csrfToken);
    },
    onSuccess: (updated) => {
      queryClient.setQueryData(gatewayConfigShadowKey(gateway.id), updated);
      setValidationError(null);
      setEditorOpen(false);
      toast.success(
        updated.desired
          ? `配置影子已保存（期望修订 v${updated.desired.desiredRevision}）`
          : '配置影子已更新',
      );
    },
  });

  const shadow = shadowQuery.data;
  const concentratorUnits = useMemo(
    () => summarizeConcentratorShadowUnits(shadow?.desired?.concentratorConfigs),
    [shadow?.desired?.concentratorConfigs],
  );

  const openEditor = () => {
    setDraft(createShadowEditorDraft(shadow));
    setValidationError(null);
    saveMutation.reset();
    setEditorOpen(true);
  };

  const handleSave = async () => {
    const currentDesired = shadow?.desired;
    const expectedRevision = currentDesired?.desiredRevision ?? 0;
    const built = buildValidatedShadowUpdateRequestFromDesired(
      draft,
      expectedRevision,
      currentDesired,
    );
    if (!built.ok) {
      setValidationError(built.error);
      return;
    }
    setValidationError(null);
    try {
      await saveMutation.mutateAsync({ expectedRevision, currentDesired, draft });
    } catch {
      // Displayed inside the dialog Alert via saveMutation.error
    }
  };

  const syncPresentation = shadow ? CONFIG_SHADOW_SYNC_PRESENTATION[shadow.syncStatus] : null;
  const toneClass = syncPresentation ? SHADOW_TONE_CLASSES[syncPresentation.tone] : null;
  const editDisabled = !canWrite || stopped || shadowQuery.isPending;

  return (
    <section aria-label="配置影子" className="flex flex-col gap-3 border-b border-divider px-6 py-4">
      <div className="flex items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <h3 className="text-sm font-semibold">配置影子</h3>
          {syncPresentation && toneClass ? (
            <span
              className={cn(
                'inline-flex h-5.5 shrink-0 items-center gap-1.5 rounded-md px-1.5 text-xs font-medium whitespace-nowrap',
                toneClass.chip,
              )}
            >
              <span className={cn('size-1.5 rounded-full', toneClass.dot)} aria-hidden="true" />
              {syncPresentation.label}
            </span>
          ) : null}
        </div>
        <div className="flex items-center gap-1.5">
          <Button
            variant="outline"
            size="sm"
            disabled={shadowQuery.isFetching}
            onClick={() => void shadowQuery.refetch()}
          >
            <RefreshCw data-icon="inline-start" aria-hidden="true" />
            刷新影子
          </Button>
          <Button
            variant="outline"
            size="sm"
            disabled={editDisabled}
            onClick={openEditor}
          >
            <Settings2 data-icon="inline-start" aria-hidden="true" />
            {shadow?.syncStatus === 'UNCONFIGURED' ? '初始化配置影子' : '编辑配置影子'}
          </Button>
        </div>
      </div>

      {shadowQuery.isPending ? (
        <Skeleton className="h-24 rounded-lg" />
      ) : shadowQuery.isError ? (
        <Alert variant="destructive">
          <AlertDescription className="flex items-center justify-between gap-3">
            <span>{describeGatewayConfigShadowError(shadowQuery.error)}</span>
            <Button variant="outline" size="sm" onClick={() => void shadowQuery.refetch()}>
              重试
            </Button>
          </AlertDescription>
        </Alert>
      ) : shadow ? (
        <GatewayShadowSummaryCard
          shadow={shadow}
          concentratorUnits={concentratorUnits}
          stopped={stopped}
        />
      ) : null}

      <Dialog
        open={editorOpen}
        onOpenChange={(nextOpen) => {
          if (!saveMutation.isPending) setEditorOpen(nextOpen);
        }}
      >
        <DialogContent className="max-h-[85vh] overflow-y-auto sm:max-w-[780px]">
          <DialogHeader>
            <DialogTitle>编辑网关配置影子</DialogTitle>
            <DialogDescription>
              修改协议驱动点表、断网自治策略、点位映射与采集周期后保存为期望配置，边缘网关在线或重连后自动拉取并回传生效状态。
            </DialogDescription>
          </DialogHeader>

          <div className="flex flex-col gap-5 py-2 text-sm">
            <DriverConfigsEditorSection
              driverConfigs={draft.driverConfigs}
              disabled={saveMutation.isPending}
              onChange={(nextDrivers) =>
                setDraft((current) => ({ ...current, driverConfigs: nextDrivers }))
              }
            />

            <AutonomyPolicyEditorSection
              autonomyPolicy={draft.autonomyPolicy}
              disabled={saveMutation.isPending}
              onChange={(nextAutonomy) =>
                setDraft((current) => ({ ...current, autonomyPolicy: nextAutonomy }))
              }
            />

            <PointMappingsEditorSection
              pointMappings={draft.pointMappings}
              disabled={saveMutation.isPending}
              onChange={(nextMappings) =>
                setDraft((current) => ({ ...current, pointMappings: nextMappings }))
              }
            />

            <SamplingPoliciesEditorSection
              samplingPolicies={draft.samplingPolicies}
              disabled={saveMutation.isPending}
              onChange={(nextPolicies) =>
                setDraft((current) => ({ ...current, samplingPolicies: nextPolicies }))
              }
            />

            {concentratorUnits.length > 0 ? (
              <p className="rounded-md border bg-muted/30 px-3 py-2 text-xs text-muted-foreground">
                本次保存会完整保留已暂存的 {concentratorUnits.length} 个无线集中器单元配置（UID{' '}
                {concentratorUnits.map((unit) => unit.unitId).join('、')}）；集中器参数与 EID 槽位请在「无线测温集中器」页编辑。
              </p>
            ) : null}

            <div className="flex flex-col gap-2">
              <Label htmlFor={reasonId} className="text-xs font-medium">
                变更原因（必填）
              </Label>
              <Textarea
                id={reasonId}
                value={draft.reason}
                disabled={saveMutation.isPending}
                maxLength={1000}
                onChange={(event) => {
                  const value = event.currentTarget.value;
                  setDraft((current) => ({ ...current, reason: value }));
                }}
                placeholder="请说明本次调整协议驱动点表、断网自治策略、点位映射或采集周期的原因"
              />
            </div>

            {validationError || saveMutation.error ? (
              <Alert variant="destructive">
                <AlertDescription className="flex flex-col gap-2">
                  <span>
                    {validationError ?? describeGatewayConfigShadowError(saveMutation.error)}
                  </span>
                  {saveMutation.error ? (
                    <Button
                      type="button"
                      variant="outline"
                      size="sm"
                      className="self-start"
                      onClick={() => {
                        void shadowQuery.refetch().then((refetched) => {
                          if (refetched.data) {
                            setDraft((current) => ({
                              ...createShadowEditorDraft(refetched.data),
                              reason: current.reason,
                            }));
                            saveMutation.reset();
                            setValidationError(null);
                          }
                        });
                      }}
                    >
                      加载最新影子修订
                    </Button>
                  ) : null}
                </AlertDescription>
              </Alert>
            ) : null}
          </div>

          <DialogFooter>
            <Button
              variant="outline"
              disabled={saveMutation.isPending}
              onClick={() => setEditorOpen(false)}
            >
              取消
            </Button>
            <Button
              disabled={saveMutation.isPending || !canWrite || stopped}
              onClick={() => void handleSave()}
            >
              {saveMutation.isPending ? '正在保存…' : '保存期望配置'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </section>
  );
}
