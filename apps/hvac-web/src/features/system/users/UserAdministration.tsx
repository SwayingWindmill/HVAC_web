import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Controller, useForm, useWatch, type Control } from 'react-hook-form';
import { MoreHorizontal, Plus, UserPlus, X } from 'lucide-react';
import { toast } from 'sonner';
import type { BuiltInRoleKey, UserAdminMember, UserAdminRole } from '@/api/generated/platformGateway.gen';
import { Panel, PanelHeader } from '@/blocks/panel';
import {
  AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle,
} from '@/components/ui/alert-dialog';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { Field, FieldDescription, FieldGroup, FieldLabel } from '@/components/ui/field';
import { Input } from '@/components/ui/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Skeleton } from '@/components/ui/skeleton';
import { Status, StatusIndicator } from '@/components/ui/status';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { PASSWORD_HINT } from '@/features/account/password';
import { ROLE_SUMMARIES, roleLabel } from '@/lib/role-labels';
import { userAdminApi, userAdminErrorMessage, userAdminKey, userAdminQuery } from './user-admin-api';

interface SiteOption { readonly id: string; readonly displayName: string }
interface RoleScope { roleKey: BuiltInRoleKey | ''; siteId: string }

const ALL_SITES = 'all';
const EMPTY_SCOPE: RoleScope = { roleKey: '', siteId: ALL_SITES };

