import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { MoreHorizontal, UserPlus } from 'lucide-react';
import { notifyError, notifySuccess } from '@/lib/toast';
import type { UserAdminMember } from '@/api/generated/platformGateway.gen';
import { Panel, PanelHeader } from '@/blocks/panel';
import { Button } from '@/components/ui/button';
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { Skeleton } from '@/components/ui/skeleton';
import { StatusBadge } from '@/components/status-badge';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { ROLE_SUMMARIES, roleLabel } from '@/lib/role-labels';
import { userAdminApi, userAdminErrorMessage, userAdminKey, userAdminQuery } from './user-admin-api';
import { formatAssignmentValidity, resolveMemberTwoFactorDisplay } from './user-admin-model';
import {
  CreateUserDialog,
  RolesDialog,
  type SiteOption,
} from './UserAdministrationDialogs';
import {
  DisableDialog,
  ProfileDialog,
  ResetPasswordDialog,
} from './UserAccountDialogs';

const clock = (value: string) => new Intl.DateTimeFormat('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false }).format(new Date(value));

function MemberState({ member }: { readonly member: UserAdminMember }) {
  const [tone, label] = !member.enabled ? ['neutral', '已停用'] as const : member.locked ? ['warning', '密码输错锁定中'] as const : ['success', '可登录'] as const;
  return <StatusBadge tone={tone} label={label} />;
}

function MemberTwoFactor({ member }: { readonly member: UserAdminMember }) {
  const display = resolveMemberTwoFactorDisplay(member);
  switch (display.kind) {
    case 'enabled':
      return <span className="text-xs whitespace-nowrap text-secondary-foreground">{display.label}</span>;
    case 'not_enabled':
      return <span className="text-xs whitespace-nowrap text-muted-foreground">{display.label}</span>;
    case 'missing_for_high_risk':
      return (
        <div className="flex flex-col gap-0.5 whitespace-nowrap">
          <span className="inline-flex items-center gap-1 text-xs font-medium text-warning-text">
            <span aria-hidden="true">▲</span>
            <span>{display.label}</span>
          </span>
          <span className="text-xs text-muted-foreground">{display.warning}</span>
        </div>
      );
    default: {
      const _exhaustive: never = display;
      return _exhaustive;
    }
  }
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
    onSuccess: () => { notifySuccess('已启用，可以重新登录'); void refresh(); },
    onError: (error) => notifyError(userAdminErrorMessage(error)),
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
          <div className="flex flex-col items-center gap-3 px-5 py-10 text-center text-sm text-muted-foreground">
            <p>{userAdminErrorMessage(directory.error)}</p>
            <Button variant="outline" size="sm" onClick={() => void directory.refetch()}>重试</Button>
          </div>
        ) : (
          <Table aria-label="用户与角色">
            <TableHeader>
              <TableRow>
                <TableHead scope="col" className="pl-5">用户</TableHead>
                <TableHead scope="col">角色与站点</TableHead>
                <TableHead scope="col">两步验证</TableHead>
                <TableHead scope="col">状态</TableHead>
                <TableHead scope="col">最近登录</TableHead>
                <TableHead scope="col" className="w-12 pr-5"><span className="sr-only">操作</span></TableHead>
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
                        {member.assignments.map((assignment) => {
                          const validity = formatAssignmentValidity(assignment.validTo);
                          return (
                            <span key={assignment.bindingId} className="inline-flex h-6 items-center rounded-md border px-2 text-xs">
                              <span className="font-medium">{roleLabel(assignment.roleKey)}</span>
                              <span className="ml-1.5 text-muted-foreground">{siteName(assignment.siteId)}</span>
                              <span className={validity.temporary ? 'ml-1.5 rounded bg-muted px-1 text-[11px] font-medium text-foreground' : 'ml-1.5 text-muted-foreground'}>
                                {validity.label}
                              </span>
                            </span>
                          );
                        })}
                      </span>
                    )}
                  </TableCell>
                  <TableCell><MemberTwoFactor member={member} /></TableCell>
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
