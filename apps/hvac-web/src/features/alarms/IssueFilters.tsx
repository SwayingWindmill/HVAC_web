import type { ReactNode } from 'react';
import { ListFilter, RotateCcw } from 'lucide-react';

import type { AlarmSeverity, AlarmSourceType } from '@/api/alarms';
import type { IssueQueueItem } from '@/api/issues';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { cn } from '@/lib/utils';
import type { AlarmAcknowledgementFilter, AlarmOwnershipFilter } from './alarm-center-model';

export type IssueImpactFilter = 'quantified' | 'high-risk';

interface Choice<T extends string> {
  readonly value: T;
  readonly label: string;
}

function ChoiceGroup<T extends string>({
  label,
  value,
  choices,
  onChange,
}: {
  readonly label: string;
  readonly value: T | undefined;
  readonly choices: readonly Choice<T>[];
  readonly onChange: (value: T | undefined) => void;
}) {
  return (
    <div className="space-y-2">
      <p className="text-xs font-medium text-muted-foreground">{label}</p>
      <div className="flex flex-wrap gap-1.5">
        <Button
          type="button"
          size="sm"
          variant={value === undefined ? 'secondary' : 'ghost'}
          className="h-7 px-2.5 text-xs"
          onClick={() => onChange(undefined)}
        >
          全部
        </Button>
        {choices.map((choice) => (
          <Button
            key={choice.value}
            type="button"
            size="sm"
            variant={value === choice.value ? 'secondary' : 'ghost'}
            className="h-7 px-2.5 text-xs"
            onClick={() => onChange(choice.value)}
          >
            {choice.label}
          </Button>
        ))}
      </div>
    </div>
  );
}

function FilterShell({
  activeCount,
  onReset,
  children,
  testId,
}: {
  readonly activeCount: number;
  readonly onReset: () => void;
  readonly children: ReactNode;
  readonly testId: string;
}) {
  return (
    <Popover>
      <PopoverTrigger asChild>
        <Button variant="outline" size="sm" className="h-8 gap-1.5" data-testid={testId}>
          <ListFilter className="size-3.5" aria-hidden="true" />
          筛选
          {activeCount > 0 ? <Badge variant="secondary" className="ml-0.5 h-5 px-1.5 font-normal">{activeCount}</Badge> : null}
        </Button>
      </PopoverTrigger>
      <PopoverContent align="start" className="w-[min(92vw,420px)] space-y-4 p-4">
        {children}
        {activeCount > 0 ? (
          <div className="flex justify-end border-t pt-3">
            <Button type="button" variant="ghost" size="sm" className="h-8 gap-1.5 text-xs" onClick={onReset}>
              <RotateCcw className="size-3.5" aria-hidden="true" />
              清除筛选
            </Button>
          </div>
        ) : null}
      </PopoverContent>
    </Popover>
  );
}

const severityChoices = [
  { value: 'CRITICAL', label: '紧急' },
  { value: 'MAJOR', label: '重要' },
  { value: 'MINOR', label: '一般' },
  { value: 'WARNING', label: '警告' },
  { value: 'INFO', label: '提示' },
] as const satisfies readonly Choice<AlarmSeverity>[];