const clock = (value: string) => new Intl.DateTimeFormat('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false }).format(new Date(value));
const scopeOf = (siteId: string) => (siteId === ALL_SITES ? {} : { siteId });

function MemberState({ member }: { readonly member: UserAdminMember }) {
  const [variant, label] = !member.enabled ? ['default', '已停用'] as const : member.locked ? ['warning', '密码输错锁定中'] as const : ['success', '可登录'] as const;
  return <Status variant={variant}><StatusIndicator />{label}</Status>;
}

/** 系统管理 → 用户与角色 for administrators: who can sign in, with which Roles on which Sites. */
export function UserAdministration({ csrfToken, sites }: { readonly csrfToken: string; readonly sites: readonly SiteOption[] }) {
  const directory = useQuery(userAdminQuery);
  const [creating, setCreating] = useState(false);
  const [editingRoles, setEditingRoles] = useState<string | null>(null);
  const [editingProfile, setEditingProfile] = useState<UserAdminMember | null>(null);
  const [resetting, setResetting] = useState<UserAdminMember | null>(null);
  const [disabling, setDisabling] = useState<UserAdminMember | null>(null);
  const queryClient = useQueryClient();
  const refresh = () => queryClient.invalidateQueries({ queryKey: userAdminKey });
  const siteName = (siteId?: string) => (siteId ? sites.find((site) => site.id === siteId)?.displayName ?? '已移除的站点' : '全部站点');

  const enable = useMutation({
    mutationFn: (member: UserAdminMember) => userAdminApi.setEnabled(csrfToken, member.principalId, true),
    onSuccess: () => { toast.success('已启用，可以重新登录'); void refresh(); },
    onError: (error) => toast.error(userAdminErrorMessage(error)),
  });

  const members = directory.data?.members ?? [];
  const roles = directory.data?.roles ?? [];
  const rolesMember = members.find((member) => member.principalId === editingRoles) ?? null;
  const disabledCount = members.filter((member) => !member.enabled).length;

  return (
    <div className="flex flex-col gap-[18px]">
      <Panel aria-label="用户">
        <PanelHeader
          title="用户"
          caption={directory.data ? `${members.length} 人${disabledCount ? ` · ${disabledCount} 人已停用` : ''}` : undefined}
          action={<Button size="sm" onClick={() => setCreating(true)} disabled={!directory.data}><UserPlus data-icon="inline-start" />新建用户</Button>}
        />
        {directory.isPending ? (
          <div className="flex flex-col gap-2 px-5 py-4">{[0, 1, 2].map((row) => <Skeleton key={row} className="h-10 rounded-md" />)}</div>
        ) : directory.isError ? (
          <p className="px-5 py-10 text-center text-sm text-muted-foreground">{userAdminErrorMessage(directory.error)}</p>
        ) : (
          <Table aria-label="用户与角色">
            <TableHeader>
              <TableRow>
                <TableHead className="pl-5">用户</TableHead>
                <TableHead>角色与站点</TableHead>
                <TableHead>状态</TableHead>
                <TableHead>最近登录</TableHead>
                <TableHead className="w-12 pr-5"><span className="sr-only">操作</span></TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {members.map((member) => (
                <TableRow key={member.principalId} className="hover:bg-transparent">
                  <TableCell className="pl-5">
                    <span className="flex items-center gap-3">
                      <span className="flex size-8 shrink-0 items-center justify-center rounded-full bg-primary-soft text-xs font-semibold text-primary" aria-hidden="true">{member.displayName.slice(0, 1)}</span>
                      <span className="flex min-w-0 flex-col">
                        <span className="font-semibold">{member.displayName}{member.self ? <span className="ml-1.5 text-xs font-normal text-muted-foreground">（你）</span> : null}</span>
                        <span className="text-xs text-muted-foreground">{member.username} · {member.email}</span>
                      </span>
                    </span>
                  </TableCell>
                  <TableCell>
                    {member.assignments.length === 0 ? <span className="text-muted-foreground">没有角色，登录后看不到任何数据</span> : (
                      <span className="flex flex-wrap gap-1.5">
                        {member.assignments.map((assignment) => (
                          <span key={assignment.bindingId} className="inline-flex h-6 items-center rounded-md border px-2 text-xs">
                            <span className="font-medium">{roleLabel(assignment.roleKey)}</span>
                            <span className="ml-1.5 text-muted-foreground">{siteName(assignment.siteId)}</span>
                          </span>
                        ))}
                      </span>
                    )}
                  </TableCell>
                  <TableCell><MemberState member={member} /></TableCell>
                  <TableCell className="font-mono text-xs text-muted-foreground">{member.lastLoginAt ? clock(member.lastLoginAt) : '从未登录'}</TableCell>
                  <TableCell className="pr-5 text-right">
                    <DropdownMenu modal={false}>
                      <DropdownMenuTrigger asChild>
                        <Button variant="ghost" size="icon-sm" aria-label={`${member.displayName} 的操作`}><MoreHorizontal /></Button>
                      </DropdownMenuTrigger>
                      <DropdownMenuContent align="end">
                        <DropdownMenuItem onSelect={() => setEditingRoles(member.principalId)}>角色与站点</DropdownMenuItem>
                        <DropdownMenuItem onSelect={() => setEditingProfile(member)}>修改姓名和邮箱</DropdownMenuItem>
                        {member.self ? null : (
                          <>
                            <DropdownMenuItem onSelect={() => setResetting(member)}>重置密码</DropdownMenuItem>
                            <DropdownMenuSeparator />
                            {member.enabled
                              ? <DropdownMenuItem variant="destructive" onSelect={() => setDisabling(member)}>停用</DropdownMenuItem>
                              : <DropdownMenuItem onSelect={() => enable.mutate(member)}>启用</DropdownMenuItem>}
                          </>
                        )}
                      </DropdownMenuContent>
                    </DropdownMenu>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </Panel>

      <Panel aria-label="角色说明">
        <PanelHeader title="角色说明" caption="角色可以授予全部站点，也可以只授予某个站点" />
        <dl className="divide-y divide-divider">
          {ROLE_SUMMARIES.map((role) => (
            <div key={role.key} className="grid grid-cols-[96px_minmax(0,1fr)] gap-4 px-5 py-2.5">
              <dt className="text-sm font-medium">{roleLabel(role.key)}</dt>
              <dd className="text-sm text-muted-foreground">{role.summary}</dd>
            </div>
          ))}
        </dl>
      </Panel>

      <CreateUserDialog open={creating} onOpenChange={setCreating} csrfToken={csrfToken} roles={roles} sites={sites} onDone={refresh} />
      <RolesDialog member={rolesMember} onClose={() => setEditingRoles(null)} csrfToken={csrfToken} roles={roles} sites={sites} siteName={siteName} onDone={refresh} />
      <ProfileDialog member={editingProfile} onClose={() => setEditingProfile(null)} csrfToken={csrfToken} onDone={refresh} />
      <ResetPasswordDialog member={resetting} onClose={() => setResetting(null)} csrfToken={csrfToken} />
      <DisableDialog member={disabling} onClose={() => setDisabling(null)} csrfToken={csrfToken} onDone={refresh} />
    </div>
  );
}

/** A Role and where it applies; a Role with Tenant-scoped capabilities always covers every Site. */
function RoleScopeFields<Form extends RoleScope>({ control, roles, sites }: {
  readonly control: Control<Form>;
  readonly roles: readonly UserAdminRole[];
  readonly sites: readonly SiteOption[];
}) {
  const scopeControl = control as unknown as Control<RoleScope>;
  const roleKey = useWatch({ control: scopeControl, name: 'roleKey' });
  const tenantScoped = roles.find((role) => role.roleKey === roleKey)?.tenantScoped ?? false;
  return (
    <div className="grid grid-cols-2 gap-3">
      <Field>
        <FieldLabel>角色</FieldLabel>
        <Controller control={scopeControl} name="roleKey" rules={{ required: '请选择角色' }} render={({ field, fieldState }) => (
          <>
            <Select value={field.value} onValueChange={field.onChange}>
              <SelectTrigger aria-label="角色" aria-invalid={Boolean(fieldState.error) || undefined}><SelectValue placeholder="选择角色" /></SelectTrigger>
              <SelectContent>{roles.map((role) => <SelectItem key={role.roleKey} value={role.roleKey}>{roleLabel(role.roleKey)}</SelectItem>)}</SelectContent>
            </Select>
            {fieldState.error ? <FieldDescription className="text-destructive-text">{fieldState.error.message}</FieldDescription> : null}
          </>
        )} />
      </Field>
      <Field>
        <FieldLabel>站点</FieldLabel>
        <Controller control={scopeControl} name="siteId" render={({ field }) => (
          <Select value={tenantScoped ? ALL_SITES : field.value} onValueChange={field.onChange} disabled={tenantScoped}>
            <SelectTrigger aria-label="站点"><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem value={ALL_SITES}>全部站点</SelectItem>
              {sites.map((site) => <SelectItem key={site.id} value={site.id}>{site.displayName}</SelectItem>)}
            </SelectContent>
          </Select>
        )} />
      </Field>
    </div>
  );
}

const assignmentOf = (scope: RoleScope, roles: readonly UserAdminRole[]) => ({
  roleKey: scope.roleKey as BuiltInRoleKey,
  ...(roles.find((role) => role.roleKey === scope.roleKey)?.tenantScoped ? {} : scopeOf(scope.siteId)),
});

interface CreateForm extends RoleScope { displayName: string; username: string; email: string; password: string }

function CreateUserDialog({ open, onOpenChange, csrfToken, roles, sites, onDone }: {
  readonly open: boolean;
  readonly onOpenChange: (open: boolean) => void;
  readonly csrfToken: string;
  readonly roles: readonly UserAdminRole[];
  readonly sites: readonly SiteOption[];
  readonly onDone: () => void;
}) {
  const empty: CreateForm = { displayName: '', username: '', email: '', password: '', ...EMPTY_SCOPE };
  const form = useForm<CreateForm>({ defaultValues: empty });
  const create = useMutation({
    mutationFn: (values: CreateForm) => userAdminApi.create(csrfToken, {
      displayName: values.displayName, username: values.username, email: values.email, password: values.password,
      assignments: [assignmentOf(values, roles)],
    }),
    onSuccess: (_, values) => { toast.success(`已创建 ${values.displayName}，请把用户名和密码告诉对方`); close(); onDone(); },
  });
  function close() {
    form.reset(empty);
    create.reset();
    onOpenChange(false);
  }
  return (
    <Dialog open={open} onOpenChange={(next) => { if (!next) close(); }}>
      <DialogContent className="sm:max-w-[480px]">
        <form onSubmit={form.handleSubmit((values) => create.mutate(values))} className="flex flex-col gap-5">
          <DialogHeader>
            <DialogTitle>新建用户</DialogTitle>
            <DialogDescription>由你设定登录密码，创建后告诉对方；对方登录后可以在右上角菜单里改成自己的密码。</DialogDescription>
          </DialogHeader>
          <FieldGroup className="gap-4">
            <div className="grid grid-cols-2 gap-3">
              <Field><FieldLabel htmlFor="new-user-name">姓名</FieldLabel><Input id="new-user-name" maxLength={256} {...form.register('displayName', { required: true })} /></Field>
              <Field><FieldLabel htmlFor="new-user-username">用户名</FieldLabel><Input id="new-user-username" maxLength={128} autoComplete="off" {...form.register('username', { required: true })} /></Field>
            </div>
            <Field><FieldLabel htmlFor="new-user-email">邮箱</FieldLabel><Input id="new-user-email" type="email" maxLength={320} {...form.register('email', { required: true })} /></Field>
            <Field>
              <FieldLabel htmlFor="new-user-password">登录密码</FieldLabel>
              <Input id="new-user-password" type="password" autoComplete="new-password" {...form.register('password', { required: true, minLength: 8, maxLength: 512 })} />
              <FieldDescription>{PASSWORD_HINT}</FieldDescription>
            </Field>
            <RoleScopeFields control={form.control} roles={roles} sites={sites} />
          </FieldGroup>
          {create.isError ? <p role="alert" className="text-sm text-destructive-text">{userAdminErrorMessage(create.error)}</p> : null}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={close}>取消</Button>
            <Button type="submit" disabled={create.isPending}>{create.isPending ? '创建中…' : '创建'}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function RolesDialog({ member, onClose, csrfToken, roles, sites, siteName, onDone }: {
  readonly member: UserAdminMember | null;
  readonly onClose: () => void;
  readonly csrfToken: string;
  readonly roles: readonly UserAdminRole[];
  readonly sites: readonly SiteOption[];
  readonly siteName: (siteId?: string) => string;
  readonly onDone: () => void;
}) {
  const form = useForm<RoleScope>({ defaultValues: EMPTY_SCOPE });
  const assign = useMutation({
    mutationFn: (values: RoleScope) => userAdminApi.assign(csrfToken, member!.principalId, assignmentOf(values, roles)),
    onSuccess: () => { form.reset(EMPTY_SCOPE); onDone(); },
  });
  const revoke = useMutation({
    mutationFn: (bindingId: string) => userAdminApi.revoke(csrfToken, member!.principalId, bindingId),
    onSuccess: onDone,
  });
  const error = assign.error ?? revoke.error;
  return (
    <Dialog open={Boolean(member)} onOpenChange={(open) => { if (!open) { form.reset(EMPTY_SCOPE); assign.reset(); revoke.reset(); onClose(); } }}>
      <DialogContent className="sm:max-w-[520px]">
        <DialogHeader>
          <DialogTitle>{member?.displayName} 的角色与站点</DialogTitle>
          <DialogDescription>改动立即生效：对方下一次操作时就按新的权限判断。</DialogDescription>
        </DialogHeader>
        <ul className="flex flex-col divide-y divide-divider rounded-lg border">
          {member?.assignments.length ? member.assignments.map((assignment) => (
            <li key={assignment.bindingId} className="flex items-center gap-3 px-4 py-2.5">
              <span className="text-sm font-medium">{roleLabel(assignment.roleKey)}</span>
              <span className="text-sm text-muted-foreground">{siteName(assignment.siteId)}</span>
              <Button variant="ghost" size="sm" className="ml-auto text-muted-foreground" disabled={revoke.isPending} onClick={() => revoke.mutate(assignment.bindingId)}>
                <X data-icon="inline-start" />撤销
              </Button>
            </li>
          )) : <li className="px-4 py-3 text-sm text-muted-foreground">还没有角色，登录后看不到任何数据</li>}
        </ul>
        <form onSubmit={form.handleSubmit((values) => assign.mutate(values))} className="flex flex-col gap-3 rounded-lg bg-muted/40 p-4">
          <RoleScopeFields control={form.control} roles={roles} sites={sites} />
          <Button type="submit" className="self-end" size="sm" disabled={assign.isPending}><Plus data-icon="inline-start" />添加角色</Button>
        </form>
        {error ? <p role="alert" className="text-sm text-destructive-text">{userAdminErrorMessage(error)}</p> : null}
      </DialogContent>
    </Dialog>
  );
}

function ProfileDialog({ member, onClose, csrfToken, onDone }: {
  readonly member: UserAdminMember | null;
  readonly onClose: () => void;
  readonly csrfToken: string;
  readonly onDone: () => void;
}) {
  return (
    <Dialog open={Boolean(member)} onOpenChange={(open) => { if (!open) onClose(); }}>
      <DialogContent className="sm:max-w-[440px]">
        {member ? <ProfileForm key={member.principalId} member={member} csrfToken={csrfToken} onSaved={() => { onClose(); onDone(); }} onCancel={onClose} /> : null}
      </DialogContent>
    </Dialog>
  );
}

function ProfileForm({ member, csrfToken, onSaved, onCancel }: { readonly member: UserAdminMember; readonly csrfToken: string; readonly onSaved: () => void; readonly onCancel: () => void }) {
  const form = useForm({ defaultValues: { displayName: member.displayName, email: member.email } });
  const save = useMutation({
    mutationFn: (values: { displayName: string; email: string }) => userAdminApi.updateProfile(csrfToken, member.principalId, values),
    onSuccess: () => { toast.success('已保存'); onSaved(); },
  });
  return (
    <form onSubmit={form.handleSubmit((values) => save.mutate(values))} className="flex flex-col gap-5">
      <DialogHeader><DialogTitle>修改姓名和邮箱</DialogTitle><DialogDescription>用户名 {member.username} 不能修改。</DialogDescription></DialogHeader>
      <FieldGroup className="gap-4">
        <Field><FieldLabel htmlFor="profile-name">姓名</FieldLabel><Input id="profile-name" maxLength={256} {...form.register('displayName', { required: true })} /></Field>
        <Field><FieldLabel htmlFor="profile-email">邮箱</FieldLabel><Input id="profile-email" type="email" maxLength={320} {...form.register('email', { required: true })} /></Field>
      </FieldGroup>
      {save.isError ? <p role="alert" className="text-sm text-destructive-text">{userAdminErrorMessage(save.error)}</p> : null}
      <DialogFooter><Button type="button" variant="outline" onClick={onCancel}>取消</Button><Button type="submit" disabled={save.isPending}>保存</Button></DialogFooter>
    </form>
  );
}

function ResetPasswordDialog({ member, onClose, csrfToken }: { readonly member: UserAdminMember | null; readonly onClose: () => void; readonly csrfToken: string }) {
  const form = useForm({ defaultValues: { password: '' } });
  const reset = useMutation({
    mutationFn: ({ password }: { password: string }) => userAdminApi.resetPassword(csrfToken, member!.principalId, password),
    onSuccess: () => { toast.success(`已为 ${member!.displayName} 设置新密码，请告诉对方`); close(); },
  });
  function close() {
    form.reset({ password: '' });
    reset.reset();
    onClose();
  }
  return (
    <Dialog open={Boolean(member)} onOpenChange={(open) => { if (!open) close(); }}>
      <DialogContent className="sm:max-w-[420px]">
        <form onSubmit={form.handleSubmit((values) => reset.mutate(values))} className="flex flex-col gap-5">
          <DialogHeader>
            <DialogTitle>重置 {member?.displayName} 的密码</DialogTitle>
            <DialogDescription>新密码立即生效，同时解除密码输错造成的锁定。</DialogDescription>
          </DialogHeader>
          <Field>
            <FieldLabel htmlFor="reset-password">新密码</FieldLabel>
            <Input id="reset-password" type="password" autoComplete="new-password" {...form.register('password', { required: true, minLength: 8, maxLength: 512 })} />
            <FieldDescription>{PASSWORD_HINT}</FieldDescription>
          </Field>
          {reset.isError ? <p role="alert" className="text-sm text-destructive-text">{userAdminErrorMessage(reset.error)}</p> : null}
          <DialogFooter><Button type="button" variant="outline" onClick={close}>取消</Button><Button type="submit" disabled={reset.isPending}>设置密码</Button></DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function DisableDialog({ member, onClose, csrfToken, onDone }: { readonly member: UserAdminMember | null; readonly onClose: () => void; readonly csrfToken: string; readonly onDone: () => void }) {
  const disable = useMutation({
    mutationFn: () => userAdminApi.setEnabled(csrfToken, member!.principalId, false),
    onSuccess: () => { toast.success(`已停用 ${member!.displayName}`); onClose(); onDone(); },
  });
  return (
    <AlertDialog open={Boolean(member)} onOpenChange={(open) => { if (!open) { disable.reset(); onClose(); } }}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>停用 {member?.displayName}？</AlertDialogTitle>
          <AlertDialogDescription>停用后对方立即退出登录，也不能再登录。角色会保留，重新启用后恢复原来的权限。</AlertDialogDescription>
        </AlertDialogHeader>
        {disable.isError ? <p role="alert" className="text-sm text-destructive-text">{userAdminErrorMessage(disable.error)}</p> : null}
        <AlertDialogFooter>
          <AlertDialogCancel>取消</AlertDialogCancel>
          <AlertDialogAction variant="destructive" disabled={disable.isPending} onClick={(event) => { event.preventDefault(); disable.mutate(); }}>停用</AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
