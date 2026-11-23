import { useMutation, useQueryClient } from '@tanstack/react-query';
import { CheckCircle2, Plus, RotateCcw, Rocket } from 'lucide-react';
import { useState, type KeyboardEvent } from 'react';
import { toast } from 'sonner';
import type {
  EdgeModelArtifactView,
  EdgeModelDeploymentView,
  EdgeModelDomain,
} from '@/api/generated/platformGateway.gen';
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
import { Field, FieldDescription, FieldLabel } from '@/components/ui/field';
import { Input } from '@/components/ui/input';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import {
  approveEdgeModelArtifact,
  createEdgeModelArtifact,
  deployEdgeModelArtifact,
  edgeModelKeys,
  rollbackEdgeModelDeployment,
} from './edge-models-query';

export interface EdgeGatewayOption {
  readonly id: string;
  readonly name: string;
}

function parseOptionalNum(raw: string): number | undefined {
  const trimmed = raw.trim();
  if (!trimmed) return undefined;
  const n = Number(trimmed);
  return Number.isFinite(n) ? n : undefined;
}

function parseNumList(raw: string): number[] | undefined {
  const parts = raw.split(',').map((s) => s.trim()).filter(Boolean);
  if (parts.length === 0) return undefined;
  const nums = parts.map((s) => Number(s));
  return nums.every((n) => Number.isFinite(n)) ? nums : undefined;
}

