import { useMutation } from '@tanstack/react-query';
import { getRouteApi } from '@tanstack/react-router';
import { useForm } from 'react-hook-form';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Textarea } from '@/components/ui/textarea';
import {
  createWorkOrder,
  workOrderErrorMessage,
  type WorkOrder,
  type WorkOrderPriority,
  type WorkOrderSourceReference,
} from '@/api/work-orders';
import { PRIORITY_LABELS } from './work-order-presentation';

const siteRoute = getRouteApi('/_app/_site');

interface WorkOrderForm {
  title: string;
  priority: WorkOrderPriority;
  description: string;
}

export function CreateWorkOrderDialog({ open, onOpenChange, title, initial, sourceReferences, onCreated }: {
  readonly open: boolean;
  readonly onOpenChange: (open: boolean) => void;
  readonly title: string;
  readonly initial: WorkOrderForm;
  /** Where the work comes from; every work order has exactly one origin. */
  readonly sourceReferences: WorkOrderSourceReference[];
  readonly onCreated: (workOrder: WorkOrder) => void;
}) {
  const { site, principal } = siteRoute.useRouteContext();
  const form = useForm<WorkOrderForm>({ values: initial });
  const create = useMutation({
    mutationFn: (input: WorkOrderForm) => createWorkOrder({
      title: input.title.trim(),
      description: input.description.trim(),
      priority: input.priority,
      sourceReferences,
      assigneeId: null,
      teamId: null,
      scheduledStart: null,
      dueAt: null,
    }, { siteId: site.id, csrfToken: principal.session.csrfToken }),
    onSuccess: (workOrder) => {
      onCreated(workOrder);
      onOpenChange(false);
    },
  });

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
        </DialogHeader>
        <form className="space-y-4" onSubmit={form.handleSubmit((values) => create.mutate(values))}>
          <div className="space-y-1.5">
            <Label htmlFor="work-order-title">标题</Label>
            <Input id="work-order-title" {...form.register('title', { validate: (value) => value.trim() !== '' || '请填写标题' })} />
            {form.formState.errors.title ? <p className="text-sm text-destructive">{form.formState.errors.title.message}</p> : null}
          </div>
          <div className="space-y-1.5">
            <Label>优先级</Label>
            <Select value={form.watch('priority')} onValueChange={(value) => form.setValue('priority', value as WorkOrderPriority)}>
              <SelectTrigger className="w-32"><SelectValue /></SelectTrigger>
              <SelectContent>
                {(Object.keys(PRIORITY_LABELS) as WorkOrderPriority[]).map((priority) => (
                  <SelectItem key={priority} value={priority}>{PRIORITY_LABELS[priority]}</SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="work-order-description">说明</Label>
            <Textarea
              id="work-order-description"
              rows={4}
              placeholder="需要做什么、在哪里，例如：检查布水器喷嘴是否堵塞并清理"
              {...form.register('description', { validate: (value) => value.trim() !== '' || '请填写工单说明' })}
            />
            {form.formState.errors.description ? <p className="text-sm text-destructive">{form.formState.errors.description.message}</p> : null}
          </div>
          {create.isError ? <p className="text-sm text-destructive">{workOrderErrorMessage(create.error)}</p> : null}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>取消</Button>
            <Button type="submit" disabled={create.isPending}>创建工单</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
