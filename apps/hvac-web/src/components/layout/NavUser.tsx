import { useEffect, useRef, useState } from 'react';
import { ChevronDown, KeyRound, LogOut, ShieldCheck } from 'lucide-react';
import { useShellSnapshot } from '@/app/ShellRuntimeContext';
import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog';
import { Avatar, AvatarFallback } from '@/components/ui/avatar';
import { Button } from '@/components/ui/button';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { Spinner } from '@/components/ui/spinner';
import { ChangePasswordDialog } from '@/features/account/ChangePasswordDialog';

interface NavUserProps {
  readonly name: string;
  readonly role: string;
  readonly csrfToken: string;
  readonly onNavigate: (target: string) => void;
  readonly onLogout: () => void;
  readonly loggingOut?: boolean;
}

/** The signed-in user at the right of the top bar: who they are, and the way out. */
export function NavUser({ name, role, csrfToken, onNavigate, onLogout, loggingOut = false }: NavUserProps) {
  const [changingPassword, setChangingPassword] = useState(false);
  const [confirmLogout, setConfirmLogout] = useState(false);
  const wasLoggingOut = useRef(false);
  const snapshot = useShellSnapshot();
  const canOpenSystem = snapshot.principal?.authorization.capabilities.includes('site.list') ?? false;

  useEffect(() => {
    if (wasLoggingOut.current && !loggingOut) {
      setConfirmLogout(false);
    }
    wasLoggingOut.current = loggingOut;
  }, [loggingOut]);

  return (
    <>
    <DropdownMenu modal={false}>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          aria-label={`${name}，用户菜单`}
          className="flex h-8 items-center gap-2 rounded-lg pr-1.5 pl-1 text-sm text-secondary-foreground transition-colors duration-100 outline-none hover:bg-accent focus-visible:ring-3 focus-visible:ring-ring-soft data-[state=open]:bg-accent"
        >
          <Avatar className="size-7">
            <AvatarFallback className="bg-primary-soft text-xs font-semibold text-primary">{name.slice(0, 1)}</AvatarFallback>
          </Avatar>
          <span className="max-w-32 truncate">{name}</span>
          <ChevronDown className="size-3.5 text-subtle-foreground" aria-hidden="true" />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent className="min-w-56" align="end" sideOffset={6}>
        <DropdownMenuLabel className="flex flex-col gap-0.5 font-normal">
          <span className="text-sm font-semibold text-foreground">{name}</span>
          <span className="text-xs text-muted-foreground">{role}</span>
        </DropdownMenuLabel>
        <DropdownMenuSeparator />
        <DropdownMenuGroup>
          {canOpenSystem ? (
            <DropdownMenuItem onSelect={() => onNavigate('/system')}>
              <ShieldCheck />
              系统管理
            </DropdownMenuItem>
          ) : null}
          <DropdownMenuItem onSelect={() => setChangingPassword(true)}>
            <KeyRound />
            修改密码
          </DropdownMenuItem>
        </DropdownMenuGroup>
        <DropdownMenuSeparator />
        <DropdownMenuItem variant="destructive" disabled={loggingOut} onSelect={() => setConfirmLogout(true)}>
          <LogOut />
          退出登录
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
    <ChangePasswordDialog open={changingPassword} onOpenChange={setChangingPassword} csrfToken={csrfToken} />
    <AlertDialog open={confirmLogout} onOpenChange={(next) => { if (!loggingOut) setConfirmLogout(next); }}>
      <AlertDialogContent onEscapeKeyDown={(event) => { if (loggingOut) event.preventDefault(); }}>
        <AlertDialogHeader>
          <AlertDialogTitle>确认退出登录？</AlertDialogTitle>
          <AlertDialogDescription>退出后将清除此浏览器上的登录状态，下次使用需要重新输入账号和密码。</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel disabled={loggingOut}>取消</AlertDialogCancel>
          <Button type="button" variant="destructive" disabled={loggingOut} onClick={onLogout}>
            {loggingOut ? <Spinner /> : null}
            {loggingOut ? '正在退出…' : '确认退出'}
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
    </>
  );
}
