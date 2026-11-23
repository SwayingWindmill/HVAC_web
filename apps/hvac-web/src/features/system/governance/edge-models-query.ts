import { queryOptions } from '@tanstack/react-query';
import {
  createPlatformGatewayClient,
  type ApproveEdgeModelRequest,
  type CreateEdgeModelArtifactRequest,
  type DeployEdgeModelRequest,
  type EdgeModelArtifactView,
  type EdgeModelDomain,
  type EdgeModelHyperparams,
  type EdgeModelWorkspace,
  type RecordEdgeModelEvaluationRequest,
  type RollbackEdgeModelRequest,
} from '@/api/generated/platformGateway.gen';

const client = createPlatformGatewayClient();

export const edgeModelKeys = {
  all: ['edge-models'] as const,
  workspace: (siteId: string | null) => ['edge-models', 'workspace', siteId] as const,
};

export function edgeModelWorkspaceQuery(siteId: string | null, enabled = true) {
  return queryOptions<EdgeModelWorkspace>({
    queryKey: edgeModelKeys.workspace(siteId),
    queryFn: async () => {
      if (!siteId) throw new Error('请选择站点');
      const res = await client.getSiteEdgeModelWorkspace(siteId);
      return res.data;
    },
    enabled: Boolean(siteId) && enabled,
    staleTime: 10_000,
  });
}

export async function createEdgeModelArtifact(
  siteId: string,
  body: CreateEdgeModelArtifactRequest,
  csrfToken: string,
) {
  const res = await client.createSiteEdgeModelArtifact(siteId, body, csrfToken);
  return res.data;
}

export async function approveEdgeModelArtifact(
  siteId: string,
  artifactId: string,
  body: ApproveEdgeModelRequest,
  csrfToken: string,
) {
  const res = await client.approveSiteEdgeModelArtifact(siteId, artifactId, body, csrfToken);
  return res.data;
}

export async function deployEdgeModelArtifact(
  siteId: string,
  body: DeployEdgeModelRequest,
  csrfToken: string,
) {
  const res = await client.deploySiteEdgeModelArtifact(siteId, body, csrfToken);
  return res.data;
}

export async function rollbackEdgeModelDeployment(
  siteId: string,
  deploymentId: string,
  body: RollbackEdgeModelRequest,
  csrfToken: string,
) {
  const res = await client.rollbackSiteEdgeModelDeployment(siteId, deploymentId, body, csrfToken);
  return res.data;
}

export async function recordEdgeModelEvaluation(
  siteId: string,
  body: RecordEdgeModelEvaluationRequest,
  csrfToken: string,
) {
  const res = await client.recordSiteEdgeModelEvaluation(siteId, body, csrfToken);
  return res.data;
}

export function formatDomainLabel(domain: EdgeModelDomain): string {
  return domain === 'EDGE_FDD' ? '端侧故障诊断' : '端侧节能寻优';
}

export function formatApprovalStatusLabel(status: string): string {
  switch (status) {
    case 'APPROVED':
      return '已审批';
    case 'REJECTED':
      return '已驳回';
    default:
      return '待审批';
  }
}

export function formatDeployEnvironmentLabel(env: string): string {
  return env === 'PRODUCTION' ? '正式运行' : '影子试运行';
}

export function formatDeployStatusLabel(status: string): string {
  switch (status) {
    case 'ACTIVE':
      return '生效中';
    case 'RETIRED':
      return '已替换';
    case 'ROLLED_BACK':
      return '已回退';
    default:
      return '待下发';
  }
}

export function formatDriftStatusLabel(status: string): string {
  switch (status) {
    case 'BREACHED':
      return '偏差超限';
    case 'WARNING':
      return '接近阈值';
    default:
      return '偏差正常';
  }
}

export function formatEvalOutcomeLabel(outcome: string): string {
  switch (outcome) {
    case 'PASSED':
      return '达标通过';
    case 'FAILED':
      return '超出阈值';
    case 'INCONCLUSIVE':
      return '待复核';
    default:
      return '未评估';
  }
}

export interface HyperparamDiffRow {
  readonly key: string;
  readonly label: string;
  readonly baseValue: string;
  readonly targetValue: string;
  readonly changed: boolean;
}

function fmtNum(val: number | null | undefined, unit = ''): string {
  if (val === null || val === undefined || Number.isNaN(val)) return '未设定';
  return `${val}${unit}`;
}

function fmtList(values: readonly number[] | readonly string[] | undefined): string {
  if (!values || values.length === 0) return '未设定';
  return values.join(', ');
}

export function buildHyperparamDiff(
  base: EdgeModelArtifactView | null,
  target: EdgeModelArtifactView | null,
): readonly HyperparamDiffRow[] {
  const b: EdgeModelHyperparams = base?.hyperparams ?? {};
  const t: EdgeModelHyperparams = target?.hyperparams ?? {};
  const defs: readonly { readonly key: string; readonly label: string; readonly b: string; readonly t: string }[] = [
    { key: 'supplyMin', label: '供水温度下限 (°C)', b: fmtNum(b.supplyTempSetpointMinC, ' °C'), t: fmtNum(t.supplyTempSetpointMinC, ' °C') },
    { key: 'supplyMax', label: '供水温度上限 (°C)', b: fmtNum(b.supplyTempSetpointMaxC, ' °C'), t: fmtNum(t.supplyTempSetpointMaxC, ' °C') },
    { key: 'pumpMin', label: '冷冻泵最低频率 (Hz)', b: fmtNum(b.pumpFrequencyMinHz, ' Hz'), t: fmtNum(t.pumpFrequencyMinHz, ' Hz') },
    { key: 'pumpMax', label: '冷冻泵最高频率 (Hz)', b: fmtNum(b.pumpFrequencyMaxHz, ' Hz'), t: fmtNum(t.pumpFrequencyMaxHz, ' Hz') },
    { key: 'fanMin', label: '冷却塔最低转速 (%)', b: fmtNum(b.towerFanSpeedMinPct, ' %'), t: fmtNum(t.towerFanSpeedMinPct, ' %') },
    { key: 'fanMax', label: '冷却塔最高转速 (%)', b: fmtNum(b.towerFanSpeedMaxPct, ' %'), t: fmtNum(t.towerFanSpeedMaxPct, ' %') },
    { key: 'stageUp', label: '冷机加机负荷阈值 (%)', b: fmtNum(b.stageUpLoadPct, ' %'), t: fmtNum(t.stageUpLoadPct, ' %') },
    { key: 'stageDown', label: '冷机减机负荷阈值 (%)', b: fmtNum(b.stageDownLoadPct, ' %'), t: fmtNum(t.stageDownLoadPct, ' %') },
    { key: 'pidKp', label: 'PID 比例系数 (Kp)', b: fmtNum(b.pidKp), t: fmtNum(t.pidKp) },
    { key: 'fddDeltaT', label: '诊断最小温差阈值 (K)', b: fmtNum(b.fddDeltaTMinK, ' K'), t: fmtNum(t.fddDeltaTMinK, ' K') },
    { key: 'copCoeffs', label: 'COP 性能曲线系数', b: fmtList(b.copCoefficients), t: fmtList(t.copCoefficients) },
    { key: 'points', label: '目标调控点位', b: fmtList(b.targetPointCodes), t: fmtList(t.targetPointCodes) },
  ];
  return defs.map((item) => ({
    key: item.key,
    label: item.label,
    baseValue: item.b,
    targetValue: item.t,
    changed: item.b !== item.t,
  }));
}
