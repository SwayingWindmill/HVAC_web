import { useMutation } from '@tanstack/react-query';
import { useForm } from 'react-hook-form';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from '@/components/ui/field';
import { Input } from '@/components/ui/input';
import { Spinner } from '@/components/ui/spinner';
import { notifySuccess } from '@/lib/toast';
import { PASSWORD_HINT, changeOwnPassword, passwordRefusal } from './password';

interface PasswordForm { currentPassword: string; newPassword: string; confirmation: string }

const EMPTY: PasswordForm = { currentPassword: '', newPassword: '', confirmation: '' };

/** The signed-in user replaces their own password; the current one is required. */
export function ChangePasswordDialog({ open, onOpenChange, csrfToken }: {
  readonly open: boolean;
  readonly onOpenChange: (open: boolean) => void;
  readonly csrfToken: string;
}) {
  const form = useForm<PasswordForm>({ defaultValues: EMPTY, mode: 'onBlur' });
  const change = useMutation({
    mutationFn: ({ currentPassword, newPassword }: PasswordForm) => changeOwnPassword(csrfToken, { currentPassword, newPassword }),
    onSuccess: () => {
      notifySuccess('密码已修改，下次登录请用新密码');
      resetAndClose();
    },
  });
  function resetAndClose() {
    form.reset(EMPTY);
    change.reset();
    onOpenChange(false);
  }
  function close() {
    if (change.isPending) return;
    resetAndClose();
  }
  const { currentPassword: currentErr, newPassword: newErr, confirmation: confirmErr } = form.formState.errors;
  return (
    <Dialog open={open} onOpenChange={(next) => { if (!next && !change.isPending) close(); }}>
      <DialogContent
        className="sm:max-w-[420px]"
        onEscapeKeyDown={(event) => { if (change.isPending) event.preventDefault(); }}
        onPointerDownOutside={(event) => { if (change.isPending) event.preventDefault(); }}
      >
        <form onSubmit={form.handleSubmit((values) => change.mutate(values))} className="flex flex-col gap-5" noValidate>
          <DialogHeader>
            <DialogTitle>修改密码</DialogTitle>
            <DialogDescription>修改后当前登录不受影响，下次登录使用新密码。</DialogDescription>
          </DialogHeader>
          <FieldGroup className="gap-4">
            <Field>
              <FieldLabel htmlFor="own-current-password">当前密码</FieldLabel>
              <Input
                id="own-current-password"
                type="password"
                autoComplete="current-password"
                autoFocus
                aria-invalid={Boolean(currentErr) || undefined}
                {...form.register('currentPassword', { required: '请输入当前密码' })}
              />
              {currentErr?.message ? <FieldError>{currentErr.message}</FieldError> : null}
            </Field>
            <Field>
              <FieldLabel htmlFor="own-new-password">新密码</FieldLabel>
              <Input
                id="own-new-password"
                type="password"
                autoComplete="new-password"
                aria-invalid={Boolean(newErr) || undefined}
                {...form.register('newPassword', {
                  required: '请输入新密码',
                  minLength: { value: 8, message: '新密码至少需要 8 位字符' },
                  maxLength: { value: 512, message: '新密码不能超过 512 个字符' },
                })}
              />
              {newErr?.message ? <FieldError>{newErr.message}</FieldError> : <FieldDescription>{PASSWORD_HINT}</FieldDescription>}
            </Field>
            <Field>
              <FieldLabel htmlFor="own-confirm-password">再输一次新密码</FieldLabel>
              <Input
                id="own-confirm-password"
                type="password"
                autoComplete="new-password"
                aria-invalid={Boolean(confirmErr) || undefined}
                {...form.register('confirmation', {
                  required: '请再次输入新密码',
                  validate: (value, values) => value === values.newPassword || '两次输入的新密码不一样',
                })}
              />
              {confirmErr?.message ? <FieldError>{confirmErr.message}</FieldError> : null}
            </Field>
          </FieldGroup>
          {change.isError ? <p role="alert" className="text-sm text-destructive-text">{passwordRefusal(change.error) ?? '没有保存成功，请稍后再试'}</p> : null}
          <DialogFooter>
            <Button type="button" variant="outline" disabled={change.isPending} onClick={close}>取消</Button>
            <Button type="submit" disabled={change.isPending}>
              {change.isPending ? <Spinner /> : null}
              {change.isPending ? '保存中…' : '修改密码'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
