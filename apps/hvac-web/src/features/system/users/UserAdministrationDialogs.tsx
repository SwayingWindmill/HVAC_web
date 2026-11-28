import { useState } from 'react';
import { useMutation } from '@tanstack/react-query';
import { Controller, useForm } from 'react-hook-form';
import { Plus, X } from 'lucide-react';
import { notifySuccess } from '@/lib/toast';
import type { BuiltInRoleKey, UserAdminMember, UserAdminRole } from '@/api/generated/platformGateway.gen';
import { ConfirmDiscardDialog } from '@/components/confirm-discard-dialog';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from '@/components/ui/field';
import { Input } from '@/components/ui/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Spinner } from '@/components/ui/spinner';
import { PASSWORD_HINT } from '@/features/account/password';
import { roleLabel } from '@/lib/role-labels';
import { userAdminApi, userAdminErrorMessage } from './user-admin-api';
import {
  ASSIGNMENT_VALIDITY_OPTIONS,
  type AssignmentValidityPreset,
  defaultCustomValidToInput,
  formatAssignmentValidity,
  resolveAssignmentValidTo,
} from './user-admin-model';

export interface SiteOption {
  readonly id: string;
  readonly displayName: string;
}

interface RoleScope {
  roleKey: BuiltInRoleKey | '';
  siteId: string;
}

const ALL_SITES = 'all';
const EMPTY_SCOPE: RoleScope = { roleKey: '', siteId: ALL_SITES };
const scopeOf = (siteId: string) => (siteId === ALL_SITES ? {} : { siteId });

function useRoleScopeValidity() {
  const [preset, setPreset] = useState<AssignmentValidityPreset>('permanent');
  const [customValidTo, setCustomValidTo] = useState('');
  const [error, setError] = useState<string | undefined>(undefined);

  const onPresetChange = (nextPreset: AssignmentValidityPreset) => {
    setPreset(nextPreset);
    setError(undefined);
    if (nextPreset === 'custom' && !customValidTo) {
      setCustomValidTo(defaultCustomValidToInput());
    }
  };

  const onCustomValidToChange = (nextValue: string) => {
    setCustomValidTo(nextValue);
    setError(undefined);
  };

  const resolve = (): { readonly ok: true; readonly validTo?: string } | { readonly ok: false } => {
    const result = resolveAssignmentValidTo(preset, customValidTo);
    if (result.error) {
      setError(result.error);
      return { ok: false };
    }
    return { ok: true, validTo: result.validTo };
  };

  const reset = () => {
    setPreset('permanent');
    setCustomValidTo('');
    setError(undefined);
  };

  return { preset, customValidTo, error, onPresetChange, onCustomValidToChange, resolve, reset };
}

type RoleScopeValidity = ReturnType<typeof useRoleScopeValidity>;

/** A Role and where it applies; a Role with Tenant-scoped capabilities always covers every Site. */
function RoleScopeFields({
  roleKey,
  siteId,
  roleError,
  disabled = false,
  validity,
  onRoleChange,
  onSiteChange,
  roles,
  sites,
}: {
  readonly roleKey: string;
  readonly siteId: string;
  readonly roleError?: string;
  readonly disabled?: boolean;
  readonly validity: RoleScopeValidity;
  readonly onRoleChange: (value: string) => void;
  readonly onSiteChange: (value: string) => void;
  readonly roles: readonly UserAdminRole[];
  readonly sites: readonly SiteOption[];
}) {
  const tenantScoped = roles.find((role) => role.roleKey === roleKey)?.tenantScoped ?? false;
  return (
    <div className="flex flex-col gap-3">
      <div className="grid grid-cols-2 gap-3">
        <Field data-invalid={Boolean(roleError) || undefined}>
          <FieldLabel>角色</FieldLabel>
          <Select value={roleKey} onValueChange={onRoleChange} disabled={disabled}>
            <SelectTrigger aria-label="角色" aria-invalid={Boolean(roleError) || undefined}><SelectValue placeholder="选择角色" /></SelectTrigger>
            <SelectContent>{roles.map((role) => <SelectItem key={role.roleKey} value={role.roleKey}>{roleLabel(role.roleKey)}</SelectItem>)}</SelectContent>
          </Select>
          <FieldError>{roleError}</FieldError>
        </Field>
        <Field>
          <FieldLabel>站点</FieldLabel>
          <Select value={tenantScoped ? ALL_SITES : siteId} onValueChange={onSiteChange} disabled={disabled || tenantScoped}>
            <SelectTrigger aria-label="站点"><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem value={ALL_SITES}>全部站点</SelectItem>
              {sites.map((site) => <SelectItem key={site.id} value={site.id}>{site.displayName}</SelectItem>)}
            </SelectContent>
          </Select>
        </Field>
      </div>
      <Field data-invalid={Boolean(validity.error) || undefined}>
        <FieldLabel>授权有效期</FieldLabel>
        <div className={validity.preset === 'custom' ? 'grid grid-cols-2 gap-3' : undefined}>
          <Select
            value={validity.preset}
            disabled={disabled}
            onValueChange={(value) => {
              const option = ASSIGNMENT_VALIDITY_OPTIONS.find((item) => item.value === value);
              if (option) validity.onPresetChange(option.value);
            }}
          >
            <SelectTrigger aria-label="授权有效期"><SelectValue /></SelectTrigger>
            <SelectContent>
              {ASSIGNMENT_VALIDITY_OPTIONS.map((option) => (
                <SelectItem key={option.value} value={option.value}>{option.label}</SelectItem>
              ))}
            </SelectContent>
          </Select>
          {validity.preset === 'custom' ? (
            <Input
              type="datetime-local"
              aria-label="自定义到期时间"
              aria-invalid={Boolean(validity.error) || undefined}
              disabled={disabled}
              value={validity.customValidTo}
              onChange={(event) => validity.onCustomValidToChange(event.target.value)}
            />
          ) : null}
        </div>
        <FieldError>{validity.error}</FieldError>
      </Field>
    </div>
  );
}