export function RegisterEdgeModelDialog({
  siteId,
  gateways,
  open,
  onOpenChange,
  csrfToken,
}: {
  readonly siteId: string;
  readonly gateways: readonly EdgeGatewayOption[];
  readonly open: boolean;
  readonly onOpenChange: (open: boolean) => void;
  readonly csrfToken: string;
}) {
  const queryClient = useQueryClient();
  const [domain, setDomain] = useState<EdgeModelDomain>('EDGE_CONTROL');
  const [modelKey, setModelKey] = useState('chiller-cop-opt');
  const [version, setVersion] = useState('1.2.0');
  const [name, setName] = useState('冷机 COP 节能寻优参数集');
  const [maxDeviation, setMaxDeviation] = useState('1.2');
  const [supplyMin, setSupplyMin] = useState('6.5');
  const [supplyMax, setSupplyMax] = useState('11.0');
  const [pumpMin, setPumpMin] = useState('28');
  const [pumpMax, setPumpMax] = useState('50');
  const [stageUp, setStageUp] = useState('82');
  const [stageDown, setStageDown] = useState('45');
  const [copCoeffs, setCopCoeffs] = useState('3.9, 0.06, -0.002');
  const [fddDeltaT, setFddDeltaT] = useState('');
  const [targetPoints, setTargetPoints] = useState('CHW_SUPPLY_TEMP_SP');
  const [gatewayId, setGatewayId] = useState(gateways[0]?.id ?? '');
  const [errorMsg, setErrorMsg] = useState<string | null>(null);

  const mutation = useMutation({
    mutationFn: () => createEdgeModelArtifact(
      siteId,
      {
        domain,
        modelKey: modelKey.trim(),
        version: version.trim(),
        name: name.trim() || undefined,
        maxShadowDeviation: parseOptionalNum(maxDeviation),
        gatewayId: gatewayId || undefined,
        hyperparams: {
          copCoefficients: parseNumList(copCoeffs),
          supplyTempSetpointMinC: parseOptionalNum(supplyMin),
          supplyTempSetpointMaxC: parseOptionalNum(supplyMax),
          pumpFrequencyMinHz: parseOptionalNum(pumpMin),
          pumpFrequencyMaxHz: parseOptionalNum(pumpMax),
          stageUpLoadPct: parseOptionalNum(stageUp),
          stageDownLoadPct: parseOptionalNum(stageDown),
          fddDeltaTMinK: parseOptionalNum(fddDeltaT),
          targetPointCodes: targetPoints.split(',').map((s) => s.trim()).filter(Boolean),
        },
      },
      csrfToken,
    ),
    onSuccess: (created) => {
      setErrorMsg(null);
      toast.success(`已登记候选版本「${created.modelKey} ${created.version}」`);
      void queryClient.invalidateQueries({ queryKey: edgeModelKeys.workspace(siteId) });
      onOpenChange(false);
    },
    onError: (err) => {
      setErrorMsg(err instanceof Error ? err.message : '登记端侧模型版本失败');
    },
  });

  function handleDomainChange(next: string) {
    if (next === 'EDGE_CONTROL' || next === 'EDGE_FDD') {
      setDomain(next);
    }
  }

  function handleKeyDown(event: KeyboardEvent<HTMLInputElement>) {
    if (event.nativeEvent.isComposing || event.keyCode === 229) return;
    if (event.key === 'Enter') {
      event.preventDefault();
      mutation.mutate();
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <Plus className="size-4 text-primary" aria-hidden="true" />
            登记端侧模型与控制超参版本
          </DialogTitle>
          <DialogDescription>
            服务端强制校验物理设备安全边界（供水 4–18°C、水泵 15–50Hz、加减机滞回 ≥ 10%）并禁止接管硬实时联锁点位。
          </DialogDescription>
        </DialogHeader>

        {errorMsg ? (
          <Alert variant="destructive">
            <AlertTitle>校验未通过</AlertTitle>
            <AlertDescription>{errorMsg}</AlertDescription>
          </Alert>
        ) : null}

        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <Field>
            <FieldLabel htmlFor="em-domain">算法域</FieldLabel>
            <Select value={domain} onValueChange={handleDomainChange}>
              <SelectTrigger id="em-domain"><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem value="EDGE_CONTROL">端侧节能寻优</SelectItem>
                <SelectItem value="EDGE_FDD">端侧故障诊断</SelectItem>
              </SelectContent>
            </Select>
          </Field>
          <Field>
            <FieldLabel htmlFor="em-gateway">默认目标网关</FieldLabel>
            <Select value={gatewayId} onValueChange={setGatewayId}>
              <SelectTrigger id="em-gateway"><SelectValue placeholder="站点全量网关" /></SelectTrigger>
              <SelectContent>
                {gateways.map((gw) => (
                  <SelectItem key={gw.id} value={gw.id}>{gw.name}</SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>
          <Field>
            <FieldLabel htmlFor="em-key">模型标识</FieldLabel>
            <Input id="em-key" value={modelKey} onChange={(e) => setModelKey(e.target.value)} onKeyDown={handleKeyDown} />
          </Field>
          <Field>
            <FieldLabel htmlFor="em-version">版本号</FieldLabel>
            <Input id="em-version" value={version} onChange={(e) => setVersion(e.target.value)} onKeyDown={handleKeyDown} />
          </Field>
          <Field>
            <FieldLabel htmlFor="em-name">版本说明</FieldLabel>
            <Input id="em-name" value={name} onChange={(e) => setName(e.target.value)} onKeyDown={handleKeyDown} />
          </Field>
          <Field>
            <FieldLabel htmlFor="em-dev">影子偏差熔断阈值</FieldLabel>
            <Input id="em-dev" type="number" step="0.1" value={maxDeviation} onChange={(e) => setMaxDeviation(e.target.value)} onKeyDown={handleKeyDown} />
          </Field>
          <Field>
            <FieldLabel htmlFor="em-smin">供水温度下限 (°C)</FieldLabel>
            <Input id="em-smin" type="number" step="0.5" value={supplyMin} onChange={(e) => setSupplyMin(e.target.value)} onKeyDown={handleKeyDown} />
          </Field>
          <Field>
            <FieldLabel htmlFor="em-smax">供水温度上限 (°C)</FieldLabel>
            <Input id="em-smax" type="number" step="0.5" value={supplyMax} onChange={(e) => setSupplyMax(e.target.value)} onKeyDown={handleKeyDown} />
          </Field>
          <Field>
            <FieldLabel htmlFor="em-pmin">水泵最低频率 (Hz)</FieldLabel>
            <Input id="em-pmin" type="number" value={pumpMin} onChange={(e) => setPumpMin(e.target.value)} onKeyDown={handleKeyDown} />
          </Field>
          <Field>
            <FieldLabel htmlFor="em-pmax">水泵最高频率 (Hz)</FieldLabel>
            <Input id="em-pmax" type="number" value={pumpMax} onChange={(e) => setPumpMax(e.target.value)} onKeyDown={handleKeyDown} />
          </Field>
          <Field>
            <FieldLabel htmlFor="em-up">加机负荷阈值 (%)</FieldLabel>
            <Input id="em-up" type="number" value={stageUp} onChange={(e) => setStageUp(e.target.value)} onKeyDown={handleKeyDown} />
          </Field>
          <Field>
            <FieldLabel htmlFor="em-down">减机负荷阈值 (%)</FieldLabel>
            <Input id="em-down" type="number" value={stageDown} onChange={(e) => setStageDown(e.target.value)} onKeyDown={handleKeyDown} />
          </Field>
          <Field>
            <FieldLabel htmlFor="em-cop">COP 拟合系数（逗号分隔）</FieldLabel>
            <Input id="em-cop" value={copCoeffs} onChange={(e) => setCopCoeffs(e.target.value)} onKeyDown={handleKeyDown} />
          </Field>
          <Field>
            <FieldLabel htmlFor="em-fdd">诊断最小温差阈值 (K)</FieldLabel>
            <Input id="em-fdd" type="number" step="0.1" value={fddDeltaT} onChange={(e) => setFddDeltaT(e.target.value)} onKeyDown={handleKeyDown} />
          </Field>
          <Field className="sm:col-span-2">
            <FieldLabel htmlFor="em-pts">目标调控点位（逗号分隔）</FieldLabel>
            <Input id="em-pts" value={targetPoints} onChange={(e) => setTargetPoints(e.target.value)} onKeyDown={handleKeyDown} />
            <FieldDescription>禁止包含高压保护、水流联锁、急停等硬实时安全点位。</FieldDescription>
          </Field>
        </div>

        <DialogFooter>
          <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>取消</Button>
          <Button type="button" disabled={mutation.isPending} onClick={() => mutation.mutate()}>
            {mutation.isPending ? '提交中...' : '保存并计算校验摘要'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

export function ApproveEdgeModelDialog({
  siteId,
  artifact,
  open,
  onOpenChange,
  csrfToken,
}: {
  readonly siteId: string;
  readonly artifact: EdgeModelArtifactView | null;
  readonly open: boolean;
  readonly onOpenChange: (open: boolean) => void;
  readonly csrfToken: string;
}) {
  const queryClient = useQueryClient();
  const [decision, setDecision] = useState<'APPROVED' | 'REJECTED'>('APPROVED');
  const [reason, setReason] = useState('离线基准与物理安全护栏检查均已达标');
  const [errorMsg, setErrorMsg] = useState<string | null>(null);

  const mutation = useMutation({
    mutationFn: () => {
      if (!artifact) throw new Error('未选择模型版本');
      return approveEdgeModelArtifact(siteId, artifact.id, { decision, reason: reason.trim() }, csrfToken);
    },
    onSuccess: () => {
      setErrorMsg(null);
      toast.success(decision === 'APPROVED' ? '已审批通过，可执行下发' : '已记录驳回意见');
      void queryClient.invalidateQueries({ queryKey: edgeModelKeys.workspace(siteId) });
      onOpenChange(false);
    },
    onError: (err) => setErrorMsg(err instanceof Error ? err.message : '提交审批失败'),
  });

  function handleDecisionChange(next: string) {
    if (next === 'APPROVED' || next === 'REJECTED') setDecision(next);
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <CheckCircle2 className="size-4 text-primary" aria-hidden="true" />
            模型上线审批
          </DialogTitle>
          <DialogDescription>
            版本 {artifact?.name ?? ''}（{artifact?.version ?? ''}）仅在审批通过后才允许下发至端侧网关。
          </DialogDescription>
        </DialogHeader>
        {errorMsg ? <Alert variant="destructive"><AlertTitle>审批失败</AlertTitle><AlertDescription>{errorMsg}</AlertDescription></Alert> : null}
        <div className="space-y-3">
          <Field>
            <FieldLabel htmlFor="approve-decision">审批结论</FieldLabel>
            <Select value={decision} onValueChange={handleDecisionChange}>
              <SelectTrigger id="approve-decision"><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem value="APPROVED">批准上线</SelectItem>
                <SelectItem value="REJECTED">驳回修改</SelectItem>
              </SelectContent>
            </Select>
          </Field>
          <Field>
            <FieldLabel htmlFor="approve-reason">审批意见</FieldLabel>
            <Input id="approve-reason" value={reason} onChange={(e) => setReason(e.target.value)} />
          </Field>
        </div>
        <DialogFooter>
          <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>取消</Button>
          <Button type="button" disabled={mutation.isPending || !reason.trim()} onClick={() => mutation.mutate()}>
            {mutation.isPending ? '提交中...' : '确认提交审批'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

export function DeployEdgeModelDialog({
  siteId,
  artifact,
  gateways,
  open,
  onOpenChange,
  csrfToken,
}: {
  readonly siteId: string;
  readonly artifact: EdgeModelArtifactView | null;
  readonly gateways: readonly EdgeGatewayOption[];
  readonly open: boolean;
  readonly onOpenChange: (open: boolean) => void;
  readonly csrfToken: string;
}) {
  const queryClient = useQueryClient();
  const [environment, setEnvironment] = useState<'SHADOW' | 'PRODUCTION'>('SHADOW');
  const [gatewayId, setGatewayId] = useState(artifact?.gatewayId ?? gateways[0]?.id ?? '');
  const [reason, setReason] = useState('开启端侧并行影子试运行验证');
  const [errorMsg, setErrorMsg] = useState<string | null>(null);

  const mutation = useMutation({
    mutationFn: () => {
      if (!artifact) throw new Error('未选择模型版本');
      return deployEdgeModelArtifact(
        siteId,
        { artifactId: artifact.id, environment, gatewayId: gatewayId || artifact.gatewayId || gateways[0]?.id, reason: reason.trim() },
        csrfToken,
      );
    },
    onSuccess: (res) => {
      setErrorMsg(null);
      const syncHint = res.shadowSyncStatus === 'SYNCED' ? '，配置影子已同步' : '';
      toast.success(`已下发至${environment === 'PRODUCTION' ? '正式运行' : '影子试运行'}${syncHint}`);
      void queryClient.invalidateQueries({ queryKey: edgeModelKeys.workspace(siteId) });
      onOpenChange(false);
    },
    onError: (err) => setErrorMsg(err instanceof Error ? err.message : '下发部署失败'),
  });

  function handleEnvChange(next: string) {
    if (next === 'SHADOW' || next === 'PRODUCTION') {
      setEnvironment(next);
      setReason(next === 'PRODUCTION' ? '提升为端侧正式运行版本' : '开启端侧并行影子试运行验证');
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <Rocket className="size-4 text-primary" aria-hidden="true" />
            下发与热更新部署
          </DialogTitle>
          <DialogDescription>
            向指定现场网关推送 {artifact?.name ?? ''}（{artifact?.version ?? ''}）配置影子，热替换或并行验证不影响采集链路。
          </DialogDescription>
        </DialogHeader>
        {errorMsg ? <Alert variant="destructive"><AlertTitle>下发受阻</AlertTitle><AlertDescription>{errorMsg}</AlertDescription></Alert> : null}
        <div className="space-y-3">
          <Field>
            <FieldLabel htmlFor="deploy-env">运行模式</FieldLabel>
            <Select value={environment} onValueChange={handleEnvChange}>
              <SelectTrigger id="deploy-env"><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem value="SHADOW">影子试运行（并行对照不上控制总线）</SelectItem>
                <SelectItem value="PRODUCTION">正式运行（原子切换生效）</SelectItem>
              </SelectContent>
            </Select>
          </Field>
          {gateways.length > 0 ? (
            <Field>
              <FieldLabel htmlFor="deploy-gw">目标网关节点</FieldLabel>
              <Select value={gatewayId || gateways[0]!.id} onValueChange={setGatewayId}>
                <SelectTrigger id="deploy-gw"><SelectValue /></SelectTrigger>
                <SelectContent>
                  {gateways.map((gw) => (
                    <SelectItem key={gw.id} value={gw.id}>{gw.name}</SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
          ) : null}
          <Field>
            <FieldLabel htmlFor="deploy-reason">变更说明</FieldLabel>
            <Input id="deploy-reason" value={reason} onChange={(e) => setReason(e.target.value)} />
          </Field>
        </div>
        <DialogFooter>
          <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>取消</Button>
          <Button type="button" disabled={mutation.isPending} onClick={() => mutation.mutate()}>
            {mutation.isPending ? '下发中...' : '确认下发'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

export function RollbackEdgeModelDialog({
  siteId,
  deployment,
  open,
  onOpenChange,
  csrfToken,
}: {
  readonly siteId: string;
  readonly deployment: EdgeModelDeploymentView | null;
  readonly open: boolean;
  readonly onOpenChange: (open: boolean) => void;
  readonly csrfToken: string;
}) {
  const queryClient = useQueryClient();
  const [reason, setReason] = useState('人工评估异常，一键回退上一稳定版本');
  const [errorMsg, setErrorMsg] = useState<string | null>(null);

  const mutation = useMutation({
    mutationFn: () => {
      if (!deployment) throw new Error('未选择在线部署');
      return rollbackEdgeModelDeployment(siteId, deployment.id, { reason: reason.trim(), gatewayId: deployment.gatewayId }, csrfToken);
    },
    onSuccess: (res) => {
      setErrorMsg(null);
      const restoredLabel = res.restoredDeployment ? `，已恢复版本 ${res.restoredDeployment.version}` : '，已切回系统基线';
      toast.success(`版本回退完成${restoredLabel}`);
      void queryClient.invalidateQueries({ queryKey: edgeModelKeys.workspace(siteId) });
      onOpenChange(false);
    },
    onError: (err) => setErrorMsg(err instanceof Error ? err.message : '回退操作失败'),
  });

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <RotateCcw className="size-4 text-destructive" aria-hidden="true" />
            端侧版本一键回退
          </DialogTitle>
          <DialogDescription>
            立即停用当前版本 {deployment?.version ?? ''}，并自动恢复上一个已审批历史版本或站点内置规则基线。
          </DialogDescription>
        </DialogHeader>
        {errorMsg ? <Alert variant="destructive"><AlertTitle>回退失败</AlertTitle><AlertDescription>{errorMsg}</AlertDescription></Alert> : null}
        <Field>
          <FieldLabel htmlFor="rb-reason">回退原因</FieldLabel>
          <Input id="rb-reason" value={reason} onChange={(e) => setReason(e.target.value)} />
        </Field>
        <DialogFooter>
          <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>取消</Button>
          <Button type="button" variant="destructive" disabled={mutation.isPending || !reason.trim()} onClick={() => mutation.mutate()}>
            {mutation.isPending ? '回退中...' : '立即执行回退'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
