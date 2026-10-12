import { useMutation, useQuery } from '@tanstack/react-query';
import { getRouteApi } from '@tanstack/react-router';
import { useForm } from 'react-hook-form';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Spinner } from '@/components/ui/spinner';
import { Textarea } from '@/components/ui/textarea';
import { siteCommandsQuery } from '@/features/control/command-api';
import { commandSummary } from '@/features/control/command-presentation';
import { useCommandLabels } from '@/features/control/use-command-labels';
import { formatTime } from '@/lib/operator-format';
import { registerMeasure, SavingsRequestError } from '../query';
import { instantFromSiteInput, KIND_LABEL, siteInputValue, type Measure, type MeasureKind } from '../savings-model';

const siteRoute = getRouteApi('/_app/_site');

interface MeasureForm {
  name: string;
  kind: MeasureKind;
  plannedAt: string;
  description: string;
  investmentCny: string;
  expectedAnnualKWh: string;
  evidence: string;
  evidenceCommandId: string;
}

const NO_COMMAND = 'none';

const optionalNumber = (value: string) => (value.trim() === '' ? undefined : Number(value));
const nonNegative = (value: string) => value.trim() === '' || (Number.isFinite(Number(value)) && Number(value) >= 0) || '请填写不小于 0 的数字';

export function RegisterMeasureDialog({ open, onOpenChange, onRegistered }: {
  readonly open: boolean;
  readonly onOpenChange: (open: boolean) => void;
  readonly onRegistered: (measure: Measure) => void;
}) {
  const { site, principal } = siteRoute.useRouteContext();
  const form = useForm<MeasureForm>({
    values: {
      name: '', kind: 'SETPOINT_RESET', plannedAt: siteInputValue(new Date(), site.timezone),
      description: '', investmentCny: '', expectedAnnualKWh: '', evidence: '', evidenceCommandId: NO_COMMAND,
    },
  });
  const register = useMutation({
    mutationFn: (input: MeasureForm) => registerMeasure(site.id, principal.session.csrfToken, {
      name: input.name.trim(),
      kind: input.kind,
      plannedAt: instantFromSiteInput(input.plannedAt, site.timezone).toISOString(),
      description: input.description.trim(),
      investmentCny: optionalNumber(input.investmentCny),
      expectedAnnualKWh: optionalNumber(input.expectedAnnualKWh),
      evidence: input.evidence.trim(),
      evidenceCommandId: input.evidenceCommandId === NO_COMMAND ? undefined : input.evidenceCommandId,
    }),
    onSuccess: (measure) => {
      onRegistered(measure);
      onOpenChange(false);
      form.reset();
    },
  });
  const errors = form.formState.errors;
  // A measure can cite a command that took effect on this Site, as the control page lists it.
  const commands = useQuery({ ...siteCommandsQuery(site.id), enabled: open });
  const labels = useCommandLabels(site.id);
  const executed = (commands.data ?? []).filter((command) => command.status === 'SUCCEEDED');

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>登记节能措施</DialogTitle>
          <DialogDescription>登记后为“计划中”。实施时再确认实施时间，系统据此冻结实施前 14 天的基线。</DialogDescription>
        </DialogHeader>
        <form className="space-y-4" onSubmit={form.handleSubmit((values) => register.mutate(values))}>
          <div className="space-y-1.5">
            <Label htmlFor="measure-name">名称</Label>
            <Input
              id="measure-name"
              placeholder="例如：冷冻水供水温度由 7 °C 提高到 8 °C"
              maxLength={100}
              {...form.register('name', { validate: (value) => value.trim() !== '' || '请填写名称' })}
            />
            {errors.name ? <p className="text-sm text-destructive">{errors.name.message}</p> : null}
          </div>
          <div className="grid gap-4 sm:grid-cols-2">
            <div className="space-y-1.5">
              <Label>类型</Label>
              <Select value={form.watch('kind')} onValueChange={(value) => form.setValue('kind', value as MeasureKind)}>
                <SelectTrigger className="w-full"><SelectValue /></SelectTrigger>
                <SelectContent>
                  {(Object.keys(KIND_LABEL) as MeasureKind[]).map((kind) => <SelectItem key={kind} value={kind}>{KIND_LABEL[kind]}</SelectItem>)}
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="measure-planned-at">计划实施时间</Label>
              <Input id="measure-planned-at" type="datetime-local" className="tabular-nums" {...form.register('plannedAt', { required: '请选择计划实施时间' })} />
            </div>
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="measure-description">说明</Label>
            <Textarea id="measure-description" rows={3} maxLength={2000} placeholder="做了什么、作用在哪些设备上" {...form.register('description')} />
          </div>
          <div className="grid gap-4 sm:grid-cols-2">
            <div className="space-y-1.5">
              <Label htmlFor="measure-investment">投资额（元，可选）</Label>
              <Input id="measure-investment" inputMode="decimal" className="tabular-nums" {...form.register('investmentCny', { validate: nonNegative })} />
              {errors.investmentCny ? <p className="text-sm text-destructive">{errors.investmentCny.message}</p> : null}
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="measure-expected">预期年节电量（kWh，可选）</Label>
              <Input id="measure-expected" inputMode="decimal" className="tabular-nums" {...form.register('expectedAnnualKWh', { validate: nonNegative })} />
              {errors.expectedAnnualKWh ? <p className="text-sm text-destructive">{errors.expectedAnnualKWh.message}</p> : null}
            </div>
          </div>
          <div className="space-y-1.5">
            <Label>依据指令（可选）</Label>
            <Select value={form.watch('evidenceCommandId')} onValueChange={(value) => form.setValue('evidenceCommandId', value)}>
              <SelectTrigger className="w-full" aria-label="依据指令"><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem value={NO_COMMAND}>不关联指令</SelectItem>
                {executed.map((command) => {
                  const summary = commandSummary(command, labels);
                  return (
                    <SelectItem key={command.commandId} value={command.commandId}>
                      {formatTime(command.updatedAt, site.timezone)} · {summary.target} {summary.title}
                    </SelectItem>
                  );
                })}
              </SelectContent>
            </Select>
            <p className="text-xs text-muted-foreground">
              {commands.isError ? '指令记录暂不可用' : executed.length === 0 ? '本站最近没有已执行的指令' : '例如按优化建议下发的供水温度设定'}
            </p>
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="measure-evidence">依据说明（可选）</Label>
            <Input id="measure-evidence" maxLength={200} placeholder="例如：10 月 12 日优化建议、改造合同编号" {...form.register('evidence')} />
          </div>
          {register.isError ? (
            <p className="text-sm text-destructive">{register.error instanceof SavingsRequestError ? register.error.message : '节能核算服务暂时不可用，请稍后重试'}</p>
          ) : null}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>取消</Button>
            <Button type="submit" disabled={register.isPending}>
              {register.isPending ? <Spinner data-icon="inline-start" /> : null}
              登记
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
