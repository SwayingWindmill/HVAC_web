import { useEffect, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { AlertCircle, AlertTriangle, CheckCircle2, Cpu, RefreshCw, Trash2, UploadCloud } from 'lucide-react';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet';
import { Skeleton } from '@/components/ui/skeleton';
import {
  gatewayConfigShadowApi,
  gatewayConfigShadowKey,
} from '@/features/connectivity/gateway-credentials';
import { summarizeEdgeRuleBundle } from '@/features/connectivity/gateway-config-shadow-model';
import {
  CONFIG_SHADOW_SYNC_PRESENTATION,
  describeGatewayConfigShadowError,
} from '@/features/integrations/integrations-model';
import { siteAssetModelQuery } from '@/api/site-asset-model';
import {
  EDGE_ELIGIBILITY_REASON_LABELS,
  EXECUTION_TARGET_LABELS,
  ruleToDraft,
} from '../alarm-rule-dag';
import {
  compileSiteAlarmRulesEdge,
  type AlarmRule,
  type EdgeRuleCompileResult,
} from '../alarm-rules';

export function AlarmRuleEdgeCompileSheet({
  open,
  siteId,
  canManage,
  canReadGatewayShadow,
  canManageGatewayShadow,
  csrfToken,
  hasUnsavedDraft,
  draftRules,
  publishedBundleRevision,
  onClose,
}: {
  readonly open: boolean;
  readonly siteId: string;
  readonly canManage: boolean;
  readonly canReadGatewayShadow: boolean;
  readonly canManageGatewayShadow: boolean;
  readonly csrfToken: string;
  readonly hasUnsavedDraft: boolean;
  readonly draftRules: readonly AlarmRule[] | undefined;
  readonly publishedBundleRevision: number;
  readonly onClose: () => void;
}) {
  const queryClient = useQueryClient();
  const [compileResult, setCompileResult] = useState<EdgeRuleCompileResult | null>(null);
  const [selectedGatewayId, setSelectedGatewayId] = useState('');
  const [pushSuccessNotice, setPushSuccessNotice] = useState<string | null>(null);

  const compileMutation = useMutation({
    mutationFn: () => compileSiteAlarmRulesEdge(
      siteId,
      hasUnsavedDraft && draftRules ? draftRules.map(ruleToDraft) : undefined,
      csrfToken,
    ),
    onSuccess: (data) => {
      setCompileResult(data);
    },
  });

  const triggerCompile = compileMutation.mutate;
  const resetCompile = compileMutation.reset;

  const assetModelQuery = useQuery(
    siteAssetModelQuery(siteId, {
      enabled: open && canReadGatewayShadow,
      staleTime: 30_000,
      retry: false,
    }),
  );

  const gateways = (assetModelQuery.data?.devices ?? []).filter(
    (device) => device.deviceType === 'GATEWAY' && device.status === 'ACTIVE',
  );

  const activeGatewayId = gateways.some((gw) => gw.id === selectedGatewayId)
    ? selectedGatewayId
    : (gateways[0]?.id ?? '');

  const shadowQuery = useQuery({
    queryKey: gatewayConfigShadowKey(activeGatewayId || 'NONE'),
    queryFn: ({ signal }) => gatewayConfigShadowApi.get(activeGatewayId, signal),
    enabled: open && canReadGatewayShadow && Boolean(activeGatewayId),
    retry: false,
  });

  const pushShadowMutation = useMutation({
    mutationFn: (payload: {
      readonly gatewayId: string;
      readonly expectedDesiredRevision: number;
      readonly edgeRuleBundle: EdgeRuleCompileResult['bundle'] | null;
      readonly reason: string;
    }) =>
      gatewayConfigShadowApi.update(
        payload.gatewayId,
        {
          expectedDesiredRevision: payload.expectedDesiredRevision,
          edgeRuleBundle: payload.edgeRuleBundle,
          reason: payload.reason,
        },
        csrfToken,
      ),
    onSuccess: (updated, variables) => {
      queryClient.setQueryData(gatewayConfigShadowKey(variables.gatewayId), updated);
      const summary = summarizeEdgeRuleBundle(updated.desired?.edgeRuleBundle);
      if (variables.edgeRuleBundle === null) {
        setPushSuccessNotice(
          `已清空目标网关配置影子中的边缘规则包（期望修订 v${updated.desired?.desiredRevision ?? 0}）。`,
        );
      } else {
        setPushSuccessNotice(
          `已下发 ${summary.ruleCount} 条边缘规则至网关配置影子（期望修订 v${updated.desired?.desiredRevision ?? 0}，摘要 ${summary.shortDigest}）。`,
        );
      }
    },
  });
  const resetPush = pushShadowMutation.reset;

  useEffect(() => {
    if (!open) {
      setCompileResult(null);
      setPushSuccessNotice(null);
      resetCompile();
      resetPush();
      return;
    }
    setPushSuccessNotice(null);
    resetPush();
    triggerCompile();
  }, [open, resetCompile, resetPush, triggerCompile]);

  const currentEdgeSummary = summarizeEdgeRuleBundle(shadowQuery.data?.desired?.edgeRuleBundle);
  const shadowSync = shadowQuery.data
    ? CONFIG_SHADOW_SYNC_PRESENTATION[shadowQuery.data.syncStatus]
    : null;
  const compileSummary = compileResult
    ? summarizeEdgeRuleBundle(compileResult.bundle)
    : null;

  return (
    <Sheet open={open} onOpenChange={(nextOpen) => { if (!nextOpen && !pushShadowMutation.isPending) onClose(); }}>
      <SheetContent side="right" className="w-full overflow-y-auto data-[side=right]:sm:max-w-[680px]">
        <SheetHeader className="border-b border-divider">
          <SheetTitle className="flex items-center gap-2">
            <Cpu className="size-4 text-primary" aria-hidden="true" />
            <span>云边统一编译与网关下发</span>
          </SheetTitle>
          <SheetDescription>
            自动筛选满足边缘执行条件的告警与保护规则，生成携带数字摘要校验的标准化边缘规则包，并同步下发至现场网关配置影子。
          </SheetDescription>
        </SheetHeader>

        <div className="flex flex-1 flex-col gap-4 p-4 text-xs">
          {hasUnsavedDraft ? (
            <div role="alert" className="flex items-start gap-2 rounded-lg border border-warning/40 bg-warning-soft px-3.5 py-2.5 text-warning-text">
              <AlertTriangle className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
              <span>
                当前工作台存在未发布草稿，本页面展示基于草稿规则的预编译结果；请先完成「预览差异并热发布」，再向边缘网关下发规则包。
              </span>
            </div>
          ) : null}

          <div className="flex flex-wrap items-center justify-between gap-2">
            <span className="font-medium text-foreground">
              {hasUnsavedDraft ? '草稿预编译检查' : `基线版本 v${publishedBundleRevision} 边缘编译`}
            </span>
            <Button
              type="button"
              variant="outline"
              size="sm"
              disabled={compileMutation.isPending}
              onClick={() => {
                setPushSuccessNotice(null);
                compileMutation.mutate();
              }}
            >
              <RefreshCw data-icon="inline-start" className="size-3.5" aria-hidden="true" />
              {compileMutation.isPending ? '正在编译…' : '重新编译'}
            </Button>
          </div>

          {compileMutation.isPending && !compileResult ? (
            <div className="flex flex-col gap-2">
              <Skeleton className="h-16" />
              <Skeleton className="h-24" />
            </div>
          ) : null}

          {compileMutation.isError ? (
            <Alert variant="destructive">
              <AlertCircle className="size-4" aria-hidden="true" />
              <AlertDescription>
                {compileMutation.error instanceof Error
                  ? compileMutation.error.message
                  : '边缘规则包编译失败，请检查规则依赖图或条件参数后重试。'}
              </AlertDescription>
            </Alert>
          ) : null}

          {compileResult && compileSummary ? (
            <>
              <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
                <div className="rounded-lg border border-divider bg-card px-3 py-2">
                  <div className="text-[11px] text-muted-foreground">边缘可执行</div>
                  <div className="mt-0.5 text-base font-semibold text-foreground tabular-nums">
                    {compileResult.edgeRuleCount} 条
                  </div>
                </div>
                <div className="rounded-lg border border-divider bg-card px-3 py-2">
                  <div className="text-[11px] text-muted-foreground">仅云端执行</div>
                  <div className="mt-0.5 text-base font-semibold text-foreground tabular-nums">
                    {compileResult.cloudOnlyCount} 条
                  </div>
                </div>
                <div className="rounded-lg border border-divider bg-card px-3 py-2">
                  <div className="text-[11px] text-muted-foreground">规则包版本</div>
                  <div className="mt-0.5 font-mono text-base font-semibold text-foreground tabular-nums">
                    v{compileResult.bundleRevision}
                  </div>
                </div>
                <div className="rounded-lg border border-divider bg-card px-3 py-2">
                  <div className="text-[11px] text-muted-foreground">边缘摘要</div>
                  <div className="mt-0.5 font-mono text-sm font-semibold text-foreground">
                    {compileSummary.shortDigest}
                  </div>
                </div>
              </div>

              <div className="flex flex-col gap-2 rounded-lg border border-divider bg-card p-3">
                <div className="flex items-center justify-between gap-2">
                  <span className="font-medium text-foreground">逐条准入检查明细</span>
                  <span className="text-[11px] text-muted-foreground">
                    共 {compileResult.eligibility.length} 条规则（按联动拓扑序）
                  </span>
                </div>
                {compileResult.eligibility.length === 0 ? (
                  <p className="py-2 text-muted-foreground">站点当前没有可检查的告警规则。</p>
                ) : (
                  <ul className="flex max-h-60 flex-col gap-1.5 overflow-y-auto">
                    {compileResult.eligibility.map((item) => {
                      const targetLabel = EXECUTION_TARGET_LABELS[item.executionTarget];
                      const rawMsg = item.reasonCode
                        ? (EDGE_ELIGIBILITY_REASON_LABELS[item.reasonCode] ?? item.message)
                        : item.message;
                      const reasonText = rawMsg && /[\u4e00-\u9fff]/.test(rawMsg)
                        ? rawMsg
                        : (!item.compiled ? '当前规则不满足边缘执行准入条件，仅在云端评估。' : undefined);
                      return (
                        <li
                          key={item.ruleId}
                          className="flex flex-col gap-1 rounded-md border border-divider bg-muted/20 px-3 py-2"
                        >
                          <div className="flex flex-wrap items-center justify-between gap-2">
                            <span className="font-medium text-foreground">{item.title}</span>
                            <div className="flex items-center gap-1.5">
                              <Badge variant="outline" className="h-5 px-1.5 text-[11px] font-normal">
                                {targetLabel}
                              </Badge>
                              <span
                                className={item.compiled
                                  ? 'inline-flex h-5 items-center rounded bg-success-soft px-1.5 text-[11px] font-medium text-success-text'
                                  : 'inline-flex h-5 items-center rounded bg-muted px-1.5 text-[11px] font-medium text-muted-foreground'}
                              >
                                {item.compiled ? '已编入边缘' : '留在云端'}
                              </span>
                            </div>
                          </div>
                          {!item.compiled && reasonText ? (
                            <p className="text-[11px] text-muted-foreground">{reasonText}</p>
                          ) : null}
                        </li>
                      );
                    })}
                  </ul>
                )}
              </div>

              <div className="flex flex-col gap-3 rounded-lg border border-divider bg-card p-3">
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <span className="font-medium text-foreground">边缘网关配置影子下发</span>
                  {shadowSync ? (
                    <Badge variant="outline" className="h-5 px-1.5 text-[11px] font-normal">
                      {shadowSync.label}
                    </Badge>
                  ) : null}
                </div>

                {!canReadGatewayShadow ? (
                  <p className="text-muted-foreground">当前账号缺少设备读取权限，暂无法读取或下发网关配置影子。</p>
                ) : assetModelQuery.isPending ? (
                  <Skeleton className="h-12" />
                ) : assetModelQuery.isError ? (
                  <Alert variant="destructive">
                    <AlertDescription>无法读取站点边缘网关列表，请稍后重试。</AlertDescription>
                  </Alert>
                ) : gateways.length === 0 ? (
                  <p className="text-muted-foreground">当前站点暂无在线或已激活的边缘网关设备。</p>
                ) : (
                  <>
                    <div className="flex flex-col gap-1">
                      <span className="text-[11px] text-muted-foreground">目标边缘网关</span>
                      <Select value={activeGatewayId} onValueChange={setSelectedGatewayId}>
                        <SelectTrigger className="w-full" aria-label="目标边缘网关">
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                          {gateways.map((gw) => (
                            <SelectItem key={gw.id} value={gw.id}>
                              {gw.displayName} ({gw.code})
                            </SelectItem>
                          ))}
                        </SelectContent>
                      </Select>
                    </div>

                    {shadowQuery.isPending ? (
                      <Skeleton className="h-12" />
                    ) : shadowQuery.isError ? (
                      <Alert variant="destructive">
                        <AlertDescription>
                          {describeGatewayConfigShadowError(shadowQuery.error)}
                        </AlertDescription>
                      </Alert>
                    ) : shadowQuery.data ? (
                      <div className="flex flex-col gap-2 rounded-md border border-divider bg-muted/20 px-3 py-2.5 text-[11px]">
                        <div className="flex flex-wrap items-center justify-between gap-2">
                          <span className="text-muted-foreground">
                            当前期望修订：
                            <span className="ml-1 font-mono font-semibold text-foreground tabular-nums">
                              {shadowQuery.data.desired ? `v${shadowQuery.data.desired.desiredRevision}` : '未设置'}
                            </span>
                          </span>
                          <span className="text-muted-foreground">
                            已下发边缘规则包：
                            <span className="ml-1 font-mono font-medium text-foreground tabular-nums">
                              {currentEdgeSummary.configured
                                ? `${currentEdgeSummary.ruleCount} 条 · 摘要 ${currentEdgeSummary.shortDigest}`
                                : '未下发'}
                            </span>
                          </span>
                        </div>
                        {!canManage || !canManageGatewayShadow ? (
                          <p className="text-muted-foreground">
                            当前账号缺少规则管理或设备配置写入权限，仅支持查看边缘编译准入结果。
                          </p>
                        ) : !hasUnsavedDraft ? (
                          <div className="flex flex-wrap items-center gap-2 pt-1">
                            <Button
                              type="button"
                              size="sm"
                              disabled={compileResult.edgeRuleCount === 0 || pushShadowMutation.isPending}
                              onClick={() => {
                                setPushSuccessNotice(null);
                                pushShadowMutation.mutate({
                                  gatewayId: activeGatewayId,
                                  expectedDesiredRevision: shadowQuery.data?.desired?.desiredRevision ?? 0,
                                  edgeRuleBundle: compileResult.bundle,
                                  reason: '告警规则工作台下发边缘规则包',
                                });
                              }}
                            >
                              <UploadCloud data-icon="inline-start" className="size-3.5" aria-hidden="true" />
                              {pushShadowMutation.isPending ? '下发中…' : '下发至网关配置影子'}
                            </Button>
                            {currentEdgeSummary.configured ? (
                              <Button
                                type="button"
                                variant="outline"
                                size="sm"
                                disabled={pushShadowMutation.isPending}
                                onClick={() => {
                                  setPushSuccessNotice(null);
                                  pushShadowMutation.mutate({
                                    gatewayId: activeGatewayId,
                                    expectedDesiredRevision: shadowQuery.data?.desired?.desiredRevision ?? 0,
                                    edgeRuleBundle: null,
                                    reason: '告警规则工作台清空边缘规则包',
                                  });
                                }}
                              >
                                <Trash2 data-icon="inline-start" className="size-3.5" aria-hidden="true" />
                                清空边缘规则包
                              </Button>
                            ) : null}
                          </div>
                        ) : null}
                      </div>
                    ) : null}

                    {pushShadowMutation.isError ? (
                      <Alert variant="destructive">
                        <AlertCircle className="size-4" aria-hidden="true" />
                        <AlertDescription>
                          {describeGatewayConfigShadowError(pushShadowMutation.error)}
                        </AlertDescription>
                      </Alert>
                    ) : null}

                    {pushSuccessNotice ? (
                      <div className="flex items-start gap-2 rounded-lg border border-success/35 bg-success-soft px-3 py-2 text-xs text-success-text">
                        <CheckCircle2 className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
                        <span>{pushSuccessNotice}</span>
                      </div>
                    ) : null}
                  </>
                )}
              </div>
            </>
          ) : null}
        </div>

        <SheetFooter className="border-t border-divider pt-3">
          <Button type="button" variant="outline" onClick={onClose}>
            关闭
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  );
}
