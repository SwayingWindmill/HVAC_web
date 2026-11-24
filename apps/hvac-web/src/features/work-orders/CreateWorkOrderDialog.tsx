import { useState } from 'react';
import { useMutation } from '@tanstack/react-query';
import { getRouteApi } from '@tanstack/react-router';
import { useForm } from 'react-hook-form';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Spinner } from '@/components/ui/spinner';
import { Textarea } from '@/components/ui/textarea';
import {
  createWorkOrder,
  workOrderErrorMessage,
  type WorkOrder,
  type WorkOrderPriority,
  type WorkOrderSourceReference,
} from '@/api/work-orders';
import { notifySuccess } from '@/lib/toast';
import { PRIORITY_LABELS } from './work-order-presentation';
import { isOneOf } from '@/lib/typed';

const PRIORITIES = ['URGENT', 'HIGH', 'MEDIUM', 'LOW'] as const satisfies readonly WorkOrderPriority[];

const siteRoute = getRouteApi('/_app/_site');

interface WorkOrderForm {
  title: string;
  priority: WorkOrderPriority;
  description: string;
}

export interface CreateWorkOrderDialogProps {
  readonly open: boolean;
  readonly onOpenChange: (open: boolean) => void;
  readonly title: string;
  readonly initial: WorkOrderForm;
  /** Where the work comes from; every work order has exactly one origin. */
  readonly sourceReferences: WorkOrderSourceReference[];
  readonly onCreated: (workOrder: WorkOrder) => void;
  /** Explicit site/CSRF context for platform-scoped routes outside `/_app/_site`. */
  readonly siteId?: string;
  readonly csrfToken?: string;
}

export function CreateWorkOrderDialog(props: CreateWorkOrderDialogProps) {
  if (props.siteId && props.csrfToken !== undefined) {
    return (
      <CreateWorkOrderDialogContent
        {...props}
        siteId={props.siteId}
        csrfToken={props.csrfToken}
      />
    );
  }
  return <SiteScopedCreateWorkOrderDialog {...props} />;
}

function SiteScopedCreateWorkOrderDialog(props: CreateWorkOrderDialogProps) {
  const { site, principal } = siteRoute.useRouteContext();
  return (
    <CreateWorkOrderDialogContent
      {...props}
      siteId={site.id}
      csrfToken={principal.session.csrfToken}
    />
  );
}

function CreateWorkOrderDialogContent({
  open,
  onOpenChange,
  title,
  initial,
  sourceReferences,
  onCreated,
  siteId,
  csrfToken,
}: CreateWorkOrderDialogProps & {
  readonly siteId: string;
  readonly csrfToken: string;
}) {
  const [busy, setBusy] = useState(false);

  return (
    <Dialog
      open={open}
      onOpenChange={(nextOpen) => {
        if (!nextOpen && !busy) onOpenChange(false);
      }}
    >
      <DialogContent
        onEscapeKeyDown={(event) => {
          if (busy) event.preventDefault();
        }}
        onInteractOutside={(event) => {
          if (busy) event.preventDefault();
        }}
      >
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription className="sr-only">填写工单标题、优先级与说明并提交创建</DialogDescription>
        </DialogHeader>
        {open ? (
          <CreateWorkOrderForm
            initial={initial}
            sourceReferences={sourceReferences}
            siteId={siteId}
            csrfToken={csrfToken}
            onBusyChange={setBusy}
            onCreated={onCreated}
            onCancel={() => onOpenChange(false)}
          />
        ) : null}
      </DialogContent>
    </Dialog>
  );
}

function CreateWorkOrderForm({
  initial,
  sourceReferences,
  siteId,
  csrfToken,
  onBusyChange,
  onCreated,
  onCancel,
}: {
  readonly initial: WorkOrderForm;
  readonly sourceReferences: WorkOrderSourceReference[];
  readonly siteId: string;
  readonly csrfToken: string;
  readonly onBusyChange: (busy: boolean) => void;
  readonly onCreated: (workOrder: WorkOrder) => void;
  readonly onCancel: () => void;
}) {
  const form = useForm<WorkOrderForm>({ defaultValues: initial });
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
    }, { siteId, csrfToken }),
    onMutate: () => onBusyChange(true),
    onSettled: () => onBusyChange(false),
    onSuccess: (workOrder) => {
      notifySuccess('工单已创建');
      onCreated(workOrder);
      onCancel();
    },
  });

  return (
    <form className="space-y-4" onSubmit={form.handleSubmit((values) => create.mutate(values))}>
      <div className="space-y-1.5">
        <Label htmlFor="work-order-title">标题</Label>
        <Input
          id="work-order-title"
          autoFocus
          onKeyDown={(event) => {
            if (event.key === 'Enter' && (event.nativeEvent.isComposing || event.keyCode === 229)) {
              event.preventDefault();
            }
          }}
          {...form.register('title', { validate: (value) => value.trim() !== '' || '请填写标题' })}
        />
        {form.formState.errors.title ? <p className="text-sm text-destructive-text">{form.formState.errors.title.message}</p> : null}
      </div>
      <div className="space-y-1.5">
        <Label>优先级</Label>
        <Select value={form.watch('priority')} onValueChange={(value) => { if (isOneOf(PRIORITIES, value)) form.setValue('priority', value); }}>
          <SelectTrigger className="w-32"><SelectValue /></SelectTrigger>
          <SelectContent>
            {PRIORITIES.map((priority) => (
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
        {form.formState.errors.description ? <p className="text-sm text-destructive-text">{form.formState.errors.description.message}</p> : null}
      </div>
      {create.isError ? <p className="text-sm text-destructive-text">{workOrderErrorMessage(create.error)}</p> : null}
      <DialogFooter>
        <Button type="button" variant="outline" disabled={create.isPending} onClick={onCancel}>取消</Button>
        <Button type="submit" disabled={create.isPending}>
          {create.isPending ? <Spinner data-icon="inline-start" /> : null}
          {create.isPending ? '创建中…' : '创建工单'}
        </Button>
      </DialogFooter>
    </form>
  );
}
