import { Lightbulb, ShieldCheck } from 'lucide-react';
import type { Alarm } from '@/api/alarms';
import { Badge } from '@/components/ui/badge';
import { alarmWorkflow, workflowLabel } from './alarm-center-model';

interface AlarmDiagnosisPanelProps {
  alarm: Alarm;
}

function recommendedNextStep(alarm: Alarm): string {
  if (alarm.condition === 'CLEARED') {
    return '告警指标已恢复正常范围。请复核现场设备运行工况与关联工单，确认设备稳定运行。';
  }
  if (!alarm.acknowledgement) {
    return '请当班人员确认告警，记录已知晓状态并在现场排查设备异常。';
  }
  if (!alarm.assigneeId) {
    return '告警已确认，建议尽快指派当班维修人员排查或派发检修工单。';
  }
  return '已指派专人跟进，请持续监控核心工况参数，等待设备状态恢复稳定。';
}

export function AlarmDiagnosisPanel({ alarm }: AlarmDiagnosisPanelProps) {
  const workflow = alarmWorkflow(alarm);
  return (
    <div className="space-y-4" data-testid="alarm-diagnosis-panel">
      <div className="rounded-md border bg-muted/20 p-4">
        <div className="flex items-start gap-3">
          <Lightbulb className="mt-0.5 size-4 shrink-0 text-information" />
          <div>
            <strong className="text-sm font-medium">智能排查建议</strong>
            <p className="mt-1 text-xs leading-5 text-muted-foreground">根据当前遥测异常与规则判定，建议优先检查设备电气回路、阀门开度与设定值参数。</p>
          </div>
        </div>
      </div>
      <div className="grid gap-3 sm:grid-cols-2">
        <div className="rounded-md border p-3"><span className="text-[11px] text-muted-foreground">当前处置状态</span><strong className="mt-1 block text-xs font-medium">{workflowLabel(workflow)}</strong></div>
        <div className="rounded-md border p-3"><span className="text-[11px] text-muted-foreground">告警状态</span><div className="mt-1"><Badge variant={alarm.condition === 'CLEARED' ? 'outline' : 'destructive'}>{alarm.condition === 'CLEARED' ? '已恢复' : '活动'}</Badge></div></div>
        <div className="rounded-md border p-3 sm:col-span-2"><span className="text-[11px] text-muted-foreground">诊断依据</span><strong className="mt-1 block text-xs font-medium">告警状态、关键参数、触发与恢复条件以及处理记录</strong></div>
      </div>
      <div className="flex items-start gap-3 rounded-md border bg-card p-4">
        <ShieldCheck className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
        <div><strong className="text-xs font-medium">当前建议</strong><p className="mt-1 text-xs leading-5 text-muted-foreground">{recommendedNextStep(alarm)}</p></div>
      </div>
    </div>
  );
}
