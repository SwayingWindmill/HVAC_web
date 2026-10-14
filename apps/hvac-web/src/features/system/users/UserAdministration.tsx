import { useState, type FormEvent, type ReactNode } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
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
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { ROLE_SUMMARIES, roleLabel } from '@/lib/role-labels';
import { cn } from '@/lib/utils';
import { userAdminApi, userAdminErrorMessage, userAdminKey, userAdminQuery } from './user-admin-api';

interface SiteOption { readonly id: string; readonly displayName: string }

const ALL_SITES = 'all';
const PASSWORD_HINT = '至少 8 位，不能是常见密码，也不能和用户名相同';

const clock = (value: string) => new Intl.DateTimeFormat('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false }).format(new Date(value));

function Chip({ tone, children }: { readonly tone: 'success' | 'warning' | 'neutral'; readonly children: ReactNode }) {
  const styles = {
    success: ['bg-success-soft text-success-text', 'bg-success'],
    warning: ['bg-warning-soft text-warning-text', 'bg-warning'],
    neutral: ['bg-secondary text-secondary-foreground', 'bg-subtle-foreground'],
  }[tone];
  return (
    <span className={cn('inline-flex h-5.5 items-center gap-1.5 rounded-md px-1.5 text-xs font-medium whitespace-nowrap', styles[0])}>
      <span className={cn('size-1.5 rounded-full', styles[1])} aria-hidden="true" />
      {children}
    </span>
  );
}

function memberState(member: UserAdminMember) {
  if (!member.enabled) return <Chip tone="neutral">已停用</Chip>;
  if (member.locked) return <Chip tone="warning">密码输错锁定中</Chip>;
  return <Chip tone="success">可登录</Chip>;
}

/** 系统管理 → 用户与角色 for administrators: who can sign in, with which Roles on which Sites. */
export function UserAdministration({ csrfToken, sites }: { readonly csrfToken: string; readonly sites: readonly SiteOption[] }) {
  const directory = useQuery(userAdminQuery(true));
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
                  <TableCell>{memberState(member)}</TableCell>
                  <TableCell className="font-mono text-xs text-muted-foreground">{member.lastLoginAt ? clock(member.lastLoginAt) : '从未登录'}</TableCell>
                  <TableCell className="pr-5 text-right">
                    <DropdownMenu modal={false}>
                      <DropdownMenuTrigger asChild>
                        <Button variant="ghost" size="icon-sm" aria-label={`${member.displayName} 的操作`}><MoreHorizontal /></Button>
                      </DropdownMenuTrigger>
                      <DropdownMenuContent align="end">
                        <DropdownMenuItem onSelect={() => setEditingRoles(member.principalId)}>角色与站点</DropdownMenuItem>
                        <DropdownMenuItem onSelect={() => setEditingProfile(member)}>修改姓名和邮箱</DropdownMenuItem>
                        <DropdownMenuItem onSelect={() => setResetting(member)}>重置密码</DropdownMenuItem>
                        {member.self ? null : (
                          <>
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

function RoleScopeFields({ roles, sites, roleKey, siteId, onRole, onSite }: {
  readonly roles: readonly UserAdminRole[];
  readonly sites: readonly SiteOption[];
  readonly roleKey: BuiltInRoleKey | '';
  readonly siteId: string;
  readonly onRole: (value: BuiltInRoleKey) => void;
  readonly onSite: (value: string) => void;
}) {
  const tenantScoped = roles.find((role) => role.roleKey === roleKey)?.tenantScoped ?? false;
  return (
    <div className="grid grid-cols-2 gap-3">
      <Field>
        <FieldLabel>角色</FieldLabel>
        <Select value={roleKey} onValueChange={(value) => { onRole(value as BuiltInRoleKey); if (roles.find((role) => role.roleKey === value)?.tenantScoped) onSite(ALL_SITES); }}>
          <SelectTrigger aria-label="角色"><SelectValue placeholder="选择角色" /></SelectTrigger>
          <SelectContent>{roles.map((role) => <SelectItem key={role.roleKey} value={role.roleKey}>{roleLabel(role.roleKey)}</SelectItem>)}</SelectContent>
        </Select>
      </Field>
      <Field>
        <FieldLabel>站点</FieldLabel>
        <Select value={siteId} onValueChange={onSite} disabled={tenantScoped}>
          <SelectTrigger aria-label="站点"><SelectValue /></SelectTrigger>
          <SelectContent>
            <SelectItem value={ALL_SITES}>全部站点</SelectItem>
            {sites.map((site) => <SelectItem key={site.id} value={site.id}>{site.displayName}</SelectItem>)}
          </SelectContent>
        </Select>
      </Field>
    </div>
  );
}

const scopeOf = (siteId: string) => (siteId === ALL_SITES ? {} : { siteId });

function CreateUserDialog({ open, onOpenChange, csrfToken, roles, sites, onDone }: {
  readonly open: boolean;
  readonly onOpenChange: (open: boolean) => void;
  readonly csrfToken: string;
  readonly roles: readonly UserAdminRole[];
  readonly sites: readonly SiteOption[];
  readonly onDone: () => void;
}) {
  const empty = { displayName: '', username: '', email: '', password: '', roleKey: '' as BuiltInRoleKey | '', siteId: ALL_SITES };
  const [form, setForm] = useState(empty);
  const create = useMutation({
    mutationFn: () => userAdminApi.create(csrfToken, {
      displayName: form.displayName, username: form.username, email: form.email, password: form.password,
      assignments: [{ roleKey: form.roleKey as BuiltInRoleKey, ...scopeOf(form.siteId) }],
    }),
    onSuccess: () => { toast.success(`已创建 ${form.displayName}，请把用户名和密码告诉对方`); setForm(empty); onOpenChange(false); onDone(); },
  });
  const submit = (event: FormEvent) => { event.preventDefault(); create.mutate(); };
  const set = (key: keyof typeof empty) => (event: { target: { value: string } }) => setForm({ ...form, [key]: event.target.value });
  return (
    <Dialog open={open} onOpenChange={(next) => { if (!next) { create.reset(); setForm(empty); } onOpenChange(next); }}>
      <DialogContent className="sm:max-w-[480px]">
        <form onSubmit={submit} className="flex flex-col gap-5">
          <DialogHeader>
            <DialogTitle>新建用户</DialogTitle>
            <DialogDescription>由你设定登录密码，创建后告诉对方；对方登录后可以在右上角菜单里改成自己的密码。</DialogDescription>
          </DialogHeader>
          <FieldGroup className="gap-4">
            <div className="grid grid-cols-2 gap-3">
              <Field><FieldLabel htmlFor="new-user-name">姓名</FieldLabel><Input id="new-user-name" value={form.displayName} onChange={set('displayName')} required maxLength={256} /></Field>
              <Field><FieldLabel htmlFor="new-user-username">用户名</FieldLabel><Input id="new-user-username" value={form.username} onChange={set('username')} required maxLength={128} autoComplete="off" /></Field>
            </div>
            <Field><FieldLabel htmlFor="new-user-email">邮箱</FieldLabel><Input id="new-user-email" type="email" value={form.email} onChange={set('email')} required maxLength={320} /></Field>
            <Field>
              <FieldLabel htmlFor="new-user-password">登录密码</FieldLabel>
              <Input id="new-user-password" type="password" value={form.password} onChange={set('password')} required minLength={8} maxLength={512} autoComplete="new-password" />
              <FieldDescription>{PASSWORD_HINT}</FieldDescription>
            </Field>
            <RoleScopeFields roles={roles} sites={sites} roleKey={form.roleKey} siteId={form.siteId}
              onRole={(roleKey) => setForm((current) => ({ ...current, roleKey }))} onSite={(siteId) => setForm((current) => ({ ...current, siteId }))} />
          </FieldGroup>
          {create.isError ? <p role="alert" className="text-sm text-destructive-text">{userAdminErrorMessage(create.error)}</p> : null}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>取消</Button>
            <Button type="submit" disabled={create.isPending || !form.roleKey}>{create.isPending ? '创建中…' : '创建'}</Button>
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
  const [roleKey, setRoleKey] = useState<BuiltInRoleKey | ''>('');
  const [siteId, setSiteId] = useState(ALL_SITES);
  const assign = useMutation({
    mutationFn: () => userAdminApi.assign(csrfToken, member!.principalId, { roleKey: roleKey as BuiltInRoleKey, ...scopeOf(siteId) }),
    onSuccess: () => { setRoleKey(''); setSiteId(ALL_SITES); onDone(); },
  });
  const revoke = useMutation({
    mutationFn: (bindingId: string) => userAdminApi.revoke(csrfToken, member!.principalId, bindingId),
    onSuccess: onDone,
  });
  const error = assign.error ?? revoke.error;
  return (
    <Dialog open={Boolean(member)} onOpenChange={(open) => { if (!open) { assign.reset(); revoke.reset(); onClose(); } }}>
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
        <div className="flex flex-col gap-3 rounded-lg bg-muted/40 p-4">
          <RoleScopeFields roles={roles} sites={sites} roleKey={roleKey} siteId={siteId} onRole={setRoleKey} onSite={setSiteId} />
          <Button className="self-end" size="sm" disabled={!roleKey || assign.isPending} onClick={() => assign.mutate()}><Plus data-icon="inline-start" />添加角色</Button>
        </div>
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
  const [displayName, setDisplayName] = useState(member.displayName);
  const [email, setEmail] = useState(member.email);
  const save = useMutation({ mutationFn: () => userAdminApi.updateProfile(csrfToken, member.principalId, { displayName, email }), onSuccess: () => { toast.success('已保存'); onSaved(); } });
  return (
    <form onSubmit={(event) => { event.preventDefault(); save.mutate(); }} className="flex flex-col gap-5">
      <DialogHeader><DialogTitle>修改姓名和邮箱</DialogTitle><DialogDescription>用户名 {member.username} 不能修改。</DialogDescription></DialogHeader>
      <FieldGroup className="gap-4">
        <Field><FieldLabel htmlFor="profile-name">姓名</FieldLabel><Input id="profile-name" value={displayName} onChange={(event) => setDisplayName(event.target.value)} required maxLength={256} /></Field>
        <Field><FieldLabel htmlFor="profile-email">邮箱</FieldLabel><Input id="profile-email" type="email" value={email} onChange={(event) => setEmail(event.target.value)} required maxLength={320} /></Field>
      </FieldGroup>
      {save.isError ? <p role="alert" className="text-sm text-destructive-text">{userAdminErrorMessage(save.error)}</p> : null}
      <DialogFooter><Button type="button" variant="outline" onClick={onCancel}>取消</Button><Button type="submit" disabled={save.isPending}>保存</Button></DialogFooter>
    </form>
  );
}

function ResetPasswordDialog({ member, onClose, csrfToken }: { readonly member: UserAdminMember | null; readonly onClose: () => void; readonly csrfToken: string }) {
  const [password, setPassword] = useState('');
  const reset = useMutation({
    mutationFn: () => userAdminApi.resetPassword(csrfToken, member!.principalId, password),
    onSuccess: () => { toast.success(`已为 ${member!.displayName} 设置新密码，请告诉对方`); setPassword(''); onClose(); },
  });
  return (
    <Dialog open={Boolean(member)} onOpenChange={(open) => { if (!open) { reset.reset(); setPassword(''); onClose(); } }}>
      <DialogContent className="sm:max-w-[420px]">
        <form onSubmit={(event) => { event.preventDefault(); reset.mutate(); }} className="flex flex-col gap-5">
          <DialogHeader>
            <DialogTitle>重置 {member?.displayName} 的密码</DialogTitle>
            <DialogDescription>新密码立即生效，同时解除密码输错造成的锁定。</DialogDescription>
          </DialogHeader>
          <Field>
            <FieldLabel htmlFor="reset-password">新密码</FieldLabel>
            <Input id="reset-password" type="password" value={password} onChange={(event) => setPassword(event.target.value)} required minLength={8} maxLength={512} autoComplete="new-password" />
            <FieldDescription>{PASSWORD_HINT}</FieldDescription>
          </Field>
          {reset.isError ? <p role="alert" className="text-sm text-destructive-text">{userAdminErrorMessage(reset.error)}</p> : null}
          <DialogFooter><Button type="button" variant="outline" onClick={onClose}>取消</Button><Button type="submit" disabled={reset.isPending}>设置密码</Button></DialogFooter>
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
