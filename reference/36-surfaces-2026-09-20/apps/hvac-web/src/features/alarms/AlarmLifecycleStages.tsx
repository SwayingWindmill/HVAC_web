import { Check } from 'lucide-react';
import type { Alarm } from '@/api/alarms';
import { Badge } from '@/components/ui/badge';
import { cn } from '@/lib/utils';
import { alarmLifecycleStages, formatInstant, type AlarmLifecycleStageKey } from './alarm-center-model';

interface AlarmLifecycleStagesProps {
  alarm: Alarm;
  timeZone: string;
}

const stageTitles: Readonly<Record<AlarmLifecycleStageKey, string>> = Object.freeze({
  triggered: '触发',
  acknowledged: '已确认',
  processing: '处理中',
  'awaiting-recovery': '等待恢复',
  recovered: '已恢复',
});

function stageDescription(alarm: Alarm, key: AlarmLifecycleStageKey, timeZone: string): string {
  switch (key) {
    case 'triggered': return formatInstant(alarm.firstOccurredAt, timeZone);
    case 'acknowledged': return alarm.acknowledgement
      ? formatInstant(alarm.acknowledgement.acknowledgedAt, timeZone)
      : alarm.condition === 'CLEARED' ? '本次恢复前没有确认记录' : '等待值班确认';
    case 'processing': return alarm.assigneeId
      ? '已建立处理责任人'
      : alarm.condition === 'CLEARED' ? '本次恢复前没有指派记录' : alarm.acknowledgement ? '已确认，等待进入责任链' : '确认后进入处理';
    case 'awaiting-recovery': return alarm.condition === 'CLEARED'
      ? '恢复条件已经满足'
      : alarm.assigneeId ? '实时规则持续评估恢复条件' : '处理责任建立后持续评估';
    case 'recovered': return alarm.clearedAt
      ? `${formatInstant(alarm.clearedAt, timeZone)} 自动恢复`
      : '尚未满足恢复条件';
  }
}

export function AlarmLifecycleStages({ alarm, timeZone }: AlarmLifecycleStagesProps) {
  const stages = alarmLifecycleStages(alarm);
  return (
    <div className="space-y-4" data-testid="alarm-lifecycle-rail">
      <div className="flex flex-wrap gap-2">
        <Badge variant={alarm.condition === 'CLEARED' ? 'outline' : 'destructive'}>物理状态 {alarm.condition === 'CLEARED' ? '已恢复' : '活动'}</Badge>
        <Badge variant="outline">处理确认 {alarm.acknowledgement ? '已确认' : '未确认'}</Badge>
        {alarm.assigneeId ? <Badge variant="outline">已进入责任链</Badge> : null}
      </div>
      <ol className="grid gap-3 md:grid-cols-3 xl:grid-cols-5">
        {stages.map((stage, index) => (
          <li key={stage.key} className={cn('relative rounded-md border p-3', stage.state === 'current' && 'border-primary/40 bg-muted/30', stage.state === 'unavailable' && 'opacity-60')}>
            <div className="flex items-center gap-2">
              <span className={cn('grid size-5 place-items-center rounded-full border text-[10px]', stage.state === 'done' && 'border-success bg-success text-white', stage.state === 'current' && 'border-primary bg-primary text-primary-foreground')}>
                {stage.state === 'done' ? <Check className="size-3" /> : index + 1}
              </span>
              <strong className="text-xs font-medium">{stageTitles[stage.key]}</strong>
            </div>
            <p className="mt-2 text-[11px] leading-5 text-muted-foreground">{stageDescription(alarm, stage.key, timeZone)}</p>
          </li>
        ))}
      </ol>
      <p className="text-[11px] leading-5 text-muted-foreground">恢复由规则和实时数据自动判定；确认、指派和抑制只描述处理责任与通知行为，不会直接改变告警的活动或恢复状态。</p>
    </div>
  );
}
