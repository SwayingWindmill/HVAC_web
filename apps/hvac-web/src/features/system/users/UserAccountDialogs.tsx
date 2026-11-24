import { useState } from 'react';
import { useMutation } from '@tanstack/react-query';
import { useForm } from 'react-hook-form';
import { toast } from 'sonner';
import type { UserAdminMember } from '@/api/generated/platformGateway.gen';
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from '@/components/ui/field';
import { Input } from '@/components/ui/input';
import { Spinner } from '@/components/ui/spinner';
import { PASSWORD_HINT } from '@/features/account/password';
import { userAdminApi, userAdminErrorMessage } from './user-admin-api';

export function ProfileDialog({
  member,
  onClose,
  csrfToken,
  onDone,
}: {
  readonly member: UserAdminMember | null;
  readonly onClose: () => void;
  readonly csrfToken: string;
  readonly onDone: () => void;
}) {
  const [busy, setBusy] = useState(false);
  return (
    <Dialog open={Boolean(member)} onOpenChange={(open) => { if (!open && !busy) onClose(); }}>
      <DialogContent
        className="sm:max-w-[440px]"
        showCloseButton={!busy}
        onEscapeKeyDown={(event) => { if (busy) event.preventDefault(); }}
        onPointerDownOutside={(event) => { if (busy) event.preventDefault(); }}
        onInteractOutside={(event) => { if (busy) event.preventDefault(); }}
      >
        {member ? (
          <ProfileForm
            key={member.principalId}
            member={member}
            csrfToken={csrfToken}
            onBusyChange={setBusy}
            onSaved={() => {
              setBusy(false);
              onClose();
              onDone();
            }}
            onCancel={onClose}
          />
        ) : null}
      </DialogContent>
    </Dialog>
  );
}

function ProfileForm({
  member,
  csrfToken,
  onBusyChange,
  onSaved,
  onCancel,
}: {
  readonly member: UserAdminMember;
  readonly csrfToken: string;
  readonly onBusyChange: (busy: boolean) => void;
  readonly onSaved: () => void;
  readonly onCancel: () => void;
}) {
  const form = useForm({ defaultValues: { displayName: member.displayName, email: member.email } });
  const save = useMutation({
    mutationFn: (values: { displayName: string; email: string }) => userAdminApi.updateProfile(csrfToken, member.principalId, values),
    onMutate: () => onBusyChange(true),
    onSettled: () => onBusyChange(false),
    onSuccess: () => {
      toast.success('已保存');
      onSaved();
    },
  });
  const errors = form.formState.errors;
  return (
    <form noValidate onSubmit={form.handleSubmit((values) => save.mutate(values))} className="flex flex-col gap-5">
      <DialogHeader>
        <DialogTitle>修改姓名和邮箱</DialogTitle>
        <DialogDescription>用户名 {member.username} 不能修改。</DialogDescription>
      </DialogHeader>
      <FieldGroup className="gap-4">
        <Field data-invalid={Boolean(errors.displayName) || undefined}>
          <FieldLabel htmlFor="profile-name">姓名</FieldLabel>
          <Input
            id="profile-name"
            maxLength={256}
            disabled={save.isPending}
            aria-invalid={Boolean(errors.displayName) || undefined}
            {...form.register('displayName', { required: '请填写姓名' })}
          />
          <FieldError>{errors.displayName?.message}</FieldError>
        </Field>
        <Field data-invalid={Boolean(errors.email) || undefined}>
          <FieldLabel htmlFor="profile-email">邮箱</FieldLabel>
          <Input
            id="profile-email"
            type="email"
            maxLength={320}
            disabled={save.isPending}
            aria-invalid={Boolean(errors.email) || undefined}
            {...form.register('email', {
              required: '请填写邮箱地址',
              pattern: { value: /^\S+@\S+\.\S+$/, message: '邮箱格式不正确' },
            })}
          />
          <FieldError>{errors.email?.message}</FieldError>
        </Field>
      </FieldGroup>
      {save.isError ? <p role="alert" className="text-sm text-destructive-text">{userAdminErrorMessage(save.error)}</p> : null}
      <DialogFooter>
        <Button type="button" variant="outline" disabled={save.isPending} onClick={onCancel}>取消</Button>
        <Button type="submit" disabled={save.isPending}>
          {save.isPending ? <Spinner data-icon="inline-start" /> : null}
          {save.isPending ? '保存中…' : '保存'}
        </Button>
      </DialogFooter>
    </form>
  );
}

