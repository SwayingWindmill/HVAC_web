import { useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Activity, LineChart, Plus, Radio, Trash2 } from 'lucide-react';
import { toast } from 'sonner';
import type { Device, GatewayWaveformRule, TelemetryPoint, WaveformTriggerType } from '@/api/generated/platformGateway.gen';
import { Panel } from '@/blocks/panel';
import { ConfirmDiscardDialog } from '@/components/confirm-discard-dialog';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Skeleton } from '@/components/ui/skeleton';
import { Spinner } from '@/components/ui/spinner';
import { Switch } from '@/components/ui/switch';
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group';
import {
  WAVEFORM_SOURCE_LABELS,
  WAVEFORM_STATUS_LABELS,
  WAVEFORM_TRIGGER_LABELS,
  describeRemoteDiagError,
  gatewayRemoteDiagApi,
  gatewayWaveformCapturesKey,
  gatewayWaveformRulesKey,
} from '@/features/connectivity/gateway-remote-diagnostics-api';
import { WaveformCaptureChartDialog } from './WaveformCaptureChartDialog';

function formatClock(iso: string, timeZone: string): string {
  return new Intl.DateTimeFormat('zh-CN', {
    timeZone,
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
  }).format(new Date(iso));
}

export function DiagnosticsViewToggle({
  view,
  onChange,
}: {
  readonly view: 'findings' | 'waveforms';
  readonly onChange: (next: 'findings' | 'waveforms') => void;
}) {
  return (
    <ToggleGroup
      type="single"
      value={view}
      onValueChange={(v) => {
        if (v === 'findings' || v === 'waveforms') onChange(v);
      }}
      aria-label="诊断工作区视图切换"
    >
      <ToggleGroupItem value="findings">诊断结论</ToggleGroupItem>
      <ToggleGroupItem value="waveforms">高频波形抓拍</ToggleGroupItem>
    </ToggleGroup>
  );
}

