import { useState } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
import type { Site } from '@/api/generated/platformGateway.gen';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import {
  TEMPLATE_EXECUTION_LABELS,
  TEMPLATE_SEVERITY_LABELS,
  alarmTemplateKeys,
  alarmTemplatesApi,
  describeAlarmTemplateError,
  type AlarmRuleBaselineTemplate,
} from '../alarm-templates-api';

const SEVERITIES = ['INFO', 'WARNING', 'MINOR', 'MAJOR', 'CRITICAL'] as const;
const EXECUTION_TARGETS = ['CLOUD', 'EDGE', 'CLOUD_AND_EDGE'] as const;
const OPERATORS = ['GT', 'GTE', 'LT', 'LTE'] as const;
const OPERATOR_LABELS: Readonly<Record<(typeof OPERATORS)[number], string>> = {
  GT: '大于 (>)',
  GTE: '大于等于 (>=)',
  LT: '小于 (<)',
  LTE: '小于等于 (<=)',
};

export function AlarmTemplateEditorDialog({
  open,
  mode,
  template,
  sites,
  csrfToken,
  onClose,
  onSaved,
}: {
  readonly open: boolean;
  readonly mode: 'create' | 'release';
  readonly template: AlarmRuleBaselineTemplate | null;
  readonly sites: readonly Site[];
  readonly csrfToken: string;
  readonly onClose: () => void;
  readonly onSaved: (templateId: string) => void;
}) {
  const queryClient = useQueryClient();
  const existingSpec = template?.latestSpec;

  const [templateKey, setTemplateKey] = useState(template?.templateKey ?? '');
  const [displayName, setDisplayName] = useState(template?.displayName ?? '');
  const [description, setDescription] = useState(template?.description ?? '');
  const [deviceType, setDeviceType] = useState(template?.deviceType ?? 'CHILLER');
  const [thingModelCode, setThingModelCode] = useState(template?.thingModelCode ?? 'chiller.standard');
  const [severity, setSeverity] = useState<'INFO' | 'WARNING' | 'MINOR' | 'MAJOR' | 'CRITICAL'>(
    existingSpec?.severity ?? template?.defaultSeverity ?? 'WARNING',
  );
  const [executionTarget, setExecutionTarget] = useState<'CLOUD' | 'EDGE' | 'CLOUD_AND_EDGE'>(
    existingSpec?.executionTarget ?? template?.defaultExecutionTarget ?? 'CLOUD_AND_EDGE',
  );
  const [pointCode, setPointCode] = useState(existingSpec?.raise.pointCode ?? 'supply_water_temperature');
  const [operator, setOperator] = useState<'GT' | 'GTE' | 'LT' | 'LTE'>(
    OPERATORS.find((o) => o === existingSpec?.raise.operator) ?? 'GT',
  );
  const [raiseThreshold, setRaiseThreshold] = useState(
    String(existingSpec?.raise.delta ?? existingSpec?.raise.value ?? '12.5'),
  );
  const [clearThreshold, setClearThreshold] = useState(
    String(existingSpec?.clear.value ?? '10.5'),
  );
  const [durationSeconds, setDurationSeconds] = useState(String(existingSpec?.durationSeconds ?? '120'));
  const [boundSiteIds, setBoundSiteIds] = useState<string[]>(
    () => (template?.boundSiteIds && template.boundSiteIds.length > 0 ? [...template.boundSiteIds] : sites.map((s) => s.id)),
  );
  const [changeNote, setChangeNote] = useState(
    mode === 'release' ? '升级跨站协同基准版本' : '初始化设备物模型告警基准模板',
  );

  const mutation = useMutation({
    mutationFn: async () => {
      const raiseNum = Number(raiseThreshold);
      const clearNum = Number(clearThreshold);
      const durNum = Math.max(0, Number(durationSeconds) || 0);
      const isSlope = template?.ruleKind === 'SLOPE' || existingSpec?.ruleKind === 'SLOPE';
      const triggerMode: 'SIMPLE' | 'DURATION' | 'REPEATING' = durNum > 0 && !isSlope ? 'DURATION' : 'SIMPLE';
      const ruleSpec = {
        title: displayName.trim() || template?.displayName || '基准告警规则',
        summary: description.trim() || undefined,
        alarmType: template?.alarmType ?? `template/${templateKey.trim()}`,
        severity,
        ruleKind: template?.ruleKind ?? 'THRESHOLD',
        executionTarget,
        triggerMode,
        durationSeconds: durNum > 0 && !isSlope ? durNum : undefined,
        raise: isSlope
          ? {
              kind: 'SLOPE',
              pointCode: pointCode.trim(),
              operator: operator === 'LT' || operator === 'LTE' ? 'LTE' : 'GTE',
              delta: raiseNum,
              windowSeconds: existingSpec?.raise.windowSeconds ?? 300,
            }
          : {
              kind: 'COMPARE',
              pointCode: pointCode.trim(),
              operator,
              value: raiseNum,
            },
        clear: existingSpec?.clear ?? {
          kind: 'COMPARE',
          pointCode: pointCode.trim(),
          operator: operator === 'LT' || operator === 'LTE' ? 'GT' : 'LTE',
          value: clearNum,
        },
      };

      if (mode === 'create') {
        const res = await alarmTemplatesApi.createTemplate(
          {
            templateKey: templateKey.trim(),
            displayName: displayName.trim(),
            description: description.trim() || undefined,
            deviceType: deviceType.trim(),
            thingModelCode: thingModelCode.trim() || undefined,
            alarmType: `template/${templateKey.trim()}`,
            ruleKind: 'THRESHOLD',
            defaultSeverity: severity,
            defaultExecutionTarget: executionTarget,
            boundSiteIds,
            initialSpec: ruleSpec,
            changeNote: changeNote.trim() || undefined,
          },
          csrfToken,
        );
        return res.template.templateId;
      }

      if (!template) throw new Error('missing template');
      await alarmTemplatesApi.publishRelease(
        template.templateId,
        { ruleSpec, changeNote: changeNote.trim() || undefined },
        csrfToken,
      );
      return template.templateId;
    },
    onSuccess: (tid) => {
      toast.success(mode === 'create' ? '已新建基准规则模板并发布 v1' : '已发布新版本基准模板');
      void queryClient.invalidateQueries({ queryKey: alarmTemplateKeys.all });
      onSaved(tid);
      onClose();
    },
    onError: (err) => toast.error(describeAlarmTemplateError(err, '保存模板失败')),
  });

  const toggleSite = (siteId: string) => {
    setBoundSiteIds((prev) => (prev.includes(siteId) ? prev.filter((id) => id !== siteId) : [...prev, siteId]));
  };

  return (
    <Dialog open={open} onOpenChange={(next) => { if (!next) onClose(); }}>
      <DialogContent className="sm:max-w-[580px]">
        <DialogHeader>
          <DialogTitle>{mode === 'create' ? '新建跨站基准告警模板' : `发布模板新版本 · ${template?.displayName ?? ''}`}</DialogTitle>
          <DialogDescription>
            统一定义冷站设备类型基准阈值、严重程度与云边执行位置；站点个性化覆写会在下发时自动保留。
          </DialogDescription>
        </DialogHeader>

        <div className="grid grid-cols-2 gap-3 text-xs">
          {mode === 'create' ? (
            <>
              <div className="flex flex-col gap-1">
                <label htmlFor="tmpl-key" className="font-medium">模板唯一编码</label>
                <Input id="tmpl-key" value={templateKey} onChange={(e) => setTemplateKey(e.target.value)} placeholder="chw-supply-high" />
              </div>
              <div className="flex flex-col gap-1">
                <label htmlFor="tmpl-name" className="font-medium">模板展示名称</label>
                <Input id="tmpl-name" value={displayName} onChange={(e) => setDisplayName(e.target.value)} placeholder="冷冻水供温过高告警" />
              </div>
              <div className="flex flex-col gap-1">
                <label htmlFor="tmpl-dev-type" className="font-medium">适用设备类型</label>
                <Input id="tmpl-dev-type" value={deviceType} onChange={(e) => setDeviceType(e.target.value)} />
              </div>
              <div className="flex flex-col gap-1">
                <label htmlFor="tmpl-model-code" className="font-medium">物模型编码</label>
                <Input id="tmpl-model-code" value={thingModelCode} onChange={(e) => setThingModelCode(e.target.value)} />
              </div>
            </>
          ) : null}

          <div className="flex flex-col gap-1">
            <span className="font-medium">默认严重程度</span>
            <Select
              value={severity}
              onValueChange={(v) => {
                const found = SEVERITIES.find((item) => item === v);
                if (found) setSeverity(found);
              }}
            >
              <SelectTrigger aria-label="默认严重程度"><SelectValue /></SelectTrigger>
              <SelectContent>
                {SEVERITIES.map((s) => (
                  <SelectItem key={s} value={s}>{TEMPLATE_SEVERITY_LABELS[s]}</SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>

          <div className="flex flex-col gap-1">
            <span className="font-medium">默认执行位置</span>
            <Select
              value={executionTarget}
              onValueChange={(v) => {
                const found = EXECUTION_TARGETS.find((item) => item === v);
                if (found) setExecutionTarget(found);
              }}
            >
              <SelectTrigger aria-label="默认执行位置"><SelectValue /></SelectTrigger>
              <SelectContent>
                {EXECUTION_TARGETS.map((t) => (
                  <SelectItem key={t} value={t}>{TEMPLATE_EXECUTION_LABELS[t]}</SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>

          <div className="flex flex-col gap-1">
            <label htmlFor="tmpl-point-code" className="font-medium">监测点位编码</label>
            <Input id="tmpl-point-code" value={pointCode} onChange={(e) => setPointCode(e.target.value)} />
          </div>

          <div className="flex flex-col gap-1">
            <span className="font-medium">判定运算符</span>
            <Select
              value={operator}
              onValueChange={(v) => {
                const found = OPERATORS.find((item) => item === v);
                if (found) setOperator(found);
              }}
            >
              <SelectTrigger aria-label="判定运算符"><SelectValue /></SelectTrigger>
              <SelectContent>
                {OPERATORS.map((op) => (
                  <SelectItem key={op} value={op}>{OPERATOR_LABELS[op]}</SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>

          <div className="flex flex-col gap-1">
            <label htmlFor="tmpl-raise-val" className="font-medium">触发阈值 / 变化量</label>
            <Input id="tmpl-raise-val" type="number" step="0.1" value={raiseThreshold} onChange={(e) => setRaiseThreshold(e.target.value)} />
          </div>

          <div className="flex flex-col gap-1">
            <label htmlFor="tmpl-clear-val" className="font-medium">恢复阈值</label>
            <Input id="tmpl-clear-val" type="number" step="0.1" value={clearThreshold} onChange={(e) => setClearThreshold(e.target.value)} />
          </div>

          <div className="flex flex-col gap-1">
            <label htmlFor="tmpl-dur" className="font-medium">持续时长 (秒)</label>
            <Input id="tmpl-dur" type="number" value={durationSeconds} onChange={(e) => setDurationSeconds(e.target.value)} />
          </div>

          <div className="flex flex-col gap-1">
            <label htmlFor="tmpl-note" className="font-medium">版本变更说明</label>
            <Input id="tmpl-note" value={changeNote} onChange={(e) => setChangeNote(e.target.value)} />
          </div>

          {mode === 'create' ? (
            <>
              <div className="col-span-2 flex flex-col gap-1">
                <label htmlFor="tmpl-desc" className="font-medium">模板说明</label>
                <Input id="tmpl-desc" value={description} onChange={(e) => setDescription(e.target.value)} />
              </div>
              <div className="col-span-2 flex flex-col gap-1.5">
                <span className="font-medium">绑定同步站点范围</span>
                <div className="flex flex-wrap gap-3 rounded-md border bg-muted/30 p-2.5">
                  {sites.map((s) => (
                    <label key={s.id} className="inline-flex items-center gap-1.5 text-xs cursor-pointer">
                      <Checkbox
                        checked={boundSiteIds.includes(s.id)}
                        onCheckedChange={() => toggleSite(s.id)}
                      />
                      <span>{s.displayName}</span>
                    </label>
                  ))}
                </div>
              </div>
            </>
          ) : null}
        </div>

        <DialogFooter>
          <Button type="button" variant="outline" onClick={onClose}>取消</Button>
          <Button type="button" disabled={mutation.isPending} onClick={() => mutation.mutate()}>
            {mutation.isPending ? '正在发布…' : mode === 'create' ? '创建并发布 v1' : '发布新基准版本'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