export function ResetPasswordDialog({
  member,
  onClose,
  csrfToken,
}: {
  readonly member: UserAdminMember | null;
  readonly onClose: () => void;
  readonly csrfToken: string;
}) {
  const form = useForm({ defaultValues: { password: '' } });
  const reset = useMutation({
    mutationFn: ({ password }: { password: string }) => userAdminApi.resetPassword(csrfToken, member!.principalId, password),
    onSuccess: () => {
      toast.success(`已为 ${member!.displayName} 设置新密码，请告诉对方`);
      resetAndClose();
    },
  });
  function resetAndClose() {
    form.reset({ password: '' });
    reset.reset();
    onClose();
  }
  function close() {
    if (reset.isPending) return;
    resetAndClose();
  }
  const errors = form.formState.errors;
  return (
    <Dialog open={Boolean(member)} onOpenChange={(open) => { if (!open) close(); }}>
      <DialogContent
        className="sm:max-w-[420px]"
        showCloseButton={!reset.isPending}
        onEscapeKeyDown={(event) => { if (reset.isPending) event.preventDefault(); }}
        onPointerDownOutside={(event) => { if (reset.isPending) event.preventDefault(); }}
        onInteractOutside={(event) => { if (reset.isPending) event.preventDefault(); }}
      >
        <form noValidate onSubmit={form.handleSubmit((values) => reset.mutate(values))} className="flex flex-col gap-5">
          <DialogHeader>
            <DialogTitle>重置 {member?.displayName} 的密码</DialogTitle>
            <DialogDescription>新密码立即生效，同时解除密码输错造成的锁定。</DialogDescription>
          </DialogHeader>
          <Field data-invalid={Boolean(errors.password) || undefined}>
            <FieldLabel htmlFor="reset-password">新密码</FieldLabel>
            <Input
              id="reset-password"
              type="password"
              autoComplete="new-password"
              disabled={reset.isPending}
              aria-invalid={Boolean(errors.password) || undefined}
              {...form.register('password', {
                required: '请输入新密码',
                minLength: { value: 8, message: '新密码至少需要 8 个字符' },
                maxLength: 512,
              })}
            />
            <FieldDescription>{PASSWORD_HINT}</FieldDescription>
            <FieldError>{errors.password?.message}</FieldError>
          </Field>
          {reset.isError ? <p role="alert" className="text-sm text-destructive-text">{userAdminErrorMessage(reset.error)}</p> : null}
          <DialogFooter>
            <Button type="button" variant="outline" disabled={reset.isPending} onClick={close}>取消</Button>
            <Button type="submit" disabled={reset.isPending}>
              {reset.isPending ? <Spinner data-icon="inline-start" /> : null}
              {reset.isPending ? '正在设置…' : '设置密码'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

export function DisableDialog({
  member,
  onClose,
  csrfToken,
  onDone,
}: {
  readonly member: UserAdminMember | null;
  readonly onClose: () => void;
  readonly csrfToken: string;
  readonly onDone: () => void;
}) {
  const disable = useMutation({
    mutationFn: () => userAdminApi.setEnabled(csrfToken, member!.principalId, false),
    onSuccess: () => {
      toast.success(`已停用 ${member!.displayName}`);
      onClose();
      onDone();
    },
  });
  return (
    <AlertDialog open={Boolean(member)} onOpenChange={(open) => { if (!open && !disable.isPending) { disable.reset(); onClose(); } }}>
      <AlertDialogContent onEscapeKeyDown={(event) => { if (disable.isPending) event.preventDefault(); }}>
        <AlertDialogHeader>
          <AlertDialogTitle>停用 {member?.displayName}？</AlertDialogTitle>
          <AlertDialogDescription>停用后对方立即退出登录，也不能再登录。角色会保留，重新启用后恢复原来的权限。</AlertDialogDescription>
        </AlertDialogHeader>
        {disable.isError ? <p role="alert" className="text-sm text-destructive-text">{userAdminErrorMessage(disable.error)}</p> : null}
        <AlertDialogFooter>
          <AlertDialogCancel disabled={disable.isPending}>取消</AlertDialogCancel>
          <AlertDialogAction
            variant="destructive"
            disabled={disable.isPending}
            onClick={(event) => {
              event.preventDefault();
              disable.mutate();
            }}
          >
            {disable.isPending ? <Spinner data-icon="inline-start" /> : null}
            {disable.isPending ? '正在停用…' : '停用'}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
