import type { ReactNode } from 'react';
import { ArrowRightOutlined, CheckCircleFilled, ExclamationCircleFilled } from '@ant-design/icons';
import { useNavigate } from 'react-router';
import { z } from 'zod';
import './AgentResultCards.css';

export const assetStatusCardSchema = z.object({
  deviceId: z.string(),
  deviceName: z.string(),
  status: z.enum(['running', 'attention', 'offline']),
  load: z.number(),
  cop: z.number(),
  baselineDelta: z.number(),
  issue: z.string(),
  diagnosisId: z.string().optional(),
});

export const energyAnomalyCardSchema = z.object({
  period: z.string(),
  comparedToBaseline: z.number(),
  extraEnergy: z.number(),
  primaryCause: z.string(),
  contributors: z.array(z.object({
    label: z.string(),
    share: z.number(),
  })),
});

export const fddEvidenceCardSchema = z.object({
  diagnosisId: z.string(),
  deviceName: z.string(),
  title: z.string(),
  severity: z.enum(['critical', 'major', 'minor']),
  confidence: z.number(),
  completeness: z.number(),
  assetId: z.string().optional(),
  evidence: z.array(z.object({
    label: z.string(),
    value: z.string(),
    verified: z.boolean(),
  })),
});

export type AssetStatusCardProps = z.infer<typeof assetStatusCardSchema>;
export type EnergyAnomalyCardProps = z.infer<typeof energyAnomalyCardSchema>;
export type FddEvidenceCardProps = z.infer<typeof fddEvidenceCardSchema>;

const STATUS_LABEL: Record<AssetStatusCardProps['status'], string> = {
  running: '运行中',
  attention: '需要关注',
  offline: '离线',
};

const SEVERITY_LABEL: Record<FddEvidenceCardProps['severity'], string> = {
  critical: '高风险',
  major: '较高风险',
  minor: '一般风险',
};

function ResultCardShell({
  eyebrow,
  title,
  badge,
  tone,
  children,
}: {
  eyebrow: string;
  title: string;
  badge: string;
  tone: 'neutral' | 'warning' | 'danger';
  children: ReactNode;
}) {
  return (
    <article className="hvac-agent-result-card" data-tone={tone}>
      <header className="hvac-agent-result-header">
        <div>
          <span className="hvac-agent-result-eyebrow">{eyebrow}</span>
          <h3>{title}</h3>
        </div>
        <span className="hvac-agent-result-badge">{badge}</span>
      </header>
      {children}
    </article>
  );
}

export function AssetStatusCard(props: AssetStatusCardProps) {
  const navigate = useNavigate();
  const tone = props.status === 'attention' ? 'warning' : props.status === 'offline' ? 'danger' : 'neutral';

  return (
    <ResultCardShell eyebrow="设备运行分析" title={props.deviceName} badge={STATUS_LABEL[props.status]} tone={tone}>
      <div className="hvac-agent-result-metrics">
        <div><span>负荷率</span><strong>{props.load}%</strong></div>
        <div><span>当前 COP</span><strong>{props.cop.toFixed(2)}</strong></div>
        <div><span>较基线</span><strong>{props.baselineDelta > 0 ? '+' : ''}{props.baselineDelta.toFixed(1)}%</strong></div>
      </div>
      <div className="hvac-agent-result-finding">
        <ExclamationCircleFilled aria-hidden="true" />
        <div><span>主要发现</span><strong>{props.issue}</strong></div>
      </div>
      <footer className="hvac-agent-result-actions">
        <button type="button" onClick={() => navigate(`/assets?device=${encodeURIComponent(props.deviceId)}`)}>
          打开设备 <ArrowRightOutlined />
        </button>
        {props.diagnosisId && (
          <button type="button" onClick={() => navigate(`/fdd?diagnosis=${encodeURIComponent(props.diagnosisId!)}`)}>
            查看诊断
          </button>
        )}
      </footer>
    </ResultCardShell>
  );
}

export function EnergyAnomalyCard(props: EnergyAnomalyCardProps) {
  const navigate = useNavigate();
  const sorted = [...props.contributors].sort((a, b) => b.share - a.share);

  return (
    <ResultCardShell eyebrow="能耗异常调查" title={props.period} badge={`基线 ${props.comparedToBaseline > 0 ? '+' : ''}${props.comparedToBaseline.toFixed(1)}%`} tone="warning">
      <div className="hvac-agent-result-summary">
        <span>额外能耗</span>
        <strong>{Math.round(props.extraEnergy).toLocaleString()} kWh</strong>
        <p>{props.primaryCause}</p>
      </div>
      <div className="hvac-agent-contributors" aria-label="能耗增量来源">
        {sorted.map((item) => (
          <div key={item.label} className="hvac-agent-contributor-row">
            <div><span>{item.label}</span><strong>{item.share.toFixed(1)}%</strong></div>
            <div className="hvac-agent-contributor-track"><span style={{ width: `${Math.min(100, Math.max(0, item.share))}%` }} /></div>
          </div>
        ))}
      </div>
      <footer className="hvac-agent-result-actions">
        <button type="button" onClick={() => navigate('/energy/day')}>
          查看日度分析 <ArrowRightOutlined />
        </button>
        <button type="button" onClick={() => navigate('/fdd')}>查看关联诊断</button>
      </footer>
    </ResultCardShell>
  );
}

export function FddEvidenceCard(props: FddEvidenceCardProps) {
  const navigate = useNavigate();
  const tone = props.severity === 'critical' ? 'danger' : props.severity === 'major' ? 'warning' : 'neutral';

  return (
    <ResultCardShell eyebrow={`${props.diagnosisId} · ${props.deviceName}`} title={props.title} badge={SEVERITY_LABEL[props.severity]} tone={tone}>
      <div className="hvac-agent-evidence-stats">
        <div><span>AI 置信度</span><strong>{Math.round(props.confidence * 100)}%</strong></div>
        <div><span>数据完整率</span><strong>{Math.round(props.completeness * 100)}%</strong></div>
      </div>
      <div className="hvac-agent-evidence-list">
        {props.evidence.map((item) => (
          <div key={`${item.label}-${item.value}`} className="hvac-agent-evidence-row">
            <CheckCircleFilled data-verified={item.verified} aria-hidden="true" />
            <span>{item.label}</span>
            <strong>{item.value}</strong>
          </div>
        ))}
      </div>
      <footer className="hvac-agent-result-actions">
        <button type="button" onClick={() => navigate(`/fdd?diagnosis=${encodeURIComponent(props.diagnosisId)}`)}>
          打开诊断 <ArrowRightOutlined />
        </button>
        {props.assetId && (
          <button type="button" onClick={() => navigate(`/assets?device=${encodeURIComponent(props.assetId!)}`)}>
            查看设备
          </button>
        )}
      </footer>
    </ResultCardShell>
  );
}
