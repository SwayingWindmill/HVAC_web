import { useMutation } from '@tanstack/react-query';
import { useForm } from 'react-hook-form';
import { toast } from 'sonner';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Field, FieldDescription, FieldGroup, FieldLabel } from '@/components/ui/field';
import { Input } from '@/components/ui/input';
import { PASSWORD_HINT, changeOwnPassword, passwordRefusal } from './password';

interface PasswordForm { currentPassword: string; newPassword: string; confirmation: string }

const EMPTY: PasswordForm = { currentPassword: '', newPassword: '', confirmation: '' };

/** The signed-in user replaces their own password; the current one is required. */
export function ChangePasswordDialog({ open, onOpenChange, csrfToken }: {
  readonly open: boolean;
  readonly onOpenChange: (open: boolean) => void;
  readonly csrfToken: string;
}) {
  const form = useForm<PasswordForm>({ defaultValues: EMPTY });
  const change = useMutation({
    mutationFn: ({ currentPassword, newPassword }: PasswordForm) => changeOwnPassword(csrfToken, { currentPassword, newPassword }),
    onSuccess: () => { toast.success('密码已修改，下次登录请用新密码'); close(); },
  });
  function close() {
    form.reset(EMPTY);
    change.reset();
    onOpenChange(false);
  }
  const mismatch = form.formState.errors.confirmation?.message;
  return (
    <Dialog open={open} onOpenChange={(next) => { if (!next) close(); }}>
      <DialogContent className="sm:max-w-[420px]">
        <form onSubmit={form.handleSubmit((values) => change.mutate(values))} className="flex flex-col gap-5">
          <DialogHeader>
            <DialogTitle>修改密码</DialogTitle>
            <DialogDescription>修改后当前登录不受影响，下次登录使用新密码。</DialogDescription>
          </DialogHeader>
          <FieldGroup className="gap-4">
            <Field>
              <FieldLabel htmlFor="own-current-password">当前密码</FieldLabel>
              <Input id="own-current-password" type="password" autoComplete="current-password" {...form.register('currentPassword', { required: true })} />
            </Field>
            <Field>
              <FieldLabel htmlFor="own-new-password">新密码</FieldLabel>
              <Input id="own-new-password" type="password" autoComplete="new-password" {...form.register('newPassword', { required: true, minLength: 8, maxLength: 512 })} />
              <FieldDescription>{PASSWORD_HINT}</FieldDescription>
            </Field>
            <Field>
              <FieldLabel htmlFor="own-confirm-password">再输一次新密码</FieldLabel>
              <Input id="own-confirm-password" type="password" autoComplete="new-password" aria-invalid={Boolean(mismatch) || undefined}
                {...form.register('confirmation', { validate: (value, values) => value === values.newPassword || '两次输入的新密码不一样' })} />
              {mismatch ? <FieldDescription className="text-destructive-text">{mismatch}</FieldDescription> : null}
            </Field>
          </FieldGroup>
          {change.isError ? <p role="alert" className="text-sm text-destructive-text">{passwordRefusal(change.error) ?? '没有保存成功，请稍后再试'}</p> : null}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={close}>取消</Button>
            <Button type="submit" disabled={change.isPending}>{change.isPending ? '保存中…' : '修改密码'}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