const assignmentOf = (scope: RoleScope, roles: readonly UserAdminRole[], validTo?: string) => {
  if (scope.roleKey === '') throw new Error('请选择角色');
  return {
    roleKey: scope.roleKey,
    ...(roles.find((role) => role.roleKey === scope.roleKey)?.tenantScoped ? {} : scopeOf(scope.siteId)),
    ...(validTo ? { validTo } : {}),
  };
};

interface CreateForm extends RoleScope {
  displayName: string;
  username: string;
  email: string;
  password: string;
}

export function CreateUserDialog({
  open,
  onOpenChange,
  csrfToken,
  roles,
  sites,
  onDone,
}: {
  readonly open: boolean;
  readonly onOpenChange: (open: boolean) => void;
  readonly csrfToken: string;
  readonly roles: readonly UserAdminRole[];
  readonly sites: readonly SiteOption[];
  readonly onDone: () => void;
}) {
  const empty: CreateForm = { displayName: '', username: '', email: '', password: '', ...EMPTY_SCOPE };
  const form = useForm<CreateForm>({ defaultValues: empty });
  const validity = useRoleScopeValidity();
  const create = useMutation({
    mutationFn: ({ values, validTo }: { values: CreateForm; validTo?: string }) =>
      userAdminApi.create(csrfToken, {
        displayName: values.displayName,
        username: values.username,
        email: values.email,
        password: values.password,
        assignments: [assignmentOf(values, roles, validTo)],
      }),
    onSuccess: (_, { values }) => {
      notifySuccess(`已创建 ${values.displayName}，请把用户名和密码告诉对方`);
      resetAndClose();
      onDone();
    },
  });
  function resetAndClose() {
    form.reset(empty);
    validity.reset();
    create.reset();
    onOpenChange(false);
  }
  function close() {
    if (create.isPending) return;
    resetAndClose();
  }
  const submitCreate = form.handleSubmit((values) => {
    const resolved = validity.resolve();
    if (!resolved.ok) return;
    create.mutate({ values, validTo: resolved.validTo });
  });
  const errors = form.formState.errors;
  return (
    <Dialog open={open} onOpenChange={(next) => { if (!next) close(); }}>
      <DialogContent
        className="sm:max-w-[480px]"
        showCloseButton={!create.isPending}
        onEscapeKeyDown={(event) => { if (create.isPending) event.preventDefault(); }}
        onPointerDownOutside={(event) => { if (create.isPending) event.preventDefault(); }}
        onInteractOutside={(event) => { if (create.isPending) event.preventDefault(); }}
      >
        <form noValidate onSubmit={submitCreate} className="flex flex-col gap-5">
          <DialogHeader>
            <DialogTitle>新建用户</DialogTitle>
            <DialogDescription>由你设定登录密码，创建后告诉对方；对方登录后可以在右上角菜单里改成自己的密码。</DialogDescription>
          </DialogHeader>
          <FieldGroup className="gap-4">
            <div className="grid grid-cols-2 gap-3">
              <Field data-invalid={Boolean(errors.displayName) || undefined}>
                <FieldLabel htmlFor="new-user-name">姓名</FieldLabel>
                <Input
                  id="new-user-name"
                  maxLength={256}
                  disabled={create.isPending}
                  aria-invalid={Boolean(errors.displayName) || undefined}
                  {...form.register('displayName', { required: '请填写姓名' })}
                />
                <FieldError>{errors.displayName?.message}</FieldError>
              </Field>
              <Field data-invalid={Boolean(errors.username) || undefined}>
                <FieldLabel htmlFor="new-user-username">用户名</FieldLabel>
                <Input
                  id="new-user-username"
                  maxLength={128}
                  autoComplete="off"
                  disabled={create.isPending}
                  aria-invalid={Boolean(errors.username) || undefined}
                  {...form.register('username', { required: '请填写用户名' })}
                />
                <FieldError>{errors.username?.message}</FieldError>
              </Field>
            </div>
            <Field data-invalid={Boolean(errors.email) || undefined}>
              <FieldLabel htmlFor="new-user-email">邮箱</FieldLabel>
              <Input
                id="new-user-email"
                type="email"
                maxLength={320}
                disabled={create.isPending}
                aria-invalid={Boolean(errors.email) || undefined}
                {...form.register('email', {
                  required: '请填写邮箱地址',
                  pattern: { value: /^\S+@\S+\.\S+$/, message: '邮箱格式不正确' },
                })}
              />
              <FieldError>{errors.email?.message}</FieldError>
            </Field>
            <Field data-invalid={Boolean(errors.password) || undefined}>
              <FieldLabel htmlFor="new-user-password">登录密码</FieldLabel>
              <Input
                id="new-user-password"
                type="password"
                autoComplete="new-password"
                disabled={create.isPending}
                aria-invalid={Boolean(errors.password) || undefined}
                {...form.register('password', {
                  required: '请设置登录密码',
                  minLength: { value: 8, message: '登录密码至少需要 8 个字符' },
                  maxLength: 512,
                })}
              />
              <FieldDescription>{PASSWORD_HINT}</FieldDescription>
              <FieldError>{errors.password?.message}</FieldError>
            </Field>
            <Controller
              control={form.control}
              name="roleKey"
              rules={{ required: '请选择角色' }}
              render={({ field: roleField, fieldState }) => (
                <Controller
                  control={form.control}
                  name="siteId"
                  render={({ field: siteField }) => (
                    <RoleScopeFields
                      roleKey={roleField.value}
                      siteId={siteField.value}
                      roleError={fieldState.error?.message}
                      disabled={create.isPending}
                      validity={validity}
                      onRoleChange={roleField.onChange}
                      onSiteChange={siteField.onChange}
                      roles={roles}
                      sites={sites}
                    />
                  )}
                />
              )}
            />
          </FieldGroup>
          {create.isError ? <p role="alert" className="text-sm text-destructive-text">{userAdminErrorMessage(create.error)}</p> : null}
          <DialogFooter>
            <Button type="button" variant="outline" disabled={create.isPending} onClick={close}>取消</Button>
            <Button type="submit" disabled={create.isPending}>
              {create.isPending ? <Spinner data-icon="inline-start" /> : null}
              {create.isPending ? '创建中…' : '创建'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

export function RolesDialog({
  member,
  onClose,
  csrfToken,
  roles,
  sites,
  siteName,
  onDone,
}: {
  readonly member: UserAdminMember | null;
  readonly onClose: () => void;
  readonly csrfToken: string;
  readonly roles: readonly UserAdminRole[];
  readonly sites: readonly SiteOption[];
  readonly siteName: (siteId?: string) => string;
  readonly onDone: () => void;
}) {
  const form = useForm<RoleScope>({ defaultValues: EMPTY_SCOPE });
  const validity = useRoleScopeValidity();
  const [revokeTarget, setRevokeTarget] = useState<UserAdminMember['assignments'][number] | null>(null);
  const assign = useMutation({
    mutationFn: ({ values, validTo }: { values: RoleScope; validTo?: string }) =>
      userAdminApi.assign(csrfToken, member!.principalId, assignmentOf(values, roles, validTo)),
    onSuccess: () => {
      form.reset(EMPTY_SCOPE);
      validity.reset();
      onDone();
    },
  });
  const revoke = useMutation({
    mutationFn: (bindingId: string) => userAdminApi.revoke(csrfToken, member!.principalId, bindingId),
    onSuccess: () => {
      setRevokeTarget(null);
      onDone();
    },
    onError: () => {
      setRevokeTarget(null);
    },
  });
  const busy = assign.isPending || revoke.isPending;
  const error = assign.error ?? revoke.error;
  const submitAssign = form.handleSubmit((values) => {
    const resolved = validity.resolve();
    if (!resolved.ok) return;
    assign.mutate({ values, validTo: resolved.validTo });
  });
  const isLastAssignment = (member?.assignments.length ?? 0) <= 1;
  return (
    <>
      <Dialog
        open={Boolean(member)}
        onOpenChange={(open) => {
          if (!open && !busy) {
            form.reset(EMPTY_SCOPE);
            validity.reset();
            assign.reset();
            revoke.reset();
            setRevokeTarget(null);
            onClose();
          }
        }}
      >
        <DialogContent
          className="sm:max-w-[520px]"
          showCloseButton={!busy}
          onEscapeKeyDown={(event) => { if (busy) event.preventDefault(); }}
          onPointerDownOutside={(event) => { if (busy) event.preventDefault(); }}
          onInteractOutside={(event) => { if (busy) event.preventDefault(); }}
        >
          <DialogHeader>
            <DialogTitle>{member?.displayName} 的角色与站点</DialogTitle>
            <DialogDescription>改动立即生效：对方下一次操作时就按新的权限判断。</DialogDescription>
          </DialogHeader>
          <ul className="flex flex-col divide-y divide-divider rounded-lg border">
            {member?.assignments.length ? member.assignments.map((assignment) => {
              const formatted = formatAssignmentValidity(assignment.validTo);
              return (
                <li key={assignment.bindingId} className="flex items-center gap-3 px-4 py-2.5">
                  <span className="text-sm font-medium">{roleLabel(assignment.roleKey)}</span>
                  <span className="text-sm text-muted-foreground">{siteName(assignment.siteId)}</span>
                  <span className={formatted.temporary ? 'rounded bg-muted px-1.5 py-0.5 text-xs font-medium text-foreground' : 'text-xs text-muted-foreground'}>
                    {formatted.label}
                  </span>
                  <Button
                    variant="ghost"
                    size="sm"
                    className="ml-auto text-muted-foreground"
                    disabled={busy}
                    onClick={() => {
                      assign.reset();
                      revoke.reset();
                      setRevokeTarget(assignment);
                    }}
                  >
                    <X data-icon="inline-start" />撤销
                  </Button>
                </li>
              );
            }) : <li className="px-4 py-3 text-sm text-muted-foreground">还没有角色，登录后看不到任何数据</li>}
          </ul>
          <form noValidate onSubmit={submitAssign} className="flex flex-col gap-3 rounded-lg bg-muted/40 p-4">
            <Controller
              control={form.control}
              name="roleKey"
              rules={{ required: '请选择角色' }}
              render={({ field: roleField, fieldState }) => (
                <Controller
                  control={form.control}
                  name="siteId"
                  render={({ field: siteField }) => (
                    <RoleScopeFields
                      roleKey={roleField.value}
                      siteId={siteField.value}
                      roleError={fieldState.error?.message}
                      disabled={busy}
                      validity={validity}
                      onRoleChange={roleField.onChange}
                      onSiteChange={siteField.onChange}
                      roles={roles}
                      sites={sites}
                    />
                  )}
                />
              )}
            />
            <Button type="submit" className="self-end" size="sm" disabled={busy}>
              {assign.isPending ? <Spinner data-icon="inline-start" /> : <Plus data-icon="inline-start" />}
              {assign.isPending ? '正在添加…' : '添加角色'}
            </Button>
          </form>
          {error ? <p role="alert" className="text-sm text-destructive-text">{userAdminErrorMessage(error)}</p> : null}
        </DialogContent>
      </Dialog>

      <ConfirmDiscardDialog
        open={Boolean(revokeTarget)}
        onOpenChange={(open) => { if (!open && !revoke.isPending) setRevokeTarget(null); }}
        busy={revoke.isPending}
        title={revokeTarget ? `撤销「${roleLabel(revokeTarget.roleKey)} · ${siteName(revokeTarget.siteId)}」？` : '撤销角色？'}
        description={
          isLastAssignment
            ? `${member?.displayName ?? '该用户'}目前仅剩这一个角色，撤销后将失去所有站点权限，登录后看不到任何数据。`
            : `撤销后立即生效，${member?.displayName ?? '该用户'}在下一次操作时将不再拥有这项权限。`
        }
        keepLabel="暂不撤销"
        discardLabel="确认撤销"
        busyLabel="正在撤销…"
        onConfirmDiscard={() => {
          if (revokeTarget) revoke.mutate(revokeTarget.bindingId);
        }}
      />
    </>
  );
}