export function WaveformCapturePanel({
  devices,
  telemetryPoints = [],
  canRead,
  canWrite,
  csrfToken,
  timeZone,
}: {
  readonly devices: readonly Device[];
  readonly telemetryPoints?: readonly TelemetryPoint[];
  readonly canRead: boolean;
  readonly canWrite: boolean;
  readonly csrfToken: string;
  readonly timeZone: string;
}) {
  const gateways = useMemo(
    () => devices.filter((d) => d.deviceType === 'GATEWAY' && d.status === 'ACTIVE'),
    [devices],
  );
  const targetDevices = useMemo(
    () => devices.filter((d) => d.deviceType !== 'GATEWAY' && d.status === 'ACTIVE'),
    [devices],
  );
  const pointsByDeviceCode = useMemo(() => {
    const idToCode = new Map(devices.map((d) => [d.id, d.code]));
    const map = new Map<string, TelemetryPoint[]>();
    for (const pt of telemetryPoints) {
      if (pt.status !== 'ACTIVE') continue;
      const code = idToCode.get(pt.reportingDeviceId);
      if (!code) continue;
      const list = map.get(code) ?? [];
      list.push(pt);
      map.set(code, list);
    }
    return map;
  }, [devices, telemetryPoints]);
  const queryClient = useQueryClient();
  const [selectedGatewayId, setSelectedGatewayId] = useState(() => gateways[0]?.id ?? '');
  const activeGatewayId = gateways.some((g) => g.id === selectedGatewayId)
    ? selectedGatewayId
    : (gateways[0]?.id ?? '');

  const [ruleDialogOpen, setRuleDialogOpen] = useState(false);
  const [triggerDialogOpen, setTriggerDialogOpen] = useState(false);
  const [pendingDeleteRule, setPendingDeleteRule] = useState<GatewayWaveformRule | null>(null);
  const [inspectCaptureId, setInspectCaptureId] = useState<string | null>(null);

  const defaultDeviceCode = targetDevices[0]?.code ?? 'chiller-01';
  const defaultPointCode = (pointsByDeviceCode.get(defaultDeviceCode)?.[0]?.pointCode) ?? 'compressor_current';
  const [ruleName, setRuleName] = useState('');
  const [deviceCode, setDeviceCode] = useState(defaultDeviceCode);
  const [pointCode, setPointCode] = useState(defaultPointCode);
  const [triggerType, setTriggerType] = useState<WaveformTriggerType>('DELTA_JUMP');
  const [thresholdValue, setThresholdValue] = useState('20');
  const [preSeconds, setPreSeconds] = useState('30');
  const [postSeconds, setPostSeconds] = useState('60');
  const [sampleRateHz, setSampleRateHz] = useState('1');
  const [rulePointCodesText, setRulePointCodesText] = useState(
    `${defaultDeviceCode}.${defaultPointCode}, ${defaultDeviceCode}.chw_supply_temp`,
  );
  const [triggerReason, setTriggerReason] = useState('手动抓取压缩机电流与供水瞬态过程');
  const [triggerPre, setTriggerPre] = useState('30');
  const [triggerPost, setTriggerPost] = useState('30');
  const [triggerPointCodesText, setTriggerPointCodesText] = useState(
    `${defaultDeviceCode}.${defaultPointCode}, ${defaultDeviceCode}.chw_supply_temp`,
  );

  const rulesQuery = useQuery({
    queryKey: gatewayWaveformRulesKey(activeGatewayId),
    queryFn: ({ signal }) => gatewayRemoteDiagApi.listWaveformRules(activeGatewayId, signal),
    enabled: Boolean(activeGatewayId) && canRead,
  });

  const capturesQuery = useQuery({
    queryKey: gatewayWaveformCapturesKey(activeGatewayId),
    queryFn: ({ signal }) => gatewayRemoteDiagApi.listWaveformCaptures(activeGatewayId, 20, signal),
    enabled: Boolean(activeGatewayId) && canRead,
  });

  const saveRuleMutation = useMutation({
    mutationFn: () => {
      const pointCodes = rulePointCodesText
        .split(',')
        .map((s) => s.trim())
        .filter(Boolean);
      const body = {
        ruleName: ruleName.trim(),
        enabled: true,
        deviceCode: deviceCode.trim(),
        pointCode: pointCode.trim(),
        triggerType,
        thresholdValue: Number(thresholdValue),
        deltaWindowSeconds: 5,
        preSeconds: Number(preSeconds),
        postSeconds: Number(postSeconds),
        sampleRateHz: Number(sampleRateHz),
        pointCodes,
        cooldownSeconds: 120,
      };
      return gatewayRemoteDiagApi.createWaveformRule(activeGatewayId, body, csrfToken);
    },
    onSuccess: () => {
      toast.success('波形抓拍触发规则已新增');
      setRuleDialogOpen(false);
      void queryClient.invalidateQueries({ queryKey: gatewayWaveformRulesKey(activeGatewayId) });
    },
    onError: (err) => toast.error(describeRemoteDiagError(err, '触发规则保存失败')),
  });

  const deleteRuleMutation = useMutation({
    mutationFn: (ruleId: string) => gatewayRemoteDiagApi.deleteWaveformRule(activeGatewayId, ruleId, csrfToken),
    onSuccess: () => {
      toast.success('触发规则已删除');
      setPendingDeleteRule(null);
      void queryClient.invalidateQueries({ queryKey: gatewayWaveformRulesKey(activeGatewayId) });
    },
    onError: (err) => {
      setPendingDeleteRule(null);
      toast.error(describeRemoteDiagError(err, '删除触发规则失败'));
    },
  });

  const toggleRuleMutation = useMutation({
    mutationFn: (rule: GatewayWaveformRule) =>
      gatewayRemoteDiagApi.updateWaveformRule(
        activeGatewayId,
        rule.id,
        {
          ruleName: rule.ruleName,
          enabled: !rule.enabled,
          deviceCode: rule.deviceCode,
          pointCode: rule.pointCode,
          triggerType: rule.triggerType,
          thresholdValue: rule.thresholdValue,
          deltaWindowSeconds: rule.deltaWindowSeconds,
          preSeconds: rule.preSeconds,
          postSeconds: rule.postSeconds,
          sampleRateHz: rule.sampleRateHz,
          pointCodes: rule.pointCodes,
          cooldownSeconds: rule.cooldownSeconds,
        },
        csrfToken,
      ),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: gatewayWaveformRulesKey(activeGatewayId) }),
    onError: (err) => toast.error(describeRemoteDiagError(err, '更新规则状态失败')),
  });

  const triggerMutation = useMutation({
    mutationFn: () =>
      gatewayRemoteDiagApi.triggerWaveformCapture(
        activeGatewayId,
        {
          preSeconds: Number(triggerPre),
          postSeconds: Number(triggerPost),
          sampleRateHz: 1,
          triggerReason: triggerReason.trim(),
          pointCodes: triggerPointCodesText.split(',').map((s) => s.trim()).filter(Boolean),
        },
        csrfToken,
      ),
    onSuccess: () => {
      toast.success('手动瞬态波形抓拍指令已下发并记入审计');
      setTriggerDialogOpen(false);
      void queryClient.invalidateQueries({ queryKey: gatewayWaveformCapturesKey(activeGatewayId) });
    },
    onError: (err) => toast.error(describeRemoteDiagError(err, '手动抓拍下发失败')),
  });

  const buildDefaultPointsForDevice = (code: string, primaryPoint: string) => {
    const devPts = pointsByDeviceCode.get(code) ?? [];
    if (devPts.length === 0) return `${code}.${primaryPoint}`;
    return devPts.slice(0, 3).map((p) => `${code}.${p.pointCode}`).join(', ');
  };

  const openCreateRule = () => {
    setRuleName('冷机电流突发突变抓拍');
    setDeviceCode(defaultDeviceCode);
    setPointCode(defaultPointCode);
    setTriggerType('DELTA_JUMP');
    setThresholdValue('20');
    setPreSeconds('30');
    setPostSeconds('60');
    setSampleRateHz('1');
    setRulePointCodesText(buildDefaultPointsForDevice(defaultDeviceCode, defaultPointCode));
    setRuleDialogOpen(true);
  };

  const openTriggerDialog = () => {
    setTriggerReason('手动抓取压缩机电流与供水瞬态过程');
    setTriggerPre('30');
    setTriggerPost('30');
    setTriggerPointCodesText(buildDefaultPointsForDevice(defaultDeviceCode, defaultPointCode));
    setTriggerDialogOpen(true);
  };

  const gatewayOptions = useMemo(
    () => gateways.map((g) => ({ id: g.id, label: g.displayName || g.code })),
    [gateways],
  );

  if (!canRead) {
    return (
      <Panel aria-label="高频波形抓拍工作台" className="p-6">
        <Alert>
          <AlertDescription>当前账号缺少网关诊断读取权限，无法查看高频波形抓拍规则与回流曲线。</AlertDescription>
        </Alert>
      </Panel>
    );
  }

  if (!activeGatewayId) {
    return (
      <Panel aria-label="高频波形抓拍工作台" className="p-6">
        <p className="text-sm text-muted-foreground">当前站点尚未登记活跃边缘网关。</p>
      </Panel>
    );
  }

  const rules = rulesQuery.data?.items ?? [];
  const captures = capturesQuery.data?.items ?? [];

  return (
    <div className="flex flex-col gap-4">
      <Panel aria-label="高频波形触发规则" className="p-5">
        <div className="flex flex-wrap items-center justify-between gap-3 border-b border-divider pb-3">
          <div className="flex items-center gap-2.5">
            <Radio className="size-4 text-primary" aria-hidden="true" />
            <div>
              <h2 className="text-sm font-semibold">运行条件触发规则配置</h2>
              <p className="text-xs text-muted-foreground">
                常规数据仍低频降采样上云；越限、跳机或突变时自动抽取故障前后 1Hz 环形缓存波形回流。
              </p>
            </div>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            {gatewayOptions.length > 1 ? (
              <Select value={activeGatewayId} onValueChange={setSelectedGatewayId}>
                <SelectTrigger aria-label="选择目标网关" className="h-8 w-52">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {gatewayOptions.map((g) => (
                    <SelectItem key={g.id} value={g.id}>{g.label}</SelectItem>
                  ))}
                </SelectContent>
              </Select>
            ) : null}
            <Button type="button" variant="outline" size="sm" disabled={!canWrite} onClick={openTriggerDialog}>
              <Activity data-icon="inline-start" aria-hidden="true" />
              手动发起抓拍
            </Button>
            <Button type="button" size="sm" disabled={!canWrite} onClick={openCreateRule}>
              <Plus data-icon="inline-start" aria-hidden="true" />
              新增触发规则
            </Button>
          </div>
        </div>

        <div className="mt-4">
          {rulesQuery.isPending ? (
            <Skeleton className="h-24 rounded-lg" />
          ) : rulesQuery.isError ? (
            <Alert variant="destructive">
              <AlertDescription>{describeRemoteDiagError(rulesQuery.error, '触发规则列表读取失败')}</AlertDescription>
            </Alert>
          ) : rules.length === 0 ? (
            <p className="rounded-lg border border-dashed px-4 py-6 text-center text-xs text-muted-foreground">
              暂无波形触发规则，点击「新增触发规则」配置跳机或阈值突变自动抓拍。
            </p>
          ) : (
            <ul aria-label="波形触发规则列表" className="flex flex-col gap-2">
              {rules.map((rule) => (
                <li key={rule.id} className="flex flex-wrap items-center justify-between gap-3 rounded-lg border bg-muted/30 px-4 py-3 text-xs">
                  <div className="flex flex-col gap-1">
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="text-sm font-semibold">{rule.ruleName}</span>
                      <Badge variant="outline">{WAVEFORM_TRIGGER_LABELS[rule.triggerType]}</Badge>
                      <span className="font-mono text-muted-foreground">
                        {rule.deviceCode}.{rule.pointCode}
                      </span>
                    </div>
                    <div className="text-muted-foreground tabular-nums">
                      阈值 {rule.thresholdValue} · 窗口前 {rule.preSeconds}s / 后 {rule.postSeconds}s · {rule.sampleRateHz} Hz · 监控 {rule.pointCodes.length} 点位
                    </div>
                  </div>
                  <div className="flex items-center gap-2">
                    <Switch
                      aria-label={`启用规则 ${rule.ruleName}`}
                      checked={rule.enabled}
                      disabled={!canWrite || toggleRuleMutation.isPending}
                      onCheckedChange={() => toggleRuleMutation.mutate(rule)}
                    />
                    <Button
                      type="button"
                      variant="outline"
                      size="sm"
                      aria-label={`删除规则 ${rule.ruleName}`}
                      disabled={!canWrite || deleteRuleMutation.isPending}
                      onClick={() => setPendingDeleteRule(rule)}
                    >
                      <Trash2 className="size-3.5 text-destructive-text" aria-hidden="true" />
                    </Button>
                  </div>
                </li>
              ))}
            </ul>
          )}
        </div>
      </Panel>

      <Panel aria-label="瞬态波形回流任务" className="p-5">
        <div className="flex items-center justify-between gap-2 border-b border-divider pb-3">
          <div className="flex items-center gap-2">
            <LineChart className="size-4 text-primary" aria-hidden="true" />
            <h2 className="text-sm font-semibold">秒级瞬态波形回流任务记录</h2>
          </div>
          <Button type="button" variant="outline" size="sm" onClick={() => void capturesQuery.refetch()}>
            刷新回流列表
          </Button>
        </div>

        <div className="mt-4">
          {capturesQuery.isPending ? (
            <Skeleton className="h-28 rounded-lg" />
          ) : capturesQuery.isError ? (
            <Alert variant="destructive">
              <AlertDescription>{describeRemoteDiagError(capturesQuery.error, '波形回流任务读取失败')}</AlertDescription>
            </Alert>
          ) : captures.length === 0 ? (
            <p className="rounded-lg border border-dashed px-4 py-6 text-center text-xs text-muted-foreground">
              暂无秒级瞬态波形回流任务记录
            </p>
          ) : (
            <ul aria-label="波形回流任务列表" className="flex flex-col gap-2">
              {captures.map((cap) => (
                <li key={cap.captureId} className="flex flex-wrap items-center justify-between gap-3 rounded-lg border bg-muted/30 px-4 py-3 text-xs">
                  <div className="flex flex-col gap-1">
                    <div className="flex flex-wrap items-center gap-2">
                      <Badge variant="outline">{WAVEFORM_SOURCE_LABELS[cap.triggerSource]}</Badge>
                      <Badge variant="outline">{WAVEFORM_STATUS_LABELS[cap.status]}</Badge>
                      <span className="text-sm font-semibold">{cap.triggerReason}</span>
                    </div>
                    <div className="text-muted-foreground tabular-nums">
                      触发时刻 {formatClock(cap.triggeredAt, timeZone)} · 样本数 {cap.sampleCount} · {cap.sampleRateHz} Hz
                    </div>
                  </div>
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onClick={() => setInspectCaptureId(cap.captureId)}
                  >
                    <LineChart data-icon="inline-start" aria-hidden="true" />
                    查看波形曲线
                  </Button>
                </li>
              ))}
            </ul>
          )}
        </div>
      </Panel>

      <Dialog
        open={ruleDialogOpen}
        onOpenChange={(open) => {
          if (!open && !saveRuleMutation.isPending) setRuleDialogOpen(false);
        }}
      >
        <DialogContent
          className="sm:max-w-[520px]"
          onEscapeKeyDown={(event) => {
            if (saveRuleMutation.isPending) event.preventDefault();
          }}
          onInteractOutside={(event) => {
            if (saveRuleMutation.isPending) event.preventDefault();
          }}
        >
          <DialogHeader>
            <DialogTitle>新增波形触发规则</DialogTitle>
            <DialogDescription>当端侧运行条件触发阈值时，从本地 15 分钟环形缓存回流秒级波形。</DialogDescription>
          </DialogHeader>
          <div className="grid grid-cols-2 gap-3 text-xs">
            <div className="col-span-2 flex flex-col gap-1">
              <label htmlFor="wf-rule-name" className="font-medium">规则名称</label>
              <Input id="wf-rule-name" value={ruleName} onChange={(e) => setRuleName(e.target.value)} />
            </div>
            <div className="flex flex-col gap-1">
              <span className="font-medium">目标设备</span>
              {targetDevices.length > 0 ? (
                <Select
                  value={deviceCode}
                  onValueChange={(nextCode) => {
                    setDeviceCode(nextCode);
                    const nextPoints = pointsByDeviceCode.get(nextCode) ?? [];
                    const firstPoint = nextPoints[0]?.pointCode ?? pointCode;
                    setPointCode(firstPoint);
                    setRulePointCodesText(buildDefaultPointsForDevice(nextCode, firstPoint));
                  }}
                >
                  <SelectTrigger aria-label="设备编码"><SelectValue /></SelectTrigger>
                  <SelectContent>
                    {targetDevices.map((d) => (
                      <SelectItem key={d.id} value={d.code}>{d.displayName || d.code}</SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              ) : (
                <Input id="wf-device-code" value={deviceCode} onChange={(e) => setDeviceCode(e.target.value)} />
              )}
            </div>
            <div className="flex flex-col gap-1">
              <span className="font-medium">判定点位</span>
              {(pointsByDeviceCode.get(deviceCode) ?? []).length > 0 ? (
                <Select value={pointCode} onValueChange={setPointCode}>
                  <SelectTrigger aria-label="判定点位编码"><SelectValue /></SelectTrigger>
                  <SelectContent>
                    {(pointsByDeviceCode.get(deviceCode) ?? []).map((pt) => (
                      <SelectItem key={pt.id} value={pt.pointCode}>
                        {pt.displayName ? `${pt.displayName} (${pt.pointCode})` : pt.pointCode}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              ) : (
                <Input id="wf-point-code" value={pointCode} onChange={(e) => setPointCode(e.target.value)} />
              )}
            </div>
            <div className="flex flex-col gap-1">
              <span className="font-medium">触发条件类型</span>
              <Select value={triggerType} onValueChange={(v) => { const next = (['THRESHOLD_HIGH', 'THRESHOLD_LOW', 'DELTA_JUMP', 'TRIP_STATE'] as const).find((k) => k === v); if (next) setTriggerType(next); }}>
                <SelectTrigger aria-label="触发条件类型"><SelectValue /></SelectTrigger>
                <SelectContent>
                  {(['THRESHOLD_HIGH', 'THRESHOLD_LOW', 'DELTA_JUMP', 'TRIP_STATE'] as const).map((k) => (
                    <SelectItem key={k} value={k}>{WAVEFORM_TRIGGER_LABELS[k]}</SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="flex flex-col gap-1">
              <label htmlFor="wf-threshold" className="font-medium">触发阈值 / 突变量</label>
              <Input id="wf-threshold" type="number" step="0.1" value={thresholdValue} onChange={(e) => setThresholdValue(e.target.value)} />
            </div>
            <div className="flex flex-col gap-1">
              <label htmlFor="wf-pre-sec" className="font-medium">前向回流秒数</label>
              <Input id="wf-pre-sec" type="number" value={preSeconds} onChange={(e) => setPreSeconds(e.target.value)} />
            </div>
            <div className="flex flex-col gap-1">
              <label htmlFor="wf-post-sec" className="font-medium">后向回流秒数</label>
              <Input id="wf-post-sec" type="number" value={postSeconds} onChange={(e) => setPostSeconds(e.target.value)} />
            </div>
            <div className="col-span-2 flex flex-col gap-1">
              <label htmlFor="wf-points-list" className="font-medium">连带回流点位清单（逗号分隔）</label>
              <Input id="wf-points-list" value={rulePointCodesText} onChange={(e) => setRulePointCodesText(e.target.value)} />
            </div>
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" disabled={saveRuleMutation.isPending} onClick={() => setRuleDialogOpen(false)}>取消</Button>
            <Button type="button" disabled={saveRuleMutation.isPending} onClick={() => saveRuleMutation.mutate()}>
              {saveRuleMutation.isPending ? <Spinner data-icon="inline-start" /> : null}
              {saveRuleMutation.isPending ? '正在保存…' : '保存触发规则'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog
        open={triggerDialogOpen}
        onOpenChange={(open) => {
          if (!open && !triggerMutation.isPending) setTriggerDialogOpen(false);
        }}
      >
        <DialogContent
          className="sm:max-w-[460px]"
          onEscapeKeyDown={(event) => {
            if (triggerMutation.isPending) event.preventDefault();
          }}
          onInteractOutside={(event) => {
            if (triggerMutation.isPending) event.preventDefault();
          }}
        >
          <DialogHeader>
            <DialogTitle>手动下发瞬态波形抓拍</DialogTitle>
            <DialogDescription>下发后网关将在目标窗口结束时把高频样本回流至当前任务列表。</DialogDescription>
          </DialogHeader>
          <div className="grid grid-cols-2 gap-3 text-xs">
            <div className="col-span-2 flex flex-col gap-1">
              <label htmlFor="wf-manual-reason" className="font-medium">抓拍原因</label>
              <Input id="wf-manual-reason" value={triggerReason} onChange={(e) => setTriggerReason(e.target.value)} />
            </div>
            <div className="flex flex-col gap-1">
              <label htmlFor="wf-manual-pre" className="font-medium">前溯窗口 (秒)</label>
              <Input id="wf-manual-pre" type="number" value={triggerPre} onChange={(e) => setTriggerPre(e.target.value)} />
            </div>
            <div className="flex flex-col gap-1">
              <label htmlFor="wf-manual-post" className="font-medium">后延窗口 (秒)</label>
              <Input id="wf-manual-post" type="number" value={triggerPost} onChange={(e) => setTriggerPost(e.target.value)} />
            </div>
            <div className="col-span-2 flex flex-col gap-1">
              <label htmlFor="wf-manual-points" className="font-medium">抓拍点位清单（逗号分隔）</label>
              <Input id="wf-manual-points" value={triggerPointCodesText} onChange={(e) => setTriggerPointCodesText(e.target.value)} />
            </div>
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" disabled={triggerMutation.isPending} onClick={() => setTriggerDialogOpen(false)}>取消</Button>
            <Button type="button" disabled={triggerMutation.isPending} onClick={() => triggerMutation.mutate()}>
              {triggerMutation.isPending ? <Spinner data-icon="inline-start" /> : null}
              {triggerMutation.isPending ? '正在下发…' : '确认下发抓拍'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <ConfirmDiscardDialog
        open={pendingDeleteRule !== null}
        onOpenChange={(open) => {
          if (!open) setPendingDeleteRule(null);
        }}
        busy={deleteRuleMutation.isPending}
        title={pendingDeleteRule ? `确认删除波形触发规则「${pendingDeleteRule.ruleName}」？` : ''}
        description="删除后网关将停止按此规则自动回流秒级波形，已回流的波形记录不受影响。"
        keepLabel="取消"
        discardLabel="删除规则"
        busyLabel="正在删除…"
        onConfirmDiscard={() => {
          if (pendingDeleteRule) deleteRuleMutation.mutate(pendingDeleteRule.id);
        }}
      />

      <WaveformCaptureChartDialog
        gatewayId={activeGatewayId}
        captureId={inspectCaptureId}
        timeZone={timeZone}
        onClose={() => setInspectCaptureId(null)}
      />
    </div>
  );
}
