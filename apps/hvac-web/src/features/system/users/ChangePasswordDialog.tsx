import { useState } from 'react';
import { useMutation } from '@tanstack/react-query';
import { toast } from 'sonner';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Field, FieldDescription, FieldGroup, FieldLabel } from '@/components/ui/field';
import { Input } from '@/components/ui/input';
import { userAdminApi, userAdminErrorMessage } from './user-admin-api';

/** The signed-in user replaces their own password; the current one is required. */
export function ChangePasswordDialog({ open, onOpenChange, csrfToken }: {
  readonly open: boolean;
  readonly onOpenChange: (open: boolean) => void;
  readonly csrfToken: string;
}) {
  const [currentPassword, setCurrentPassword] = useState('');
  const [newPassword, setNewPassword] = useState('');
  const [confirmation, setConfirmation] = useState('');
  const mismatch = confirmation !== '' && confirmation !== newPassword;
  const change = useMutation({
    mutationFn: () => userAdminApi.changeOwnPassword(csrfToken, { currentPassword, newPassword }),
    onSuccess: () => { toast.success('密码已修改，下次登录请用新密码'); close(); },
  });
  function close() {
    setCurrentPassword('');
    setNewPassword('');
    setConfirmation('');
    change.reset();
    onOpenChange(false);
  }
  return (
    <Dialog open={open} onOpenChange={(next) => { if (!next) close(); }}>
      <DialogContent className="sm:max-w-[420px]">
        <form onSubmit={(event) => { event.preventDefault(); if (!mismatch) change.mutate(); }} className="flex flex-col gap-5">
          <DialogHeader>
            <DialogTitle>修改密码</DialogTitle>
            <DialogDescription>修改后当前登录不受影响，下次登录使用新密码。</DialogDescription>
          </DialogHeader>
          <FieldGroup className="gap-4">
            <Field>
              <FieldLabel htmlFor="own-current-password">当前密码</FieldLabel>
              <Input id="own-current-password" type="password" value={currentPassword} onChange={(event) => setCurrentPassword(event.target.value)} required autoComplete="current-password" />
            </Field>
            <Field>
              <FieldLabel htmlFor="own-new-password">新密码</FieldLabel>
              <Input id="own-new-password" type="password" value={newPassword} onChange={(event) => setNewPassword(event.target.value)} required minLength={8} maxLength={512} autoComplete="new-password" />
              <FieldDescription>至少 8 位，不能是常见密码，也不能和用户名相同</FieldDescription>
            </Field>
            <Field>
              <FieldLabel htmlFor="own-confirm-password">再输一次新密码</FieldLabel>
              <Input id="own-confirm-password" type="password" value={confirmation} onChange={(event) => setConfirmation(event.target.value)} required autoComplete="new-password" aria-invalid={mismatch || undefined} />
              {mismatch ? <FieldDescription className="text-destructive-text">两次输入的新密码不一样</FieldDescription> : null}
            </Field>
          </FieldGroup>
          {change.isError ? <p role="alert" className="text-sm text-destructive-text">{userAdminErrorMessage(change.error)}</p> : null}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={close}>取消</Button>
            <Button type="submit" disabled={change.isPending || mismatch}>{change.isPending ? '保存中…' : '修改密码'}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