export function ProblemQueueFilterPopover({
  severity,
  state,
  diagnosis,
  owner,
  impact,
  onChange,
}: {
  readonly severity?: AlarmSeverity;
  readonly state?: IssueQueueItem['state'];
  readonly diagnosis?: IssueQueueItem['diagnosisState'];
  readonly owner?: Exclude<AlarmOwnershipFilter, 'all'>;
  readonly impact?: IssueImpactFilter;
  readonly onChange: (patch: {
    severity?: AlarmSeverity;
    issueState?: IssueQueueItem['state'];
    diagnosis?: IssueQueueItem['diagnosisState'];
    owner?: Exclude<AlarmOwnershipFilter, 'all'>;
    impact?: IssueImpactFilter;
  }) => void;
}) {
  const activeCount = [severity, state, diagnosis, owner, impact].filter(Boolean).length;
  return (
    <FilterShell
      activeCount={activeCount}
      testId="problem-filter"
      onReset={() => onChange({ severity: undefined, issueState: undefined, diagnosis: undefined, owner: undefined, impact: undefined })}
    >
      <ChoiceGroup label="严重程度" value={severity} choices={severityChoices} onChange={(value) => onChange({ severity: value })} />
      <ChoiceGroup
        label="处理进度"
        value={state}
        choices={[
          { value: 'OPEN', label: '待处理' },
          { value: 'INVESTIGATING', label: '排查中' },
          { value: 'ACTION_PENDING', label: '待执行' },
          { value: 'VERIFYING', label: '验证中' },
          { value: 'RESOLVED', label: '已解决' },
        ]}
        onChange={(value) => onChange({ issueState: value })}
      />
      <ChoiceGroup
        label="诊断"
        value={diagnosis}
        choices={[
          { value: 'PENDING', label: '待诊断' },
          { value: 'PUBLISHED', label: '已有诊断' },
          { value: 'EVIDENCE_LIMITED', label: '证据待核实' },
          { value: 'ROOT_CAUSE_CONFIRMED', label: '原因已确认' },
        ]}
        onChange={(value) => onChange({ diagnosis: value })}
      />
      <ChoiceGroup
        label="负责人"
        value={owner}
        choices={[
          { value: 'unassigned', label: '未指派' },
          { value: 'assigned', label: '已指派' },
        ]}
        onChange={(value) => onChange({ owner: value })}
      />
      <ChoiceGroup
        label="影响"
        value={impact}
        choices={[
          { value: 'quantified', label: '已有量化影响' },
          { value: 'high-risk', label: '高可靠性风险' },
        ]}
        onChange={(value) => onChange({ impact: value })}
      />
    </FilterShell>
  );
}

export function AlarmFilterPopover({
  severity,
  acknowledgement,
  owner,
  sourceType,
  onChange,
}: {
  readonly severity?: AlarmSeverity;
  readonly acknowledgement?: Exclude<AlarmAcknowledgementFilter, 'all'>;
  readonly owner?: Exclude<AlarmOwnershipFilter, 'all'>;
  readonly sourceType?: AlarmSourceType;
  readonly onChange: (patch: {
    severity?: AlarmSeverity;
    ack?: Exclude<AlarmAcknowledgementFilter, 'all'>;
    owner?: Exclude<AlarmOwnershipFilter, 'all'>;
    sourceType?: AlarmSourceType;
  }) => void;
}) {
  const activeCount = [severity, acknowledgement, owner, sourceType].filter(Boolean).length;
  return (
    <FilterShell
      activeCount={activeCount}
      testId="alarm-filter"
      onReset={() => onChange({ severity: undefined, ack: undefined, owner: undefined, sourceType: undefined })}
    >
      <ChoiceGroup label="严重程度" value={severity} choices={severityChoices} onChange={(value) => onChange({ severity: value })} />
      <ChoiceGroup
        label="确认状态"
        value={acknowledgement}
        choices={[
          { value: 'unacknowledged', label: '未确认' },
          { value: 'acknowledged', label: '已确认' },
        ]}
        onChange={(value) => onChange({ ack: value })}
      />
      <ChoiceGroup
        label="负责人"
        value={owner}
        choices={[
          { value: 'unassigned', label: '未指派' },
          { value: 'assigned', label: '已指派' },
        ]}
        onChange={(value) => onChange({ owner: value })}
      />
      <ChoiceGroup
        label="来源"
        value={sourceType}
        choices={[
          { value: 'DEVICE_RULE', label: '设备规则' },
          { value: 'SITE_RULE', label: '站点规则' },
          { value: 'EXTERNAL', label: '外部接入' },
        ]}
        onChange={(value) => onChange({ sourceType: value })}
      />
    </FilterShell>
  );
}

export function issueImpactMatches(item: IssueQueueItem, filter: IssueImpactFilter | undefined): boolean {
  if (!filter) return true;
  if (filter === 'quantified') {
    return item.impact.avoidableCost !== null
      || item.impact.avoidableEnergyKwh !== null
      || item.impact.comfortImpactHours !== null;
  }
  return item.impact.reliabilityRisk === 'HIGH' || item.impact.reliabilityRisk === 'CRITICAL';
}

export function activeFilterLabel(label: string, active: boolean) {
  return <span className={cn('text-xs', active ? 'text-foreground' : 'text-muted-foreground')}>{label}</span>;
}
